//go:build integration

package seed_test

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/seed"
	"github.com/stashapp/stash-box/internal/service/trust"
)

// SEED COHORTS AND CAMPAIGNS against a real database (SPEC §11, plan phase 11 slice 11d).
//
// Four claims define the slice, and every one of them is a claim about who did NOT get paid:
//
//  1. THE CAMPAIGN PAYS ITS MEMBERS AND NOBODY ELSE. A non-member with an identical history gains
//     nothing -- asserted as an absolute (no event of the kind exists for them), not as "their number
//     did not change", because a member's own number moving is what the first half proves.
//  2. RUNNING IT TWICE PAYS THE COHORT ONCE. The second run reports the members as SKIPPED rather than
//     paying them, and the row count is the proof.
//  3. A MEMBER WHO LEFT BEFORE THE RUN IS NOT PAID. Membership is read when the campaign runs, so this
//     is true by construction rather than by a check -- which is exactly what a test has to be able to
//     tell apart from a check that happens to work.
//  4. THE HEAD START IS POINTS, NOT A LEVEL. The level afterwards is the one curve applied to the
//     stored counters, so a campaign cannot hand out standing that nothing earned.
//
// And the fifth, which is the reason the other four are shaped this way: A COHORT GRANTS NOTHING. Being
// added to one changes no role and no permission, and the test asserts it.
//
// TestMain brings up the test database.
func TestMain(m *testing.M) {
	testutil.TestWithDatabase(m, nil)
}

func q() *queries.Queries {
	return queries.New(testutil.DB())
}

func withTxn() queries.WithTxnFunc {
	return func(fn func(*queries.Queries) error) error {
		return fn(q())
	}
}

func newTrust() *trust.Trust {
	return trust.NewTrust(q(), withTxn())
}

func newSeed() *seed.Service {
	return seed.NewService(q(), withTxn(), newTrust())
}

func createUser(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	_, err := q().CreateUser(t.Context(), queries.CreateUserParams{
		ID:           id,
		Name:         "Seed Member " + id.String(),
		PasswordHash: "not-a-real-hash-this-test-never-logs-in",
		Email:        id.String() + "@seed.invalid",
	})
	require.NoError(t, err, "creating a member")
	return id
}

// bonusOf is the points-only column: the one place a campaign's head start lands.
func bonusOf(t *testing.T, userID uuid.UUID) int {
	t.Helper()
	row, err := q().GetUserTrust(t.Context(), userID)
	if err != nil {
		return 0
	}
	return row.BonusPoints
}

func levelOf(t *testing.T, userID uuid.UUID) int {
	t.Helper()
	row, err := q().GetUserTrust(t.Context(), userID)
	if err != nil {
		return 0
	}
	return row.Level
}

// fastTrackEvents counts the campaign's events FOR ONE MEMBER. Read straight from the log rather than
// from a counter, because the log is what deduplication acts on.
func fastTrackEvents(t *testing.T, userID uuid.UUID) int {
	t.Helper()
	var count int
	require.NoError(t, testutil.DB().QueryRow(t.Context(),
		`select count(*) from trust_events where user_id = $1 and kind = 'seed_fast_track'`, userID).Scan(&count))
	return count
}

func campaignRow(t *testing.T, id uuid.UUID) queries.SeedCampaign {
	t.Helper()
	campaign, err := newSeed().Campaign(t.Context(), id)
	require.NoError(t, err)
	return *campaign
}

// aCohortWith makes a cohort and puts the given accounts in it.
func aCohortWith(t *testing.T, name string, members ...uuid.UUID) uuid.UUID {
	t.Helper()
	cohort, err := newSeed().CreateCohort(t.Context(), name, nil)
	require.NoError(t, err, "creating a cohort")

	for _, member := range members {
		_, err := newSeed().AddMembers(t.Context(), cohort.ID, []uuid.UUID{member})
		require.NoError(t, err, "adding a member")
	}
	return cohort.ID
}

