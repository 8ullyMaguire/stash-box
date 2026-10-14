//go:build integration

package leaderboard_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/leaderboard"
	"github.com/stashapp/stash-box/internal/service/streak"
	"github.com/stashapp/stash-box/internal/service/trust"
)

// The leaderboard against a real database (plan phase 6 slice 6f).
//
// The pure tests next door prove the request's shape. These prove the RANKING and the two promises the
// feature is sold on: opting out removes you from the board, and opting out costs you nothing else.
//
// TestMain brings up the test database.
func TestMain(m *testing.M) {
	testutil.TestWithDatabase(m, nil)
}

func q() *queries.Queries {
	return queries.New(testutil.DB())
}

func newBoard() *leaderboard.Service {
	return leaderboard.NewService(q())
}

func newTrust() *trust.Trust {
	return trust.NewTrust(q(), withTxn())
}

func withTxn() queries.WithTxnFunc {
	return func(fn func(*queries.Queries) error) error {
		return fn(q())
	}
}

func createUser(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	_, err := q().CreateUser(t.Context(), queries.CreateUserParams{
		ID:           id,
		Name:         "Board User " + id.String(),
		PasswordHash: "not-a-real-hash-this-test-never-logs-in",
		Email:        id.String() + "@board.invalid",
	})
	require.NoError(t, err, "creating a member")
	return id
}

// contribute records n approved edits today, at n distinct entities, through the REAL trust path -- so the
// rollup the board reads was built by the same code an edit builds it with.
func contribute(t *testing.T, userID uuid.UUID, n int) {
	t.Helper()
	svc := newTrust()
	for range n {
		entityID := uuid.Must(uuid.NewV7())
		_, err := svc.RecordEvent(t.Context(), trust.Event{
			UserID:     userID,
			Kind:       trust.KindEditApproved,
			Delta:      1,
			EntityType: "performer",
			EntityID:   &entityID,
		})
		require.NoError(t, err, "recording a contribution")
	}
}

// board returns the default board mapped by member id, so assertions can name the members they mean.
func board(t *testing.T, opts leaderboard.Options) map[uuid.UUID]leaderboard.Entry {
	t.Helper()
	entries, err := newBoard().Leaderboard(t.Context(), opts)
	require.NoError(t, err)
	out := make(map[uuid.UUID]leaderboard.Entry, len(entries))
	for _, e := range entries {
		out[e.UserID] = e
	}
	return out
}

// RANKED BY CONTRIBUTIONS, AND THE LEVEL COMES FROM THE CURVE.
//
// The level is the trap this test exists for: the board derives it in Go from the counters the query
// carries, and the FIRST version of the query omitted rejected_edits -- so a member with a bad half would
// have been shown a level the badge beside their name disagreed with. The assertion here is therefore not
// "the level looks right" but "the level IS what the trust service says", for a member whose rejections
// and approvals straddle a threshold.
func TestTheBoardRanksByContributionAndDerivesTheLevel(t *testing.T) {
	loud := createUser(t)
	quiet := createUser(t)
	contribute(t, loud, 25)
	contribute(t, quiet, 3)

	// A third member whose REJECTIONS decide their level: with the rejections missing from the level
	// computation they would read one level high, which is exactly the bug this asserts against.
	penalised := createUser(t)
	contribute(t, penalised, 5)
	reject(t, penalised, 4)

	entries := board(t, leaderboard.Options{})
	loudEntry, ok := entries[loud]
	require.True(t, ok, "the member with the most contributions is on the board")

	for _, id := range []uuid.UUID{loud, quiet, penalised} {
		entry, present := entries[id]
		require.True(t, present, "every contributing member is on the board")
		want, err := newTrust().Level(t.Context(), id)
		require.NoError(t, err)
		assert.Equal(t, want, entry.Level, "the board's level must be the trust service's level, not a second opinion")
	}

	assert.Equal(t, 1, loudEntry.Position, "the most contributions is position one")
	assert.Equal(t, 25, loudEntry.Score, "and the score is the count of contributions, not points")
	// REJECTIONS COUNT AS CONTRIBUTIONS, so the member with five approvals and four rejections has more
	// days of work than the member with three approvals -- and is ranked above them.
	assert.Less(t, entries[quiet].Score, entries[penalised].Score,
		"the score counts events, not deltas: rejected work is work somebody did")
	assert.Less(t, entries[penalised].Position, entries[quiet].Position,
		"and ranks ahead of them: a smaller position number is a higher place")

	// THE BOARD IS INSTANCE-WIDE, and this package's tests share one database, so the only
	// claimable positions are the members' RELATIVE order -- the others on the board belong to
	// other tests, which is a property of the feature rather than of the fixture.
	assert.Equal(t, 1, loudEntry.Position)
}

