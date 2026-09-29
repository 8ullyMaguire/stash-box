//go:build integration

package api_test

import (
	"slices"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/config"
	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/models"
)

// docs/SPEC.md §8.1: ApplyEdit promoted the edit author's vote rights in a bare
// `go func()`.
//
// Why nothing caught it, and why these tests are shaped the way they are:
//
//   - `go test -race ./internal/service/edit/` passes on the UNFIXED code. That
//     package has no concurrent test reaching this path, so a clean race run was
//     never evidence. A test that cannot observe the defect is not a test.
//   - Reading roles through the GraphQL resolver would be wrong even if the
//     goroutine were the only problem: `userResolver.Roles` goes through
//     `dataloader.For(ctx).UserRolesByID`, which caches per request. A cached
//     read would return the pre-promotion value no matter what the goroutine
//     did, so the test would pass for the wrong reason AND miss the race.
//     These tests therefore read the roles table directly through dbtest.
//
// The fix: call PromoteUserVoteRights synchronously with the request context,
// so the promotion is complete by the time Apply returns. That turns the race
// into a deterministic assertion rather than a flaky one.

// withVotePromotionThreshold sets the threshold for one test. The config setter
// returns a restore func so a test cannot leak the value into the next one.
func withVotePromotionThreshold(t *testing.T, n int) {
	t.Helper()
	restore := config.SetVotePromotionThresholdForTest(n)
	t.Cleanup(restore)
}

// userRoles reads a user's roles straight from the database, bypassing the
// per-request dataloader cache. Without this the whole file is theatre.
//
// Factory.User().GetRoles is existing production API, not something added for
// the test: it reads the roles table on every call and caches nothing, which is
// precisely the property needed here.
func userRoles(t *testing.T, userID uuid.UUID) []models.RoleEnum {
	t.Helper()
	roles, err := dbtest.Factory().User().GetRoles(t.Context(), userID)
	require.NoError(t, err, "reading roles directly from the database")
	return roles
}

func userHasRole(t *testing.T, userID uuid.UUID, role models.RoleEnum) bool {
	t.Helper()
	return slices.Contains(userRoles(t, userID), role)
}

func countUserRole(t *testing.T, userID uuid.UUID, role models.RoleEnum) int {
	t.Helper()
	n := 0
	for _, got := range userRoles(t, userID) {
		if got == role {
			n++
		}
	}
	return n
}

// nonVotingAuthor creates a user who cannot vote yet, as its own test runner.
//
// NOT ReadOnly, and that is load-bearing. PromoteUserVoteRights returns nil
// immediately on a ReadOnly role:
//
//	for _, role := range roles {
//	    if role == models.RoleEnumReadOnly {
//	        return nil
//	    }
//
// so a read-only user is never promoted, by design. The first draft of this
// test used ReadOnly as the "cannot vote yet" role and therefore asserted the
// exact opposite of the code, failing for a reason that had nothing to do with
// the race. A user with no roles at all is the right starting point: they lack
// Vote, so the promotion path is actually reachable.
func nonVotingAuthor(t *testing.T) (*testRunner, *models.User) {
	t.Helper()

	// See the note in the existing-voter test: roles go on the input.
	suffix := uuid.Must(uuid.NewV7()).String()
	// The roles argument is an EMPTY slice, not nil, on purpose:
	// createTestUser defaults a nil argument to Admin, and Admin implies Vote,
	// so the author would be promoted trivially and the test would pass without
	// exercising the threshold at all.
	user, err := asAdmin(t).createTestUser(
		&models.UserCreateInput{
			Name:     "promotion_author_" + suffix,
			Email:    "promotion_author_" + suffix + "@example.com",
			Password: "aValidPassword1!",
		},
		[]models.RoleEnum{},
	)
	require.NoError(t, err)
	require.False(t, userHasRole(t, user.ID, models.RoleEnumVote),
		"the author must not start with vote rights (#8.1)")

	author := createTestRunner(t, user, []models.RoleEnum{})
	return author, user
}

