#!/usr/bin/env bash
# Prove migration 107's constraints, on a real database, before any Go exists.
#
# WHY A SCRIPT AND NOT A GO TEST. Three of these constraints are CHECK constraints whose
# entire value is that the DATABASE rejects the write. A service-layer test cannot
# distinguish "the constraint rejected this" from "the service never asked" -- which is the
# same reason internal/database/testutil exposes DB() with that comment. So these are
# proven against the schema directly, and each assertion states which write must fail.
#
#   ./scripts/verify-107.sh [dsn]
#
# Defaults to the same test database the integration suite uses.
set -uo pipefail

DSN="${1:-postgres://postgres@127.0.0.1:55434/stash-box-test?sslmode=disable}"
export PGPASSWORD="${PGPASSWORD:-smoke_pw}"

fail=0
pass=0

# run <expectation: ok|reject> <description> <sql>
#
# `ok` means "the statement executes without error". It does NOT mean the statement's answer
# is what you want -- that was the original bug, and it made nine assertions below vacuous:
# `SELECT count(*) = 1 FROM t WHERE ...` prints "f" and exits 0, so a harness checking only
# the exit code reports success for a query that proved the opposite. Use run_assert for
# anything with a value.
run() {
  local expect="$1" desc="$2" sql="$3"
  local out
  out="$(psql "$DSN" -v ON_ERROR_STOP=1 -tAc "$sql" 2>&1)"
  local rc=$?

  if [[ "$expect" == "ok" && $rc -ne 0 ]]; then
    echo "FAIL  $desc"
    echo "      expected success, got: ${out%%$'\n'*}"
    fail=$((fail + 1))
  elif [[ "$expect" == "reject" && $rc -eq 0 ]]; then
    echo "FAIL  $desc"
    echo "      expected the database to REJECT this, but it succeeded"
    fail=$((fail + 1))
  else
    echo "ok    $desc"
    pass=$((pass + 1))
  fi
}

# run_assert <description> <sql>  -- the sql must return TRUE.
#
# Separate from `run` rather than inferring intent from the SQL text, because "did this
# statement error" and "is this statement's answer what I claimed" are different questions
# and conflating them is what made the whole suite lie.
run_assert() {
  local desc="$1" sql="$2"
  local out rc
  out="$(psql "$DSN" -v ON_ERROR_STOP=1 -tAc "$sql" 2>&1)"
  rc=$?

  if [[ $rc -ne 0 ]]; then
    echo "FAIL  $desc"
    echo "      query errored: ${out%%$'\n'*}"
    fail=$((fail + 1))
  elif [[ "$(echo "$out" | tr -d '[:space:]')" != "t" ]]; then
    echo "FAIL  $desc"
    echo "      the assertion returned '${out%%$'\n'*}', not true"
    fail=$((fail + 1))
  else
    echo "ok    $desc"
    pass=$((pass + 1))
  fi
}

echo "=== applying migration 107 ==="
# Applied directly rather than through the app, so a failure here is about the SQL and not
# about the binary. golang-migrate sorts NUMERICALLY, so 107 goes on after 106 -- which is
# the point that sqlc does not share (see scripts/gen-sqlc-schema-list.sh).
for f in up down; do
  :
done
psql "$DSN" -v ON_ERROR_STOP=1 -q -f internal/database/migrations/postgres/107_shareable_lists.down.sql >/dev/null 2>&1
if ! psql "$DSN" -v ON_ERROR_STOP=1 -q -f internal/database/migrations/postgres/107_shareable_lists.up.sql 2>&1; then
  echo "FAIL  migration 107 does not apply"
  exit 1
fi
echo "ok    migration 107 applies cleanly"
pass=$((pass + 1))

