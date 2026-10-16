package backup

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ConnEnv turns the configured DSN into the PG* environment variables libpq's
// tools read.
//
// The connection string is not passed as an argument, and that is not a style
// preference: an argument to pg_dump appears in `ps` output for every user on the
// machine and in whatever shell history ran it, and this string carries the
// database password. `sdbimport` refuses the source password as a flag for the
// same reason; there is no way to refuse here except to use the environment.
//
// The DSN is validated with pgxpool.ParseConfig -- the same parser
// database.Initialize uses, so a string this tool accepts is a string the server
// accepts -- and then translated with net/url, because sslmode is a value that
// exists only in the URL's text: pgx consumes it into a TLS config that cannot be
// turned back into the word libpq wants. Those are two different interfaces, not
// two opinions about the same one.
func ConnEnv(dsn string) ([]string, error) {
	if _, err := pgxpool.ParseConfig("postgres://" + dsn); err != nil {
		return nil, fmt.Errorf("the configured database is not a DSN this build can parse (pgxpool.ParseConfig said: %w)", err)
	}

	u, err := url.Parse("postgres://" + dsn)
	if err != nil {
		return nil, fmt.Errorf("parsing the database DSN: %w", err)
	}

	env := []string{"PGAPPNAME=stash-box-backup"}
	host := u.Hostname()
	if host != "" {
		env = append(env, "PGHOST="+host)
	}
	if port := u.Port(); port != "" {
		env = append(env, "PGPORT="+port)
	}
	if u.User != nil {
		if name := u.User.Username(); name != "" {
			env = append(env, "PGUSER="+name)
		}
		// Only when present: an empty PGPASSWORD tells libpq "this is the
		// password", which suppresses the ~/.pgpass lookup that a DSN without a
		// password is supposed to fall back to.
		if pass, ok := u.User.Password(); ok && pass != "" {
			env = append(env, "PGPASSWORD="+pass)
		}
	}
	if db := strings.TrimPrefix(u.Path, "/"); db != "" {
		env = append(env, "PGDATABASE="+db)
	}
	if sslmode := u.Query().Get("sslmode"); sslmode != "" {
		env = append(env, "PGSSLMODE="+sslmode)
	}
	return env, nil
}

// databaseName returns the DSN's database name, which pg_restore needs as an
// argument even when PGDATABASE is set. The name is not a secret; the password is.
func databaseName(dsn string) (string, error) {
	u, err := url.Parse("postgres://" + dsn)
	if err != nil {
		return "", fmt.Errorf("parsing the database DSN: %w", err)
	}
	name := strings.TrimPrefix(u.Path, "/")
	if name == "" {
		return "", errors.New("the database DSN names no database")
	}
	return name, nil
}

// ErrToolMissing names a missing pg_dump/pg_restore rather than letting the shell
// report it: "executable file not found" from a command whose whole job is
// preserving the archive is a message about the wrong thing.
var ErrToolMissing = errors.New("PostgreSQL client tools not found")

// querier is the read surface a pool and a transaction share, so the snapshot's
// counts and row lists can be read through the SAME transaction that exports the
// snapshot the dump uses. Without that, the counts describe a moment slightly
// different from the dump's and a restore's row check fails on a live instance
// for a reason that has nothing to do with the backup being wrong.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// OpenPool opens a pool WITHOUT running migrations.
//
// database.Initialize cannot be used here, and the reason is this tool's whole
// design: Initialize calls runMigrations FIRST, which would (a) MIGRATE the
// database a backup is reading -- a backup that changes its source is not a
// backup -- and (b) migrate a restore's TARGET to the newest version before the
// artifact's migration version could be compared against it, making the one check
// this tool exists to make impossible to fail.
//
// The connection string is parsed exactly as the server parses it, so a DSN this
// accepts is a DSN the server accepts.
func OpenPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, "postgres://"+dsn)
	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", RedactDSN(dsn), err)
	}
	return pool, nil
}

