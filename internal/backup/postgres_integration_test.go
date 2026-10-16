//go:build integration

package backup_test

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/stashapp/stash-box/internal/backup"
	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/storage"
)

// This package needs its own TestMain for the usual reason: the harness drops
// every table before the tests run, so the database cannot be shared with another
// integration package in the same binary.
func TestMain(m *testing.M) {
	dbtest.TestWithDatabase(m, nil)
}

// sourceDSN is the connection string the harness itself was given, which is the
// only way to reach this database from OUTSIDE the process: pg_dump and
// pg_restore are separate programs.
func sourceDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("POSTGRES_DB")
	if dsn == "" {
		t.Fatal("POSTGRES_DB is not set: this test runs pg_dump and pg_restore against the integration database")
	}
	return dsn
}

// scratchDatabase creates an empty database beside the test one and returns its
// DSN and a cleanup that drops it. The pool must be closed before the drop, or
// the drop fails with "database is being accessed by other users" -- which is how
// this drill leaves debris behind the first time anyone writes it.
func scratchDatabase(ctx context.Context, t *testing.T, dsn string) (string, func()) {
	t.Helper()
	admin, err := backup.OpenPool(ctx, dsn)
	if err != nil {
		t.Fatalf("opening the source database: %v", err)
	}
	defer admin.Close()

	name := fmt.Sprintf("backup_drill_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgQuote(name)); err != nil {
		t.Fatalf("creating the scratch database: %v", err)
	}
	scratchDSN := replaceDatabase(dsn, name)

	var dropped bool
	return scratchDSN, func() {
		if dropped {
			return
		}
		dropped = true
		pool, err := backup.OpenPool(context.Background(), dsn)
		if err != nil {
			t.Logf("could not reopen the source database to drop %s: %v", name, err)
			return
		}
		defer pool.Close()
		if _, err := pool.Exec(context.Background(), "DROP DATABASE IF EXISTS "+pgQuote(name)+" WITH (FORCE)"); err != nil {
			t.Logf("could not drop %s: %v", name, err)
		}
	}
}