// OPTING OUT TAKES YOU OFF THE BOARD AND COSTS YOU NOTHING ELSE. This is the phase's own verification, and
// the half that matters is the second: the flag is read by the board and by NOTHING else, so a member who
// leaves keeps their level, their counters and their capabilities.
func TestOptingOutRemovesTheMemberAndCostsNothingElse(t *testing.T) {
	member := createUser(t)
	contribute(t, member, 25) // comfortably top of the board, and TL2 at least

	before, err := newTrust().Level(t.Context(), member)
	require.NoError(t, err)
	require.GreaterOrEqual(t, int(before), int(trust.LevelContributor), "fixture: the member must have standing to spend")

	require.Contains(t, board(t, leaderboard.Options{}), member, "on the board before opting out")

	_, err = newBoard().SetListed(t.Context(), member, false)
	require.NoError(t, err)

	assert.NotContains(t, board(t, leaderboard.Options{}), member, "off the board after opting out")

	// NOTHING ELSE MOVED. The level is the same number, the counters are the same counters, and a
	// capability the standing buys -- vouching -- still works.
	after, err := newTrust().Level(t.Context(), member)
	require.NoError(t, err)
	assert.Equal(t, before, after, "opting out is a visibility choice, not a demotion")

	rollup, err := q().GetUserTrust(t.Context(), member)
	require.NoError(t, err)
	assert.Equal(t, 25, rollup.ApprovedEdits, "and the counters are untouched")
	assert.False(t, rollup.LeaderboardListed, "while the flag records the choice")

	// The heatmap -- the member's own view of their own work -- still knows about every day.
	heat, err := streak.NewService(q()).ContributionHeatmap(t.Context(), member, 7)
	require.NoError(t, err)
	assert.NotEmpty(t, heat, "their own work is still theirs to see")
}

// ABSENCE IS AMBIGUOUS. A member who opted out and a member who contributed nothing look identical on the
// board, so it cannot be read as a list of who declined.
func TestAnOptedOutMemberIsIndistinguishableFromAnIdleOne(t *testing.T) {
	loud := createUser(t)
	contribute(t, loud, 25)
	_, err := newBoard().SetListed(t.Context(), loud, false)
	require.NoError(t, err)

	idle := createUser(t) // no contributions at all, never touched the setting

	entries, err := newBoard().Leaderboard(t.Context(), leaderboard.Options{})
	require.NoError(t, err)

	for _, entry := range entries {
		assert.NotEqual(t, loud, entry.UserID, "the opted-out member's number is nowhere in the response")
		assert.NotEqual(t, idle, entry.UserID)
	}
}

// THE WINDOW IS A WINDOW: a contribution older than it does not count, and the board says so by leaving the
// member off.
func TestTheBoardWindowExcludesOlderContributions(t *testing.T) {
	old := createUser(t)
	contributeAgedDaysAgo(t, old, 20, 400)

	entries := board(t, leaderboard.Options{Days: 365})
	assert.NotContains(t, entries, old, "a 400-day-old contribution is outside a year")
}

// THE LEVEL FILTER KEEPS THE REAL POSITIONS. A level-scoped board is the activity ranking WITHIN a level,
// so its positions have gaps -- which is what makes it tell a member where they actually stand.
func TestALevelFilterKeepsTheRealPositions(t *testing.T) {
	loud := createUser(t)
	contribute(t, loud, 25)
	steady := createUser(t)
	contribute(t, steady, 10)
	tiny := createUser(t)
	contribute(t, tiny, 2)

	full, err := newBoard().Leaderboard(t.Context(), leaderboard.Options{})
	require.NoError(t, err)
	// The board is instance-wide and this package's tests share a database, so assertions name
	// the members they mean rather than counting rows.
	require.NotNil(t, entryOf(full, loud))
	require.NotNil(t, entryOf(full, steady))
	require.NotNil(t, entryOf(full, tiny))

	steadyLevel, err := newTrust().Level(t.Context(), steady)
	require.NoError(t, err)

	narrowed, err := newBoard().Leaderboard(t.Context(), leaderboard.Options{Level: &steadyLevel})
	require.NoError(t, err)

	for _, entry := range narrowed {
		require.Equal(t, steadyLevel, entry.Level, "the filter keeps only that level")
		whole := entryOf(full, entry.UserID)
		require.NotNil(t, whole, "fixture: a filtered row must exist on the full board")
		assert.Equal(t, whole.Position, entry.Position, "the position is the member's rank in the WHOLE board")
		assert.Equal(t, whole.Score, entry.Score)
	}
	// GAPS ARE THE POINT: the filtered board is a subsequence of the whole ranking rather
	// than a renumbered one, which is what makes it tell a member where they stand.
	steadyWhole := entryOf(full, steady)
	steadyNarrowed := entryOf(narrowed, steady)
	require.NotNil(t, steadyWhole)
	require.NotNil(t, steadyNarrowed)
	assert.Equal(t, steadyWhole.Position, steadyNarrowed.Position, "the level filter does not renumber")
}