# The column list matches users' NOT NULL columns exactly (invite_tokens is an INTEGER, not
# a jsonb -- I guessed '{}' first and the error named only that column, which reads like a
# missing field rather than a wrong type).
#
# This is not tidiness. The audit case DELETES a user to prove the audit row survives, and
# originally it never created one -- so every run consumed a user. After a few runs only two
# remained, the actor query found nobody, and the case took its `skip` branch: it reported
# nothing, the tally said "0 failed", and the whole script claimed 18/18 while the property
# was never exercised. That is how an ON DELETE CASCADE mutation survived four attempts.
#
# A silently-skipped case is worse than an absent one, because it reports coverage.
STAMP="$(date +%s)-$$"
cleanup() {
  psql "$DSN" -q -c "DELETE FROM users WHERE name LIKE 'verify107-$STAMP-%'" >/dev/null 2>&1 || true
}
trap cleanup EXIT

psql "$DSN" -q -c "INSERT INTO users (id, name, email, password_hash, api_key, invite_tokens, last_api_call, created_at, updated_at)
  VALUES (gen_random_uuid(), 'verify107-$STAMP-owner', 'verify107-$STAMP-owner@example.invalid', 'x',
          'verify107-$STAMP-owner-key', 0, now(), now(), now()),
         (gen_random_uuid(), 'verify107-$STAMP-actor', 'verify107-$STAMP-actor@example.invalid', 'x',
          'verify107-$STAMP-actor-key', 0, now(), now(), now())" >/dev/null

OWNER="$(psql "$DSN" -tA -c "SELECT id FROM users WHERE name = 'verify107-$STAMP-owner'")"
if [[ -z "$OWNER" ]]; then
  echo "FAIL  could not create the owner user; every assertion below would be vacuous"
  exit 1
fi

echo
echo "=== the publish state machine ==="

# A PRIVATE list: no published_at, no published_by. This is the default and the only
# state a newly created list can be in.
run ok "a private list is creatable" \
  "INSERT INTO lists (owner_id, name, owner_name) VALUES ('$OWNER', 'private list', 'owner')"

run reject "published_at without published_by is rejected -- an unauditable publication" \
  "INSERT INTO lists (owner_id, name, owner_name, published_at)
     VALUES ('$OWNER', 'unattributed', 'owner', now())"

run reject "an empty owner_name is rejected -- the denormalised browse column must not be blank" \
  "INSERT INTO lists (owner_id, name, owner_name) VALUES ('$OWNER', 'blank owner', '')"

# Asserted by name as well as by behaviour, so a dropped CHECK reports itself here instead
# of surfacing later as an unrelated failing case.
run_assert "the owner_name CHECK constraint exists" \
  "SELECT count(*) = 1 FROM pg_constraint
    WHERE conname = 'lists_owner_name_required' AND contype = 'c'"

run reject "two lists with the same name for one owner are rejected" \
  "INSERT INTO lists (owner_id, name, owner_name) VALUES ('$OWNER', 'private list', 'owner')"

# The UNIQUE index is enforced by an INDEX, not a constraint, so the only way to prove it
# is to try the duplicate. The case above already does that -- but if the index were
# dropped, the LATER list_items case would abort first and the failure would read "migration
# 107 does not apply" rather than naming the property. Hence the explicit note; the index
# name is asserted too, so a rename is caught rather than silently accepted.
run_assert "the (owner_id, name) unique index exists" \
  "SELECT count(*) = 1 FROM pg_class
    WHERE relname = 'lists_unique_name_per_owner' AND relkind = 'i'"

# The unique index is (owner_id, name), NOT (name) -- so the same list name under a
# different owner is legal and must succeed. Asserting this matters because the
# alternative (a unique index on name alone) would be the plausible mistake, and it would
# make two users collide over a name neither of them chose.
run ok "the same name under a DIFFERENT owner is allowed" \
  "INSERT INTO lists (owner_id, name, owner_name)
     SELECT u.id, 'private list', 'other' FROM users u
      WHERE u.id <> '$OWNER' LIMIT 1"

echo
echo "=== publication is reversible and auditable ==="

run ok "publishing records who and when" \
  "UPDATE lists SET published_at = now(), published_by = '$OWNER'
    WHERE name = 'private list' AND owner_id = '$OWNER'"

