//go:build integration

package trust_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/trust"
)

// The decay policy WIRED UP: grace, demotion and recovery against a real database (SPEC §5.3, plan
// phase 6 slice 6b-2).
//
// The unit tests in decay_test.go prove the decision is right. These prove the decision is REACHED and
// its result is STORED, which is a different claim and the one that fails silently: a policy nothing
// calls is a policy that passes its own tests forever. So the assertions here are about what the
// database ends up holding after real events and real sweeps -- including the two columns the level
// depends on -- and never about what a function returned.
//
// TestMain brings up the test database.
func TestMain(m *testing.M) {
	testutil.TestWithDatabase(m, nil)
}

// testTime is a fixed clock. Every elapsed-time figure here is supplied explicitly rather than
// measured, so no assertion depends on how long the test took to run.
var testTime = time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)

func q() *queries.Queries {
	return queries.New(testutil.DB())
}

func createWithTxn() queries.WithTxnFunc {
	pool := testutil.DB()
	return func(fn func(*queries.Queries) error) error {
		return pgx.BeginFunc(context.Background(), pool, func(tx pgx.Tx) error {
			return fn(queries.New(tx))
		})
	}
}

// decayingTrust is a trust service whose clock the test owns. A fresh one per test, built from the pool
// rather than from a shared factory: WithClock mutates the receiver, and every test in this binary
// shares one process.
func decayingTrust(at time.Time) *trust.Trust {
	return trust.NewTrust(q(), createWithTxn()).WithClock(func() time.Time { return at })
}

func createDecayUser(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	_, err := q().CreateUser(t.Context(), queries.CreateUserParams{
		ID:           id,
		Name:         "Decay Test User " + id.String(),
		PasswordHash: "not-a-real-hash-this-test-never-logs-in",
		Email:        id.String() + "@decay.invalid",
	})
	require.NoError(t, err, "creating the member under test")
	return id
}

// contribute records n approvals at n DISTINCT entities.
//
// Distinct because the trust log dedups per entity: n approvals of one entity are ONE event, and a
// test reusing an id would read the resulting 1 as a service bug. A NEGATIVE delta with the same kind
// is how the model expresses a reversal -- migration 76 says so in as many words -- so `approve(-2)`
// is two approvals withdrawn, which is the "two flags" of the plan's verification sentence written in
// the log's own vocabulary.
func contribute(t *testing.T, svc *trust.Trust, userID uuid.UUID, n int) {
	t.Helper()
	delta := 1
	if n < 0 {
		delta, n = -1, -n
	}
	for range n {
		entityID := uuid.Must(uuid.NewV7())
		_, err := svc.RecordEvent(t.Context(), trust.Event{
			UserID:     userID,
			Kind:       trust.KindEditApproved,
			Delta:      delta,
			EntityType: "performer",
			EntityID:   &entityID,
		})
		require.NoError(t, err)
	}
}

// sameInstant compares two timestamps as INSTANTS.
//
// The round trip through a timestamptz column preserves the instant and not the zone: what comes back
// is the same moment expressed in the connection's location, so comparing time.Time values with
// assert.Equal fails on the location while the database is holding exactly what was written. Every
// clock assertion in this file goes through here.
func sameInstant(t *testing.T, want time.Time, got *time.Time, msg string) {
	t.Helper()
	require.NotNil(t, got, msg)
	assert.Truef(t, want.Equal(*got), "%s: want %s, got %s", msg, want.UTC(), got.UTC())
}

// levelOf reads the level the way the rest of the application does, from the rollup.
func levelOf(t *testing.T, userID uuid.UUID) int {
	t.Helper()
	rollup, err := q().GetUserTrust(t.Context(), userID)
	require.NoError(t, err)
	return int(rollup.Level)
}

