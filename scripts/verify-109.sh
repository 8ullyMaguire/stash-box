#!/usr/bin/env bash
# Prove migration 109's constraints and the queue's claim behaviour, on a real database,
# before any Go exists.
#
# WHY A SCRIPT AND NOT A GO TEST. Every assertion below is about what the DATABASE
# refuses, or about which row a locking statement returns. A service-layer test cannot
# tell "the constraint rejected this" from "the service never asked", and it cannot
# observe SKIP LOCKED at all -- the same reason verify-108.sh exists.
#
#   ./scripts/verify-109.sh [dsn]
#
# Defaults to sbx-scratch, which scripts/provision-scratch-db.sh builds with the full
# migration chain and leaves standing. NOT the integration suite's database: that harness
# drops every table at teardown, so a probe against it finds no tables at rest and reports
# a real migration as missing. Provision first if the database does not exist:
#
#   ./scripts/provision-scratch-db.sh sbx-scratch
#   ./scripts/verify-109.sh
set -uo pipefail

cd "$(dirname "$0")/.."
DSN="${1:-postgres://postgres@127.0.0.1:55434/sbx-scratch?sslmode=disable}"
export PGPASSWORD="${PGPASSWORD:-smoke_pw}"

fail=0
pass=0
MIG=internal/database/migrations/postgres
QUERIES=internal/queries/sql/ingest.sql

# A single quote, needed because bash REMOVES quotes from a ${var//pattern/replacement}
# replacement: `${v//@a/'x'}` yields `x`, not `'x'` -- so every literal text value below
# reaches psql as a bare identifier and the database answers `column "running" does not
# exist` from a statement that looks correct in the script. Measured, not guessed:
# `${t//@a/'x'}` printed `x` while `${t//@a/${SQ}x${SQ}}` printed `'x'`. Characters that
# arrive via expansion are not subject to quote removal; characters written literally in
# the replacement are.
SQ="'"

# run <expectation: ok|reject> <description> <sql>
#
# `ok` means "the statement executes without error". It does NOT mean the answer is what
# you want: a query that returns 'f' also exits 0, so use run_assert for anything with a
# value.
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

SCENE=91000000-0000-0000-0000-000000000001

echo "=== rolling back to a pre-109 state, then planting a row for the ALTER ==="
psql "$DSN" -v ON_ERROR_STOP=1 -q -f "$MIG/109_scene_origin_and_ingest_queue.down.sql" >/dev/null 2>&1
# Planted BEFORE the migration: this is the row the ADD COLUMN has to reach. Asserting the
# default on a row inserted AFTER the migration proves only that the default exists; it
# says nothing about the rows the ALTER actually rewrote, which is the half that matters
# on a live database.
run ok "a scene exists before 109, so the ADD COLUMN has a row to rewrite" \
  "INSERT INTO scenes (id, title, created_at, updated_at) VALUES ('$SCENE', 'verify-109', NOW(), NOW()) ON CONFLICT (id) DO NOTHING"

echo
echo "=== applying migration 109 ==="
if ! err=$(psql "$DSN" -v ON_ERROR_STOP=1 -q -f "$MIG/109_scene_origin_and_ingest_queue.up.sql" 2>&1); then
  echo "FAIL  migration 109 does not apply"
  echo "      ${err%%$'\n'*}"
  exit 1
fi
echo "ok    migration 109 applies cleanly"
pass=$((pass + 1))

# Re-running is not a nicety: this harness applies the file directly, the Go test harness
# applies the chain as a side effect, and a fresh-install rehearsal applies it from empty.
# A migration that only works on its first application is a migration that fails on the
# database you actually care about.
if ! err=$(psql "$DSN" -v ON_ERROR_STOP=1 -q -f "$MIG/109_scene_origin_and_ingest_queue.up.sql" 2>&1); then
  echo "FAIL  migration 109 does not re-apply (it must be idempotent)"
  echo "      ${err%%$'\n'*}"
  fail=$((fail + 1))
else
  echo "ok    migration 109 re-applies without error"
  pass=$((pass + 1))
fi

echo
echo "=== scenes.origin reached the rows that predate it ==="
run_assert "the pre-109 scene row still reads origin='library' (not NULL, not absent)" \
  "SELECT origin = 'library' FROM scenes WHERE id = '$SCENE'"
run_assert "no scene is NULL or empty in origin" \
  "SELECT count(*) = 0 FROM scenes WHERE origin IS NULL OR origin = ''"
