package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Options is what a backup run needs beyond a connection: where the image store
// is, what to call the tool in the manifest, and what time it is.
type Options struct {
	ImagesDir   string
	ToolVersion string
	Now         time.Time
}

// Snapshot writes an artifact into outDir and returns the manifest it wrote.
//
// The order of the writes is the design (see the package comment): the dump and
// the store copy come first, the manifest last, and any failure before the
// manifest leaves a directory that Verify and Restore both refuse.
//
// The database half is read inside ONE exported snapshot: the dump, the row
// counts, the migration version and the image rows all come from the same point
// in time, which is what makes the counts a statement about the dump instead of a
// statement about a moment near it. The image store is still not inside that
// snapshot, and the md5 cross-check in the middle of this function is exactly how
// far that claim goes.
func Snapshot(ctx context.Context, pool *pgxpool.Pool, dsn string, opts Options, outDir string) (*Manifest, error) {
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating %s: %w", outDir, err)
	}
	if err := dirMustBeEmpty(outDir); err != nil {
		return nil, err
	}

	m := NewManifest(opts.ToolVersion, opts.Now)

	// One snapshot for every read of the database, and the dump itself.
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("opening a repeatable-read transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var snapshotID string
	if err := tx.QueryRow(ctx, "SELECT pg_export_snapshot()").Scan(&snapshotID); err != nil {
		return nil, fmt.Errorf("exporting a snapshot for the dump to read (pg_dump --snapshot needs this; PostgreSQL 9.2+): %w", err)
	}

	if m.Database.ServerVersion, err = ServerVersion(ctx, tx); err != nil {
		return nil, err
	}
	if m.Database.Migration, m.Database.MigrationDirty, err = MigrationVersion(ctx, tx); err != nil {
		return nil, err
	}
	if m.Database.Tables, err = TableCounts(ctx, tx); err != nil {
		return nil, err
	}
	imageRows, err := ImageRows(ctx, tx)
	if err != nil {
		return nil, err
	}
	m.Notes = append(m.Notes,
		"the dump, the table counts, the migration version and the image rows were all read in ONE exported snapshot (pg_export_snapshot), so the counts describe the dump rather than a moment near it.")

	// The store, checked before anything is written. A file whose bytes do not
	// match the row that names it is corruption, and it stops the backup here:
	// everything after this point assumes the copy and the rows agree.
	audit, err := VerifyImagesAgainstStore(opts.ImagesDir, imageRows)
	if err != nil {
		return nil, err
	}
	m.Images = ImagesChecked{
		Store:           opts.ImagesDir,
		Files:           audit.Files,
		Bytes:           audit.Bytes,
		RowsVerified:    audit.Verified,
		RowsWithoutFile: audit.RowsWithoutFile,
		FilesWithoutRow: audit.FilesWithoutRow,
	}
	m.Notes = append(m.Notes, fmt.Sprintf(
		"of %d image rows, %d have a file in this store and every one of them matches the md5 the database records; %d have no file here and %d store files are referenced by no row. Both are facts about the instance, recorded rather than judged: this schema has no column that says an image row must have local bytes.",
		len(imageRows), audit.Verified, audit.RowsWithoutFile, audit.FilesWithoutRow))
	if opts.ImagesDir == "" {
		m.Notes = append(m.Notes, "this instance configures no image_location, so it keeps no image bytes and nothing was copied: every image row counts as a row without a file.")
	}

	if err := DumpDatabase(ctx, dsn, filepath.Join(outDir, DumpName), snapshotID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("closing the snapshot: %w", err)
	}

	if _, _, err := copyStore(opts.ImagesDir, filepath.Join(outDir, ImagesDirName)); err != nil {
		return nil, err
	}

	if err := WriteArtifactTail(outDir, m); err != nil {
		return nil, err
	}
	return m, nil
}