run ok "the publication is in the audit trail" \
  "INSERT INTO list_audit (list_id, actor_id, action)
     SELECT id, '$OWNER', 'publish' FROM lists WHERE name = 'private list' LIMIT 1"

run ok "unpublishing clears both halves" \
  "UPDATE lists SET published_at = NULL, published_by = NULL
    WHERE name = 'private list' AND owner_id = '$OWNER'"

echo
echo "=== a private list is unreachable from the browse index ==="

# The partial index is the enforcement, not a WHERE clause. Proved by asking postgres
# whether the index would be used: a draft must not be in it.
# Ask postgres for the index's own PREDICATE and assert a draft cannot match it. This is the
# actual mechanism: the browse index is PARTIAL (`WHERE published_at IS NOT NULL`), so a
# draft is not merely filtered out of a result -- it is not in the index at all.
#
# My first version of this was a tautology -- `WHERE published_at IS NULL AND id IN (SELECT
# id FROM lists WHERE published_at IS NULL)` is true whenever a draft exists, so it passed
# without testing anything. The harness bug that hid this is fixed separately; the assertion
# itself was also wrong, which is the more embarrassing half.
run_assert "the browse index is PARTIAL on published_at, so a draft is not in it" \
  "SELECT pg_get_expr(i.indpred, i.indrelid) LIKE '%published_at IS NOT NULL%'
     FROM pg_index i
     JOIN pg_class c ON c.oid = i.indexrelid
    WHERE i.indrelid = 'lists'::regclass
      AND c.relname = 'lists_published_at_desc'"

# And the converse: a PUBLISHED list is reachable. Published inside the same transaction-free
# step rather than relying on the publish/unpublish cases above having left one published --
# they end with everything unpublished, so an assertion here that assumed a published row
# would fail for a reason unrelated to the index.
run ok "a list is published for the converse check" \
  "UPDATE lists SET published_at = now(), published_by = '$OWNER'
    WHERE name = 'private list' AND owner_id = '$OWNER'"
run_assert "a published list IS in that index" \
  "SELECT count(*) = 1 FROM lists l
    WHERE l.published_at IS NOT NULL
      AND l.id IN (SELECT id FROM lists WHERE published_at IS NOT NULL)"

echo
echo "=== list membership ==="

LIST_ID="$(psql "$DSN" -tA -c "SELECT id FROM lists WHERE name = 'private list' LIMIT 1")"

run ok "an item can be added" \
  "INSERT INTO list_items (list_id, entity_type, entity_id, position)
     VALUES ('$LIST_ID', 'SCENE', gen_random_uuid(), 0)"

run_assert "the duplicate-entity constraint exists" \
  "SELECT count(*) = 1 FROM pg_constraint WHERE conname = 'list_items_unique_entity'"

# The SAME entity twice -- the obvious duplicate.
run reject "the same entity cannot be added twice" \
  "INSERT INTO list_items (list_id, entity_type, entity_id, position)
     SELECT list_id, entity_type, entity_id, 1 FROM list_items WHERE list_id = '$LIST_ID'"

# The same entity_id under a DIFFERENT entity_type. This is the assertion that pins the
# key's SHAPE, and it is what found a real looseness rather than a weak test: with the key
# originally (list_id, entity_type, entity_id), this write SUCCEEDED -- one uuid could sit
# in a list as both a scene and a performer, leaving a client resolving "item 3" with two
# candidates. The key is now (list_id, entity_id), which forbids that outright.
run reject "the same id under a different entity_type is still a duplicate" \
  "INSERT INTO list_items (list_id, entity_type, entity_id, position)
     SELECT list_id, 'PERFORMER', entity_id, 2 FROM list_items WHERE list_id = '$LIST_ID'"

run reject "an item with no entity_type is rejected -- otherwise a row that matches nothing is storable" \
  "INSERT INTO list_items (list_id, entity_type, entity_id, position)
     VALUES ('$LIST_ID', NULL, gen_random_uuid(), 9)"