// decayStateOf reads the two persisted timestamps back, as VALUES, so an assertion about the state is
// an assertion about what a later request will see rather than about what a call returned.
func decayStateOf(t *testing.T, userID uuid.UUID) (shortSince, demotedAt *time.Time) {
	t.Helper()
	rollup, err := q().GetUserTrust(t.Context(), userID)
	require.NoError(t, err)
	if rollup.ShortSince.Valid {
		shortSince = &rollup.ShortSince.Time
	}
	if rollup.DemotedAt.Valid {
		demotedAt = &rollup.DemotedAt.Time
	}
	return shortSince, demotedAt
}

// The plan's verification sentence, end to end: a member with two flags loses TL3 after the grace
// period and regains it in 14 days.
func TestDecayHoldsTheLevelThroughGraceAndRecovery(t *testing.T) {
	userID := createDecayUser(t)

	// TL3, earned the ordinary way. Twenty approvals at ten points each is the 200-point threshold.
	earning := decayingTrust(testTime)
	contribute(t, earning, userID, 20)
	require.Equal(t, 3, levelOf(t, userID), "fixture: the member must start at TL3, or every assertion below is about nothing")

	// TWO FLAGS. The level does NOT move: this is the half of the policy that lives on the write path,
	// and a service that still followed the totals here would make the grace period a decoration.
	contribute(t, earning, userID, -2)
	assert.Equal(t, 3, levelOf(t, userID), "the member keeps TL3: that is what grace is")
	shortSince, demotedAt := decayStateOf(t, userID)
	require.NotNil(t, shortSince, "and the shortfall is on record, which is what the sweep works from")
	sameInstant(t, testTime, shortSince, "the shortfall is dated from when it was seen")
	assert.Nil(t, demotedAt, "with no demotion yet")

	// BEFORE THE DEADLINE: a sweep finds the shortfall standing and changes nothing.
	justBefore := testTime.Add(trust.GracePeriod - time.Hour)
	sweep, err := decayingTrust(justBefore).SweepDecay(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, sweep.Examined, "the member is in the sweep's working set")
	assert.Equal(t, 0, sweep.Demoted)
	assert.Equal(t, 3, levelOf(t, userID), "still TL3, an hour before the deadline")

	// THE DEADLINE: the level falls to what the totals justify -- TL2, 180 points' worth.
	atDeadline := testTime.Add(trust.GracePeriod)
	sweep, err = decayingTrust(atDeadline).SweepDecay(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, sweep.Demoted, "the sweep is what fires the deadline")
	assert.Equal(t, 2, levelOf(t, userID), "the level now matches the totals")
	shortSince, demotedAt = decayStateOf(t, userID)
	assert.Nil(t, shortSince, "the shortfall is settled, not still pending")
	require.NotNil(t, demotedAt)
	sameInstant(t, atDeadline, demotedAt, "the demotion is dated from when the sweep fired it")

	// THE POINTS COME BACK INSIDE THE WINDOW -- and the level stays down. Without this half the member
	// would be back the same hour, and a level that flips twice a day means nothing.
	contribute(t, decayingTrust(atDeadline.Add(time.Hour)), userID, 2)
	assert.Equal(t, 2, levelOf(t, userID), "they have earned TL3 back by the totals; the window does not care")

	day13 := atDeadline.Add(trust.RecoveryPeriod - 24*time.Hour)
	sweep, err = decayingTrust(day13).SweepDecay(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, sweep.Held)
	assert.Equal(t, 2, levelOf(t, userID), "held at day 13")

	// AND REGAINS IT IN 14 DAYS: the window closes and the level follows the totals again.
	day14 := atDeadline.Add(trust.RecoveryPeriod)
	sweep, err = decayingTrust(day14).SweepDecay(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, sweep.Recovered)
	assert.Equal(t, 3, levelOf(t, userID), "the level the member had is the level they are back at")
	shortSince, demotedAt = decayStateOf(t, userID)
	assert.Nil(t, shortSince, "both clocks are cleared, so the row leaves the sweep's working set")
	assert.Nil(t, demotedAt)

	// The sweep's working set really does empty itself: nothing left to examine.
	final, err := decayingTrust(day14.Add(time.Hour)).SweepDecay(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 0, final.Examined, "a member with nothing in flight is not examined at all")
}

