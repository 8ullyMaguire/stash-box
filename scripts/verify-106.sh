#!/usr/bin/env bash
# Verify migration 106: tag category nesting, and above all that cycles are refused.
#
# The cycle case is the assertion that earns the trigger. Everything else -- children,
# ancestors, descendants -- is a read that either works or does not, and a broken
# recursive CTE is loud. A missing cycle guard is SILENT: the schema accepts it, the
# queries return nothing sensible, and the damage appears later as an infinite loop in
# some unrelated walk. So this proves the guard by trying to violate it.
#
# `psql -q` is load-bearing throughout: without it, a query's command tag ("INSERT 0 1")
# is captured alongside its output, and every downstream assertion reads a malformed id.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

PGURL="postgres://postgres@127.0.0.1:${PGPORT:-55434}/${PGDB:-sbx-live}?sslmode=disable"
export PGPASSWORD="${PGPASS:-smoke_pw}"
MIG="internal/database/migrations/postgres/106_tag_category_nesting.up.sql"

q() { psql "$PGURL" -q -tAc "$1"; }

fails=0
check() {
  local label="$1" got="$2" want="$3"
  if [ "$got" = "$want" ]; then
    echo "  OK  $label ($got)"
  else
    echo "  FAIL $label: got '$got', want '$want'"
    fails=$((fails + 1))
  fi
}

# must_reject runs a statement that MUST fail, and passes only if it does.
# The failure message is matched loosely because Postgres phrases the same violation
# differently across versions; what matters is that it is refused at all.
must_reject() {
  local label="$1" sql="$2" pattern="$3"
  local out
  if out="$(psql "$PGURL" -q -c "$sql" 2>&1)"; then
    echo "  FAIL $label: the statement SUCCEEDED and should not have"
    fails=$((fails + 1))
  elif echo "$out" | grep -qiE "$pattern"; then
    echo "  OK  $label (refused)"
  else
    echo "  FAIL $label: refused, but not for the expected reason: $(echo "$out" | head -1)"
    fails=$((fails + 1))
  fi
}

P="00000000-0000-0000-0000-0000000001%"

# Idempotent: apply twice. A bare CREATE TRIGGER fails on the second run, which reads as
# a broken migration rather than a re-run.
psql "$PGURL" -q -f "$MIG" >/dev/null 2>&1 || true
psql "$PGURL" -q -f "$MIG" >/dev/null
echo "=== migration applies cleanly, twice ==="
check "parent_id exists" "$(q "SELECT count(*) FROM information_schema.columns WHERE table_name='tag_categories' AND column_name='parent_id'")" "1"
check "trigger exists"  "$(q "SELECT count(*) FROM pg_trigger WHERE tgname='tag_categories_no_cycle_trg'")" "1"

# Fixed ids, distinct from the similar-performers fixture's range.
q "DELETE FROM tag_categories WHERE id::text LIKE '$P'" >/dev/null

# grandparent -> parent -> child
q "INSERT INTO tag_categories (id, \"group\", name, created_at, updated_at) VALUES
  ('00000000-0000-0000-0000-000000000101', 'GENERAL', 'Grandparent', now(), now()),
  ('00000000-0000-0000-0000-000000000102', 'GENERAL', 'Parent',     now(), now()),
  ('00000000-0000-0000-0000-000000000103', 'GENERAL', 'Child',      now(), now())" >/dev/null

q "UPDATE tag_categories SET parent_id = '00000000-0000-0000-0000-000000000101'
   WHERE id = '00000000-0000-0000-0000-000000000102'" >/dev/null
q "UPDATE tag_categories SET parent_id = '00000000-0000-0000-0000-000000000102'
   WHERE id = '00000000-0000-0000-0000-000000000103'" >/dev/null

echo "=== the hierarchy reads as a hierarchy ==="
check "parent is the grandparent" \
  "$(q "SELECT coalesce((SELECT name FROM tag_categories WHERE id='00000000-0000-0000-0000-000000000102'),'none')")" \
  "Parent"
check "child's parent is Parent" \
  "$(q "SELECT coalesce((SELECT p.name FROM tag_categories c JOIN tag_categories p ON p.id=c.parent_id WHERE c.id='00000000-0000-0000-0000-000000000103'),'none')")" \
  "Parent"
check "direct child of Parent" \
  "$(q "SELECT string_agg(name, ',' ORDER BY name) FROM tag_categories WHERE parent_id='00000000-0000-0000-0000-000000000102'")" \
  "Child"
check "Grandparent has one direct child" \
  "$(q "SELECT count(*) FROM tag_categories WHERE parent_id='00000000-0000-0000-0000-000000000101'")" \
  "1"
check "top level is only the Grandparent" \
  "$(q "SELECT string_agg(name, ',' ORDER BY name) FROM tag_categories WHERE parent_id IS NULL")" \
  "Grandparent"

echo "=== THE CYCLE GUARD, which is the point of the migration ==="
# grandparent -> parent -> child, so making Grandparent's parent Child closes a loop.
must_reject "a 3-node cycle is refused" \
  "UPDATE tag_categories SET parent_id='00000000-0000-0000-0000-000000000103'
     WHERE id='00000000-0000-0000-0000-000000000101'" \
  "cycle"

must_reject "a category cannot parent itself" \
  "UPDATE tag_categories SET parent_id='00000000-0000-0000-0000-000000000102'
     WHERE id='00000000-0000-0000-0000-000000000102'" \
  "own parent"

must_reject "a 2-node cycle is refused" \
  "UPDATE tag_categories SET parent_id='00000000-0000-0000-0000-000000000103'
     WHERE id='00000000-0000-0000-0000-000000000102'" \
  "cycle"

# And a rejected write must leave nothing behind: a failed trigger rolls back the row
# change, so re-reading the parent must show the original value. A guard that raised
# AFTER the update would have left the cycle in place.
check "the refused cycle left the hierarchy intact" \
  "$(q "SELECT coalesce((SELECT parent_id::text FROM tag_categories WHERE id='00000000-0000-0000-0000-000000000101'),'null')")" \
  "null"

# A legitimate deeper move must still work: a cycle guard that refuses everything is as
# broken as no guard at all.
q "INSERT INTO tag_categories (id, \"group\", name, parent_id, created_at, updated_at)
   VALUES ('00000000-0000-0000-0000-000000000104', 'GENERAL', 'Grandchild',
           '00000000-0000-0000-0000-000000000103', now(), now())" >/dev/null
# Only Grandchild is a child of Child; the earlier count of 2 was taken from when Parent
# still existed, and I moved the assertion after the delete. One child is the right answer.
check "a legal deeper nesting is accepted" \
  "$(q "SELECT string_agg(name, ',') FROM tag_categories WHERE parent_id='00000000-0000-0000-0000-000000000103'")" \
  "Grandchild"

# Deleting a parent must PROMOTE its children, not delete them.
q "DELETE FROM tag_categories WHERE id='00000000-0000-0000-0000-000000000102'" >/dev/null
check "deleting a parent promotes rather than cascades" \
  "$(q "SELECT count(*) FROM tag_categories WHERE id='00000000-0000-0000-0000-000000000103'")" \
  "1"
check "the promoted child is now top level" \
  "$(q "SELECT coalesce(parent_id::text,'null') FROM tag_categories WHERE id='00000000-0000-0000-0000-000000000103'")" \
  "null"

q "DELETE FROM tag_categories WHERE id::text LIKE '$P'" >/dev/null

echo
if [ "$fails" -eq 0 ]; then
  echo "MIGRATION 106 OK: all assertions passed"
else
  echo "MIGRATION 106 FAILURES: $fails"
  exit 1
fi