run reject "a plausible misspelling is refused" \
  "UPDATE scenes SET origin = 'aggregated' WHERE id = '$SCENE'"
run reject "an empty origin is refused" \
  "UPDATE scenes SET origin = '' WHERE id = '$SCENE'"
run ok "'aggregate' is accepted" \
  "UPDATE scenes SET origin = 'aggregate' WHERE id = '$SCENE'"
run_assert "and it stuck" \
  "SELECT origin = 'aggregate' FROM scenes WHERE id = '$SCENE'"
run_assert "the aggregate index exists under the name the migration gives it" \
  "SELECT count(*) = 1 FROM pg_class WHERE relname = 'scenes_origin_aggregate_idx'"
# The index is asserted by NAME above and behaviourally nowhere, because a partial index's
# effect on a reader is the planner's business and asserting the plan would be asserting
# the row count this fixture invalidates. The predicate's correctness is asserted through
# the queue's index below, which has a direct behavioural test available.

# Back to library so the aggregate index predicate is not carrying a stray fixture row
# through the rest of this script.
psql "$DSN" -v ON_ERROR_STOP=1 -q -c "UPDATE scenes SET origin = 'library' WHERE id = '$SCENE'" >/dev/null

echo
echo "=== the queue's state machine is a constraint, not a convention ==="
run reject "an unknown status is refused" \
  "INSERT INTO ingest_queue (url, url_normalized, status) VALUES ('https://q.invalid/1', 'https://q.invalid/1', 'queued')"
run ok "'pending' is accepted" \
  "INSERT INTO ingest_queue (url, url_normalized) VALUES ('https://q.invalid/1', 'https://q.invalid/1')"
run reject "an unknown outcome is refused" \
  "INSERT INTO ingest_queue (url, url_normalized, status, outcome) VALUES ('https://q.invalid/2', 'https://q.invalid/2', 'done', 'duplicated')"
run reject "a done row with no outcome is refused" \
  "INSERT INTO ingest_queue (url, url_normalized, status) VALUES ('https://q.invalid/3', 'https://q.invalid/3', 'done')"
run reject "a pending row carrying an outcome is refused" \
  "INSERT INTO ingest_queue (url, url_normalized, status, outcome) VALUES ('https://q.invalid/4', 'https://q.invalid/4', 'pending', 'created')"
run ok "a done row with an outcome is accepted" \
  "INSERT INTO ingest_queue (url, url_normalized, status, outcome, finished_at) VALUES ('https://q.invalid/5', 'https://q.invalid/5', 'done', 'merged', now())"
run reject "a queue row for a scene that does not exist is refused" \
  "INSERT INTO ingest_queue (url, url_normalized, result_scene_id) VALUES ('https://q.invalid/6', 'https://q.invalid/6', gen_random_uuid())"

echo
echo "=== the active-URL uniqueness is a PARTIAL index, and both halves are tested ==="
# This is the section a text probe on indexdef cannot do. The predicate's whole point is
# that two rows coexist for the same URL in one state and not in another, and only
# attempting both writes settles it.
run reject "a second PENDING row for the same normalised URL is refused" \
  "INSERT INTO ingest_queue (url, url_normalized) VALUES ('https://q.invalid/1', 'https://q.invalid/1')"
run reject "a PENDING row for a URL that is already DONE is refused" \
  "INSERT INTO ingest_queue (url, url_normalized) VALUES ('https://q.invalid/5', 'https://q.invalid/5')"
run ok "a FAILED row for a URL that is already failed is accepted" \
  "INSERT INTO ingest_queue (url, url_normalized, status) VALUES ('https://q.invalid/7', 'https://q.invalid/7', 'failed')"
run ok "and a second failed row for it is accepted too" \
  "INSERT INTO ingest_queue (url, url_normalized, status) VALUES ('https://q.invalid/7', 'https://q.invalid/7', 'failed')"
run_assert "which is what proves the index is partial rather than plain UNIQUE" \
  "SELECT count(*) = 2 FROM ingest_queue WHERE url_normalized = 'https://q.invalid/7'"
run ok "and a FAILED url can be queued again (the retry the partial predicate exists for)" \
  "INSERT INTO ingest_queue (url, url_normalized) VALUES ('https://q.invalid/7', 'https://q.invalid/7')"