// A BOUNTY IS POINTS. The rollup counts BonusPoints, ApplyTrustEvent increments it, and Totals.Points()
// adds it -- but the level derivation built its Totals by hand and left the column out, so a quest's
// bounty raised nothing. Reading the whole row is the fix; this is the case that says so out loud,
// because the failure was silent in the direction that matters (a member who earned points did not
// get them).
func TestABountyRaisesTheLevel(t *testing.T) {
	userID := createDecayUser(t)
	svc := decayingTrust(testTime)

	// 60 points of counted contributions -- TL2, six approvals -- plus a 700-point quest bounty,
	// which is more than enough for TL4 on its own.
	contribute(t, svc, userID, 6)
	require.Equal(t, 2, levelOf(t, userID), "fixture: the counted contributions alone are TL2")

	_, err := svc.RecordEvent(t.Context(), trust.Event{
		UserID: userID,
		// BOUNTY_BONUS, not QUEST_COMPLETED. The first version of this test used the quest kind with a
		// delta of 700 and got level 5: the quest kind moves the QUEST COUNT, so 700 arrived as 700
		// completed quests at twenty points each. The query's own comment says exactly this -- "folding
		// it into quests_completed would credit 500 completed quests for one bounty" -- which is why the
		// points-only kind exists. The delta on a counted kind is a COUNT.
		Kind:  trust.KindBountyBonus,
		Delta: 700,
		// No entity: a bounty is not attached to one, and migration 76's dedup index is
		// NULLS NOT DISTINCT precisely so an entity-less event can be recorded more than once.
	})
	require.NoError(t, err)
	assert.Equal(t, 4, levelOf(t, userID), "the bounty is points like any other")
}

// A SHORTFALL THAT CLEARS ITSELF NEVER DEMOTES. The member earns their standing back inside the grace
// period, so the level never moved and there is nothing to undo.
func TestAShortfallThatClearsInsideTheGraceStopsTheClock(t *testing.T) {
	userID := createDecayUser(t)
	svc := decayingTrust(testTime)
	contribute(t, svc, userID, 20)
	contribute(t, svc, userID, -2)
	require.Equal(t, 3, levelOf(t, userID))

	// Back over the line, still inside the grace.
	contribute(t, decayingTrust(testTime.Add(24*time.Hour)), userID, 2)
	shortSince, demotedAt := decayStateOf(t, userID)
	assert.Nil(t, shortSince, "the clock is stopped as soon as the member is back over their level")
	assert.Nil(t, demotedAt)

	// And a LATER shortfall starts a fresh grace rather than resuming the abandoned one: well past the
	// first clock's deadline, still TL3, with a new deadline ahead of it.
	later := testTime.Add(30 * 24 * time.Hour)
	contribute(t, decayingTrust(later), userID, -2)
	assert.Equal(t, 3, levelOf(t, userID), "a fresh grace, not the residue of the old one")
	shortSince, _ = decayStateOf(t, userID)
	require.NotNil(t, shortSince)
	sameInstant(t, later, shortSince, "dated from the SECOND shortfall, not the first")
}

// A PROMOTION IS IMMEDIATE, and takes no notice of grace or of a recovery window: there is no such
// thing as a bad moment on the way up.
func TestAPromotionIsImmediateAndIsNotHeldByAnything(t *testing.T) {
	userID := createDecayUser(t)
	svc := decayingTrust(testTime)
	contribute(t, svc, userID, 20)
	contribute(t, svc, userID, -2)
	atDeadline := testTime.Add(trust.GracePeriod)
	_, err := decayingTrust(atDeadline).SweepDecay(t.Context())
	require.NoError(t, err)
	require.Equal(t, 2, levelOf(t, userID), "fixture: demoted, and inside the recovery window")

	// Enough to reach TL4 -- 750 points, so 75 approvals all told. The window does not hold a member
	// DOWN any more than it holds them up.
	contribute(t, decayingTrust(atDeadline.Add(time.Hour)), userID, 75)
	assert.Equal(t, 4, levelOf(t, userID), "a promotion requires no deadline to pass")
}