func pgQuote(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

// md5Of is the algorithm the images table records, so a seeded row and a seeded
// file agree for the reason production's do.
func md5Of(body []byte) string {
	sum := md5.Sum(body)
	return hex.EncodeToString(sum[:])
}

// replaceDatabase swaps the database name in a scheme-less DSN, keeping the
// credentials, host, port and parameters.
func replaceDatabase(dsn, name string) string {
	i := strings.Index(dsn, "/")
	if i < 0 {
		return dsn + "/" + name
	}
	rest := dsn[i+1:]
	if q := strings.Index(rest, "?"); q >= 0 {
		return dsn[:i+1] + name + rest[q:]
	}
	return dsn[:i+1] + name
}

const (
	drillTagID       = "b1a1e0aa-0000-4000-8000-000000000001"
	drillImageID     = "d06cf97b-1ac0-469b-9e1a-1ff2b67a40d5"
	drillStrayID     = "d06cf97b-1ac0-469b-9e1a-0000000000ff"
	drillOrphanRowID = "11111111-2222-4333-8444-000000000042"
)

// seed writes one tag (so a row count has something to lose) and one image whose
// bytes are placed where the server itself would look for them.
func seed(ctx context.Context, t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO tags (id, name, description, created_at, updated_at)
		VALUES ($1, 'drill-tag', 'planted by the backup drill', now(), now())`, drillTagID); err != nil {
		t.Fatalf("seeding a tag: %v", err)
	}

	store := t.TempDir()
	body := []byte("these are the bytes of one image, and they are the only copy")
	path := storage.GetImagePath(store, drillImageID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO images (id, url, width, height, checksum)
		VALUES ($1, 'file://drill.jpg', 100, 100, $2)`, drillImageID, md5Of(body)); err != nil {
		t.Fatalf("seeding an image row: %v", err)
	}

	// A row with no file in the store. Nothing in the schema says a row must have
	// local bytes -- `organized` is a moderator's categorization sign-off, measured
	// rather than assumed -- so this row is counted, not judged.
	if _, err := pool.Exec(ctx, `
		INSERT INTO images (id, url, width, height, checksum)
		VALUES ($1, 'file://never-fetched.jpg', 100, 100, 'a-checksum-of-bytes-that-were-never-stored')`,
		drillOrphanRowID); err != nil {
		t.Fatalf("seeding an image row with no file: %v", err)
	}

	// A file in the store the database does not reference. It is copied, because
	// the artifact is a copy of the store and every byte of it is listed.
	if err := os.WriteFile(filepath.Join(store, drillStrayID), []byte("nobody references me"), 0o644); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestSnapshotRestoreDrill(t *testing.T) {
	ctx := t.Context()
	dsn := sourceDSN(t)
	pool := dbtest.DB()
	store := seed(ctx, t, pool)

	dir := filepath.Join(t.TempDir(), "artifact")
	m, err := backup.Snapshot(ctx, pool, dsn, backup.Options{ImagesDir: store, ToolVersion: "drill"}, dir)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	if m.Images.RowsVerified != 1 {
		t.Errorf("RowsVerified = %d, want 1: the image whose bytes are in the store must have been checked against the database", m.Images.RowsVerified)
	}
	if m.Images.Files != 2 {
		t.Errorf("Images.Files = %d, want 2 (the referenced image and the unreferenced one: the store is copied verbatim)", m.Images.Files)
	}
	if m.Images.RowsWithoutFile != 1 {
		t.Errorf("RowsWithoutFile = %d, want 1: the row whose bytes were never stored is a fact the manifest has to carry, or a reader comparing two backups cannot tell which number moved", m.Images.RowsWithoutFile)
	}
	if m.Images.FilesWithoutRow != 1 {
		t.Errorf("FilesWithoutRow = %d, want 1: the store file no row names is copied and counted", m.Images.FilesWithoutRow)
	}
	if rows, ok := m.TableRows("tags"); !ok || rows != 1 {
		t.Errorf("manifest records tags=%d,%v want 1,true", rows, ok)
	}
	if _, err := os.Stat(filepath.Join(dir, backup.ManifestName)); err != nil {
		t.Fatalf("no manifest: %v", err)
	}
	if _, err := backup.Verify(dir); err != nil {
		t.Fatalf("the artifact does not verify against itself: %v", err)
	}

	scratchDSN, dropScratch := scratchDatabase(ctx, t, dsn)
	defer dropScratch()

	scratch, err := backup.OpenPool(ctx, scratchDSN)
	if err != nil {
		t.Fatalf("opening the scratch database: %v", err)
	}
	has, err := backup.HasTables(ctx, scratch)
	if err != nil {
		t.Fatalf("HasTables: %v", err)
	}
	if has {
		t.Fatal("the scratch database has tables: OpenPool must NOT run migrations -- a backup tool that migrates its source is not a backup tool, and one that migrates a restore's target makes the artifact's migration check unfailable")
	}

	if _, err := backup.RestoreArtifact(ctx, scratch, scratchDSN, dir, false); err != nil {
		t.Fatalf("RestoreArtifact: %v", err)
	}

	// The rows really arrived, not merely the counts.
	var tags int64
	if err := scratch.QueryRow(ctx, "SELECT count(*) FROM tags WHERE name = 'drill-tag'").Scan(&tags); err != nil {
		t.Fatalf("reading the restored tag: %v", err)
	}
	if tags != 1 {
		t.Fatalf("the restored database has %d drill tags, want 1", tags)
	}

	// THE SECOND HALF OF THE PAIR, checked against what the RESTORE produced: the
	// artifact's images are what the restored database's rows describe.
	rows, err := backup.ImageRows(ctx, scratch)
	if err != nil {
		t.Fatalf("reading the restored image rows: %v", err)
	}
	audit, err := backup.VerifyImagesAgainstStore(filepath.Join(dir, backup.ImagesDirName), rows)
	if err != nil {
		t.Fatalf("the artifact's images do not match the restored database: %v", err)
	}
	if audit.Verified != 1 || audit.RowsWithoutFile != 1 || audit.FilesWithoutRow != 1 {
		t.Errorf("audit = %+v, want one verified image, one row with no file and one file with no row", audit)
	}

	// A restore into a database that already holds tables is REFUSED, and refused
	// before anything is dropped.
	_, err = backup.RestoreArtifact(ctx, scratch, scratchDSN, dir, false)
	if !errors.Is(err, backup.ErrRefused) {
		t.Fatalf("a second restore without -force = %v, want ErrRefused", err)
	}
	if !strings.Contains(err.Error(), "-force") {
		t.Errorf("the refusal does not say how to proceed: %v", err)
	}
	var stillThere int64
	if err := scratch.QueryRow(ctx, "SELECT count(*) FROM tags").Scan(&stillThere); err != nil {
		t.Fatalf("the refusal dropped the table it was protecting: %v", err)
	}
	if stillThere != 1 {
		t.Errorf("after the refusal the target holds %d tags, want 1: the refusal must happen before any destructive work", stillThere)
	}

	// With -force it is re-runnable, which is what makes a restore drill a drill
	// rather than a one-shot.
	scratch.Close()
	scratch, err = backup.OpenPool(ctx, scratchDSN)
	if err != nil {
		t.Fatalf("reopening the scratch database: %v", err)
	}
	defer scratch.Close()
	if _, err := backup.RestoreArtifact(ctx, scratch, scratchDSN, dir, true); err != nil {
		t.Fatalf("RestoreArtifact with -force: %v", err)
	}
}

func TestSnapshotRefusesAnImageWhoseBytesChanged(t *testing.T) {
	ctx := t.Context()
	dsn := sourceDSN(t)
	pool := dbtest.DB()

	// A file at the path the row names, whose bytes are NOT the bytes the row's
	// checksum describes: corruption, and the one thing this check must not accept.
	const id = "11111111-2222-4333-8444-555555555555"
	store := t.TempDir()
	path := storage.GetImagePath(store, id)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("these are not the bytes you are looking for"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO images (id, url, width, height, checksum)
		VALUES ($1, 'file://drill.jpg', 100, 100, 'the-checksum-of-something-else')`, id); err != nil {
		t.Fatalf("seeding an image row: %v", err)
	}

	dir := filepath.Join(t.TempDir(), "artifact")
	_, err := backup.Snapshot(ctx, pool, dsn, backup.Options{ImagesDir: store, ToolVersion: "drill"}, dir)
	if err == nil {
		t.Fatal("a copy whose bytes do not match the row that names it was written as a backup: nothing downstream could tell it from an intact one")
	}
	if !strings.Contains(err.Error(), id) {
		t.Errorf("the failure does not name the image: %v", err)
	}

	// THE ORDERING CLAIM, measured: the manifest is written last, so a failed run
	// leaves a directory that Verify and Restore both refuse.
	if _, err := os.Stat(filepath.Join(dir, backup.ManifestName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a failed run left a manifest behind (%v): a directory that looks like a backup of a smaller instance is worse than no directory", err)
	}
	if _, err := backup.Verify(dir); !errors.Is(err, backup.ErrNoManifest) {
		t.Errorf("Verify on an incomplete run = %v, want ErrNoManifest", err)
	}
}

func TestSnapshotRefusesToWriteIntoANonEmptyDirectory(t *testing.T) {
	ctx := t.Context()
	dsn := sourceDSN(t)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "an-older-backup"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := backup.Snapshot(ctx, dbtest.DB(), dsn, backup.Options{ImagesDir: t.TempDir(), ToolVersion: "drill"}, dir)
	if err == nil {
		t.Fatal("wrote an artifact into a directory that already held one: the resulting manifest would describe a mixture of two runs, and the mixture would verify")
	}
}
