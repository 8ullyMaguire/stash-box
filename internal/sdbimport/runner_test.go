package sdbimport

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/stashapp/stash-box/internal/service"
)

func testImporter() *Importer {
	return &Importer{Client: NewClient("https://example.invalid/graphql"), Factory: &service.Factory{}}
}

// The two refusals that would otherwise be discovered a long way in.
func TestTheSyncRefusesWhatsMissingBeforeItWalks(t *testing.T) {
	imp := testImporter()
	ok := SyncOptions{Source: "stashdb.org", Entities: []string{"performers"}}

	if _, err := NewRunner(nil, nil, ok); err == nil {
		t.Error("NewRunner accepted a nil importer")
	}
	if _, err := NewRunner(imp, nil, ok); err == nil {
		t.Error("NewRunner accepted a nil watermark store: without one every run is a first run")
	}
	blank := ok
	blank.Source = "   "
	if _, err := NewRunner(imp, nil, blank); err == nil {
		t.Error("NewRunner accepted a blank source, which would key the watermark (entity, <empty>)")
	}
}

// An entity name or a mode that the sync does not know is refused up front. A run that finds out 400,000 records
// in has already spent the source's good will.
func TestAnUnknownEntityOrModeIsRefused(t *testing.T) {
	imp := testImporter()
	db := stubDB{}

	if _, err := NewRunner(imp, db, SyncOptions{Source: "s", Entities: []string{"performer"}}); !errors.Is(err, ErrUnknownSyncEntity) {
		t.Errorf("an unknown entity = %v, want ErrUnknownSyncEntity", err)
	}
	if _, err := NewRunner(imp, db, SyncOptions{Source: "s", Modes: map[string]SyncMode{"performers": "sideways"}}); err == nil {
		t.Error("an unknown sync mode was accepted")
	}
	if _, err := NewRunner(imp, db, SyncOptions{Source: "s", Modes: map[string]SyncMode{"performer": ModeLocal}}); !errors.Is(err, ErrUnknownSyncEntity) {
		t.Errorf("an unknown entity in -sync-modes = %v, want ErrUnknownSyncEntity", err)
	}
	if _, err := NewRunner(imp, db, SyncOptions{Source: "s"}); err != nil {
		t.Errorf("a valid run was refused: %v", err)
	}
}

func TestSyncModesAreParsedAndValidated(t *testing.T) {
	got, err := ParseSyncModes(" performers = upstream-wins , scenes=local-wins ,,")
	if err != nil {
		t.Fatal(err)
	}
	if got["performers"] != ModeUpstream || got["scenes"] != ModeLocal {
		t.Errorf("modes = %v", got)
	}
	for _, bad := range []string{"performers", "performers=sideways", "performer=upstream-wins", "performers=upstream-wi"} {
		if _, err := ParseSyncModes(bad); err == nil {
			t.Errorf("ParseSyncModes(%q) was accepted", bad)
		}
	}

	// An EMPTY mode is the package's own default, which ParseMode's contract already defines as ""
	// meaning latest-wins. Accepting it is deliberate: refusing would make `-sync-modes performers=` a typo
	// error for asking for the documented default.
	got, err = ParseSyncModes("performers=")
	if err != nil || got["performers"] != ModeLatest {
		t.Errorf("ParseSyncModes with an empty mode = (%v, %v), want the default mode", got, err)
	}
}

// An entity the sync does not reconcile is REPORTED as owned by the importer, not silently left at zero. A
// report that shows "scenes: 0 updated" without saying nobody looked is the failure this guards.
func TestEntitiesTheSyncDoesNotWalkAreNamedRatherThanZeroed(t *testing.T) {
	r := RunReport{Entities: []EntityReport{
		{Entity: "tags", Walked: false, Owned: "cmd/sdbimport's import path"},
		{Entity: "performers", Walked: true, Scanned: 10, Updated: 2},
	}}
	summary := r.Summary()
	if !strings.Contains(summary, "not reconciled by the sync (cmd/sdbimport's import path)") {
		t.Errorf("the summary does not name the owner of an unwalked entity:\n%s", summary)
	}
	if !strings.Contains(summary, "performers") {
		t.Errorf("the summary omits the walked entity:\n%s", summary)
	}

	// The ownership lists must agree with SyncEntities, or the report is describing a design that is not there.
	all := append(append([]string{}, ReconciledEntities()...), ImporterOwnedEntities()...)
	if len(all) != len(SyncEntities) {
		t.Fatalf("the ownership lists cover %d entities and the walk covers %d", len(all), len(SyncEntities))
	}
	for _, a := range all {
		if !IsSyncEntity(a) {
			t.Errorf("%q is in an ownership list and not in SyncEntities", a)
		}
	}
}

// A DRY RUN'S REPORT MUST NOT READ AS A REAL ONE, and a real run that reported failures must not exit green.
func TestTheReportSaysWhetherItFailedAndWhetherItWasDry(t *testing.T) {
	clean := RunReport{Entities: []EntityReport{{Entity: "performers", Scanned: 5, Updated: 1}}}
	if clean.Failed() {
		t.Error("a clean run reported failure")
	}
	counted := RunReport{Entities: []EntityReport{{Entity: "performers", Failed: 1}}}
	if !counted.Failed() {
		t.Error("a run with a failed record reported success: the exit code would read as 'the sync is fine'")
	}
	named := RunReport{Entities: []EntityReport{{Entity: "performers", Errors: []string{"boom"}}}}
	if !named.Failed() {
		t.Error("a run with a recorded error reported success")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "sync.json")
	if err := named.WriteJSON(path); err != nil {
		t.Fatal(err)
	}
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var back RunReport
	if err := json.Unmarshal(blob, &back); err != nil {
		t.Fatalf("the written report is not readable JSON: %v", err)
	}
	if !back.Failed() || len(back.Entities) != 1 {
		t.Errorf("the round-tripped report lost its failure: %+v", back)
	}
	if back.Entities[0].Errors[0] != "boom" {
		t.Errorf("the round-tripped report lost the error text: %+v", back.Entities[0])
	}
}

// A zero-length walk has no stop decision to report, and the report must not claim one.
func TestAStopReasonIsAlwaysStated(t *testing.T) {
	r := RunReport{Entities: []EntityReport{{Entity: "performers", Walked: true,
		StopReason: "the source ran out of records before the watermark"}}}
	if !strings.Contains(r.Summary(), "ran out of records") {
		t.Errorf("the summary hides why the walk stopped:\n%s", r.Summary())
	}
}

func TestATimePointerIsRenderedOrDashedNeverInvented(t *testing.T) {
	if got := atTime(nil); got != "-" {
		t.Errorf("atTime(nil) = %q", got)
	}
	zero := time.Time{}
	if got := atTime(&zero); got != "-" {
		t.Errorf("atTime(zero) = %q, want a dash: a first run has no watermark and must not print a date", got)
	}
	at := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	if got := atTime(&at); got != "2026-10-16T12:00:00Z" {
		t.Errorf("atTime = %q", got)
	}
}

// stubDB satisfies WatermarkDB for the constructor tests. Its methods are never called: NewRunner refuses on the
// options before it runs anything, and a nil pool would panic if that ever stopped being true.
type stubDB struct{}

func (stubDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	panic("stubDB.Query called: the constructor tests must not reach the database")
}
func (stubDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	panic("stubDB.QueryRow called")
}
func (stubDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	panic("stubDB.Exec called")
}
