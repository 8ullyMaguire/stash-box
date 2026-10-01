package database

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stashapp/stash-box/internal/config"
	"github.com/stashapp/stash-box/pkg/logger"

	// Register pgx stdlib driver and postgres migrate driver
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Upstream #1215 carries `schemaVersion = 76` in this const block. It is NOT
// restored here, and that is the third time this fork has declined it (see #1183
// and #1266). The constant was deleted deliberately: its value being 76 while
// migration 76 shipped and was never applied meant a fresh database silently
// skipped that migration. Pinning it again would reintroduce exactly that bug, and
// #1215's own migration was renumbered 76 -> 94 for the same family of reason.
const postgresDriver = "postgres"

//go:embed migrations/postgres/*.sql
var migrationsFS embed.FS

// extractSQLCQueryName extracts the query name from sqlc-generated SQL comments
// sqlc embeds query names as comments like: "-- name: GetUser :one"
// For non-sqlc queries, returns the full query (otelpgx default behavior)
func extractSQLCQueryName(query string) string {
	// Check if the query starts with a sqlc name comment
	if strings.HasPrefix(query, "-- name:") {
		parts := strings.Fields(query)
		if len(parts) > 2 {
			return parts[2] // Return the query name (e.g., "GetUser")
		}
	}
	return query // Fallback to full query for non-sqlc queries (default otelpgx behavior)
}

// Initialize opens a PostgreSQL connection pool and runs migrations
func Initialize(databasePath string) *pgxpool.Pool {
	if err := runMigrations(databasePath); err != nil {
		logger.Fatal(err)
	}

	// Parse connection string into pgxpool config
	poolConfig, err := pgxpool.ParseConfig("postgres://" + databasePath)
	if err != nil {
		logger.Fatal(err)
	}

	// Set connection pool configuration
	poolConfig.MaxConns = int32(config.GetMaxOpenConns())
	poolConfig.MinConns = int32(config.GetMaxIdleConns())
	poolConfig.MaxConnLifetime = time.Duration(config.GetConnMaxLifetime()) * time.Minute

	// Add otelpgx tracing with custom span name function to use sqlc query names
	poolConfig.ConnConfig.Tracer = otelpgx.NewTracer(
		otelpgx.WithTrimSQLInSpanName(),
		otelpgx.WithSpanNameFunc(extractSQLCQueryName),
		otelpgx.WithDisableAcquireTracer(),
	)

	// Eagerly load the pg-spgist_hamming extension instead of lazy-loading
	poolConfig.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		var hasBktree bool
		if err := conn.QueryRow(ctx,
			"SELECT EXISTS(SELECT 1 FROM pg_extension WHERE extname = 'bktree')",
		).Scan(&hasBktree); err != nil {
			return err
		}
		if hasBktree {
			if _, err := conn.Exec(ctx, "LOAD 'bktree'"); err != nil {
				return err
			}
		}
		return nil
	}

	// Create connection pool
	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		logger.Fatal(err)
	}

	return pool
}

// runMigrations runs database migrations
func runMigrations(databasePath string) error {
	migrations, err := iofs.New(migrationsFS, "migrations/postgres")
	if err != nil {
		return fmt.Errorf("failed to create migration source: %w", err)
	}

	m, err := migrate.NewWithSourceInstance(
		"iofs",
		migrations,
		fmt.Sprintf("%s://%s", postgresDriver, databasePath),
	)
	if err != nil {
		return fmt.Errorf("failed to initialize migration: %w", err)
	}
	defer m.Close()

	m.Log = &migrateLogger{}

	// Migrate to the latest available version, rather than to a hardcoded one.
	//
	// This used to subtract a constant `schemaVersion` from the database's
	// current version and step by the difference. That silently skipped every
	// migration above the constant: the constant was 75, so migration 76 was
	// parsed, embedded, shipped and then never applied — in every environment,
	// with no error. `migrate.Up` is both simpler and correct; it walks to the
	// newest migration the source actually contains.
	//
	// The constant is gone on purpose. Deriving the target from the filesystem
	// means a new migration cannot be forgotten, and the failure mode of the old
	// code (silently doing less than it appears to) is not one worth keeping.
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("failed to run database migrations: %w", err)
	}

	return nil
}

type migrateLogger struct {
	migrate.Logger
}

// Printf is like fmt.Printf
func (*migrateLogger) Printf(format string, v ...any) {
	logger.Debugf("Migration: "+format, v...)
}

// Verbose should return true when verbose logging output is wanted
func (*migrateLogger) Verbose() bool {
	return true
}