// ---------------------------------------------------------------- 1. the cohort and nobody else

func TestTheCampaignPaysItsMembersAndNobodyElse(t *testing.T) {
	member := createUser(t)
	outsider := createUser(t)
	// THE OPERATOR IS NOT A MEMBER, and that is deliberate: a fixture where the person who runs the
	// campaign is also the person it pays cannot tell "paid the cohort" apart from "paid the caller".
	operator := createUser(t)
	cohortID := aCohortWith(t, "the-first-hundred", member)

	campaign, err := newSeed().CreateCampaign(t.Context(), cohortID, "welcome-head-start", 100, operator)
	require.NoError(t, err)

	result, err := newSeed().RunCampaign(t.Context(), campaign.ID, operator)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Members, "one member in the cohort when it ran")
	assert.Equal(t, 1, result.Paid)
	assert.Equal(t, 0, result.Skipped)

	assert.Equal(t, 100, bonusOf(t, member), "the head start is the campaign's points")
	assert.Equal(t, 1, fastTrackEvents(t, member), "paid exactly once, as one event")

	// THE ABSOLUTE, NOT THE DELTA. An outsider's points being unchanged would also be true of a
	// campaign that paid nobody at all, so this asserts the event does not EXIST for them.
	assert.Equal(t, 0, fastTrackEvents(t, outsider), "a non-member was not paid")
	assert.Equal(t, 0, bonusOf(t, outsider))
	// Nor was the operator, who is not in the cohort: running a campaign is not joining it.
	assert.Equal(t, 0, fastTrackEvents(t, operator), "the operator was not paid")
	assert.Equal(t, 0, bonusOf(t, operator))
}

// ---------------------------------------------------------------- 4. points, not a level

func TestTheHeadStartIsPointsAndTheLevelFollowsTheOneCurve(t *testing.T) {
	member := createUser(t)
	cohortID := aCohortWith(t, "level-curve", member)

	// 100 points on a member with no other contribution. The level is not asserted against a number
	// written here: it is asserted against THE CURVE APPLIED TO THE STORED COUNTERS, which is what
	// "not an override" means and what would still hold if the curve were retuned.
	campaign, err := newSeed().CreateCampaign(t.Context(), cohortID, "head-start", 100, member)
	require.NoError(t, err)
	_, err = newSeed().RunCampaign(t.Context(), campaign.ID, member)
	require.NoError(t, err)

	row, err := q().GetUserTrust(t.Context(), member)
	require.NoError(t, err)

	honest := trust.LevelForTotals(trust.Totals{
		ApprovedEdits:        row.ApprovedEdits,
		RejectedEdits:        row.RejectedEdits,
		IdentificationSolves: row.IdentificationSolves,
		QuestsCompleted:      row.QuestsCompleted,
		ReplicasHosted:       row.ReplicasHosted,
		BonusPoints:          row.BonusPoints,
	})

	assert.Equal(t, int(honest), row.Level,
		"the level is the curve applied to the counters, not a value the campaign set")
	assert.Greater(t, row.Level, 0, "100 points is above the floor, or this proves nothing")
}

// ---------------------------------------------------------------- 2. twice is once

func TestRunningTheCampaignTwicePaysTheCohortOnce(t *testing.T) {
	member := createUser(t)
	cohortID := aCohortWith(t, "paid-once", member)

	campaign, err := newSeed().CreateCampaign(t.Context(), cohortID, "head-start", 40, member)
	require.NoError(t, err)

	first, err := newSeed().RunCampaign(t.Context(), campaign.ID, member)
	require.NoError(t, err)
	require.Equal(t, 1, first.Paid)

	second, err := newSeed().RunCampaign(t.Context(), campaign.ID, member)
	require.NoError(t, err)
	assert.Equal(t, 1, second.Members, "the cohort is still one member")
	assert.Equal(t, 0, second.Paid, "the second run paid nobody")
	assert.Equal(t, 1, second.Skipped, "and says so rather than erroring")

	assert.Equal(t, 40, bonusOf(t, member), "40 points, not 80")
	assert.Equal(t, 1, fastTrackEvents(t, member), "one event, because the dedup index refused the second")

	// The run is RECORDED as having happened a second time. "Never run" and "run again and paid nobody
	// new" are different states, and an operator reads this column to tell them apart.
	assert.NotNil(t, campaignRow(t, campaign.ID).LastRunAt)
}