echo
echo "=== the claim, extracted from the query file rather than copied ==="
# Extracted BY NAME from internal/queries/sql/ingest.sql so this harness tests the
# statement sqlc will generate Go from, not a transcription of it that can drift.
claim_sql="$(awk '/^-- name: ClaimIngestJob :one$/{f=1;next} f&&/^-- name:/{exit} f{print}' "$QUERIES" | sed '/^--/d')"
if [[ -z "${claim_sql//[[:space:]]/}" ]]; then
  echo "FAIL  could not extract ClaimIngestJob from $QUERIES"
  echo "      (the anchor line changed, so this check tested nothing)"
  fail=$((fail + 1))
else
  echo "ok    extracted the claim statement ($(echo "$claim_sql" | grep -c . ) non-empty lines)"
  pass=$((pass + 1))
fi

# A clean slate for the claim section: whatever the sections above left is not part of
# this fixture, and a claim over leftover rows would make the counts below meaningless.
psql "$DSN" -v ON_ERROR_STOP=1 -q -c "DELETE FROM ingest_queue" >/dev/null
run ok "two claimable rows for distinct URLs" \
  "INSERT INTO ingest_queue (url, url_normalized) VALUES ('https://c.invalid/a', 'https://c.invalid/a'), ('https://c.invalid/b', 'https://c.invalid/b')"
run ok "one row that is NOT claimable yet (backed off into the future)" \
  "INSERT INTO ingest_queue (url, url_normalized, next_attempt_at) VALUES ('https://c.invalid/c', 'https://c.invalid/c', now() + interval '1 hour')"

claimed1="$(psql "$DSN" -v ON_ERROR_STOP=1 -tAc "$claim_sql" 2>&1 | head -1)"
run_assert "the first claim returned a row" \
  "SELECT count(*) = 1 FROM ingest_queue WHERE status = 'running'"
run_assert "and its attempt count moved at CLAIM time (not at failure time)" \
  "SELECT bool_and(attempts = 1) FROM ingest_queue WHERE status = 'running'"
run_assert "and it carries a started_at, so a stale claim is identifiable" \
  "SELECT bool_and(started_at IS NOT NULL AND claimed_at IS NOT NULL) FROM ingest_queue WHERE status = 'running'"

claimed2="$(psql "$DSN" -v ON_ERROR_STOP=1 -tAc "$claim_sql" 2>&1 | head -1)"
run_assert "the second claim took the OTHER row, not the one already running" \
  "SELECT count(*) = 2 FROM ingest_queue WHERE status = 'running'"
if [[ "${claimed1:0:36}" == "${claimed2:0:36}" ]]; then
  echo "FAIL  the two claims returned the same id -- SKIP LOCKED did not hold the first row"
  fail=$((fail + 1))
else
  echo "ok    the two claims returned different ids"
  pass=$((pass + 1))
fi

claimed3="$(psql "$DSN" -v ON_ERROR_STOP=1 -tAc "$claim_sql" 2>&1 | head -1)"
# NOT `[[ -z ... ]]`: an UPDATE that matches nothing still prints the command tag `UPDATE 0`
# to stdout, so an emptiness check reads a correct refusal as a returned row. Measured:
# `psql -tAc 'UPDATE t SET x = x WHERE false'` prints `UPDATE 0`. The thing that is absent
# when no row is claimed is an ID, so that is what is tested for.
if [[ ! "$claimed3" =~ ^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}- ]]; then
  echo "ok    a third claim returned no row (a running row is not re-claimable, and the"
  echo "      future-dated row is not yet due)"
  pass=$((pass + 1))
else
  echo "FAIL  the third claim returned a row: '$claimed3'"
  echo "      expected none -- either the running rows are re-claimable or the"
  echo "      next_attempt_at guard is not in the statement"
  fail=$((fail + 1))
fi

run_assert "the future-dated row is still pending and still at attempts = 0" \
  "SELECT status = 'pending' AND attempts = 0 AND next_attempt_at > now() FROM ingest_queue WHERE url_normalized = 'https://c.invalid/c'"

echo
echo "=== the terminal statements, also extracted ==="
finish_sql="$(awk '/^-- name: FinishIngestJob :one$/{f=1;next} f&&/^-- name:/{exit} f{print}' "$QUERIES" | sed '/^--/d')"
# 'created' rather than a plausible-looking 'live'/'ingested': the outcome vocabulary is
# what phases 1-3 established, and a harness that proves the statement runs by writing a
# value the CHECK refuses is a harness that proves the opposite.
finish_sql="${finish_sql//@outcome/${SQ}created${SQ}}"
finish_sql="${finish_sql//@result_scene_id/NULL}"
finish_sql="${finish_sql//@confidence/0.9}"
finish_sql="${finish_sql//@id/(SELECT id FROM ingest_queue WHERE status = ${SQ}running${SQ} LIMIT 1)}"
run ok "FinishIngestJob runs against a running row" "$finish_sql"
run_assert "and it produced a consistent terminal state" \
  "SELECT count(*) = 1 FROM ingest_queue WHERE status = 'done' AND outcome = 'created' AND finished_at IS NOT NULL"

