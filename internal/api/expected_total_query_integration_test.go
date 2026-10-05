//go:build integration

package api_test

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/queries"
)

// These tests call the sqlc-GENERATED queries, not a copy of the SQL pasted into
// the test file.
//
// That distinction is the whole point of this file. The sibling tests in this
// package embed their own SQL, which means they keep passing when the real query
// file is wrong: mutating `expected_total.sql` from `NOT st.deleted` to
// `st.deleted IS NULL` -- the defect this feature actually shipped once -- left
// every one of them green, because none of them read the file. A test that
// re-types the query under test has tested the typing.
//
// These go through `queries.New(...)`, the same construction the other
// integration tests in this package use. A wrong column, a bad filter or an
// unparseable placeholder in the query file shows up here and nowhere else.

func queriesFor(t *testing.T) *queries.Queries {
	t.Helper()
	return queries.New(testutil.DB())
}

func TestGeneratedListStudioCompleteness(t *testing.T) {
	pool := testutil.DB()
	ctx := t.Context()
	q := queriesFor(t)

	userID := seedUserRow(t, "gen-race-asserter")

	// 2 of 400: the largest absolute gap.
	bigGap := seedStudioRow(t, "gen-big-gap")
	seedScenes(t, bigGap, 2)
	assertTotalFor(t, bigGap, 400, userID)

	// 40 of 60: the largest ratio, the SMALLEST absolute gap. This is the studio
	// a ratio-ordered board would wrongly promote above bigGap.
	smallGap := seedStudioRow(t, "gen-small-gap")
	seedScenes(t, smallGap, 40)
	assertTotalFor(t, smallGap, 60, userID)

	// Holds scenes, has no denominator: must not appear on the race at all.
	uncounted := seedStudioRow(t, "gen-uncounted")
	seedScenes(t, uncounted, 7)

	// A soft-deleted studio with a denominator: a merged-away studio's claim is
	// a claim about a record that no longer exists, and leaving it on the board
	// puts a permanent unclosable gap at the top of a race nobody can win.
	deleted := seedStudioRow(t, "gen-deleted")
	seedScenes(t, deleted, 3)
	assertTotalFor(t, deleted, 300, userID)
	_, err := pool.Exec(ctx, `UPDATE studios SET deleted = true WHERE id = $1`, deleted)
	require.NoError(t, err)

	rows, err := q.ListStudioCompleteness(ctx, queries.ListStudioCompletenessParams{
		LimitCount: 200, LimitOffset: 0,
	})
	require.NoError(t, err,
		"the generated query must run; a placeholder or column error in "+
			"expected_total.sql surfaces here and nowhere else")

	ids := make([]string, 0, len(rows))
	uuids := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.StudioID.String())
		uuids = append(uuids, r.StudioID)
	}

	assert.NotContains(t, ids, uncounted,
		"a studio with no denominator has nothing to race against; an inner JOIN "+
			"keeps it off the board, where a NULL score would make it look like a "+
			"participant rather than an absence")
	assert.NotContains(t, ids, deleted,
		"a soft-deleted studio must not hold a permanent unclosable gap at the "+
			"top of the board")

	require.Contains(t, ids, bigGap)
	require.Contains(t, ids, smallGap)
	require.NotEmpty(t, rows)

	// The assertion is about bigGap coming BEFORE smallGap, not about being
	// first overall. Asserting `ids[0] == bigGap` makes the test depend on every
	// other studio in the database, and it fails in the group run because a
	// sibling test seeded a studio with a larger gap — which is the "passes
	// alone, fails in company" shape, and the first version of this assertion
	// was that mistake. A relative ordering between the two studios the test
	// created is what the query actually promises.
	// Reuse the package's indexOf(uuid, uuid); ids here are strings only for
	// readable failure messages.
	bigUUID := uuid.FromStringOrNil(bigGap)
	smallUUID := uuid.FromStringOrNil(smallGap)
	assert.Less(t, indexOf(uuids, bigUUID), indexOf(uuids, smallUUID),
		"the larger ABSOLUTE gap must rank above the larger ratio: 2-of-400 is "+
			"398 missing, 40-of-60 is 20 missing. A ratio-ordered board promotes "+
			"the smaller opportunity, and a board with no clear leader is not a race")

	for _, r := range rows {
		if r.StudioID.String() == bigGap {
			// The generated row mixes widths: `observed` comes back from a
			// count() cast to bigint, while `expected`/`missing` are INTEGER
			// arithmetic. Comparing them without the conversion is a test that
			// fails on types rather than on values.
			assert.Equal(t, 400, int(r.Expected))
			assert.Equal(t, int64(2), r.Observed,
				"observed must count DISTINCT scenes; a join that multiplies rows "+
					"reports a well-linked studio as over-claimed")
			assert.Equal(t, 398, int(r.Missing))
		}
	}
}