// ---------------------------------------------------------------- 3. membership is read now

func TestAMemberWhoLeftBeforeTheRunIsNotPaid(t *testing.T) {
	stayer := createUser(t)
	leaver := createUser(t)
	cohortID := aCohortWith(t, "who-is-in-it-now", stayer, leaver)

	campaign, err := newSeed().CreateCampaign(t.Context(), cohortID, "head-start", 25, stayer)
	require.NoError(t, err)

	removed, err := newSeed().RemoveMember(t.Context(), cohortID, leaver)
	require.NoError(t, err)
	require.True(t, removed, "the fixture has to have removed somebody")

	result, err := newSeed().RunCampaign(t.Context(), campaign.ID, stayer)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Members, "the cohort as it stands, not as it was")
	assert.Equal(t, 1, result.Paid)

	assert.Equal(t, 0, fastTrackEvents(t, leaver), "somebody who left is not paid")
	assert.Equal(t, 25, bonusOf(t, stayer))

	// And removing them did NOT take back what a previous run gave them: the ledger is not a roster.
	// (Paid above, then removed, then run again: the event is still there and there is no second one.)
	_, err = newSeed().RemoveMember(t.Context(), cohortID, stayer)
	require.NoError(t, err)
	after, err := newSeed().RunCampaign(t.Context(), campaign.ID, stayer)
	require.NoError(t, err)
	assert.Equal(t, 0, after.Members)
	assert.Equal(t, 25, bonusOf(t, stayer), "the points a run paid are not a roster entry")
}

// ---------------------------------------------------------------- and the resumable direction

func TestAMemberAddedLaterIsPaidByTheNextRun(t *testing.T) {
	first := createUser(t)
	cohortID := aCohortWith(t, "grows-later", first)

	campaign, err := newSeed().CreateCampaign(t.Context(), cohortID, "head-start", 30, first)
	require.NoError(t, err)
	_, err = newSeed().RunCampaign(t.Context(), campaign.ID, first)
	require.NoError(t, err)

	late := createUser(t)
	_, err = newSeed().AddMembers(t.Context(), cohortID, []uuid.UUID{late})
	require.NoError(t, err)

	result, err := newSeed().RunCampaign(t.Context(), campaign.ID, first)
	require.NoError(t, err)
	assert.Equal(t, 2, result.Members)
	assert.Equal(t, 1, result.Paid, "only the member who did not have it")
	assert.Equal(t, 1, result.Skipped)
	assert.Equal(t, 30, bonusOf(t, late))
}

// ---------------------------------------------------------------- two campaigns, two payments

// ONE CAMPAIGN'S PAYMENT MUST NOT SHADOW ANOTHER'S. Both campaigns pay the same member with the same
// kind, so the only thing keeping their slots apart is the campaign's id in the event's entity_id. Fold
// that id away and the second campaign's payment reads as a duplicate of the first and is silently
// dropped -- a member loses points and nothing anywhere says so.
func TestTwoCampaignsOverOneCohortEachPayOnce(t *testing.T) {
	member := createUser(t)
	cohortID := aCohortWith(t, "two-campaigns", member)

	first, err := newSeed().CreateCampaign(t.Context(), cohortID, "first", 10, member)
	require.NoError(t, err)
	second, err := newSeed().CreateCampaign(t.Context(), cohortID, "second", 15, member)
	require.NoError(t, err)

	_, err = newSeed().RunCampaign(t.Context(), first.ID, member)
	require.NoError(t, err)
	run, err := newSeed().RunCampaign(t.Context(), second.ID, member)
	require.NoError(t, err)

	assert.Equal(t, 1, run.Paid, "the second campaign pays its own member")
	assert.Equal(t, 25, bonusOf(t, member), "10 + 15, both campaigns' points")
	assert.Equal(t, 2, fastTrackEvents(t, member), "one event per campaign")

	// And re-running BOTH pays neither again.
	for _, id := range []uuid.UUID{first.ID, second.ID} {
		again, err := newSeed().RunCampaign(t.Context(), id, member)
		require.NoError(t, err)
		assert.Zero(t, again.Paid)
	}
	assert.Equal(t, 25, bonusOf(t, member), "still 25")
}