// WriteArtifactTail closes an artifact: README, then CHECKSUMS.sha256, then the
// manifest listing EVERY file including the two it just wrote.
//
// The list is collected twice, and the second collection is the one that goes
// into the manifest. Collecting once and reusing it -- which is what this
// function did first -- produces an artifact whose own manifest omits
// CHECKSUMS.sha256, and the strict "a file present but not listed" rule then
// refuses the artifact the tool just wrote. That is exactly what happened, caught
// by the drill rather than by reading it, which is why the rule is strict.
//
// It is a separate function so this ordering can be tested without a database:
// a directory with any file in it is enough.
func WriteArtifactTail(dir string, m *Manifest) error {
	if err := writeReadme(dir, m); err != nil {
		return err
	}
	collected, err := CollectFiles(dir)
	if err != nil {
		return err
	}
	if err := WriteChecksums(dir, collected); err != nil {
		return err
	}
	collected, err = CollectFiles(dir)
	if err != nil {
		return err
	}
	m.Files = collected
	return m.Write(dir) // LAST: nothing reads a backup that has no manifest
}

func dirMustBeEmpty(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("reading %s: %w", dir, err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("%w: %s holds %d entries; backing up into it would write a manifest that describes a mixture of two runs, and the mixture would verify", ErrDirNotEmpty, dir, len(entries))
	}
	return nil
}

// copyStore copies the image store tree into dst, verbatim, and returns how many
// files and bytes it copied.
//
// Verbatim rather than "only the files the database references": the store is the
// instance's own directory, and a copy of it is a copy of it. A file the database
// does not reference is dead weight in the artifact and is still listed in the
// manifest, which is the artefact's whole promise -- every byte accounted for.
func copyStore(store, dst string) (int, int64, error) {
	if store == "" {
		return 0, 0, nil
	}
	if _, err := os.Stat(store); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, 0, nil // no store on this instance; the manifest's counts say the rows have no files
		}
		return 0, 0, fmt.Errorf("image store %s: %w", store, err)
	}

	var files int
	var bytes int64
	err := eachStoreFile(store, func(path string, size int64) error {
		rel, err := filepath.Rel(store, path)
		if err != nil {
			return err
		}
		dest := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if _, err := copyFile(path, dest); err != nil {
			return err
		}
		files++
		bytes += size
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	return files, bytes, nil
}

func copyFile(src, dst string) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return 0, err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return 0, fmt.Errorf("copying %s: %w", src, err)
	}
	return n, nil
}