func runTool(ctx context.Context, dsn string, name string, args ...string) error {
	env, err := ConnEnv(dsn)
	if err != nil {
		return err
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return fmt.Errorf("%w: %s is not on PATH; install the PostgreSQL client tools", ErrToolMissing, name)
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// The output is libpq's own diagnosis of a lock, a version mismatch or a
		// missing database, and it is the only thing that says which of those
		// happened. Trimming is not censoring: the DSN never appears in it
		// because the DSN was never an argument.
		return fmt.Errorf("%s failed: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// DumpDatabase writes a custom-format dump of dsn to out.
//
// Custom format rather than plain SQL, deliberately: a plain dump is text a
// half-finished restore leaves behind, while a custom-format dump is all-or-
// nothing per object and carries its own compression and per-table structure.
func DumpDatabase(ctx context.Context, dsn, out string, snapshotID string) error {
	args := []string{"--format=custom", "--file=" + out}
	if snapshotID != "" {
		// The dump reads the snapshot an open transaction exported, so the dump
		// and the counts and image rows read through that transaction describe
		// one point in time rather than two moments a write apart.
		args = append(args, "--snapshot="+snapshotID)
	}
	return runTool(ctx, dsn, "pg_dump", args...)
}

// RestoreDatabase restores a dump into dsn. --clean --if-exists makes a repeated
// restore re-runnable instead of failing on the first object that already exists,
// and --no-owner --no-privileges keeps the restored schema out of the roles of
// whichever machine took the backup: a restored database that only works for a
// user named alvaro is not a restore.
func RestoreDatabase(ctx context.Context, dsn, dump string) error {
	name, err := databaseName(dsn)
	if err != nil {
		return err
	}
	return runTool(ctx, dsn, "pg_restore",
		"--clean", "--if-exists", "--no-owner", "--no-privileges",
		"--dbname="+name, dump)
}

// ServerVersion is recorded so a restore can be recognised as a cross-version one
// before somebody debugs the schema instead of the version.
func ServerVersion(ctx context.Context, pool querier) (string, error) {
	var v string
	if err := pool.QueryRow(ctx, "SHOW server_version").Scan(&v); err != nil {
		return "", fmt.Errorf("reading the server version: %w", err)
	}
	return v, nil
}

// MigrationVersion reads golang-migrate's own bookkeeping. A dump taken at
// migration 96 restored into a database at 90 is a shape mismatch, and this is
// the number that says so.
func MigrationVersion(ctx context.Context, pool querier) (int64, bool, error) {
	var version int64
	var dirty bool
	if err := pool.QueryRow(ctx, "SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty); err != nil {
		return 0, false, fmt.Errorf("reading schema_migrations: %w", err)
	}
	return version, dirty, nil
}

// TableCounts counts every table in the public schema.
//
// The table list comes from information_schema rather than from a list in this
// file: a hardcoded list stops covering the table a later migration adds, and a
// completeness check that silently omits the newest table reports success about
// the thing it cannot see.
func TableCounts(ctx context.Context, pool querier) ([]TableCount, error) {
	rows, err := pool.Query(ctx, `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = 'public' AND table_type = 'BASE TABLE'
		ORDER BY table_name`)
	if err != nil {
		return nil, fmt.Errorf("listing tables: %w", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]TableCount, 0, len(names))
	for _, name := range names {
		var n int64
		q := "SELECT count(*) FROM " + pgx.Identifier{name}.Sanitize()
		if err := pool.QueryRow(ctx, q).Scan(&n); err != nil {
			return nil, fmt.Errorf("counting %s: %w", name, err)
		}
		out = append(out, TableCount{Table: name, Rows: n})
	}
	return out, nil
}

// HasTables reports whether the target database already holds tables, which is
// what makes a restore destructive rather than additive.
func HasTables(ctx context.Context, pool querier) (bool, error) {
	var exists bool
	err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'public' AND table_type = 'BASE TABLE'
		)`).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("checking whether the target database is empty: %w", err)
	}
	return exists, nil
}

// ImageRows reads the rows the image store must satisfy.
func ImageRows(ctx context.Context, pool querier) ([]ImageRow, error) {
	rows, err := pool.Query(ctx, "SELECT id::text, checksum FROM images ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("reading images: %w", err)
	}
	defer rows.Close()

	var out []ImageRow
	for rows.Next() {
		var r ImageRow
		if err := rows.Scan(&r.ID, &r.Checksum); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
