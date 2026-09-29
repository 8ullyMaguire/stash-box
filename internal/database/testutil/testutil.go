package testutil

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/stashapp/stash-box/internal/database"
	"github.com/stashapp/stash-box/internal/service"
)

var (
	db      *pgxpool.Pool
	factory *service.Factory
)

const defaultTestDB = "postgres@localhost/stash-box-test?sslmode=disable"

type DatabasePopulater interface {
	PopulateDB(factory *service.Factory) error
}

func pgDropAll(conn *pgxpool.Pool) {
	// we want to drop all tables so that the migration initialises
	// the schema
	//
	// schemaname = 'public' is REQUIRED, not a tidy-up. An unqualified pg_tables
	// also returns PostgreSQL's own catalog (86 rows here, 17 of them ours), and
	// this loop would try to DROP TABLE pg_statistic, pg_type, pg_foreign_table
	// and 66 more inside pg_catalog. Every one of those fails -- correctly, since
	// the catalog is not ours to drop -- and because the Exec error is discarded
	// the loop reports success. The net effect is that a real table which fails to
	// drop is indistinguishable from the 69 guaranteed failures, so the next
	// package's migration run collides with a leftover "relation already exists"
	// and the suite fails in a way that looks like a flaky test.
	rows, err := conn.Query(context.TODO(), `select 'drop table if exists "' || tablename || '" cascade;' from pg_tables where schemaname = 'public'`)

	if err != nil {
		panic("Error dropping tables: " + err.Error())
	}
	defer rows.Close()

	for rows.Next() {
		var stmt string
		if err := rows.Scan(&stmt); err != nil {
			panic("Error dropping tables: " + err.Error())
		}

		// The error is checked rather than discarded. With the catalog rows
		// filtered out a failure here is a genuine one -- a lock held by a
		// previous package that did not close its pool -- and swallowing it is
		// what turned a deterministic failure into an intermittent one that
		// moved between packages and read as flakiness.
		if _, err := conn.Exec(context.TODO(), stmt); err != nil {
			panic("Error dropping table " + stmt + ": " + err.Error())
		}
	}
}

func initPostgres(connString string) func() {
	conn, err := pgxpool.New(context.TODO(), "postgres://"+connString)

	if err != nil {
		panic(fmt.Sprintf("Could not connect to postgres database at %s: %s", connString, err.Error()))
	}

	pgDropAll(conn)
	conn.Close()

	db = database.Initialize(connString)
	factory = service.NewFactory(db, nil) // nil EmailManager is fine for tests

	// Create system users (root, StashBot, etc.) just like main.go does
	factory.User().CreateSystemUsers(context.TODO())

	return teardownPostgres
}

func teardownPostgres() {
	noDrop := os.Getenv("POSTGRES_NODROP")
	if noDrop == "" {
		pgDropAll(db)
	}
	db.Close()
}

func runTests(m *testing.M, populater DatabasePopulater) int {
	var deferFn func()

	pgConnStr := os.Getenv("POSTGRES_DB")
	if pgConnStr == "" {
		pgConnStr = defaultTestDB
	}
	deferFn = initPostgres(pgConnStr)
	// defer close and delete the database
	if deferFn != nil {
		defer deferFn()
	}

	if populater != nil {
		err := populater.PopulateDB(factory)
		if err != nil {
			panic(fmt.Sprintf("Could not populate database: %s", err.Error()))
		}
	}

	// run the tests
	return m.Run()
}

func TestWithDatabase(m *testing.M, populater DatabasePopulater) {
	ret := runTests(m, populater)
	os.Exit(ret)
}

// DB returns the test database pool.
//
// It exists for tests that need to assert something the service layer cannot
// express: migration constraints, indexes and cascade behaviour. Those are
// properties of the schema, and a test that goes through a service cannot
// distinguish "the constraint rejected this" from "the service never asked".
//
// It returns nil before initPostgres has run, which is only possible outside
// TestWithDatabase — such a test would panic on use rather than fail quietly.
func DB() *pgxpool.Pool {
	return db
}

// Factory returns the service factory under test.
func Factory() *service.Factory {
	return factory
}