func writeReadme(dir string, m *Manifest) error {
	body := strings.Join([]string{
		"stash-box backup artifact",
		"",
		"Created:  " + m.CreatedUTC,
		"Tool:     " + m.ToolVersion,
		"Database: PostgreSQL " + m.Database.ServerVersion + ", migration " + fmt.Sprintf("%d", m.Database.Migration),
		"",
		"Contents:",
		"  db.dump          pg_dump custom format. Restore with pg_restore (NOT psql).",
		"  images/          a copy of the instance's image store, when it has one.",
		"  CHECKSUMS.sha256 every file above; verify with: cd <this dir> && sha256sum -c CHECKSUMS.sha256",
		"  MANIFEST.json    what this artifact claims about itself: file list with sha256,",
		"                   table row counts, migration version, image counts.",
		"",
		"Check it before you need it:",
		"  stash-box-backup -verify -dir <this dir>",
		"",
		"Restore it:",
		"  1. create an empty database (a restore into a directory of existing tables is",
		"     refused unless -force is given, because it drops what is there)",
		"  2. stash-box-backup -restore -dir <this dir> -database <host:port/db?sslmode=...>",
		"  3. the command then compares the restored database's migration version and every",
		"     table's row count against MANIFEST.json, and fails if they differ",
		"  4. point the instance at the restored database and run the integration suite",
		"",
		"What this artifact is and is not:",
		"  db.dump is one point-in-time snapshot, and the row counts in the manifest were",
		"  read inside that same exported snapshot. The image store is NOT inside it: the",
		"  files were copied afterwards, and every file the store held was checked against",
		"  the md5 checksum of the row that names its path, so a copy whose bytes had",
		"  changed cannot pass. What neither the dump nor the copy can decide is whether an",
		"  image row SHOULD have a file -- the schema does not record that -- so the",
		"  manifest counts the rows without files and the files without rows instead of",
		"  calling either one an error.",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(dir, ReadmeName), []byte(body), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", ReadmeName, err)
	}
	return nil
}

// RestoreArtifact restores an artifact into dsn, then checks what it produced
// against what the artifact claimed.
//
// The artifact is verified first: restoring from a directory whose bytes do not
// match its own manifest is how a corrupted dump reaches a live database, and the
// whole point of writing checksums is to make that impossible rather than
// unlikely.
func RestoreArtifact(ctx context.Context, target querier, dsn, dir string, force bool) (*Manifest, error) {
	m, err := Verify(dir)
	if err != nil {
		return nil, err
	}

	has, err := HasTables(ctx, target)
	if err != nil {
		return nil, err
	}
	if has && !force {
		return nil, fmt.Errorf("%w: %s already contains tables. A restore drops and replaces what is there (pg_restore --clean --if-exists). Pass -force if that is what you mean, or restore into an empty database", ErrRefused, dsn)
	}

	if err := RestoreDatabase(ctx, dsn, filepath.Join(dir, DumpName)); err != nil {
		return nil, err
	}

	version, dirty, err := MigrationVersion(ctx, target)
	if err != nil {
		return nil, err
	}
	if err := compareVersion(m.Database.Migration, m.Database.MigrationDirty, version, dirty); err != nil {
		return nil, err
	}

	counts, err := TableCounts(ctx, target)
	if err != nil {
		return nil, err
	}
	if err := compareCounts(m.Database.Tables, counts); err != nil {
		return nil, err
	}
	return m, nil
}

// compareVersion is a function rather than four lines inside the restore because
// it is the check that says "this is the database the artifact was taken from",
// and the only way to prove it fires is to hand it two different versions.
func compareVersion(want int64, wantDirty bool, got int64, gotDirty bool) error {
	if got != want {
		return fmt.Errorf("the restore produced migration %d but the manifest describes migration %d: this is not the database the artifact was taken from", got, want)
	}
	if gotDirty != wantDirty {
		return fmt.Errorf("the restore's schema_migrations.dirty is %v and the manifest's is %v", gotDirty, wantDirty)
	}
	return nil
}

// compareCounts is the claim that the rows came with the dump. It compares what
// the manifest recorded against what the restored database now holds, and it
// reports a table that is absent as loudly as a table whose count moved: a
// restore that silently dropped a table would otherwise be a restore that
// reported success about the tables it still had.
func compareCounts(want []TableCount, got []TableCount) error {
	have := make(map[string]int64, len(got))
	for _, c := range got {
		have[c.Table] = c.Rows
	}
	var problems []string
	for _, w := range want {
		n, ok := have[w.Table]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s: in the manifest, absent after the restore", w.Table))
			continue
		}
		if n != w.Rows {
			problems = append(problems, fmt.Sprintf("%s: %d rows after the restore, %d in the manifest", w.Table, n, w.Rows))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("the restore does not match the manifest: %s", summarise(problems))
	}
	return nil
}

// dsnPassword matches libpq's userinfo:password@ both with and without a scheme,
// because the configured database value has its scheme stripped by the server's
// own parser, and so does this one.
var dsnPassword = regexp.MustCompile(`^([^:/@?]+):([^@]*)@`)

// RedactDSN removes the password from a DSN for display. Everything this tool
// prints goes to a terminal, a log capture or a CI job, and the server itself
// never logs its password.
func RedactDSN(dsn string) string {
	scheme := ""
	rest := dsn
	if i := strings.Index(dsn, "://"); i >= 0 {
		scheme, rest = dsn[:i+3], dsn[i+3:]
	}
	return scheme + dsnPassword.ReplaceAllString(rest, "$1:[REDACTED]@")
}
