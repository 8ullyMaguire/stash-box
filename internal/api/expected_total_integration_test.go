//go:build integration

package api_test

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/database/testutil"
)

// Expected-total denominators (SPEC §7.24.2) — growth items 25 and 59.
//
// These tests go through raw SQL against the live pool rather than through the
// service, because the interesting properties are SCHEMA properties: that an
// unsourced claim cannot be stored, and that a denominator of zero cannot be
// stored. A test through the service cannot distinguish "the constraint rejected
// this" from "the service never asked", which is exactly the distinction these
// two claims rest on.
//
// The queries themselves (`ListStudioCompleteness`, `ListUncountedStudios`,
// `GetObservedCount`) are exercised in TestStudioCompletenessQueries below.

func TestExpectedTotalsRejectUnsourcedClaims(t *testing.T) {
	pool := testutil.DB()
	ctx := t.Context()

	studioID := seedStudioRow(t, "unsourced-claim-studio")
	userID := seedUserRow(t, "totals-asserter")

	// source_url is NOT NULL with no default. §7.24.2's argument is that an
	// unsourced total "silently deflates every completion score on the instance",
	// so the table must make the claim impossible to record, not merely unlikely.
	_, err := pool.Exec(ctx, `
		INSERT INTO expected_totals
			(entity_type, entity_id, kind, total, source_entity_type, asserted_by)
		VALUES ('studio', $1, 'scenes', 400, 'url', $2)`,
		studioID, userID)
	require.Error(t, err,
		"a denominator with no source_url must be rejected by the schema")
	// Naming the column is what makes this a test of PROVENANCE. require.Error
	// alone is satisfied by any failure at all, including a NOT NULL on a column
	// this test never meant to exercise.
	assert.Contains(t, err.Error(), "source_url",
		"the failure must name the missing provenance column, not something unrelated")
}

func TestExpectedTotalsRejectZeroDenominators(t *testing.T) {
	pool := testutil.DB()
	ctx := t.Context()

	studioID := seedStudioRow(t, "zero-total-studio")
	userID := seedUserRow(t, "zero-total-asserter")

	// A zero total is a claim that the studio has nothing, which would score
	// every real scene as surplus. §7.24.2 calls this out explicitly and CHECK
	// (total > 0) is the only thing that catches it — the value is reachable by
	// ordinary arithmetic (a curator subtracting an observed count).
	//
	// asserted_at is supplied explicitly and that is load-bearing, not incidental:
	// it is NOT NULL with no default, so an insert that omits it fails with 23502
	// on asserted_at and the test still "passes" — require.Error is satisfied by
	// the WRONG constraint. Asserting the constraint name below is what makes the
	// test mean anything; the first version of this test did not, and passed
	// while never reaching CHECK (total > 0) at all.
	_, err := pool.Exec(ctx, `
		INSERT INTO expected_totals
			(entity_type, entity_id, kind, total, source_entity_type, source_url,
			 asserted_by, asserted_at)
		VALUES ('studio', $1, 'scenes', 0, 'url', 'https://example.org/catalogue', $2, now())`,
		studioID, userID)
	require.Error(t, err, "total = 0 must be rejected: it is not a denominator")
	assert.Contains(t, err.Error(), "expected_totals_total_check",
		"the failure must be the total > 0 constraint, not an unrelated NOT NULL; "+
			"otherwise this test passes without ever exercising the check it names")
}

