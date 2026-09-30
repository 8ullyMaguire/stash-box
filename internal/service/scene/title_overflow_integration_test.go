//go:build integration

package scene_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/database/testutil"
)

// Upstream #1262: `scenes.title` was varchar(255), so a longer title was rejected
// by the DATABASE rather than by any application check -- an error that surfaces
// as a rejected edit with no indication of why.
//
// The fix is migration 88_scene_title_text.up.sql (renumbered from upstream's 76,
// which collided with this fork's 76_add_user_trust).
//
// WHY THIS TEST EXISTS AT ALL. It replaces an earlier version of itself that
// computed len(title) and logged it when it exceeded 255. That test could not fail:
// with the migration removed, `title` is still a 284-character Go string, len is
// still 284, the comparison is still true, and the test still passes. Verified by
// mutation -- green with the migration in place AND green with it neutered. A test
// that cannot fail is not evidence that the column was widened, which is the only
// thing that matters here.
//
// The `TestMain` below is load-bearing. Without it testutil.DB() is nil and the
// require.NotNil fires -- which is CORRECT, but it means the previous version of
// this test (a length log, in a package with no database at all) was passing for a
// reason that had nothing to do with the column's width. The nil guard is what
// turned that from a silent vacuous pass into a visible failure.
//
// So this one asks the DATABASE, from three independent angles, because any one of
// them alone can be satisfied by something other than the fix:
//
//  1. the declared type is `text`, read from information_schema, so it is the real
//     schema rather than a Go-side assumption;
//  2. a 3000-character title INSERTS, which varchar(255) refuses outright;
//  3. the value comes back COMPLETE -- a column that silently truncates on insert
//     is a different bug from one that rejects, and only reading it back tells
//     them apart.
//
// A nil populater is correct here: every fixture this file needs is one INSERT,
// and a shared populator for a single-test package would be a second place to keep
// in sync.
func TestMain(m *testing.M) {
	testutil.TestWithDatabase(m, nil)
}

func TestSceneTitleColumnIsUnboundedText(t *testing.T) {
	db := testutil.DB()
	require.NotNil(t, db, "testutil.DB() is nil: this test must run under TestWithDatabase")

	t.Run("declared type is text, not varchar", func(t *testing.T) {
		var dataType string
		err := db.QueryRow(context.Background(), `
			SELECT data_type
			  FROM information_schema.columns
			 WHERE table_name = 'scenes'
			   AND column_name = 'title'`).Scan(&dataType)
		require.NoError(t, err, "could not read the declared type of scenes.title")
		require.Equal(t, "text", dataType,
			"scenes.title is %q; upstream #1262 widened it to text, so a varchar "+
				"here is the bug the migration was supposed to fix", dataType)
	})

	// Well past 255, so the test cannot pass by luck on a boundary. Built at run
	// time rather than as a `const`: strings.Repeat is not a constant expression,
	// and a literal 3000-character string in source is unreviewable anyway.
	longTitle := "A scene title that is deliberately far longer than two hundred " +
		"and fifty-five characters, so that a column still declared as varchar(255) " +
		"must reject it outright rather than accept it and quietly lose the tail. " +
		strings.Repeat("Padding. ", 120)

	require.Greater(t, len(longTitle), 255,
		"the fixture must exceed 255 characters or the test is vacuous")

	t.Run("a long title inserts without error", func(t *testing.T) {
		_, err := db.Exec(context.Background(), `
			INSERT INTO scenes (id, title, date, details, created_at, updated_at)
			VALUES (gen_random_uuid(), $1, '2000-01-01', '', now(), now())`,
			longTitle)
		require.NoError(t, err,
			"a %d-character title was rejected; scenes.title is still bounded",
			len(longTitle))
	})

	t.Run("the stored title comes back complete", func(t *testing.T) {
		var got string
		err := db.QueryRow(context.Background(),
			`SELECT title FROM scenes WHERE title = $1`, longTitle).Scan(&got)
		require.NoError(t, err, "the long title is not in the table at all")
		require.Equal(t, longTitle, got,
			fmt.Sprintf("the title was altered in storage: %d characters in, %d out",
				len(longTitle), len(got)))
	})
}
