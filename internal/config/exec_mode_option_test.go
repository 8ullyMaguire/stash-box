package config

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Upstream PR #1280 "Fix query plan issues" adds `ExecuteCountCustomPlan`, which calls
// `db.QueryRow(ctx, sql, append([]any{pgx.QueryExecModeCacheDescribe}, args...)...)` to stop
// PostgreSQL settling on a generic plan for a parameter-sensitive ILIKE count.
//
// The mechanism looks wrong on inspection. `DBTX.QueryRow` is pgx's ordinary
// `(ctx, sql, args...)`; there is no variadic exec-mode parameter; and `QueryExecMode` is an
// `int32` enum, not something bindable. Prepending it to a query's arguments looks exactly
// like the mode landing on `$1`.
//
// It is not. pgx runs an **option loop** over the leading arguments before binding anything:
//
//	for len(args) > 0 {
//		switch arg := args[0].(type) {
//		case QueryExecMode:
//			mode = arg
//			args = args[1:]
//		...
//		default:
//			break optionLoop
//		}
//	}
//
// So a leading `QueryExecMode` is consumed as a driver option and never becomes a bind
// parameter. #1280's ordering is right for that reason, not by luck.
//
// I read the signature first, concluded the PR was broken, and was about to record a "do not
// port" note. The mode-only test below is what corrected it: a call with NO placeholders
// succeeds and returns 7, which is impossible if the mode were bound. These tests exist so
// the next reader checks rather than assumes — in either direction.

// A mode argument with no placeholders at all. If it were bound as $1 this would fail with
// "bind message supplies 1 parameters, but prepared statement requires 0".
func TestAQueryExecModeArgumentIsConsumedAsAnOptionNotBound(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	var n int
	if err := pool.QueryRow(ctx, "SELECT 7", pgx.QueryExecModeCacheDescribe).Scan(&n); err != nil {
		t.Fatalf("a leading QueryExecMode was rejected: %v. If this starts failing, pgx's "+
			"option loop over leading arguments has changed, and #1280's mechanism with it.", err)
	}
	if n != 7 {
		t.Errorf("SELECT 7 returned %d, want 7", n)
	}
}

// The mode is consumed, so the query's OWN parameters still land correctly. This is the
// failure that would matter: if the mode occupied $1 every argument would shift by one and a
// count query would return a silently wrong number.
func TestTheModeDoesNotShiftTheQueriesOwnParameters(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	const q = "SELECT COUNT(*) FROM (SELECT 1 FROM generate_series(1, 10) AS t LIMIT $1) s"

	for _, limit := range []int{0, 1, 5, 10} {
		var want, got int
		if err := pool.QueryRow(ctx, q, limit).Scan(&want); err != nil {
			t.Fatalf("plain form with LIMIT %d: %v", limit, err)
		}
		if err := pool.QueryRow(ctx, q, pgx.QueryExecModeCacheDescribe, limit).Scan(&got); err != nil {
			t.Fatalf("mode form with LIMIT %d: %v", limit, err)
		}
		if got != want {
			t.Errorf("LIMIT %d: plain form gave %d but the mode form gave %d — the mode "+
				"consumed a bind position", limit, want, got)
		}
		if want != limit {
			t.Errorf("LIMIT %d returned %d, so the fixture does not exercise the parameter",
				limit, want)
		}
	}
}

// Every mode pgx defines must be consumed the same way. A mode that is *not* in the option
// loop would bind instead, and which modes those are is a property of pgx's version — the
// kind of thing a dependency bump can change underneath a port decision.
func TestEveryDefinedExecModeIsConsumedAsAnOption(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	modes := []pgx.QueryExecMode{
		pgx.QueryExecModeCacheStatement,
		pgx.QueryExecModeCacheDescribe,
		pgx.QueryExecModeDescribeExec,
		pgx.QueryExecModeExec,
		pgx.QueryExecModeSimpleProtocol,
	}
	for _, mode := range modes {
		var n int
		if err := pool.QueryRow(ctx, "SELECT 7", mode).Scan(&n); err != nil {
			t.Errorf("mode %d was not consumed as an option: %v", mode, err)
			continue
		}
		if n != 7 {
			t.Errorf("mode %d returned %d, want 7", mode, n)
		}
	}
}

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("POSTGRES_DB")
	if databaseURL == "" {
		t.Skip("POSTGRES_DB not set")
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
