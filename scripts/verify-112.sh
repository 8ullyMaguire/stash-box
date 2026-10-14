#!/usr/bin/env bash
# Prove migration 112 at the database: the version is written in the same transaction as the
# change, the snapshot is a readable document, and a merge retargets the graph instead of
# eating it.
#
# WHY A SCRIPT AND NOT A GO TEST. "The version moved but the change did not land" and "the
# merge silently dropped the implications" are both statements about what the DATABASE does
# inside a transaction. A service test can only see the end state it asked for.
#
#   ./scripts/verify-112.sh [dsn]
#
#   ./scripts/provision-scratch-db.sh sbx-scratch
#   ./scripts/verify-112.sh
set -uo pipefail

cd "$(dirname "$0")/.."
DSN="${1:-postgres://postgres@127.0.0.1:55434/sbx-scratch?sslmode=disable}"
export PGPASSWORD="${PGPASSWORD:-smoke_pw}"

fail=0
pass=0
QUERIES=internal/queries/sql/tag_ontology.sql

# See verify-109.sh for why a literal quote needs a variable: bash strips quotes from a
# ${var//pattern/replacement} replacement, so a written 'x' arrives as a bare identifier.
SQ="'"

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

run_reject_matching() {
  local desc="$1" want="$2" sql="$3"
  local out rc
  out="$(psql "$DSN" -v ON_ERROR_STOP=1 -tAc "$sql" 2>&1)"
  rc=$?

  if [[ $rc -eq 0 ]]; then
    echo "FAIL  $desc"
    echo "      expected a refusal, but the statement succeeded"
    fail=$((fail + 1))
  elif [[ "$out" != *"$want"* ]]; then
    echo "FAIL  $desc"
    echo "      refused, but not for the stated reason. wanted: $want"
    echo "      got: ${out%%$'\n'*}"
    fail=$((fail + 1))
  else
    echo "ok    $desc"
    pass=$((pass + 1))
  fi
}

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
    echo "      the assertion returned '${out%%$'\n'*}' / not true"
    fail=$((fail + 1))
  else
    echo "ok    $desc"
    pass=$((pass + 1))
  fi
}

A=11220000-0000-0000-0000-00000000000a
B=11220000-0000-0000-0000-00000000000b
C=11220000-0000-0000-0000-00000000000c
D=11220000-0000-0000-0000-00000000000d
T=11220000-0000-0000-0000-00000000000e
Y=11220000-0000-0000-0000-00000000000f
Z=11220000-0000-0000-0000-000000000010
S=11220000-0000-0000-0000-000000000011

NAMES="'zzz_verify112_a','zzz_verify112_b','zzz_verify112_c','zzz_verify112_d','zzz_verify112_e','zzz_verify112_f','zzz_verify112_g','zzz_verify112_h'"
IDS="'$A','$B','$C','$D','$T','$Y','$Z','$S'"

# clear_graph: empty the fixture graph. Every change bumps a version, which is fine -- the
# assertions below are all relative to the current version, never to a hard-coded number,
# because a script that asserts "version 7" fails the second time it runs.
clear_graph() {
  psql "$DSN" -v ON_ERROR_STOP=1 -q -c "
    DELETE FROM tag_implications WHERE tag_id IN ($IDS) OR implied_tag_id IN ($IDS);
  " >/dev/null
}

version() { psql "$DSN" -tAc "SELECT COALESCE(MAX(version), 0) FROM ontology_versions"; }

echo "=== preconditions: this database is migrated to 112 ==="
run_assert "the version table exists" \
  "SELECT to_regclass('public.ontology_versions') IS NOT NULL"
run_assert "the version trigger exists on the graph" \
  "SELECT count(*) = 1 FROM pg_trigger WHERE tgname = 'ontology_version_trg' AND NOT tgisinternal"
run_assert "version 0 is seeded, with the empty graph as its document" \
  "SELECT count(*) = 1 FROM ontology_versions WHERE version = 0 AND snapshot = '[]'::jsonb"
run_reject_matching "a snapshot that is not a list is refused" "ontology_versions_snapshot_is_array" \
  "INSERT INTO ontology_versions (version, snapshot) VALUES (999999, '{}'::jsonb)"

echo
echo "=== fixtures ==="
psql "$DSN" -v ON_ERROR_STOP=1 -q -c "
  INSERT INTO tags (id, name, created_at, updated_at)
  SELECT u.id, n.name, now(), now()
    FROM unnest(ARRAY[$IDS]::uuid[]) WITH ORDINALITY AS u(id, ord)
    JOIN unnest(ARRAY[$NAMES]::text[]) WITH ORDINALITY AS n(name, ord) USING (ord)
  ON CONFLICT (id) DO NOTHING;
" >/dev/null 2>&1 || { echo "FAIL  could not plant the fixture tags"; exit 1; }
clear_graph
V=$(version)
echo "ok    eight tags planted, graph cleared, ontology at version $V"

