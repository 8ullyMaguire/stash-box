#!/usr/bin/env bash
# Prove migration 111's tag graph at the database: the DAG guarantee, the uniqueness, the
# cascade, and that the recursive read returns what the search will filter on.
#
# WHY A SCRIPT AND NOT A GO TEST. Every assertion below is about what the DATABASE refuses.
# A service-layer test cannot tell "the trigger rejected this" from "the service never
# asked", and the whole point of putting the cycle guard in the database is that the graph is
# written by edits, merges, seeds and future migrations -- not only by the path that remembers
# to check.
#
#   ./scripts/verify-111.sh [dsn]
#
# Defaults to sbx-scratch, which scripts/provision-scratch-db.sh builds with the full
# migration chain and leaves standing. NOT the integration suite's database: that harness
# drops every table at teardown, so a probe against it finds no tables at rest and reports a
# real migration as missing. Provision first if the database does not exist:
#
#   ./scripts/provision-scratch-db.sh sbx-scratch
#   ./scripts/verify-111.sh
set -uo pipefail

cd "$(dirname "$0")/.."
DSN="${1:-postgres://postgres@127.0.0.1:55434/sbx-scratch?sslmode=disable}"
export PGPASSWORD="${PGPASSWORD:-smoke_pw}"

fail=0
pass=0

# A single quote, needed because bash REMOVES quotes from a ${var//pattern/replacement}
# replacement -- so a literal text value written into one reaches psql as a bare identifier.
# (Measured in verify-109.sh, which explains why.)
SQ="'"

# run <expectation: ok|reject> <description> <sql>
#
# `ok` means "the statement executes without error". It does NOT mean the answer is what you
# want: a query that returns 'f' also exits 0, so use run_assert for anything with a value.
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

# run_reject_matching <description> <substring> <sql>
#
# The reason matters, not just the refusal. A typo'd column also "rejects", and a harness that
# only checks the exit code reports it as the cycle guard firing -- which is the one thing this
# file exists to prove. So the message has to name the reason.
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
    echo "      refused, but not for the stated reason."
    echo "      wanted: $want"
    echo "      got:    ${out%%$'\n'*}"
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
    echo "      the assertion returned '${out%%$'\n'*}' with no rows / not true"
    fail=$((fail + 1))
  else
    echo "ok    $desc"
    pass=$((pass + 1))
  fi
}

# Five fixture tags. Names are prefixed so a leftover from an interrupted run is obvious in a
# stray query, and the ids are fixed so the file is re-runnable.
#
# NOTE ON tag_redirects AND THE FILTERS BELOW: nothing here goes through the tag service, so
# a soft-deleted tag is still insertable as a graph node. That is deliberate -- the graph is
# about tags, not about which of them a reader can see, and a deleted tag's implications are
# removed by the CASCADE (section E) rather than by hiding them.
TA=11110000-0000-0000-0000-00000000000a
TB=11110000-0000-0000-0000-00000000000b
TC=11110000-0000-0000-0000-00000000000c
TD=11110000-0000-0000-0000-00000000000d
TE=11110000-0000-0000-0000-00000000000e
NAME_A=zzz_verify111_a
NAME_B=zzz_verify111_b
NAME_C=zzz_verify111_c
NAME_D=zzz_verify111_d
NAME_E=zzz_verify111_e

echo "=== preconditions: this database is migrated to 111 ==="
run_assert "the table exists" \
  "SELECT to_regclass('public.tag_implications') IS NOT NULL"
run_assert "the cycle trigger exists" \
  "SELECT count(*) = 1 FROM pg_trigger WHERE tgname = 'tag_implications_no_cycle_trg' AND NOT tgisinternal"
run_assert "and its function" \
  "SELECT count(*) = 1 FROM pg_proc WHERE proname = 'tag_implications_no_cycle'"
# The reverse index is what the search's recursive walk uses for its first step; the primary
# key already covers the forward direction. Without it the closure query is a sequential scan
# per level.
run_assert "the implied-tag index exists (the direction every search walks)" \
  "SELECT count(*) = 1 FROM pg_indexes WHERE indexname = 'tag_implications_implied_idx'"

echo
echo "=== fixtures ==="
psql "$DSN" -v ON_ERROR_STOP=1 -q -c "
  INSERT INTO tags (id, name, created_at, updated_at) VALUES
    ('$TA', '$NAME_A', now(), now()),
    ('$TB', '$NAME_B', now(), now()),
    ('$TC', '$NAME_C', now(), now()),
    ('$TD', '$NAME_D', now(), now()),
    ('$TE', '$NAME_E', now(), now())
  ON CONFLICT (id) DO NOTHING;
  DELETE FROM tag_implications
   WHERE tag_id IN ('$TA','$TB','$TC','$TD','$TE')
      OR implied_tag_id IN ('$TA','$TB','$TC','$TD','$TE');
" >/dev/null 2>&1 || { echo "FAIL  could not plant the fixtures"; exit 1; }
echo "ok    five tags, graph cleared"

echo
echo "=== the self-implication, refused ==="
# The TRIGGER answers first for an insert (BEFORE INSERT), so the message is its own. The
# CHECK is a backstop for a path that bypasses triggers -- a manual ALTER, a restore -- and is
# unreachable from here by design.
run_reject_matching "a tag cannot imply itself" "cannot imply itself" \
  "INSERT INTO tag_implications (tag_id, implied_tag_id) VALUES ('$TA', '$TA')"

