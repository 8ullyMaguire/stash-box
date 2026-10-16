// Package backup implements the artifact §7.24.11 calls for: a point-in-time
// database dump paired with a copy of the image store, checksummed, described by
// a manifest that is written LAST, and restorable into a database that is then
// checked against what the manifest claimed.
//
// # Two claims, not one word
//
// "A consistent snapshot" is really two claims and only one of them is a
// transaction. The database half IS point-in-time: one pg_dump is one snapshot.
// The image store is NOT inside that snapshot -- the files live outside the
// database, and copying them is not atomic with the dump -- so the pair is
// CHECKED rather than asserted: every row in `images` that claims to have local
// bytes must have a file whose md5 equals the checksum the database records, and
// a missing or altered file fails the backup before any manifest is written. An
// artifact that carries a manifest is therefore one whose two halves were
// compared against each other, which is the strongest thing that can be said
// about a store that is not inside the transaction.
//
// # Why the manifest is written last
//
// A run that dies partway -- a full disk, a pg_dump that fails on a lock, a kill
// -- leaves a directory with some bytes in it. The manifest is what turns that
// directory into a backup, so writing it last means an interrupted run leaves
// something that Verify and Restore both REFUSE instead of something that looks
// like a backup of a smaller instance. The same reasoning puts README and
// CHECKSUMS.sha256 inside the artifact: cold storage is where instructions go to
// die, so the instructions travel with the bytes.
//
// # What is deliberately not here
//
// There is no engine detection. `database.Initialize` prepends "postgres://" to
// the configured value, so this binary has exactly one database backend and a
// switch for a second one would be a claim about a code path that does not exist.
// There is no incremental or differential mode: a backup whose validity depends
// on an earlier artifact is a backup that fails when the earlier artifact is the
// one that was lost.
package backup

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// FormatVersion is the artifact layout's version. It is checked on read rather
// than assumed, so an artifact from a later tool is refused by name instead of
// being half-understood.
const FormatVersion = 1

// The names inside an artifact. They are constants because Verify's "every file
// is listed" rule needs to know which files the tool itself writes.
const (
	ManifestName  = "MANIFEST.json"
	ChecksumName  = "CHECKSUMS.sha256"
	ReadmeName    = "README"
	DumpName      = "db.dump"
	ImagesDirName = "images"
)

// FileEntry is one file of the artifact. Paths are relative to the artifact root
// and slash-separated, so a manifest written on any platform reads the same.
type FileEntry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// TableCount is a table's row count at the moment of the dump. Every table is
// recorded, read from information_schema rather than from a list written here:
// a list would go stale the first time a migration added a table, and a backup
// whose completeness check is missing the table added last month is worse than
// no check, because it reports success.
type TableCount struct {
	Table string `json:"table"`
	Rows  int64  `json:"rows"`
}

// DBInfo describes the database half of the artifact.
type DBInfo struct {
	ServerVersion  string       `json:"server_version"`
	Migration      int64        `json:"migration_version"`
	MigrationDirty bool         `json:"migration_dirty"`
	Tables         []TableCount `json:"tables"`
}

// ImagesChecked is the image half: how many rows claimed local bytes and were
// verified against the copy, and how many did not claim any. The second number
// is recorded rather than dropped -- an unorganized row is a legitimate state
// (an image known only by its URL, with no bytes fetched yet), and a reader
// comparing two backups needs to see which of the two numbers moved.
type ImagesChecked struct {
	Store           string `json:"store,omitempty"`
	Files           int    `json:"files"`
	Bytes           int64  `json:"bytes"`
	RowsVerified    int    `json:"rows_verified"`
	RowsWithoutFile int    `json:"rows_without_file"`
	FilesWithoutRow int    `json:"files_without_row"`
}

// Manifest is the artifact's description of itself.
type Manifest struct {
	FormatVersion int           `json:"format_version"`
	CreatedUTC    string        `json:"created_utc"`
	ToolVersion   string        `json:"tool_version"`
	Database      DBInfo        `json:"database"`
	Images        ImagesChecked `json:"images"`
	Files         []FileEntry   `json:"files"`
	Notes         []string      `json:"notes"`
}

// ErrNoManifest is returned when a directory is not an artifact. It is a named
// error because every caller treats it specially: it is the difference between
// "this backup is broken" and "this is not a backup".
var ErrNoManifest = errors.New("no " + ManifestName + ": this directory is not a backup artifact (a run that was interrupted before it finished leaves one of these)")

// ErrRefused marks an action the tool declined to take rather than failed at --
// today only a restore into a database that already holds tables. The command
// maps it to its own exit code so a script can tell refusal from breakage.
var ErrRefused = errors.New("refused")

// NewManifest builds a manifest for an artifact created now.
func NewManifest(toolVersion string, now time.Time) *Manifest {
	return &Manifest{
		FormatVersion: FormatVersion,
		CreatedUTC:    now.UTC().Format(time.RFC3339),
		ToolVersion:   toolVersion,
		Notes: []string{
			"db.dump is one point-in-time snapshot (one pg_dump).",
			"images/ is a copy of the store, NOT inside that snapshot; it is checked against the database instead: every file it holds was matched to the row that names its path, and the md5 of the bytes on disk equals the checksum that row records. A row with no file and a file with no row are both counted below rather than judged: nothing in this schema says an image row must have local bytes.",
			"A listed file that is missing or altered, or a file present but unlisted, means this directory is not the backup it claims to be.",
		},
	}
}

// Write writes the manifest into dir. It is the last write of a backup run.
func (m *Manifest) Write(dir string) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding manifest: %w", err)
	}
	data = append(data, '\n')
	path := filepath.Join(dir, ManifestName)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing manifest: %w", err)
	}
	return nil
}

// ReadManifest reads dir's manifest, returning ErrNoManifest when there is none.
func ReadManifest(dir string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNoManifest
		}
		return nil, fmt.Errorf("reading manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", ManifestName, err)
	}
	if m.FormatVersion != FormatVersion {
		return nil, fmt.Errorf("%s is format version %d, this tool writes %d: refusing to interpret it", ManifestName, m.FormatVersion, FormatVersion)
	}
	return &m, nil
}

// FileList returns the manifest's files sorted by path, so two manifests compare
// by eye and by diff.
func (m *Manifest) FileList() []FileEntry {
	out := append([]FileEntry(nil), m.Files...)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// TableRows returns the recorded count for a table, and whether it was recorded
// at all -- the difference between "zero rows" and "not in this manifest".
func (m *Manifest) TableRows(table string) (int64, bool) {
	for _, t := range m.Database.Tables {
		if t.Table == table {
			return t.Rows, true
		}
	}
	return 0, false
}
