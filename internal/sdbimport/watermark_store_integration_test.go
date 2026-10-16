//go:build integration

package sdbimport_test

import (
	"context"
	"errors"
	"testing"
	"time"

	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/sdbimport"
)

// The store's tests need the real schema, not a fake with the same shape: two of the three properties below
// (the conflict branch that refuses to move a mark backwards, and the composite key) are STATEMENTS, and a
// hand-written fake would assert what this file already believes.
//
// Same harness as internal/api: it opens POSTGRES_DB (a DSN without a scheme -- the util prepends
// postgres://), runs the migrations, and drops every table at teardown, which is why the suite runs with -p 1.
func TestMain(m *testing.M) {
	dbtest.TestWithDatabase(m, nil)
}

func testDB(t *testing.T) sdbimport.WatermarkDB {
	t.Helper()
	db := dbtest.DB()
	if db == nil {
		t.Fatal("no database pool: TestMain did not open one")
	}
	// Each test starts from a clean table. The harness drops tables at teardown, not between tests.
	if _, err := db.Exec(context.Background(), "DELETE FROM sync_watermarks"); err != nil {
		t.Fatalf("clearing sync_watermarks: %v", err)
	}
	return db
}

// A mark survives the process, which is the entire reason this table exists: before it, "since last sync" was
// a Go map and a restart was a full walk.
func TestAMarkSurvivesAndIsReadBack(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 16, 9, 30, 0, 0, time.UTC)

	landed, err := sdbimport.SaveWatermark(ctx, db, "stashdb.org", "performers", at)
	if err != nil || !landed {
		t.Fatalf("SaveWatermark = (%v, %v), want (true, nil)", landed, err)
	}

	got, found, err := sdbimport.WatermarkFor(ctx, db, "stashdb.org", "performers")
	if err != nil || !found {
		t.Fatalf("WatermarkFor = (%v, %v, %v), want the saved mark", got, found, err)
	}
	if !got.Equal(at) {
		t.Errorf("mark = %v, want %v", got, at)
	}

	all, err := sdbimport.LoadWatermarks(ctx, db, "stashdb.org")
	if err != nil {
		t.Fatal(err)
	}
	if w := all.Get("performers"); !w.Equal(at) {
		t.Errorf("LoadWatermarks' performers = %v, want %v", w, at)
	}
}

// AN ENTITY THAT WAS NEVER SYNCED IS ABSENT, NOT ZERO. The runner walks in full for an absent entity and stops
// at the mark for a present one, so conflating the two is the difference between a first run and a run that
// believes it has already seen everything.
func TestAnEntityWithNoMarkIsAbsentRatherThanZero(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	all, err := sdbimport.LoadWatermarks(ctx, db, "stashdb.org")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Errorf("LoadWatermarks on an empty table = %v, want an empty map", all)
	}
	at, found, err := sdbimport.WatermarkFor(ctx, db, "stashdb.org", "scenes")
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Errorf("WatermarkFor on a missing row = (%v, true), want found=false: 'never synced' and 'synced "+
			"to the beginning of time' are different states", at)
	}
}

// TWO SOURCES MUST NOT SHARE A MARK. Without `source` in the key the second source reads the first one's
// position and silently skips everything older than it -- a whole source imported as nothing.
func TestTwoSourcesKeepSeparateMarks(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	first := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	second := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)

	for _, c := range []struct {
		source string
		at     time.Time
	}{{"stashdb.org", first}, {"stashdb.example", second}} {
		if _, err := sdbimport.SaveWatermark(ctx, db, c.source, "performers", c.at); err != nil {
			t.Fatal(err)
		}
	}

	for _, c := range []struct {
		source string
		want   time.Time
	}{{"stashdb.org", first}, {"stashdb.example", second}} {
		got, found, err := sdbimport.WatermarkFor(ctx, db, c.source, "performers")
		if err != nil || !found {
			t.Fatalf("WatermarkFor(%s) = (%v, %v, %v)", c.source, got, found, err)
		}
		if !got.Equal(c.want) {
			t.Errorf("mark for %s = %v, want %v: the two sources shared a row", c.source, got, c.want)
		}
	}
}

// A MARK NEVER MOVES BACKWARDS. A stale writer -- a manual run beside a timer run, a crash and a retry -- must
// not be able to send the next walk over records it has already passed. The store refuses in SQL, and the
// caller is told whether the write landed, because an UPDATE that changes nothing is not an error.
func TestAMarkNeverMovesBackwards(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	newer := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	older := newer.Add(-72 * time.Hour)

	if landed, err := sdbimport.SaveWatermark(ctx, db, "stashdb.org", "performers", newer); err != nil || !landed {
		t.Fatalf("the first save = (%v, %v)", landed, err)
	}
	landed, err := sdbimport.SaveWatermark(ctx, db, "stashdb.org", "performers", older)
	if err != nil {
		t.Fatal(err)
	}
	if landed {
		t.Error("an older mark reported that it landed: the conflict branch must refuse to move a mark back")
	}
	got, _, err := sdbimport.WatermarkFor(ctx, db, "stashdb.org", "performers")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(newer) {
		t.Errorf("mark = %v after an older save, want %v", got, newer)
	}

	// And forward still works, which is what stops this from being "refuse everything".
	further := newer.Add(time.Hour)
	if landed, err := sdbimport.SaveWatermark(ctx, db, "stashdb.org", "performers", further); err != nil || !landed {
		t.Fatalf("a newer save = (%v, %v), want it to land", landed, err)
	}
}

// A NEAR-MISS ENTITY NAME IS REFUSED AND WRITES NOTHING. A row for `performer` would be read by no walk while
// that entity walked in full on every run -- and it would sit in the table looking like state.
func TestAnUnknownEntityIsRefusedAndWritesNothing(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

	for _, bad := range []string{"performer", "performers ", "Performers", ""} {
		if _, err := sdbimport.SaveWatermark(ctx, db, "stashdb.org", bad, at); !errors.Is(err, sdbimport.ErrUnknownSyncEntity) {
			t.Errorf("SaveWatermark(%q) = %v, want ErrUnknownSyncEntity", bad, err)
		}
		if _, found, err := sdbimport.WatermarkFor(ctx, db, "stashdb.org", bad); !errors.Is(err, sdbimport.ErrUnknownSyncEntity) {
			t.Errorf("WatermarkFor(%q) = (found=%v, %v), want ErrUnknownSyncEntity", bad, found, err)
		}
	}

	all, err := sdbimport.LoadWatermarks(ctx, db, "stashdb.org")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Errorf("a refused save left %d row(s) behind: %v", len(all), all)
	}
}

// A ZERO MARK IS REFUSED. A walk that read no records has no position, and storing one would look to the next
// run like a completed walk that found the source empty -- the difference between "nothing to do" and "nothing
// was asked".
func TestAZeroMarkIsRefused(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if _, err := sdbimport.SaveWatermark(ctx, db, "stashdb.org", "performers", time.Time{}); err == nil {
		t.Fatal("a zero watermark was saved: a run that read nothing has no position")
	}
}

// An empty source is refused by both readers and the writer: it would make the key (entity, <empty>), which the
// first second source would then share.
func TestAnEmptySourceIsRefused(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	if _, err := sdbimport.SaveWatermark(ctx, db, "  ", "performers", at); err == nil {
		t.Error("SaveWatermark with a blank source succeeded")
	}
	if _, err := sdbimport.LoadWatermarks(ctx, db, ""); err == nil {
		t.Error("LoadWatermarks with a blank source succeeded")
	}
}