echo
echo "=== the two-node cycle, refused on INSERT ==="
run ok "A implies B is accepted" \
  "INSERT INTO tag_implications (tag_id, implied_tag_id) VALUES ('$TA', '$TB')"
run_reject_matching "and B implying A is refused" "cycle" \
  "INSERT INTO tag_implications (tag_id, implied_tag_id) VALUES ('$TB', '$TA')"
run_reject_matching "the pair is unique" "duplicate key" \
  "INSERT INTO tag_implications (tag_id, implied_tag_id) VALUES ('$TA', '$TB')"

echo
echo "=== a LONGER cycle, refused ==="
run ok "B implies C" \
  "INSERT INTO tag_implications (tag_id, implied_tag_id) VALUES ('$TB', '$TC')"
run ok "C implies D" \
  "INSERT INTO tag_implications (tag_id, implied_tag_id) VALUES ('$TC', '$TD')"
# Three steps away, which is the case a naive check (does the tail imply the head directly?)
# would wave through.
run_reject_matching "D implying A would close A->B->C->D->A" "cycle" \
  "INSERT INTO tag_implications (tag_id, implied_tag_id) VALUES ('$TD', '$TA')"

echo
echo "=== the UPDATE arm of the trigger, which is a different code path ==="
run ok "E implies A" \
  "INSERT INTO tag_implications (tag_id, implied_tag_id) VALUES ('$TE', '$TA')"
# A -> B is retargeted to A -> E, and E -> A already exists. The trigger has to notice on
# UPDATE too: a merge retargets rows, so an INSERT-only guard would let a merge fold a cycle in.
run_reject_matching "retargeting A->B to A->E would close A->E->A" "cycle" \
  "UPDATE tag_implications SET implied_tag_id = '$TE' WHERE tag_id = '$TA' AND implied_tag_id = '$TB'"
run_assert "and the refused update left the row alone" \
  "SELECT count(*) = 1 FROM tag_implications WHERE tag_id = '$TA' AND implied_tag_id = '$TB'"

echo
echo "=== the transitive read, which is what a search expands to ==="
# The graph at this point is A->B, B->C, C->D and E->A, so above D sit C, B, A and E at
# depths 1 to 4. E is in there deliberately: the chain is one longer than the obvious
# A->B->C->D, which is what catches a walk that stops early or mis-numbered a level.
run_assert "D has four tags above it, at depths 1, 2, 3 and 4" \
  "WITH RECURSIVE reach(tag_id, depth) AS (
     SELECT ti.tag_id, 1 FROM tag_implications ti WHERE ti.implied_tag_id = '$TD'
     UNION
     SELECT ti.tag_id, r.depth + 1 FROM reach r
       JOIN tag_implications ti ON ti.implied_tag_id = r.tag_id
      WHERE r.depth < 64
   )
   SELECT count(*) = 4 AND min(depth) = 1 AND max(depth) = 4 FROM reach"
run_assert "and they are C, B, A and E" \
  "WITH RECURSIVE reach(tag_id, depth) AS (
     SELECT ti.tag_id, 1 FROM tag_implications ti WHERE ti.implied_tag_id = '$TD'
     UNION
     SELECT ti.tag_id, r.depth + 1 FROM reach r
       JOIN tag_implications ti ON ti.implied_tag_id = r.tag_id
      WHERE r.depth < 64
   )
   SELECT bool_and(tag_id IN ('$TA','$TB','$TC','$TE')) FROM reach"
# The search expands PER REQUESTED TAG, and that shape is what makes "all of these" answerable
# by one specific tag. Root A must satisfy A and B (A implies B).
run_assert "the expansion groups by the tag that was asked for" \
  "WITH RECURSIVE reach(root_id, tag_id, depth) AS (
     SELECT u.id, u.id, 0 FROM unnest(ARRAY['$TA'::uuid, '$TD'::uuid]) AS u(id)
     UNION
     SELECT r.root_id, ti.tag_id, r.depth + 1 FROM reach r
       JOIN tag_implications ti ON ti.implied_tag_id = r.tag_id
      WHERE r.depth < 64
   )
   SELECT (SELECT count(*) FROM reach WHERE root_id = '$TA') = 2
      AND (SELECT count(*) FROM reach WHERE root_id = '$TD') = 5"

echo
echo "=== deleting a tag takes its implications with it ==="
run_assert "B is in two rows before the delete" \
  "SELECT count(*) = 2 FROM tag_implications WHERE tag_id = '$TB' OR implied_tag_id = '$TB'"
run ok "the tag row is deleted" "DELETE FROM tags WHERE id = '$TB'"
run_assert "and both are gone: no dangling edge is left behind" \
  "SELECT count(*) = 0 FROM tag_implications WHERE tag_id = '$TB' OR implied_tag_id = '$TB'"
run_assert "while an edge that did not touch it survives" \
  "SELECT count(*) = 1 FROM tag_implications WHERE tag_id = '$TC' AND implied_tag_id = '$TD'"

# Leave the fixtures in place: a re-run clears them at the top, and a failure is easier to
# read when the state that produced it is still there.

echo
echo "=== $pass passed, $fail failed ==="
exit $((fail > 0))
