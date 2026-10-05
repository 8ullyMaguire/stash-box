#!/usr/bin/env bash
# Provision sbx-scratch with the full migration chain, LEAVING the schema in
# place so ad-hoc SQL probes have tables to query.
#
# Why this script exists, both reasons learned the hard way:
#
# 1. The integration harness drops every table at teardown. Run the suite, then
#    probe, and every probe finds no tables -- so a probe that checks "is the
#    trigger there?" answers "no" against an empty schema and looks like a real
#    finding. Two mutation-verification runs were invalidated this way before the
#    schema was made to survive.
#
# 2. Order must match golang-migrate: numeric on the version prefix.
#    `sort -V` on BASENAMES, and not `sort -t_ -k1,1n` on full paths. That looks
#    equivalent and is not: with `_` as the field separator and full paths as
#    input, field 1 is the whole directory prefix, so the numeric comparison
#    applies to the wrong text and the order comes out as
#      09, 100, 102, 103, 10, 11, ...
#    which fails migration 100 on a foreign key to authored_quests (created by 80).
#    Measured, not assumed -- this cost two provisioning runs.
#
# Usage: scripts/provision-scratch-db.sh [database-name]
set -euo pipefail
cd "$(dirname "$0")/.."

DB="${1:-sbx-scratch}"
export PGPASSWORD="${PGPASSWORD:-smoke_pw}"
PSQL=(psql -h 127.0.0.1 -p 55434 -U postgres -d "$DB" -v ON_ERROR_STOP=1 -q)

"${PSQL[@]}" -c "DROP SCHEMA public CASCADE; CREATE SCHEMA public;" >/dev/null 2>&1

MIGRATIONS=internal/database/migrations/postgres
applied=0
while read -r name; do
  if ! err=$("${PSQL[@]}" -f "$MIGRATIONS/$name" 2>&1 >/dev/null); then
    echo "FAILED at $name (after $applied migrations)"
    echo "$err" | grep -E 'ERROR' | head -3
    exit 1
  fi
  applied=$((applied + 1))
done < <(ls "$MIGRATIONS" | grep '\.up\.sql$' | sort -V)

# Mark the schema as fully migrated.
#
# The loop above pipes each .up.sql through psql directly, which does NOT write
# golang-migrate's schema_migrations table. So the database is left with the full
# schema and a schema_migrations row saying version 1, dirty -- and the app's
# runMigrations (database.go:99) then tries to apply migration 1 again and dies
# with `relation "performers" already exists`. That error reads like a schema
# problem and is really a bookkeeping one.
#
# Force the version to the highest migration actually applied, with dirty=false,
# so the app starts instead of re-running the chain.
HIGHEST=$(ls "$MIGRATIONS" | grep '\.up\.sql$' | sort -V | tail -1 | cut -d_ -f1)
# The table is created BY the first migration, so on a freshly dropped schema it
# may not exist yet -- CREATE IF NOT EXISTS rather than assuming, or provisioning
# a brand new database fails at the very last step.
"${PSQL[@]}" -c "CREATE TABLE IF NOT EXISTS schema_migrations (version bigint not null primary key, dirty boolean not null);" >/dev/null
# Clear any stale lower versions: golang-migrate reads the HIGHEST row, and a
# leftover "1, dirty" from an earlier partial run would otherwise be picked up
# and make the app re-run the whole chain.
"${PSQL[@]}" -c "DELETE FROM schema_migrations WHERE version < $HIGHEST;" >/dev/null
"${PSQL[@]}" -c "INSERT INTO schema_migrations (version, dirty) VALUES ($HIGHEST, false)
  ON CONFLICT (version) DO UPDATE SET dirty = false;" >/dev/null

echo "database:    $DB"
echo "schema_migrations: version $HIGHEST, dirty false"
echo "migrations:  $applied applied"
echo "tables:      $("${PSQL[@]}" -tAc "select count(*) from pg_tables where schemaname='public'")"
echo "triggers:    $("${PSQL[@]}" -tAc "select count(*) from pg_trigger where not tgisinternal")"
echo "scene_search columns: $("${PSQL[@]}" -tAc "select count(*) from information_schema.columns where table_name='scene_search'")"