echo
echo "=== a change writes a version, with the change ==="
psql "$DSN" -v ON_ERROR_STOP=1 -q -c "INSERT INTO tag_implications (tag_id, implied_tag_id) VALUES ('$A','$B')" >/dev/null
run_assert "the version moved by exactly one" \
  "SELECT MAX(version) = $V + 1 FROM ontology_versions"
run_assert "the note names the change, by tag name" \
  "SELECT note = 'add zzz_verify112_a -> zzz_verify112_b' FROM ontology_versions WHERE version = $V + 1"
# Containment and ordering rather than one exact document: another harness's fixtures may be
# in the graph, and an assertion that pins the whole array would fail on their leftovers while
# saying nothing about this change.
run_assert "and the snapshot is the graph after the change, by name" \
  "SELECT snapshot @> '[{\"tag\": \"zzz_verify112_a\", \"implies\": \"zzz_verify112_b\"}]'::jsonb
     FROM ontology_versions WHERE version = $V + 1"
run_assert "the document is ORDERED, so two versions diff by eye" \
  "SELECT (SELECT jsonb_agg(x ORDER BY x->>'tag', x->>'implies') FROM jsonb_array_elements(snapshot) x) = snapshot
     FROM ontology_versions WHERE version = $V + 1"

echo
echo "=== a change that is rolled back takes its version with it ==="
psql "$DSN" -v ON_ERROR_STOP=1 -q -c "
  BEGIN;
  INSERT INTO tag_implications (tag_id, implied_tag_id) VALUES ('$C','$D');
  ROLLBACK;
" >/dev/null
run_assert "no version was written for it" \
  "SELECT NOT EXISTS (SELECT 1 FROM ontology_versions WHERE version > $V + 1)"
run_assert "and the graph does not have the edge either" \
  "SELECT NOT EXISTS (SELECT 1 FROM tag_implications WHERE tag_id = '$C' AND implied_tag_id = '$D')"

echo
echo "=== the document is complete, not a diff ==="
psql "$DSN" -v ON_ERROR_STOP=1 -q -c "INSERT INTO tag_implications (tag_id, implied_tag_id) VALUES ('$B','$C')" >/dev/null
run_assert "the second version holds both pairs of this fixture" \
  "SELECT snapshot @> '[{\"tag\": \"zzz_verify112_a\", \"implies\": \"zzz_verify112_b\"}, {\"tag\": \"zzz_verify112_b\", \"implies\": \"zzz_verify112_c\"}]'::jsonb
     FROM ontology_versions WHERE version = $V + 2"
# A snapshot that appended the change to the PREVIOUS document would also contain both pairs,
# so the length is what separates "the whole graph" from "a growing log".
run_assert "and it holds exactly one more pair than the version before it" \
  "SELECT jsonb_array_length(snapshot)
        = (SELECT jsonb_array_length(snapshot) FROM ontology_versions WHERE version = $V + 1) + 1
     FROM ontology_versions WHERE version = $V + 2"

echo
echo "=== removal and retarget name themselves ==="
psql "$DSN" -v ON_ERROR_STOP=1 -q -c "DELETE FROM tag_implications WHERE tag_id = '$A' AND implied_tag_id = '$B'" >/dev/null
run_assert "a removal is recorded as one" \
  "SELECT note = 'remove zzz_verify112_a -> zzz_verify112_b' FROM ontology_versions WHERE version = $V + 3"
psql "$DSN" -v ON_ERROR_STOP=1 -q -c "UPDATE tag_implications SET implied_tag_id = '$D' WHERE tag_id = '$B' AND implied_tag_id = '$C'" >/dev/null
run_assert "and a retarget is not recorded as an add or a remove" \
  "SELECT note = 'retarget zzz_verify112_b -> zzz_verify112_d' FROM ontology_versions WHERE version = $V + 4"

echo
echo "=== concurrent writers serialise instead of racing ==="
clear_graph
V=$(version)
# A second writer must wait for the first transaction and then SUCCEED. The bump is
# LOCK TABLE ontology_versions IN EXCLUSIVE MODE followed by MAX(version)+1.
#
# The assertion is the OUTCOME, not the waiting: without the lock the second writer computes
# the same MAX(version)+1 (a concurrent uncommitted row is invisible at READ COMMITTED), so it
# waits on the primary key instead and dies with a duplicate-key error. "It blocked" is true
# either way -- only "it then succeeded" separates the lock from the collision.
psql "$DSN" -q -c "
  BEGIN;
  INSERT INTO tag_implications (tag_id, implied_tag_id) VALUES ('$A','$B');
  SELECT pg_sleep(3);
  COMMIT;
" >/dev/null 2>&1 &
holder=$!
sleep 1

out="$(psql "$DSN" -tAc "INSERT INTO tag_implications (tag_id, implied_tag_id) VALUES ('$C','$D')" 2>&1)"
rc=$?
wait "$holder" 2>/dev/null

if [[ $rc -eq 0 ]]; then
  echo "ok    the second writer waited and then succeeded (the lock serialised them)"
  pass=$((pass + 1))
