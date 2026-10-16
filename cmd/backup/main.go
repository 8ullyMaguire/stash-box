// Command backup writes and restores a stash-box backup artifact: a
// point-in-time database dump paired with a copy of the image store, checksummed,
// described by a manifest written last.
//
// Usage:
//
//	backup -config <file> -out <dir>            # write an artifact
//	backup -verify -dir <dir>                   # check one, without a database
//	backup -restore -dir <dir> -database <dsn>  # restore one, then check the result
//
// # What "consistent" means here, because the word is doing two jobs
//
// The database half is point-in-time: the dump, the table row counts, the
// migration version and the image rows are all read inside ONE exported snapshot
// (`SELECT pg_export_snapshot()`), and pg_dump is pointed at that same snapshot
// with `--snapshot=`. A backup whose row counts describe a different moment than
// its dump is a backup whose restore check fails on a live instance for a reason
// that has nothing to do with the backup being wrong.
//
// The image store is NOT inside that snapshot -- the files live outside the
// database and copying them is not atomic with the dump -- so the pair is
// CHECKED instead of asserted: every row in `images` that claims local bytes must
// have a file whose md5 equals the checksum the row records, and a missing or
// altered file fails the run before any manifest is written. That is how far the
// claim goes, and the manifest says so in its own notes rather than leaving a
// reader to infer it.
//
// # The order of the writes is the design
//
// db.dump, then images/, then README, then CHECKSUMS.sha256, then MANIFEST.json --
// LAST. A run that dies partway (a full disk, a failed dump, a SIGTERM) leaves a
// directory that both -verify and -restore REFUSE, instead of something that
// looks like a backup of a smaller instance.
//
// # Exit codes
//
//	0  it worked
//	1  it failed
//	2  it refused: -restore was pointed at a database that already contains
//	   tables and -force was not given
//
// Refusal gets its own code because a script needs to tell "I declined to destroy
// something" from "the backup is broken", and the two want opposite responses.
//
// # The connection string is never an argument
//
// pg_dump and pg_restore take a connection string only as an argument, where it
// lands in `ps` output and in shell history. This command parses the configured
// DSN the way the server does and passes the parts as PGHOST/PGPORT/PGUSER/
// PGPASSWORD/PGDATABASE/PGSSLMODE in the child's environment. `sdbimport` refuses
// the source password as a flag for the same reason.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/viper"

	"github.com/stashapp/stash-box/internal/backup"
	"github.com/stashapp/stash-box/internal/config"
)

// version is stamped at build time with -ldflags "-X main.version=...". "dev" is
// what an unstamped build says, and it is written into the manifest: an artifact
// that does not say which tool wrote it cannot be trusted to be readable later.
var version = "dev"

func main() {
	code, err := run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "backup: %v\n", err)
	}
	if errors.Is(err, backup.ErrRefused) {
		os.Exit(2)
	}
	if err != nil {
		os.Exit(1)
	}
	os.Exit(code)
}