// TestVotePromotionIsCompleteWhenApplyReturns is the regression test for §8.1.
//
// A non-voting author with `threshold` accepted edits must hold the vote role BY
// THE TIME ApplyEdit RETURNS. The assertion is taken immediately after the call
// with no sleep, no retry and no Eventually, which is precisely what makes it
// deterministic: with the old `go func()` the grant happens at an unspecified
// later time, so this fails every run instead of intermittently.
func TestVotePromotionIsCompleteWhenApplyReturns(t *testing.T) {
	withVotePromotionThreshold(t, 1)

	author, authorUser := nonVotingAuthor(t)

	// Guard: the author must NOT start with vote rights, or promotion is not
	// what is being observed and the test would pass vacuously.
	assert.False(t, userHasRole(t, authorUser.ID, models.RoleEnumVote),
		"the author must start WITHOUT the vote role (#8.1)")

	name := author.generatePerformerName()
	created, err := author.createTestPerformerEdit(
		models.OperationEnumCreate,
		&models.PerformerEditDetailsInput{Name: &name},
		nil,
		nil,
	)
	require.NoError(t, err)
	require.True(t, created.UserID.Valid, "the created edit must have an author")
	require.Equal(t, authorUser.ID, created.UserID.UUID,
		"the edit must be owned by the author, or the promotion targets someone else")

	applied, err := author.approveEdit(created.ID)
	require.NoError(t, err)
	require.True(t, applied.Applied,
		"the edit must actually apply, or no promotion path is exercised")

	// The deterministic assertion. No polling, no sleep.
	assert.True(t, userHasRole(t, authorUser.ID, models.RoleEnumVote),
		"the author must hold the vote role by the time ApplyEdit returns; "+
			"a `go func()` defers the grant to an unspecified later time and "+
			"makes every ApplyEdit caller racy (SPEC §8.1)")
}

// TestVotePromotionGrantsRoleExactlyOnce pins the second half.
//
// PromoteUserVoteRights reads the roles and then inserts, which is not atomic.
// A user can be promoted by several applies in a row, and the insert must not
// produce a duplicate. The roles table is keyed on (user, role), so this is
// mostly belt-and-braces -- but "mostly" is the word that hides a bug, and the
// count is the only thing that distinguishes a single grant from several.
func TestVotePromotionGrantsRoleExactlyOnce(t *testing.T) {
	withVotePromotionThreshold(t, 1)

	author, authorUser := nonVotingAuthor(t)

	// Two separate approved edits by the same author. Each apply runs the
	// promotion path, so without an idempotency check the second one would
	// insert again.
	for range 2 {
		name := author.generatePerformerName()
		created, err := author.createTestPerformerEdit(
			models.OperationEnumCreate,
			&models.PerformerEditDetailsInput{Name: &name},
			nil,
			nil,
		)
		require.NoError(t, err)

		applied, err := author.approveEdit(created.ID)
		require.NoError(t, err)
		require.True(t, applied.Applied)
	}

	assert.Equal(t, 1, countUserRole(t, authorUser.ID, models.RoleEnumVote),
		"the vote role must be granted exactly once across repeated applies "+
			"(SPEC §8.1: read-then-insert is not atomic)")
}

// TestVotePromotionIsSkippedForExistingVoters is the "leave it alone" case.
//
// PromoteUserVoteRights returns early when the user already implies Vote, so a
// user who already votes must not get a second row, and must not be demoted or
// otherwise touched. Without this, a bug that re-inserts on every apply would
// pass the tests above by accident.
func TestVotePromotionIsSkippedForExistingVoters(t *testing.T) {
	withVotePromotionThreshold(t, 1)

	// This author ALREADY has vote rights.
	// Roles must be set on the INPUT, not just passed as the second argument:
	// createTestUser copies its `roles` argument into input.Roles only when
	// input is nil, so a non-nil input without Roles creates a user with none.
	// That is not a quirk to work around silently -- it is the exact reason an
	// earlier draft of this test asserted against a user who had no vote role
	// at all, and therefore proved nothing.
	suffix := uuid.Must(uuid.NewV7()).String()
	user, err := asAdmin(t).createTestUser(
		&models.UserCreateInput{
			Name:     "existing_voter_" + suffix,
			Email:    "existing_voter_" + suffix + "@example.com",
			Password: "aValidPassword1!",
			Roles:    []models.RoleEnum{models.RoleEnumVote},
		},
		[]models.RoleEnum{models.RoleEnumVote},
	)
	require.NoError(t, err)
	require.True(t, userHasRole(t, user.ID, models.RoleEnumVote),
		"this test needs a user who ALREADY votes; without it the case is "+
			"identical to the promotion test and proves nothing")

	author := createTestRunner(t, user, []models.RoleEnum{models.RoleEnumVote})

	before := len(userRoles(t, user.ID))

	name := author.generatePerformerName()
	created, err := author.createTestPerformerEdit(
		models.OperationEnumCreate,
		&models.PerformerEditDetailsInput{Name: &name},
		nil,
		nil,
	)
	require.NoError(t, err)

	applied, err := author.approveEdit(created.ID)
	require.NoError(t, err)
	require.True(t, applied.Applied)

	assert.Len(t, userRoles(t, user.ID), before,
		"a user who already votes must have their roles left exactly as they "+
			"were (#8.1): before=%d after=%d", before, len(userRoles(t, user.ID)))
	assert.Equal(t, 1, countUserRole(t, user.ID, models.RoleEnumVote),
		"an existing voter must not gain a duplicate vote role (#8.1)")
}
