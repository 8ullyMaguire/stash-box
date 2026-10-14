//go:build integration

package streak_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/streak"
)

// The freeze WRITE path against a real database (SPEC §5.3, plan phase 6 slice 6c).
//
// The pure tests in streak_test.go prove the walk and the day choice are right. These prove the one
// thing that cannot be checked there: that spending a freeze RECORDS it, that the derived numbers move
// as a result, and that the refusals are refusals rather than silent successes.
//
// TestMain brings up the test database.
func TestMain(m *testing.M) {
	testutil.TestWithDatabase(m, nil)
}

// referenceDay is a fixed date. Every fixture day is an offset from it, so no assertion depends on
// when the test runs -- and it is midday UTC so a backdated event cannot land on the day either side
// of the one intended.
var referenceDay = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

func q() *queries.Queries {
	return queries.New(testutil.DB())
}

func newStreak() *streak.Service {
	return streak.NewService(q())
}

func createUser(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	_, err := q().CreateUser(t.Context(), queries.CreateUserParams{
		ID:           id,
		Name:         "Freeze Test User " + id.String(),
		PasswordHash: "not-a-real-hash-this-test-never-logs-in",
		Email:        id.String() + "@freeze.invalid",
	})
	require.NoError(t, err, "creating the member under test")
	return id
}

// contributeOn records events on given days, backdated.
//
// It writes trust_events DIRECTLY rather than through the trust service, because the service stamps
// events with the database's clock and this test needs history: a streak is a fact about days that have
// already happened, and there is no way to have a month of them without saying so. Each event gets its
// own entity id, because migration 76's dedup index is NULLS NOT DISTINCT and two entity-less events of
// one kind are a duplicate by design.
func contributeOn(t *testing.T, userID uuid.UUID, offsets ...int) {
	t.Helper()
	for _, offset := range offsets {
		entityID := uuid.Must(uuid.NewV7())
		day := referenceDay.AddDate(0, 0, -offset)
		_, err := testutil.DB().Exec(t.Context(),
			`INSERT INTO trust_events (user_id, kind, entity_type, entity_id, delta, created_at)
			 VALUES ($1, 'edit_approved', 'performer', $2, 1, $3)`,
			userID, entityID, day)
		require.NoError(t, err, "backdating an event %d days before the reference day", offset)
	}
}

// A member with a month of contributions and a gap can repair the gap, once, and the numbers say so.
func TestSpendingAFreezeRepairsTheRunAndIsRecorded(t *testing.T) {
	userID := createUser(t)

	// Thirty distinct CONTRIBUTING days -- thirty-one offsets minus the gap -- which is exactly the
	// threshold for one freeze. (The first version used 0..29 and got 29 active days, so the member had
	// earned nothing and the test failed on its own arithmetic rather than on the feature.)
	var offsets []int
	for offset := 0; offset < 31; offset++ {
		if offset != 3 {
			offsets = append(offsets, offset)
		}
	}
	contributeOn(t, userID, offsets...)
	require.Len(t, offsets, 30, "fixture: the contributing days must be exactly the earn threshold")

	before, err := newStreak().For(t.Context(), userID, referenceDay)
	require.NoError(t, err)
	assert.Equal(t, 1, before.FreezesEarned, "thirty contributing days earn one freeze")
	assert.Equal(t, 0, before.FreezesUsed)
	assert.Equal(t, 1, before.FreezesAvailable)
	assert.Equal(t, 3, before.CurrentStreak, "the run stops at the gap")
	assert.Equal(t, 30, before.TotalActiveDays)

	after, err := newStreak().SpendFreeze(t.Context(), userID, referenceDay)
	require.NoError(t, err)
	assert.Equal(t, 1, after.FreezesUsed, "the spend is recorded")
	assert.Equal(t, 0, after.FreezesAvailable, "and the allowance is spent")
	assert.Equal(t, 30, after.TotalActiveDays, "the covered day is NOT a day of activity")
	assert.Equal(t, len(offsets)+1, after.CurrentStreak, "the covering day joins every offset into one run")

	// THE DERIVATION AGREES WITH ITSELF ON A LATER READ. The repair is a fact in the table, not a
	// return value, so a fresh call has to reach the same answer.
	again, err := newStreak().For(t.Context(), userID, referenceDay)
	require.NoError(t, err)
	assert.Equal(t, after, again, "reading again gives the same streak, including the allowance")
}