// ---------------------------------------------------------------- the roster's own arithmetic

func TestAddingAMemberWhoIsAlreadyThereCountsForNothing(t *testing.T) {
	member := createUser(t)
	cohortID := aCohortWith(t, "roster-count")

	added, err := newSeed().AddMembers(t.Context(), cohortID, []uuid.UUID{member})
	require.NoError(t, err)
	assert.Equal(t, 1, added, "one account was added")

	added, err = newSeed().AddMembers(t.Context(), cohortID, []uuid.UUID{member})
	require.NoError(t, err)
	assert.Zero(t, added, "the second add changed nothing, and must not report that it did")

	// Removing somebody who is not a member is reported as such rather than counted as a removal.
	removed, err := newSeed().RemoveMember(t.Context(), cohortID, createUser(t))
	require.NoError(t, err)
	assert.False(t, removed, "they were never in it")

	assert.Equal(t, 1, mustCountMembers(t, cohortID))
}

func mustCountMembers(t *testing.T, cohortID uuid.UUID) int {
	t.Helper()
	count, err := newSeed().MemberCount(t.Context(), cohortID)
	require.NoError(t, err)
	return count
}

// ---------------------------------------------------------------- a cohort grants nothing

func TestACohortGrantsNothingByExisting(t *testing.T) {
	member := createUser(t)

	// Read through the helpers rather than GetUserTrust: a fresh account has NO trust row at all (it
	// is created by the first event), and "no row" is zero points rather than an error.
	levelBefore, bonusBefore := levelOf(t, member), bonusOf(t, member)

	cohortID := aCohortWith(t, "grants-nothing", member)

	assert.Equal(t, levelBefore, levelOf(t, member), "being listed in a cohort is not standing")
	assert.Equal(t, bonusBefore, bonusOf(t, member), "and not points")
	assert.NotNil(t, cohortID)

	// And the row is not created by the membership either: nothing at all was written for this member.
	var rows int
	require.NoError(t, testutil.DB().QueryRow(t.Context(),
		`select count(*) from trust_events where user_id = $1`, member).Scan(&rows))
	assert.Zero(t, rows, "a cohort writes no ledger row")

	// The cohort grants no ROLE either. Roles are permissions and this is a group of people; a cohort
	// that widened an account's reach would be a permission granted by a membership list.
	var roles int
	require.NoError(t, testutil.DB().QueryRow(t.Context(),
		`select count(*) from user_roles where user_id = $1`, member).Scan(&roles))
	assert.Zero(t, roles, "a cohort is not a role")
}

// ---------------------------------------------------------------- the head start has to be a head start

func TestACampaignHasToBeWorthSomething(t *testing.T) {
	member := createUser(t)
	cohortID := aCohortWith(t, "positive-only", member)

	_, err := newSeed().CreateCampaign(t.Context(), cohortID, "no-op", 0, member)
	assert.Error(t, err, "a zero head start would record an event that moves nothing")

	_, err = newSeed().CreateCampaign(t.Context(), cohortID, "negative", -50, member)
	assert.Error(t, err, "a campaign that takes standing away is a different feature")

	_, err = newSeed().CreateCampaign(t.Context(), uuid.Must(uuid.NewV7()), "homeless", 10, member)
	assert.ErrorIs(t, err, seed.ErrNoCohort, "a campaign pays a cohort that exists")
}
