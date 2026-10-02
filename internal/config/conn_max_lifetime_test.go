package config

import "testing"

// `GetConnMaxLifetime` returned `C.Postgres.MaxIdleConns` — a copy-paste slip in which the
// max-idle-conns defaulting helper was cloned and its body never changed. Two consequences,
// and both are silent:
//
//   - `conn_max_lifetime` is IGNORED. An operator who sets it gets the idle-conns value.
//   - with the config file absent (the default), MaxIdleConns is 0, so the returned value is
//     0 and `MaxConnLifetime` becomes 0, which `pgxpool` reads as "no limit" — connections
//     are never recycled.
//
// The second is the one that bites in production: an unbounded connection lifetime against
// Postgres behind a pgbouncer, or through a NAT that silently drops idle connections, shows
// up as a pool that hands out dead sockets.

// `C.Postgres` is an inline anonymous struct, not a named `PostgresConfig`, so the helper
// takes the three values rather than a struct literal. Assigning the fields individually is
// also narrower than swapping the whole struct: a test can then never disturb an unrelated
// setting such as the CSP header.
func withPostgres(t *testing.T, maxIdle, connMaxLifetime int) {
	t.Helper()
	prevIdle, prevLifetime := C.Postgres.MaxIdleConns, C.Postgres.ConnMaxLifetime
	C.Postgres.MaxIdleConns = maxIdle
	C.Postgres.ConnMaxLifetime = connMaxLifetime
	t.Cleanup(func() {
		C.Postgres.MaxIdleConns = prevIdle
		C.Postgres.ConnMaxLifetime = prevLifetime
	})
}

func TestConnMaxLifetimeIsReadFromItsOwnField(t *testing.T) {
	withPostgres(t, 7, 90)

	if got := GetConnMaxLifetime(); got != 90 {
		t.Errorf("GetConnMaxLifetime() = %d, want 90: conn_max_lifetime is being read from "+
			"max_idle_conns", got)
	}
}

// The two fields must be independent. The bug made them the same number, so a fixture that
// sets only one of them is what distinguishes the two readings.
func TestConnMaxLifetimeIsIndependentOfMaxIdleConns(t *testing.T) {
	withPostgres(t, 25, 0)
	if got := GetConnMaxLifetime(); got == 25 {
		t.Error("GetConnMaxLifetime() returned the idle-conns count, so the two settings " +
			"cannot be varied independently")
	}

	withPostgres(t, 0, 42)
	if got := GetMaxIdleConns(); got == 42 {
		t.Error("GetMaxIdleConns() returned the conn-lifetime value")
	}
	if got := GetConnMaxLifetime(); got != 42 {
		t.Errorf("GetConnMaxLifetime() = %d with max_idle_conns unset, want 42", got)
	}
}

// A zero or negative setting falls back to a real default. Without this, an absent config
// file yields 0, and pgxpool reads MaxConnLifetime == 0 as "never recycle".
func TestConnMaxLifetimeDefaultsRatherThanDisablingTheLimit(t *testing.T) {
	for _, unset := range []int{0, -1, -60} {
		withPostgres(t, 0, unset)
		got := GetConnMaxLifetime()
		if got <= 0 {
			t.Errorf("GetConnMaxLifetime() = %d with conn_max_lifetime=%d, want a positive "+
				"default: 0 means pgxpool never recycles a connection", got, unset)
		}
		if got == GetMaxIdleConns() && GetMaxIdleConns() != 10 {
			t.Errorf("GetConnMaxLifetime() = %d, which is the idle-conns count", got)
		}
	}
}

// An explicitly configured value is honoured exactly, not treated as a request for the
// default. An operator who writes 5 means 5 minutes.
func TestAnExplicitConnMaxLifetimeIsNotOverriddenByTheDefault(t *testing.T) {
	withPostgres(t, 10, 5)
	if got := GetConnMaxLifetime(); got != 5 {
		t.Errorf("GetConnMaxLifetime() = %d, want the configured 5", got)
	}
}