else
  echo "FAIL  the second writer did not survive the overlap"
  echo "      got: ${out%%$'\n'*}"
  echo "      (a duplicate key on ontology_versions_pkey means the lock is gone)"
  fail=$((fail + 1))
fi
run_assert "and exactly two versions were written for the two committed changes" \
  "SELECT MAX(version) = $V + 2 FROM ontology_versions"

echo
echo "=== the merge retarget, extracted from the query file rather than copied ==="
# Extracted BY NAME from internal/queries/sql/tag_ontology.sql so this harness tests the
# statements sqlc generates Go from, not a transcription that can drift. The whole point of
# this section is that the ORDER and the deletes are load-bearing.
extract() {
  local name="$1"
  local sql
  sql="$(awk -v anchor="-- name: $name " 'index($0, anchor) == 1 {f=1; next} f && /^-- name: / {exit} f {print}' "$QUERIES" | sed '/^--/d')"
  # @source_id / @target_id are sqlc named parameters; substitute them for a merge of S into Y
  # or of B into T, which the caller decides by passing the two ids.
  sql="${sql//@source_id/$SRC}"
  sql="${sql//@target_id/$DST}"
  printf '%s' "$sql"
}

merge_into() { # <source> <target> <statement-name>
  SRC="'$1'" DST="'$2'" extract "$3"
}

for stmt in DeleteSelfTagImplicationsForMerge DeleteDuplicateTagImplicationTails DeleteDuplicateTagImplicationHeads RetargetTagImplicationTails RetargetTagImplicationHeads; do
  if [[ -z "$(merge_into "$A" "$B" "$stmt" | tr -d '[:space:]')" ]]; then
    echo "FAIL  could not extract $stmt from $QUERIES"
    echo "      (the anchor line changed, so this section tested nothing)"
    fail=$((fail + 1))
  fi
done
echo "ok    extracted all five merge statements"

clear_graph
# The graph a merge has to survive. B is merged into T:
#   A -> B    an implication of the merged-away tag, which must move to T
#   B -> C    and B's own implication, which must move too
#   A -> T    already there, so A -> B is a duplicate once it moves
#   B -> T    becomes a self-implication once the two tags are one
#   T -> D    unrelated, and must survive untouched
psql "$DSN" -v ON_ERROR_STOP=1 -q -c "
  INSERT INTO tag_implications (tag_id, implied_tag_id) VALUES
    ('$A','$B'), ('$B','$C'), ('$A','$T'), ('$B','$T'), ('$T','$D');
" >/dev/null

run ok "the self-implication pair is dropped first" \
  "$(merge_into "$B" "$T" DeleteSelfTagImplicationsForMerge)"
run ok "then the row whose pair the survivor already holds" \
  "$(merge_into "$B" "$T" DeleteDuplicateTagImplicationTails)"
run ok "then the head duplicates (none in this fixture, and that is the point)" \
  "$(merge_into "$B" "$T" DeleteDuplicateTagImplicationHeads)"
run ok "then the tails move" \
  "$(merge_into "$B" "$T" RetargetTagImplicationTails)"
run ok "then the heads move" \
  "$(merge_into "$B" "$T" RetargetTagImplicationHeads)"

run_assert "the merged-away tag is gone from the graph entirely" \
  "SELECT NOT EXISTS (SELECT 1 FROM tag_implications WHERE tag_id = '$B' OR implied_tag_id = '$B')"
run_assert "and the survivor holds exactly the three edges the merge implies" \
  "SELECT count(*) = 3
      AND bool_and((tag_id, implied_tag_id) IN
        (('$A','$T'), ('$T','$C'), ('$T','$D')))
     FROM tag_implications WHERE tag_id IN ($IDS) OR implied_tag_id IN ($IDS)"
run_assert "with no self-implication folded in" \
  "SELECT NOT EXISTS (SELECT 1 FROM tag_implications WHERE tag_id = implied_tag_id)"
run_assert "and the document was rewritten with each step, not once" \
  "SELECT MAX(version) > $V + 4 FROM ontology_versions"

echo
echo "=== a merge that would fold a cycle in is REFUSED, not applied ==="
clear_graph
# Z -> S moves to Z -> Y, and Y -> Z already exists. The trigger refuses that update, which
# means the merge fails loudly instead of leaving a corrupt graph behind.
psql "$DSN" -v ON_ERROR_STOP=1 -q -c "
  INSERT INTO tag_implications (tag_id, implied_tag_id) VALUES ('$Z','$S'), ('$Y','$Z');
" >/dev/null
run ok "the deletes run first and find nothing" \
  "$(merge_into "$S" "$Y" DeleteSelfTagImplicationsForMerge)"
run_reject_matching "and the tail retarget is refused by the cycle trigger" "cycle" \
  "$(merge_into "$S" "$Y" RetargetTagImplicationTails)"
run_assert "so the graph still has the edge that was not moved" \
  "SELECT count(*) = 1 FROM tag_implications WHERE tag_id = '$Z' AND implied_tag_id = '$S'"

echo
echo "=== $pass passed, $fail failed ==="
exit $((fail > 0))