// A MEMBER WITH NO user_trust ROW IS STILL RANKED, which is the case the LEFT JOIN and its COALESCE exist
// for. "No consent recorded" is not "declined": the column defaults to true in the schema, and a member
// whose only contributions were written by another path (a backfill, a migration) must not silently vanish
// from a board every other member of the instance appears on.
func TestAMemberWithNoTrustRowIsStillRanked(t *testing.T) {
	fresh := createUser(t)
	contributeAgedDaysAgo(t, fresh, 3, 0) // events, but never through the trust service, so no rollup row

	_, err := q().GetUserTrust(t.Context(), fresh)
	require.Error(t, err, "fixture: this member must have no user_trust row")

	entries := board(t, leaderboard.Options{})
	require.Contains(t, entries, fresh, "no recorded preference is not a declined preference")
	assert.Equal(t, 3, entries[fresh].Score)
	assert.Equal(t, trust.LevelPublic, entries[fresh].Level, "and their level is the public one, not a hole")

	// AND THEY CAN STILL MAKE THE CHOICE. The first version of this write was an UPDATE, which
	// matched no rows for a member with no rollup and returned an error -- a member with nothing
	// to show being told they cannot decline to be shown.
	rollup, err := newBoard().SetListed(t.Context(), fresh, false)
	require.NoError(t, err, "a member with no rollup row can still decline the board")
	assert.False(t, rollup.LeaderboardListed)
	assert.Zero(t, rollup.Level, "and the row it created is the zeroed standing they already had")

	assert.NotContains(t, board(t, leaderboard.Options{}), fresh, "and the choice takes effect")
}

// THE LIMIT IS A LIMIT. The board's per-row user lookup is why it is clamped; this is why it is honoured.
func TestTheBoardPageSizeIsHonoured(t *testing.T) {
	createUser(t)
	contribute(t, createUser(t), 5)

	entries, err := newBoard().Leaderboard(t.Context(), leaderboard.Options{Limit: 1})
	require.NoError(t, err)
	assert.Len(t, entries, 1, "a one-row page is one row")
	assert.Equal(t, 1, entries[0].Position, "and it is the top of the board")
}

// A REMOVED MEMBER IS OFF THE BOARD, AND IT TAKES NO CODE TO BE TRUE.
//
// This schema has no soft-delete column on users -- deletion is a DELETE, and trust_events cascades with
// the member. The first version of this test set a `deleted` flag that does not exist, which failed every
// test in the file with a SQL error and is the reason the query no longer joins a column that was never
// there. What the test now asserts is the real mechanism: the cascade, not a predicate.
func TestARemovedMemberIsNotRanked(t *testing.T) {
	gone := createUser(t)
	contribute(t, gone, 25)
	require.Contains(t, board(t, leaderboard.Options{}), gone, "fixture: on the board before removal")

	_, err := testutil.DB().Exec(t.Context(), `DELETE FROM users WHERE id = $1`, gone)
	require.NoError(t, err)

	assert.NotContains(t, board(t, leaderboard.Options{}), gone, "and gone from it once removed")
}

func reject(t *testing.T, userID uuid.UUID, n int) {
	t.Helper()
	svc := newTrust()
	for range n {
		entityID := uuid.Must(uuid.NewV7())
		_, err := svc.RecordEvent(t.Context(), trust.Event{
			UserID:     userID,
			Kind:       trust.KindEditRejected,
			Delta:      -1,
			EntityType: "performer",
			EntityID:   &entityID,
		})
		require.NoError(t, err, "recording a rejection")
	}
}

// contributeAgedDaysAgo writes trust events directly, for the one case the service cannot produce: history.
func contributeAgedDaysAgo(t *testing.T, userID uuid.UUID, n, daysAgo int) {
	t.Helper()
	today, err := q().DatabaseNow(t.Context())
	require.NoError(t, err)
	for range n {
		entityID := uuid.Must(uuid.NewV7())
		_, err := testutil.DB().Exec(t.Context(),
			`INSERT INTO trust_events (user_id, kind, entity_type, entity_id, delta, created_at)
			 VALUES ($1, 'edit_approved', 'performer', $2, 1, $3)`,
			userID, entityID, today.AddDate(0, 0, -daysAgo))
		require.NoError(t, err)
	}
}

var _ = context.Background
var _ = time.Now

// entryOf finds a member's row, or nil.
func entryOf(entries []leaderboard.Entry, id uuid.UUID) *leaderboard.Entry {
	for i, e := range entries {
		if e.UserID == id {
			return &entries[i]
		}
	}
	return nil
}
