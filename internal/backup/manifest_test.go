package backup

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManifestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	m := NewManifest("v-test", time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC))
	m.Database = DBInfo{
		ServerVersion:  "18.6",
		Migration:      96,
		MigrationDirty: false,
		Tables:         []TableCount{{Table: "performers", Rows: 3}, {Table: "images", Rows: 0}},
	}
	m.Images = ImagesChecked{Store: "/store", Files: 2, Bytes: 10, RowsVerified: 2, RowsWithoutFile: 1, FilesWithoutRow: 3}
	m.Files = []FileEntry{{Path: DumpName, Size: 4, SHA256: strings.Repeat("a", 64)}}

	if err := m.Write(dir); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := ReadManifest(dir)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if got.CreatedUTC != "2026-10-16T12:00:00Z" {
		t.Errorf("CreatedUTC = %q, want the UTC RFC3339 stamp", got.CreatedUTC)
	}
	if got.Database.Migration != 96 || got.ToolVersion != "v-test" {
		t.Errorf("round trip lost data: %+v", got.Database)
	}
	if got.Images.RowsWithoutFile != 1 || got.Images.RowsVerified != 2 || got.Images.FilesWithoutRow != 3 {
		t.Errorf("image counts lost: %+v", got.Images)
	}
	if len(got.Files) != 1 || got.Files[0].Path != DumpName {
		t.Errorf("files lost: %+v", got.Files)
	}
	if len(got.Notes) == 0 {
		t.Error("the manifest carries no notes: the two-claims statement is part of what it says about itself")
	}
}

func TestReadManifestRefusesAnInterruptedRun(t *testing.T) {
	// The whole point of writing the manifest last: a directory that has bytes in
	// it but no manifest is not a backup, and every reader must say so.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, DumpName), []byte("half a dump"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadManifest(dir)
	if !errors.Is(err, ErrNoManifest) {
		t.Fatalf("ReadManifest on a directory with no manifest = %v, want ErrNoManifest", err)
	}
	if !strings.Contains(err.Error(), "interrupted") {
		t.Errorf("the message does not explain WHY a directory can look like this: %v", err)
	}
}

func TestReadManifestRefusesALaterFormatVersion(t *testing.T) {
	dir := t.TempDir()
	raw := map[string]any{"format_version": FormatVersion + 1, "files": []FileEntry{}}
	data, _ := json.Marshal(raw)
	if err := os.WriteFile(filepath.Join(dir, ManifestName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadManifest(dir)
	if err == nil {
		t.Fatal("a manifest from a later tool was accepted: half-understanding an artifact is worse than refusing it")
	}
	if !strings.Contains(err.Error(), "format version") {
		t.Errorf("the refusal does not name the version: %v", err)
	}
}

func TestTableRowsTellsZeroFromAbsent(t *testing.T) {
	m := &Manifest{Database: DBInfo{Tables: []TableCount{{Table: "images", Rows: 0}}}}
	if n, ok := m.TableRows("images"); !ok || n != 0 {
		t.Errorf("TableRows(images) = %d,%v want 0,true: an empty table is a recorded fact", n, ok)
	}
	if _, ok := m.TableRows("scenes"); ok {
		t.Error("TableRows reported a table the manifest does not record: absence and zero are different facts")
	}
}

func TestCompareVersionAndCounts(t *testing.T) {
	if err := compareVersion(96, false, 96, false); err != nil {
		t.Errorf("identical versions were refused: %v", err)
	}
	if err := compareVersion(96, false, 95, false); err == nil {
		t.Error("a restore from another migration was accepted")
	}
	if err := compareVersion(96, false, 96, true); err == nil {
		t.Error("a dirty schema_migrations was accepted")
	}

	want := []TableCount{{Table: "performers", Rows: 3}, {Table: "scenes", Rows: 1}}
	if err := compareCounts(want, []TableCount{{Table: "scenes", Rows: 1}, {Table: "performers", Rows: 3}}); err != nil {
		t.Errorf("matching counts were refused (order must not matter): %v", err)
	}
	err := compareCounts(want, []TableCount{{Table: "performers", Rows: 2}, {Table: "scenes", Rows: 1}})
	if err == nil {
		t.Fatal("a lost row was accepted")
	}
	if !strings.Contains(err.Error(), "performers") || !strings.Contains(err.Error(), "3 in the manifest") {
		t.Errorf("the mismatch does not say which table and by how much: %v", err)
	}
	err = compareCounts(want, []TableCount{{Table: "performers", Rows: 3}})
	if err == nil {
		t.Fatal("a table dropped by the restore was accepted")
	}
	if !strings.Contains(err.Error(), "absent") {
		t.Errorf("a missing table is reported as something other than missing: %v", err)
	}
}