func run() (int, error) {
	var (
		configPath = flag.String("config", "", "path to the instance's config file (required to back up; optional for -restore when -database is given)")
		outDir     = flag.String("out", "", "directory to write the artifact into (default backup-<UTC timestamp>)")
		dir        = flag.String("dir", "", "an existing artifact, for -verify and -restore")
		verify     = flag.Bool("verify", false, "check an artifact against its own manifest and exit")
		restore    = flag.Bool("restore", false, "restore an artifact into -database")
		database   = flag.String("database", "", "the database to restore INTO (default: the configured one); never printed")
		force      = flag.Bool("force", false, "allow -restore to drop and replace an existing database")
	)
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch {
	case *verify && *restore:
		return 1, errors.New("-verify and -restore are different questions; pick one")
	case *verify:
		if *dir == "" {
			return 1, errors.New("-verify needs -dir")
		}
		m, err := backup.Verify(*dir)
		if err != nil {
			return 1, err
		}
		fmt.Printf("%s verifies: %d files, dump of migration %d, %d tables recorded, %d images verified against the database's md5 when it was taken\n",
			*dir, len(m.Files), m.Database.Migration, len(m.Database.Tables), m.Images.RowsVerified)
		return 0, nil
	case *restore:
		if *dir == "" {
			return 1, errors.New("-restore needs -dir")
		}
		dsn, err := resolveDSN(*database, *configPath)
		if err != nil {
			return 1, err
		}
		pool, err := backup.OpenPool(ctx, dsn)
		if err != nil {
			return 1, err
		}
		defer pool.Close()

		m, err := backup.RestoreArtifact(ctx, pool, dsn, *dir, *force)
		if err != nil {
			return 1, err
		}
		fmt.Printf("restored %s into %s: migration %d, %d tables, every recorded row count matches the manifest\n",
			*dir, backup.RedactDSN(dsn), m.Database.Migration, len(m.Database.Tables))
		return 0, nil
	}

	// The writing path.
	if *configPath == "" {
		return 1, errors.New("-config is required to take a backup")
	}
	dsn, err := loadConfig(*configPath)
	if err != nil {
		return 1, err
	}
	if *outDir == "" {
		*outDir = "backup-" + time.Now().UTC().Format("20060102T150405Z")
	}
	if !filepath.IsAbs(*outDir) {
		abs, err := filepath.Abs(*outDir)
		if err != nil {
			return 1, err
		}
		*outDir = abs
	}

	pool, err := backup.OpenPool(ctx, dsn)
	if err != nil {
		return 1, err
	}
	defer pool.Close()

	m, err := backup.Snapshot(ctx, pool, dsn, backup.Options{
		ImagesDir:   config.GetImageLocation(),
		ToolVersion: version,
		Now:         time.Now(),
	}, *outDir)
	if err != nil {
		return 1, err
	}

	fmt.Printf("wrote %s\n", *outDir)
	fmt.Printf("  database: PostgreSQL %s, migration %d, %d tables recorded\n", m.Database.ServerVersion, m.Database.Migration, len(m.Database.Tables))
	if m.Images.Files > 0 || m.Images.RowsWithoutFile > 0 {
		fmt.Printf("  images:   %d files (%d bytes) copied; %d rows matched the database's md5, %d rows have no file in the store, %d store files are referenced by no row\n",
			m.Images.Files, m.Images.Bytes, m.Images.RowsVerified, m.Images.RowsWithoutFile, m.Images.FilesWithoutRow)
	}
	fmt.Printf("  artifact: %d files, manifest written last\n", len(m.Files))
	fmt.Printf("check it with: backup -verify -dir %s\n", *outDir)
	return 0, nil
}

// resolveDSN prefers -database and falls back to the configured one.
func resolveDSN(flagDSN, configPath string) (string, error) {
	if flagDSN != "" {
		return flagDSN, nil
	}
	if configPath == "" {
		return "", errors.New("-restore needs a target: pass -database, or -config so the configured database can be used")
	}
	return loadConfig(configPath)
}

// loadConfig reads the instance's config and returns its database DSN.
//
// The ORDER IS LOAD-BEARING and is copied from cmd/sdbimport: viper.ReadInConfig()
// must come FIRST. The trap that motivated the warning there is in
// config.InitializeDefaults(), which ends in viper.WriteConfig() -- called before
// the file has been read, viper writes its DEFAULTS over the real config, silently
// replacing the DSN, the JWT key and the session key, on disk. This command calls
// config.Initialize(), which is `viper.Unmarshal(&C)` and writes nothing (read it
// before trusting this comment), so a backup cannot rewrite the config it was
// pointed at. The order is kept anyway: the two functions differ by one word and
// this is the wrong place to find that out.
func loadConfig(path string) (string, error) {
	viper.SetConfigFile(path)
	if err := viper.ReadInConfig(); err != nil {
		return "", fmt.Errorf("reading config %s: %w", path, err)
	}
	if err := config.Initialize(); err != nil {
		return "", fmt.Errorf("loading config %s: %w", path, err)
	}
	dsn := strings.TrimSpace(config.GetDatabasePath())
	if dsn == "" {
		return "", fmt.Errorf("config %s sets no database", path)
	}
	return dsn, nil
}