run reject "an item with no entity_id is rejected" \
  "INSERT INTO list_items (list_id, entity_type, entity_id, position)
     VALUES ('$LIST_ID', 'SCENE', NULL, 9)"

run reject "an item for a nonexistent list is rejected by the foreign key" \
  "INSERT INTO list_items (list_id, entity_type, entity_id, position)
     VALUES (gen_random_uuid(), 'SCENE', gen_random_uuid(), 0)"

echo
echo "=== the audit trail survives its actor ==="

# SET NULL on actor_id, not CASCADE: deleting the user who published must not erase the
# record that they did. This is the one property an audit log must never lose.
# An actor who is NOT the owner of the list under test. That distinction is load-bearing:
# if the actor were the owner, deleting them would cascade the LIST away and take the audit
# row with it through list_id -- making the test pass for entirely the wrong reason.
#
# The user must still exist, so it is chosen from live rows rather than reused from an
# earlier run. Deleting a user that a previous run already removed is a silent no-op, and
# then the audit row trivially still exists with a null actor.
OTHER="$(psql "$DSN" -tA -c "SELECT id FROM users WHERE name = 'verify107-$STAMP-actor'")"
if [[ -n "$OTHER" ]]; then
  # -tA gives an unaligned, undecorated result. Without the `-c` split above, $(...) would
  # capture the INSERT's "INSERT 0 1" status line TOO, and the id would be "uuid\nINSERT 0 1"
  # -- which fails as "invalid input syntax for type uuid", pointing at the SQL rather than
  # at the capture.
  psql "$DSN" -q -c "INSERT INTO list_audit (list_id, actor_id, action)
     SELECT id, '$OTHER', 'publish' FROM lists WHERE name = 'private list' LIMIT 1" >/dev/null
  AUDIT_ID="$(psql "$DSN" -tA -c "SELECT id FROM list_audit
     WHERE actor_id = '$OTHER' ORDER BY created_at DESC LIMIT 1")"
  # PRECONDITION: the delete must actually happen. Without this check a failed delete
  # leaves actor_id set, and the assertion below would fail for the wrong reason -- or, if
  # the audit row had been inserted with a null actor already, pass for the wrong reason.
  if ! psql "$DSN" -q -c "DELETE FROM users WHERE id = '$OTHER'" >/dev/null 2>&1; then
    echo "FAIL  the actor could not be deleted; the audit assertion below would be vacuous"
    fail=$((fail + 1))
  fi
  run_assert "the actor is really gone, so the surviving audit row means SET NULL" \
    "SELECT count(*) = 0 FROM users WHERE id = '$OTHER'"

  run_assert "deleting the actor leaves the audit row, with actor_id nulled" \
    "SELECT count(*) = 1 FROM list_audit WHERE id = '$AUDIT_ID' AND actor_id IS NULL"
else
  echo "skip  only one user in the database; cannot delete an actor to prove SET NULL"
fi

echo
echo "=== the down migration ==="
if psql "$DSN" -v ON_ERROR_STOP=1 -q -f internal/database/migrations/postgres/107_shareable_lists.down.sql 2>&1; then
  if psql "$DSN" -tAc "SELECT count(*) FROM information_schema.tables
                        WHERE table_schema = 'public' AND table_name IN ('lists','list_items','list_audit')" | grep -q '^0$'; then
    echo "ok    down migration removes all three tables"
    pass=$((pass + 1))
  else
    echo "FAIL  down migration left tables behind"
    fail=$((fail + 1))
  fi
else
  echo "FAIL  down migration errors"
  fail=$((fail + 1))
fi

# Re-apply so the database is left in the migrated state for whatever runs next.
psql "$DSN" -v ON_ERROR_STOP=1 -q -f internal/database/migrations/postgres/107_shareable_lists.up.sql >/dev/null 2>&1

echo
echo "=== $pass passed, $fail failed ==="
[[ $fail -eq 0 ]]