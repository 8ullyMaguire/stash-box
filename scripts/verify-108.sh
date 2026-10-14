#!/usr/bin/env bash
# Prove migration 108's constraints, its backfill and its one-way mirror, on a real
# database, before any Go exists.
#
# WHY A SCRIPT AND NOT A GO TEST. Every assertion below is about what the DATABASE
# refuses, or about what a trigger does to a row nobody asked it to touch. A
# service-layer test cannot tell "the constraint rejected this" from "the service
# never asked", and it cannot observe a trigger firing at all -- the same reason
# internal/database/testutil exposes DB() with that comment.
#
#   ./scripts/verify-108.sh [dsn]
#
# Defaults to the same test database the integration suite uses. An empty database
# is not a failure case: the down migration is applied first, so this can be pointed
# at a freshly created database and still work.
set -uo pipefail

cd "$(dirname "$0")/.."
DSN="${1:-postgres://postgres@127.0.0.1:55434/stash-box-test?sslmode=disable}"
export PGPASSWORD="${PGPASSWORD:-smoke_pw}"

fail=0
pass=0
MIG=internal/database/migrations/postgres

# run <expectation: ok|reject> <description> <sql>
#
# `ok` means "the statement executes without error". It does NOT mean the answer is
# what you want: `SELECT count(*) = 1 FROM t WHERE ...` prints "f" and exits 0, so a
# harness checking only the exit code reports success for a query that proved the
# opposite. Use run_assert for anything with a value.
run() {
  local expect="$1" desc="$2" sql="$3"
  local out rc
  out="$(psql "$DSN" -v ON_ERROR_STOP=1 -tAc "$sql" 2>&1)"
  rc=$?

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

# run_assert <description> <sql> -- the sql must return TRUE.
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

SCENE=11111111-1111-1111-1111-111111111111
SITE=22222222-2222-2222-2222-222222222222
SITE2=33333333-3333-3333-3333-333333333333

echo "=== rolling back to a pre-108 state, then planting rows for the backfill ==="
psql "$DSN" -v ON_ERROR_STOP=1 -q -f "$MIG/108_content_sources.down.sql" >/dev/null 2>&1
run ok "a site exists to hang URLs on" \
  "INSERT INTO sites (id, name, valid_types, created_at, updated_at) VALUES ('$SITE', 'verify-108-site', '{}', NOW(), NOW()) ON CONFLICT (id) DO NOTHING"
run ok "a scene exists to hang sources on" \
  "INSERT INTO scenes (id, title, created_at, updated_at) VALUES ('$SCENE', 'verify-108', NOW(), NOW()) ON CONFLICT (id) DO NOTHING"
# Planted BEFORE 108 is applied: this is the row the migration's backfill has to
# notice. The trigger does not exist yet, so nothing else can create its twin, and
# without this row the backfill assertion below would be vacuously true.
run ok "a scene_urls row that predates the migration" \
  "INSERT INTO scene_urls (scene_id, url, site_id) VALUES ('$SCENE', 'https://example.invalid/preexisting', '$SITE') ON CONFLICT DO NOTHING"

echo
echo "=== applying migration 108 ==="
if ! err=$(psql "$DSN" -v ON_ERROR_STOP=1 -q -f "$MIG/108_content_sources.up.sql" 2>&1); then
  echo "FAIL  migration 108 does not apply"
  echo "      ${err%%$'\n'*}"
  exit 1
fi
echo "ok    migration 108 applies cleanly"
pass=$((pass + 1))

echo
echo "=== the backfill reached the rows that existed before it ==="
# Both halves are the point. Without the count, "every scene_urls row has a twin" is
# vacuously true on an empty database -- the failure mode this repository has already
# shipped once, where an absence assertion passed with the thing present on screen.
run_assert "there IS a pre-108 scene_urls row to check (a vacuous pass proves nothing)" \
  "SELECT count(*) = 1 FROM scene_urls WHERE scene_id = '$SCENE' AND url = 'https://example.invalid/preexisting'"
run_assert "the pre-existing URL now has a content_sources twin" \
  "SELECT count(*) = 1 FROM content_sources WHERE entity_type = 'scene' AND entity_id = '$SCENE' AND url = 'https://example.invalid/preexisting'"
run_assert "and the backfill made NO health claim: status is 'unknown', not 'live'" \
  "SELECT status = 'unknown' FROM content_sources WHERE entity_id = '$SCENE' AND url = 'https://example.invalid/preexisting'"
run_assert "and elected nothing canonical" \
  "SELECT NOT canonical FROM content_sources WHERE entity_id = '$SCENE' AND url = 'https://example.invalid/preexisting'"
run_assert "every scene_urls row has a twin" \
  "SELECT count(*) = 0 FROM scene_urls su WHERE NOT EXISTS (SELECT 1 FROM content_sources cs WHERE cs.entity_type = 'scene' AND cs.entity_id = su.scene_id AND cs.url = su.url)"

echo
echo "=== the status machine is a constraint, not a convention ==="
run reject "an unknown status is refused" \
  "INSERT INTO content_sources (entity_type, entity_id, url, status) VALUES ('scene', gen_random_uuid(), 'x://1', 'flaky')"
run ok "'resurrected' is NOT a status (a revived source is live again)" \
  "INSERT INTO content_sources (entity_type, entity_id, url, status) VALUES ('scene', gen_random_uuid(), 'x://2', 'live')"
run reject "an unknown entity_type is refused" \
  "INSERT INTO content_sources (entity_type, entity_id, url) VALUES ('performer', gen_random_uuid(), 'x://3')"
run reject "a negative failure count is refused" \
  "INSERT INTO content_sources (entity_type, entity_id, url, check_failures) VALUES ('scene', gen_random_uuid(), 'x://4', -1)"
run reject "an archived_url without a timestamp is refused" \
  "INSERT INTO content_sources (entity_type, entity_id, url, archived_url) VALUES ('scene', gen_random_uuid(), 'x://5', 'https://web.archive.org/x')"
run reject "an archive timestamp without a url is refused" \
  "INSERT INTO content_sources (entity_type, entity_id, url, archived_at) VALUES ('scene', gen_random_uuid(), 'x://6', now())"

echo
echo "=== one row per (thing, url), one canonical per thing ==="
run ok "promoting a source to canonical" \
  "INSERT INTO content_sources (entity_type, entity_id, url, canonical) VALUES ('scene', '$SCENE', 'https://example.invalid/a', true)"
run reject "the same url twice on one entity" \
  "INSERT INTO content_sources (entity_type, entity_id, url) VALUES ('scene', '$SCENE', 'https://example.invalid/a')"
run reject "a SECOND canonical source for the same scene" \
  "INSERT INTO content_sources (entity_type, entity_id, url, canonical) VALUES ('scene', '$SCENE', 'https://example.invalid/b', true)"
run ok "a non-canonical second source is fine" \
  "INSERT INTO content_sources (entity_type, entity_id, url) VALUES ('scene', '$SCENE', 'https://example.invalid/b')"
run_assert "exactly one canonical source exists for that scene" \
  "SELECT count(*) = 1 FROM content_sources WHERE entity_id = '$SCENE' AND canonical"

echo
echo "=== the mirror: scene_urls writes reach content_sources ==="
run ok "inserting a scene_urls row" \
  "INSERT INTO scene_urls (scene_id, url, site_id) VALUES ('$SCENE', 'https://example.invalid/mirrored', '$SITE')"
run_assert "the mirror created a source row" \
  "SELECT count(*) = 1 FROM content_sources WHERE entity_id = '$SCENE' AND url = 'https://example.invalid/mirrored'"
run_assert "and it made NO health claim (status is 'unknown', not 'live')" \
  "SELECT status = 'unknown' FROM content_sources WHERE entity_id = '$SCENE' AND url = 'https://example.invalid/mirrored'"

echo
echo "=== health SURVIVES an edit, which is the property this design exists for ==="
# The edit path replaces a scene's URL list by deleting every row and re-inserting
# the submitted set. If a scene_urls delete also deleted the health row, every edit
# would erase the health history of every URL of that scene. This is the assertion
# that would have caught that design.
run ok "mark that source dead, as a health sweep would" \
  "UPDATE content_sources SET status = 'dead', check_failures = 9, last_checked = NOW() WHERE entity_id = '$SCENE' AND url = 'https://example.invalid/mirrored'"
run ok "an edit deletes the scene_urls row (the edit path's delete-all)" \
  "DELETE FROM scene_urls WHERE scene_id = '$SCENE' AND url = 'https://example.invalid/mirrored'"
run_assert "the health row SURVIVED the delete" \
  "SELECT count(*) = 1 FROM content_sources WHERE entity_id = '$SCENE' AND url = 'https://example.invalid/mirrored'"
run_assert "and it still says dead with 9 failures" \
  "SELECT status = 'dead' AND check_failures = 9 FROM content_sources WHERE entity_id = '$SCENE' AND url = 'https://example.invalid/mirrored'"
run_assert "but the REMOVED url no longer appears on the scene through the read view" \
  "SELECT count(*) = 0 FROM scene_sources WHERE scene_id = '$SCENE' AND url = 'https://example.invalid/mirrored'"

run ok "the same url comes back in a later edit" \
  "INSERT INTO scene_urls (scene_id, url, site_id) VALUES ('$SCENE', 'https://example.invalid/mirrored', '$SITE')"
run_assert "the read view lists it again" \
  "SELECT count(*) = 1 FROM scene_sources WHERE scene_id = '$SCENE' AND url = 'https://example.invalid/mirrored'"
run_assert "and its health state was NOT reset by the round trip" \
  "SELECT status = 'dead' AND check_failures = 9 FROM scene_sources WHERE scene_id = '$SCENE' AND url = 'https://example.invalid/mirrored'"
run_assert "still exactly one source row for that url (the upsert did not duplicate it)" \
  "SELECT count(*) = 1 FROM content_sources WHERE entity_id = '$SCENE' AND url = 'https://example.invalid/mirrored'"

echo
echo "=== a URL whose health row is missing still renders (the LEFT JOIN) ==="
run ok "an orphan scene_urls row, as if the mirror had never fired" \
  "INSERT INTO scene_urls (scene_id, url, site_id) VALUES ('$SCENE', 'https://example.invalid/orphan', '$SITE')"
run ok "whose health row is then removed behind its back" \
  "DELETE FROM content_sources WHERE entity_id = '$SCENE' AND url = 'https://example.invalid/orphan'"
run_assert "the read view still lists it, as 'unknown' rather than dropping it" \
  "SELECT count(*) = 1 AND min(status) = 'unknown' FROM scene_sources WHERE scene_id = '$SCENE' AND url = 'https://example.invalid/orphan'"

echo
echo "=== the update branch re-points the site WITHOUT touching health ==="
# This is the assertion that has teeth. The one before it passes even if the update
# branch reset the health state, because it re-inserts the SAME site_id and the
# WHERE clause skips the update entirely. Moving the URL to a DIFFERENT site forces
# the update branch to run, so anything it writes to the row shows up here.
run ok "a second site to move the URL to" \
  "INSERT INTO sites (id, name, valid_types, created_at, updated_at) VALUES ('$SITE2', 'verify-108-site-2', '{}', NOW(), NOW()) ON CONFLICT (id) DO NOTHING"
run ok "moving the URL to that other site" \
  "UPDATE scene_urls SET site_id = '$SITE2' WHERE scene_id = '$SCENE' AND url = 'https://example.invalid/mirrored'"
run_assert "content_sources followed the move (the mirror fires on UPDATE, not just INSERT)" \
  "SELECT site_id = '$SITE2' FROM content_sources WHERE entity_id = '$SCENE' AND url = 'https://example.invalid/mirrored'"
run_assert "and the health state is STILL dead with 9 failures after that write" \
  "SELECT status = 'dead' AND check_failures = 9 FROM content_sources WHERE entity_id = '$SCENE' AND url = 'https://example.invalid/mirrored'"

echo
echo "=== cleanup ==="
run ok "removing the fixture scene" \
  "DELETE FROM scenes WHERE id = '$SCENE'"
run_assert "deleting the SCENE removes its health rows (the one delete that really ends them)" \
  "SELECT count(*) = 0 FROM content_sources WHERE entity_id = '$SCENE'"
run_assert "and its URLs went with it through the existing cascade" \
  "SELECT count(*) = 0 FROM scene_urls WHERE scene_id = '$SCENE'"

echo
echo "=========================================================="
echo "verify-108: $pass passed, $fail failed"
[[ $fail -eq 0 ]] || exit 1
