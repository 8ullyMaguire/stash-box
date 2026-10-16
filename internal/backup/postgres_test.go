package backup

import (
	"strings"
	"testing"
)

const testDSN = "stashbox:pw-with-special@127.0.0.1:55434/stash-box-test?sslmode=disable"

func TestConnEnvNeverCarriesTheWholeConnectionString(t *testing.T) {
	env, err := ConnEnv(testDSN)
	if err != nil {
		t.Fatalf("ConnEnv: %v", err)
	}
	joined := strings.Join(env, "\n")
	// The point of the env-var route: pg_dump/pg_restore take a connection string
	// only as an argument, where it lands in `ps` and shell history. Nothing that
	// goes into the environment may be the string itself.
	if strings.Contains(joined, "postgres://") {
		t.Errorf("a connection string reached the environment: %v", env)
	}
	if strings.Contains(joined, "pw-with-special@") {
		t.Errorf("userinfo reached the environment as one value: %v", env)
	}
	if !strings.Contains(joined, "PGPASSWORD=pw-with-special") {
		t.Errorf("the password is not carried as its own variable: %v", env)
	}
	for _, want := range []string{"PGHOST=127.0.0.1", "PGPORT=55434", "PGUSER=stashbox", "PGDATABASE=stash-box-test", "PGSSLMODE=disable"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %s in %v", want, env)
		}
	}
}

func TestConnEnvOmitsAnAbsentPassword(t *testing.T) {
	// An empty PGPASSWORD is not the same as an unset one: it tells libpq "this is
	// the password", which suppresses the ~/.pgpass lookup a DSN without a
	// password is supposed to fall back to.
	env, err := ConnEnv("127.0.0.1:55434/stash-box-test?sslmode=disable")
	if err != nil {
		t.Fatalf("ConnEnv: %v", err)
	}
	if strings.Contains(strings.Join(env, "\n"), "PGPASSWORD") {
		t.Errorf("PGPASSWORD was set for a DSN that carries no password: %v", env)
	}
}

func TestConnEnvLeavesSSLMODEOutWhenTheDSNDoes(t *testing.T) {
	env, err := ConnEnv("127.0.0.1:55434/db")
	if err != nil {
		t.Fatalf("ConnEnv: %v", err)
	}
	if strings.Contains(strings.Join(env, "\n"), "PGSSLMODE") {
		t.Errorf("PGSSLMODE was invented for a DSN that does not mention sslmode: %v", env)
	}
}

func TestConnEnvRefusesSomethingTheServerWouldRefuse(t *testing.T) {
	// The DSN is validated with pgxpool.ParseConfig -- the parser the server
	// itself uses -- so this tool cannot accept a string the server rejects.
	if _, err := ConnEnv("%zz:8080/db"); err == nil {
		t.Fatal("an unparseable DSN was accepted")
	}
}

func TestDatabaseNameIsRequired(t *testing.T) {
	if name, err := databaseName(testDSN); err != nil || name != "stash-box-test" {
		t.Fatalf("databaseName = %q,%v want stash-box-test", name, err)
	}
	if _, err := databaseName("127.0.0.1:55434"); err == nil {
		t.Fatal("a DSN with no database was accepted: pg_restore needs one as an argument even when PGDATABASE is set")
	}
}

func TestRedactDSNHidesThePasswordBothWays(t *testing.T) {
	cases := map[string]string{
		testDSN: "stashbox:[REDACTED]@127.0.0.1:55434/stash-box-test?sslmode=disable",
		"postgres://stashbox:pw@localhost:5432/db": "postgres://stashbox:[REDACTED]@localhost:5432/db",
		"postgres://stashbox@localhost:5432/db":    "postgres://stashbox@localhost:5432/db",
		"127.0.0.1:5432/db?sslmode=disable":        "127.0.0.1:5432/db?sslmode=disable",
	}
	for in, want := range cases {
		if got := RedactDSN(in); got != want {
			t.Errorf("RedactDSN(%q) = %q, want %q", in, got, want)
		}
	}
}