func TestGeneratedListUncountedStudios(t *testing.T) {
	q := queriesFor(t)

	uncounted := seedStudioRow(t, "gen2-uncounted")
	seedScenes(t, uncounted, 9)

	counted := seedStudioRow(t, "gen2-counted")
	seedScenes(t, counted, 5)
	assertTotalFor(t, counted, 100, seedUserRow(t, "gen2-asserter"))

	rows, err := q.ListUncountedStudios(t.Context(), queries.ListUncountedStudiosParams{
		LimitCount: 200, LimitOffset: 0,
	})
	require.NoError(t, err)

	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.StudioID.String())
	}
	assert.Contains(t, ids, uncounted,
		"a studio holding scenes with no denominator is one sourced claim away "+
			"from the board, which is the cheapest actionable row in this feature")
	assert.NotContains(t, ids, counted,
		"a studio WITH a denominator is already on the race; listing it here too "+
			"double-counts it")
}

// TestGeneratedGetObservedCount covers the per-kind branches, including the
// performers branch that is derived through scene_performers because performers
// have no studio link at all.
func TestGeneratedGetObservedCount(t *testing.T) {
	q := queriesFor(t)

	studioID := uuid.FromStringOrNil(seedStudioRow(t, "gen3-observed"))
	seedScenes(t, studioID.String(), 5)

	for _, tc := range []struct {
		kind string
		want int64
	}{
		{"scenes", 5},
		{"images", 0},
		{"performers", 0},
		// An unknown kind returns 0, not NULL: a count of nothing is a real
		// answer, whereas NULL makes the ratio undefined and silently drops the
		// row from every report built on it.
		{"something-else", 0},
	} {
		got, err := q.GetObservedCount(t.Context(), queries.GetObservedCountParams{
			StudioID: studioID,
			Kind:     tc.kind,
		})
		require.NoError(t, err, "kind %q must not error", tc.kind)
		assert.Equal(t, tc.want, got, "kind %q", tc.kind)
	}
}

// TestGeneratedCreateAndDeleteExpectedTotal exercises the write path, including
// the re-assert semantics: a source that revises its count must be correctable,
// and two rows for one (entity, kind) would make "the" denominator ambiguous.
func TestGeneratedCreateAndDeleteExpectedTotal(t *testing.T) {
	q := queriesFor(t)
	ctx := t.Context()

	studioID := uuid.FromStringOrNil(seedStudioRow(t, "gen4-crud"))
	userID := uuid.FromStringOrNil(seedUserRow(t, "gen4-asserter"))
	_ = userID

	for _, want := range []int{400, 512} {
		got, err := q.CreateExpectedTotal(ctx, queries.CreateExpectedTotalParams{
			EntityType:       "studio",
			EntityID:         studioID,
			Kind:             "scenes",
			Total:            want,
			SourceEntityType: "url",
			SourceUrl:        "https://example.org/catalogue",
			AssertedBy:       userID,
		})
		require.NoError(t, err, "CreateExpectedTotal must run")
		assert.Equal(t, want, int(got.Total))
	}

	all, err := q.GetExpectedTotals(ctx, queries.GetExpectedTotalsParams{
		EntityType: "studio",
		EntityID:   studioID,
	})
	require.NoError(t, err)
	require.Len(t, all, 1,
		"re-asserting must REPLACE, not append: two claims for one denominator "+
			"make \"the\" denominator ambiguous")
	assert.Equal(t, 512, int(all[0].Total), "the revised count must win")

	removed, err := q.DeleteExpectedTotal(ctx, queries.DeleteExpectedTotalParams{
		EntityType: "studio",
		EntityID:   studioID,
		Kind:       "scenes",
	})
	require.NoError(t, err)
	require.NotNil(t, removed,
		"DeleteExpectedTotal returns the withdrawn claim so a retraction can be "+
			"logged; deleting a claim that was never there must be distinguishable")
	assert.Equal(t, 512, int(removed.Total))
}

// assertTotalFor asserts a denominator via raw SQL, which is the constraint-level
// path; the generated CreateExpectedTotal is covered separately above.
func assertTotalFor(t *testing.T, studioID string, total int, assertedBy string) {
	t.Helper()
	_, err := testutil.DB().Exec(t.Context(), `
		INSERT INTO expected_totals
			(entity_type, entity_id, kind, total, source_entity_type, source_url,
			 asserted_by, asserted_at)
		VALUES ('studio', $1, 'scenes', $2, 'url', 'https://example.org/catalogue', $3, now())
		ON CONFLICT (entity_type, entity_id, kind) DO UPDATE
			SET total = EXCLUDED.total, asserted_at = now()`,
		studioID, total, assertedBy)
	require.NoError(t, err)
}