// NOTHING TO COVER is its own answer, distinct from having none left.
func TestAFreezeIsRefusedWhenThereIsNothingToRepair(t *testing.T) {
	userID := createUser(t)
	// Thirty consecutive days: a full window of activity and a freeze available.
	var offsets []int
	for offset := 0; offset < 30; offset++ {
		offsets = append(offsets, offset)
	}
	contributeOn(t, userID, offsets...)

	before, err := newStreak().For(t.Context(), userID, referenceDay)
	require.NoError(t, err)
	require.Equal(t, 1, before.FreezesAvailable, "fixture: the member must be able to afford one")

	_, err = newStreak().SpendFreeze(t.Context(), userID, referenceDay)
	assert.ErrorIs(t, err, streak.ErrNothingToCover, "a member with no missed day is told exactly that")

	// And nothing was spent: the refusal did not consume the allowance.
	after, err := newStreak().For(t.Context(), userID, referenceDay)
	require.NoError(t, err)
	assert.Equal(t, 1, after.FreezesAvailable)
	assert.Equal(t, 0, after.FreezesUsed)
}

// A member who has not earned one is refused with the OTHER error, and the two are not interchangeable.
func TestAFreezeIsRefusedWhenNoneIsEarned(t *testing.T) {
	userID := createUser(t)
	contributeOn(t, userID, 0, 1, 2, 4) // a gap, but only four contributing days

	before, err := newStreak().For(t.Context(), userID, referenceDay)
	require.NoError(t, err)
	assert.Equal(t, 0, before.FreezesEarned, "four days is not a month")
	assert.Equal(t, 0, before.FreezesAvailable)

	_, err = newStreak().SpendFreeze(t.Context(), userID, referenceDay)
	assert.ErrorIs(t, err, streak.ErrNoFreezes)
	assert.NotErrorIs(t, err, streak.ErrNothingToCover,
		"the two refusals must not be the same error: one accuses a member of spending, the other of nothing")
}

// THE RUN SURVIVES THE IDLE DAY ITSELF. Yesterday was missed and today has not happened yet -- the case
// the freeze exists for -- and covering yesterday keeps the run alive rather than repairing it after
// the fact.
func TestAFreezeCoversYesterdayWithoutBreakingTheGrace(t *testing.T) {
	userID := createUser(t)
	// Active on days 2..31 and NOT today: yesterday (offset 1) is the miss, and the day before
	// yesterday is the most recent activity. Two days back is a BROKEN run -- a streak survives being
	// idle for today only -- so this is the case a freeze exists for: the run is already dead and the
	// member revives it rather than preserving it.
	var offsets []int
	for offset := 2; offset < 32; offset++ {
		offsets = append(offsets, offset)
	}
	contributeOn(t, userID, offsets...)
	require.Len(t, offsets, 30, "fixture: enough days to have earned a freeze")

	before, err := newStreak().For(t.Context(), userID, referenceDay)
	require.NoError(t, err)
	require.Equal(t, 1, before.FreezesAvailable, "fixture: the member must be able to afford one")
	assert.Equal(t, 0, before.CurrentStreak, "the run is already dead: the last activity was two days ago")
	assert.False(t, before.ActiveToday, "and nothing happened today")

	after, err := newStreak().SpendFreeze(t.Context(), userID, referenceDay)
	require.NoError(t, err)
	// The covered yesterday, plus every offset: the run is the fixture, and deriving the expectation
	// from the fixture is what keeps a fixture change from reading as a feature bug.
	assert.Equal(t, len(offsets)+1, after.CurrentStreak, "covering yesterday brings the dead run back")
	assert.NotNil(t, after.LastActiveDay)
	assert.False(t, after.ActiveToday, "and today is still not an active day: nothing happened today")
}

var _ = context.Background
