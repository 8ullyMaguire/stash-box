#!/usr/bin/env bash
#
# Run the integration suite against a scratch database this script owns.
#
# WHY A SCRATCH DATABASE AND NOT "THE" TEST DATABASE
# --------------------------------------------------
# The suite's packages share one database, and each package's TestMain DROPS
# EVERY TABLE before it runs. Two consequences, both measured:
#
#   1. It must run with -p 1. Two packages migrating concurrently into the same
#      database produce "relation already exists" failures that say nothing
#      about the code.
#   2. The database must be EMPTY of anything the tests did not create. Several
#      tests query a table unfiltered and then assert that a row they just made
#      is in the results; the API clamps a page to query.MaxPerPage = 100, so an
#      archive with more than a hundred matching rows pushes the subject off the
#      page and the test fails for a reason that has nothing to do with what it
#      tests. This is not hypothetical: the API suite run against a seeded
#      instance failed TestPerformerFilterEyeColorIsNull ("performer with no
#      eye_color should match IS NULL") returning exactly 100 ids, and passed
#      unchanged against a fresh database. internal/api/authored_quest_
#      integration_test.go carries the same lesson from an earlier round.
#
# So the script creates its own database, points the suite at it, and drops it
# afterwards whether the run passed or failed.
#
# USAGE
#   INTEGRATION_PG='user:pw@host:port' ./scripts/integration-tests.sh [go test args]
# or with the standard variables:
#   PGHOST=127.0.0.1 PGPORT=5432 PGUSER=postgres PGPASSWORD=... ./scripts/integration-tests.sh
#
# The DSN is never printed with its password: every line this script prints
# about the connection goes through the same redaction the backup tool uses.

set -uo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

redact() {
  # user:pw@host:port -> user:***@host:port
  local s="$1"
  if [[ "$s" == *"@"* ]]; then
    local creds="${s%@*}" rest="${s#*@}"
    printf '%s:***@%s' "${creds%%:*}" "$rest"
  else
    printf '%s' "$s"
  fi
}

# A base connection, WITHOUT a scheme: internal/testutil prepends postgres://
# itself, so a URL here becomes "postgres://postgres://..." and fails with a
# message about the host name.
if [[ -n "${INTEGRATION_PG:-}" ]]; then
  base="$INTEGRATION_PG"
elif [[ -n "${PGHOST:-}" ]]; then
  base="${PGUSER:-postgres}:${PGPASSWORD:-}@${PGHOST}:${PGPORT:-5432}"
else
  echo "integration-tests: set INTEGRATION_PG='user:pw@host:port' or PGHOST/PGPORT/PGUSER/PGPASSWORD" >&2
  exit 2
fi

# The database name is the third part of the base DSN when one is given there.
case "$base" in
  */*) base_dsn="${base%%/*}"; base_db="${base#*/}"; base_db="${base_db%%\?*}" ;;
  *)   base_dsn="$base"; base_db="postgres" ;;
esac

# psql needs host and port separately; split them once, here.
hostport="${base_dsn#*@}"
pg_host="${hostport%%:*}"
pg_port="${hostport##*:}"
pg_user="${base_dsn%%:*}"
pg_pass="${base_dsn#*:}"; pg_pass="${pg_pass%%@*}"

run_sql() {
  local db="$1"; shift
  PGPASSWORD="$pg_pass" psql -h "$pg_host" -p "$pg_port" -U "$pg_user" -d "$db" -q "$@"
}

scratch="stash_box_it_$$"
echo "integration-tests: creating $scratch on $(redact "$base_dsn")"

if ! run_sql "$base_db" -c "CREATE DATABASE \"$scratch\""; then
  echo "integration-tests: could not create $scratch" >&2
  exit 2
fi

cleanup() {
  local rc=$?
  if run_sql "$base_db" -c "DROP DATABASE IF EXISTS \"$scratch\" WITH (FORCE)"; then
    echo "integration-tests: dropped $scratch"
  else
    echo "integration-tests: WARNING: could not drop $scratch" >&2
  fi
  exit "$rc"
}
trap cleanup EXIT INT TERM

echo "integration-tests: go test -tags integration -count=1 -p 1 $* ./..."
POSTGRES_DB="${pg_user}:${pg_pass}@${pg_host}:${pg_port}/${scratch}?sslmode=disable" \
  go test -tags integration -count=1 -p 1 "$@" ./...