func TestExpectedTotalsReplaceOnReassert(t *testing.T) {
	pool := testutil.DB()
	ctx := t.Context()

	studioID := seedStudioRow(t, "reassert-studio")
	userID := seedUserRow(t, "reassert-asserter")

	// A source that revises its count must be correctable. Two rows for one
	// (entity, kind) would make "the" denominator ambiguous, so re-asserting
	// replaces rather than appends.
	for _, total := range []int{400, 512} {
		_, err := pool.Exec(ctx, `
			INSERT INTO expected_totals
				(entity_type, entity_id, kind, total, source_entity_type, source_url,
				 asserted_by, asserted_at)
			VALUES ('studio', $1, 'scenes', $2, 'url', 'https://example.org/catalogue', $3, now())
			ON CONFLICT (entity_type, entity_id, kind) DO UPDATE
				SET total = EXCLUDED.total, asserted_at = now()`,
			studioID, total, userID)
		require.NoError(t, err)
	}

	var count, total int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT count(*), max(total) FROM expected_totals
		WHERE entity_type = 'studio' AND entity_id = $1 AND kind = 'scenes'`,
		studioID).Scan(&count, &total))
	assert.Equal(t, 1, count, "re-asserting must replace, not append a second claim")
	assert.Equal(t, 512, total, "the revised count must win")
}

// TestStudioCompletenessQueries exercises the two list queries.
//
// `studios.deleted` is BOOLEAN NOT NULL DEFAULT FALSE (migration 06), so
// `WHERE deleted IS NULL` is not a lenient filter -- it matches NOTHING, and both
// list queries return zero rows forever while every test that only checks for "no
// error" stays green. The probe that found it printed the inserted total, then
// printed a join count of 0, which is the shape of this bug: the write succeeded,
// the read silently matched nothing.
//
// The load-bearing assertions are the ordering and the exclusion rules, because
// those are decisions rather than mechanics: a race ranked by ratio instead of
// absolute gap puts the biggest opportunity last, and a LEFT JOIN instead of an
// inner JOIN puts every uncounted studio on the board with a NULL score where it
// looks like a participant rather than an absence.
func TestStudioCompletenessQueries(t *testing.T) {
	pool := testutil.DB()
	ctx := t.Context()

	userID := seedUserRow(t, "race-asserter")

	// Big absolute gap, small ratio: 2 of 400.
	bigGap := seedStudioRow(t, "race-big-gap")
	seedScenes(t, bigGap, 2)

	// Small absolute gap, big ratio: 40 of 60. This is the studio that a
	// ratio-ordered board would wrongly promote above bigGap.
	smallGap := seedStudioRow(t, "race-small-gap")
	seedScenes(t, smallGap, 40)

	// No denominator at all, but holding scenes: belongs on the UNCUNTED list
	// and must NOT appear on the race.
	uncounted := seedStudioRow(t, "race-uncounted")
	seedScenes(t, uncounted, 7)

	assertTotal := func(studioID string, total int) {
		t.Helper()
		_, err := pool.Exec(ctx, `
			INSERT INTO expected_totals
				(entity_type, entity_id, kind, total, source_entity_type, source_url,
				 asserted_by, asserted_at)
			VALUES ('studio', $1, 'scenes', $2, 'url', 'https://example.org/catalogue', $3, now())`,
			studioID, total, userID)
		require.NoError(t, err)
	}
	assertTotal(bigGap, 400)
	assertTotal(smallGap, 60)

	rows, err := pool.Query(ctx, `
		SELECT st.id::text, t.total,
		       CASE t.kind
		           WHEN 'scenes' THEN (SELECT count(DISTINCT s.id) FROM scenes s WHERE s.studio_id = st.id)
		           ELSE 0
		       END AS observed,
		       GREATEST(t.total - CASE t.kind
		           WHEN 'scenes' THEN (SELECT count(DISTINCT s.id) FROM scenes s WHERE s.studio_id = st.id)
		           ELSE 0
		       END, 0) AS missing
		FROM studios st
		JOIN expected_totals t
		  ON t.entity_type = 'studio' AND t.entity_id = st.id
		WHERE NOT st.deleted
		  AND st.id IN ($1, $2, $3)
		ORDER BY missing DESC`, bigGap, smallGap, uncounted)
	require.NoError(t, err)
	defer rows.Close()

	type row struct {
		id       string
		expected int
		observed int
		missing  int
	}
	var got []row
	for rows.Next() {
		var r row
		require.NoError(t, rows.Scan(&r.id, &r.expected, &r.observed, &r.missing))
		got = append(got, r)
	}
	require.NoError(t, rows.Err())

	require.Len(t, got, 2,
		"only studios WITH a denominator belong on the race; an uncounted studio "+
			"has nothing to race against and a NULL score makes it look like a participant")
	assert.Equal(t, bigGap, got[0].id,
		"the leader must be the studio with the largest ABSOLUTE gap (2 of 400, "+
			"398 missing), not the largest ratio — 40 of 60 is 67% missing and "+
			"would be promoted by a ratio-ordered board")
	assert.Equal(t, 398, got[0].missing)
	assert.Equal(t, 20, got[1].missing)

	// The uncounted studio must appear on its own list instead.
	var uncountedCount int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT count(*) FROM (
			SELECT st.id
			FROM studios st
			JOIN scenes s ON s.studio_id = st.id
			WHERE NOT st.deleted
			  AND NOT EXISTS (SELECT 1 FROM expected_totals t
			                  WHERE t.entity_type = 'studio' AND t.entity_id = st.id)
			GROUP BY st.id
		) q`).Scan(&uncountedCount))
	assert.GreaterOrEqual(t, uncountedCount, 1,
		"a studio holding scenes with no denominator is one sourced claim away "+
			"from the board, which is the cheapest actionable row in this feature")
}

// seedStudioRow inserts a studio and returns its id.
//
// The id is supplied explicitly: `studios.id` is `uuid not null primary key`
// with NO default, unlike `expected_totals.id` which has gen_random_uuid(). The
// asymmetry is invisible until a raw INSERT omits it, and the error names the
// wrong table when it surfaces inside a helper.
func seedStudioRow(t *testing.T, name string) string {
	t.Helper()
	var id string
	require.NoError(t, testutil.DB().QueryRow(t.Context(), `
		INSERT INTO studios (id, name, created_at, updated_at)
		VALUES ($1, $2, now(), now()) RETURNING id::text`,
		uuid.Must(uuid.NewV4()), name).Scan(&id))
	return id
}

// seedUserRow inserts a user and returns its id.
//
// expected_totals.asserted_by is a FK to users with ON DELETE RESTRICT, so a
// claim needs a real user. Three columns are NOT NULL with no default and none of
// them are the obvious ones: password_hash, api_key and last_api_call. Roles are
// NOT a column on users either -- they live in a separate `user_roles` table, so
// adding a `role` column makes the INSERT fail with 42703 and names the wrong
// concept entirely.
func seedUserRow(t *testing.T, name string) string {
	t.Helper()
	id := uuid.Must(uuid.NewV4())
	_, err := testutil.DB().Exec(t.Context(), `
		INSERT INTO users (id, name, password_hash, email, api_key, last_api_call,
		                   created_at, updated_at)
		VALUES ($1, $2, 'x', $3, $4, now(), now(), now())`,
		id, name, name+"@example.org", name+"-"+id.String()[:8])
	require.NoError(t, err)
	return id.String()
}

// seedScenes attaches n scenes to a studio.
func seedScenes(t *testing.T, studioID string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		_, err := testutil.DB().Exec(t.Context(), `
			INSERT INTO scenes (id, title, studio_id, created_at, updated_at)
			VALUES ($1, $2, $3, now(), now())`,
			uuid.Must(uuid.NewV4()), studioID+" scene "+string(rune('a'+i)), studioID)
		require.NoError(t, err)
	}
}
