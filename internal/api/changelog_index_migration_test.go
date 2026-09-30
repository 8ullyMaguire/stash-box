//go:build integration

package api_test

import (
	"context"
	"testing"

	"github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestChangelogIndexesExist asserts the four keyset indexes from migration
// 90_changelog_indexes are actually present, and that each one carries the
// INCLUDE (deleted) column the migration exists to add.
//
// It exists because the PR's own changelog tests pass with the migration deleted.
// The queries are correct either way -- Postgres will sequential-scan a table this
// small -- so the FEATURE is covered and the PERFORMANCE claim is not. A migration
// that ships and is never asserted is one a refactor can drop silently.
//
// The INCLUDE list is asserted as well as the name, because that is the entire
// point of the migration. The pre-existing sort indexes are PARTIAL
// (WHERE deleted = false), so they cannot serve a tombstone at all: an index with
// the right name but no `deleted` in its INCLUDE clause would pass a name-only
// check and still fail the requirement stated in the migration's own first line.
func TestChangelogIndexesExist(t *testing.T) {
	ctx := context.Background()
	pool := testutil.DB()

	// table -> index name created by migration 90.
	want := map[string]string{
		"scenes":     "scenes_updated_at_id_idx",
		"performers": "performers_updated_at_id_idx",
		"studios":    "studios_updated_at_id_idx",
		"tags":       "tags_updated_at_id_idx",
	}

	for table, index := range want {
		t.Run(table, func(t *testing.T) {
			var cols []string
			// indexdef is the canonical description and states the INCLUDE list,
			// so one query answers both "does it exist" and "is it the right one".
			rows, err := pool.Query(ctx,
				`SELECT indexdef FROM pg_indexes WHERE tablename = $1 AND indexname = $2`,
				table, index)
			require.NoError(t, err, "migration 90_changelog_indexes should have created %s on %s", index, table)
			defer rows.Close()

			require.True(t, rows.Next(),
				"index %s missing on %s -- migration 90_changelog_indexes did not apply", index, table)
			var def string
			require.NoError(t, rows.Scan(&def))
			cols = append(cols, def)

			assert.Contains(t, def, index, "indexdef should name the index")
			// The keyset scan order the resolver relies on.
			assert.Contains(t, def, "updated_at", "index must be ordered on updated_at for the keyset scan")
			// The reason this migration exists at all.
			assert.Contains(t, def, "deleted",
				"index must INCLUDE deleted, or it cannot serve a tombstone: the pre-existing sort indexes are PARTIAL WHERE deleted = false")
		})
	}
}
