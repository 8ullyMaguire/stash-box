//go:build integration

package api_test

import (
	"context"
	"regexp"
	"testing"

	"github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPerformerFilterIndexesExist asserts that migration 142_performer_filter_indexes applied AND that each
// index has the shape the migration states: the column the filter compares, and the PARTIAL predicate
// (`WHERE NOT deleted`) that makes it usable for the queries that read live performers.
//
// IT EXISTS FOR THE REASON THE CHANGELOG INDEX TEST EXISTS: this migration arrives from upstream PR #1284,
// whose own tests pass with the migration deleted -- a filter on a table this small is sequential-scanned
// either way, so the FEATURE is covered by the ported integration test and the PERFORMANCE claim is not.
// Nothing in the test suite would notice the migration being dropped; this is the thing that would.
//
// THE COLUMN IS ASSERTED, NOT ONLY THE NAME. An index called `performers_height_idx` on `(cup_size)` would
// satisfy a name-only check and serve none of the queries the migration was written for -- the same failure
// the changelog test describes for an index with the right name and the wrong INCLUDE list.
func TestPerformerFilterIndexesExist(t *testing.T) {
	ctx := context.Background()
	pool := testutil.DB()

	// index name -> the column it must be built on.
	performers := map[string]string{
		"performers_gender_idx":            "gender",
		"performers_ethnicity_idx":         "ethnicity",
		"performers_country_idx":           "country",
		"performers_eye_color_idx":         "eye_color",
		"performers_hair_color_idx":        "hair_color",
		"performers_breast_type_idx":       "breast_type",
		"performers_height_idx":            "height",
		"performers_band_size_idx":         "band_size",
		"performers_waist_size_idx":        "waist_size",
		"performers_hip_size_idx":          "hip_size",
		"performers_career_start_year_idx": "career_start_year",
		"performers_career_end_year_idx":   "career_end_year",
		"performers_birthdate_idx":         "birthdate",
		"performers_deathdate_idx":         "deathdate",
	}

	require.NotEmpty(t, performers, "the table above is the subject of this test")

	for index, column := range performers {
		t.Run(index, func(t *testing.T) {
			var def string
			err := pool.QueryRow(ctx,
				`SELECT indexdef FROM pg_indexes WHERE tablename = 'performers' AND indexname = $1`,
				index).Scan(&def)
			require.NoError(t, err,
				"index %s missing on performers -- migration 142_performer_filter_indexes did not apply", index)
			// THE COLUMN IS MATCHED AS A PARENTHESISED EXPRESSION, not as a substring, and the mutation
			// battery is why: `performers_height_idx` on `(waist_size)` still CONTAINS "height" -- in its own
			// name -- so the first version of this assertion passed an index built on a column the filter
			// never compares. The index's NAME is not evidence about its COLUMN.
			assert.Regexp(t, `\(\s*`+regexp.QuoteMeta(column)+`\s*\)`, def,
				"the index must be built on the column the filter compares, not merely named after it")
			// MATCHED AS A REGEXP, because `indexdef` is Postgres's own pretty-printing of the statement
			// rather than the text this repository wrote: it renders the partial predicate as
			// `WHERE (NOT deleted)` with the parentheses added, so a literal "WHERE NOT deleted" fails
			// against an index that is exactly what the migration asked for. (The first run of this test did
			// exactly that, on all fourteen indexes.)
			assert.Regexp(t, `WHERE\s+\(?\s*NOT deleted`, def,
				"the index must be partial on live rows, or it does not match the predicate the query applies")
		})
	}

	// The favourite filter starts from the viewer, so it needs the reverse column order of the unique index
	// that already exists on this table.
	var favorites string
	err := pool.QueryRow(ctx,
		`SELECT indexdef FROM pg_indexes WHERE tablename = 'performer_favorites' AND indexname = $1`,
		"performer_favorites_user_performer_idx").Scan(&favorites)
	require.NoError(t, err, "performer_favorites_user_performer_idx is missing")
	assert.Regexp(t, `\(user_id, performer_id\)`, favorites,
		"the index must lead with user_id: the existing unique index leads with performer_id")

	// AND THE URL HALF OF THE PORT DEPENDS ON AN INDEX THIS MIGRATION DOES NOT CREATE. The case-insensitive
	// comparison (`LOWER(performer_urls.url) = LOWER(?)`) can only use an expression index, and this fork has
	// carried one since migration 49. If that index is ever dropped, the case-insensitive filter silently
	// becomes a sequential scan -- which is the kind of regression a name-only test never sees.
	var urlIdx string
	err = pool.QueryRow(ctx,
		`SELECT indexdef FROM pg_indexes WHERE tablename = 'performer_urls' AND indexname = $1`,
		"performer_urls_url_lower_idx").Scan(&urlIdx)
	require.NoError(t, err,
		"performer_urls_url_lower_idx is missing: the case-insensitive URL filter would fall back to a scan")
	// Also a regexp, and for the same reason: Postgres prints the expression as `lower((url)::text)`.
	assert.Regexp(t, `lower\(\(?url`, urlIdx,
		"the URL filter compares LOWER(url), so the index has to be on that expression and not on url")
}