# An outcome outside the constraint's set is the arm that proves FinishIngestJob does not
# bypass it. If this ever stops being rejected, the CHECK is gone and the enum is a
# convention the interface documents.
finish_bad="${finish_sql//${SQ}created${SQ}/${SQ}ingested${SQ}}"
run reject "FinishIngestJob cannot write an outcome the CHECK refuses" "$finish_bad"

fail_sql="$(awk '/^-- name: FailIngestJob :one$/{f=1;next} f&&/^-- name:/{exit} f{print}' "$QUERIES" | sed '/^--/d')"
fail_sql="${fail_sql//@permanent::boolean/true}"
fail_sql="${fail_sql//@last_error/${SQ}the host refused the connection${SQ}}"
fail_sql="${fail_sql//@reason/${SQ}connection_refused${SQ}}"
fail_sql="${fail_sql//@backoff_seconds::int/60}"
fail_sql="${fail_sql//@id/(SELECT id FROM ingest_queue WHERE status = ${SQ}running${SQ} LIMIT 1)}"
run ok "FailIngestJob runs against a running row" "$fail_sql"
run_assert "a PERMANENT failure lands 'failed'" \
  "SELECT count(*) = 1 FROM ingest_queue WHERE status = 'failed' AND reason = 'connection_refused'"

# A FRESH running row for the transient arm. The two claims above consumed the fixture's
# claimable rows, so the subquery inside the extracted statement would match nothing --
# `WHERE id = NULL` updates zero rows and still exits 0, which would make the assertion
# below report a state the statement never produced.
run ok "one more claimable row" \
  "INSERT INTO ingest_queue (url, url_normalized) VALUES ('https://c.invalid/d', 'https://c.invalid/d')"
psql "$DSN" -v ON_ERROR_STOP=1 -tAc "$claim_sql" >/dev/null 2>&1
run_assert "it is running, so the transient arm has a row to reach" \
  "SELECT count(*) = 1 FROM ingest_queue WHERE url_normalized = 'https://c.invalid/d' AND status = 'running'"

transient_sql="${fail_sql//${SQ}the host refused the connection${SQ}/${SQ}read timeout${SQ}}"
transient_sql="${transient_sql//true/false}"
run ok "FailIngestJob runs for a TRANSIENT failure too" "$transient_sql"
run_assert "a TRANSIENT failure returns the row to 'pending' with a future retry" \
  "SELECT count(*) = 1 FROM ingest_queue WHERE status = 'pending' AND last_error = 'read timeout' AND next_attempt_at > now()"

echo
echo "=== the down migration leaves nothing of 109 behind ==="
if ! err=$(psql "$DSN" -v ON_ERROR_STOP=1 -q -f "$MIG/109_scene_origin_and_ingest_queue.down.sql" 2>&1); then
  echo "FAIL  migration 109 does not roll back"
  echo "      ${err%%$'\n'*}"
  fail=$((fail + 1))
else
  echo "ok    migration 109 rolls back cleanly"
  pass=$((pass + 1))
fi
run_assert "the queue table is gone" \
  "SELECT count(*) = 0 FROM pg_class WHERE relname = 'ingest_queue'"
run_assert "the origin column is gone" \
  "SELECT count(*) = 0 FROM information_schema.columns WHERE table_name = 'scenes' AND column_name = 'origin'"
run_assert "the scenes row itself survived the teardown" \
  "SELECT count(*) = 1 FROM scenes WHERE id = '$SCENE'"

# Put the schema back: this database is the integration suite's, and leaving it with 109
# rolled back makes the next `go test` fail on a missing table for a reason that has
# nothing to do with the code under test.
psql "$DSN" -v ON_ERROR_STOP=1 -q -f "$MIG/109_scene_origin_and_ingest_queue.up.sql" >/dev/null 2>&1
psql "$DSN" -v ON_ERROR_STOP=1 -q -c "DELETE FROM ingest_queue" >/dev/null 2>&1

echo
echo "=== $pass passed, $fail failed ==="
[[ $fail -eq 0 ]]
