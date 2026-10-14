#!/usr/bin/env bash
# Prove the ingestion pipeline end to end: a submitted URL becomes a content record, or a
# recorded reason why not (docs/spec/feature-ingestion.md, plan phase 2).
#
#   ./scripts/verify-110.sh
#
# WHY THIS IS A WRAPPER THAT RUNS GO TESTS, WHEN verify-108.sh AND verify-109.sh ARE PURE
# SQL. Those two prove what the DATABASE does -- a constraint refusing a write, a locking
# statement choosing a row -- and no Go test can see either. This phase's claims are the
# opposite shape: what the PIPELINE does with rows that already exist, and the ordering of
# its stages. Those need a real database AND a real HTTP server AND the real extractor, and
# a shell script cannot host a Go service.
#
# So the assertions live in internal/api/ingest_integration_test.go, and this script's job is
# the thing a test run cannot do for itself: prove the tests RAN. An integration test behind
# a build tag exits 0 when it is skipped, and a `go test` that filtered everything out prints
# "no tests to run" and also exits 0 -- both of which look exactly like a pass in CI. The
# checks below are therefore that every named test reported PASS, and that none reported SKIP.
#
# It uses the INTEGRATION SUITE'S database and not sbx-scratch: dbtest's TestMain drops every
# table and re-runs the migration chain, on the way in and on the way out.
set -uo pipefail

cd "$(dirname "$0")/.."

# The DSN dbtest expects: no scheme, `user:password@host/db?opts`.
export POSTGRES_DB="${POSTGRES_DB:-postgres:smoke_pw@127.0.0.1:55434/stash-box-test?sslmode=disable}"

# Every test this phase added, BY NAME. A missing name here is a test nobody runs, and the
# count below is what makes this list accountable rather than decorative.
EXPECTED=(
  TestSubmitContentUrlAndDrainCreatesAnAggregateScene
  TestSubmitContentUrlTwiceIsADuplicateAndQueuesOneRow
  TestSubmitContentUrlRefusesAURLItCannotIngest
  TestSubmitContentUrlRequiresEdit
  TestANamedTargetBeatsThePipelinesOwnInference
  TestAMatchingFingerprintMergesIntoTheExistingScene
  TestAURLAlreadyClaimedIsAttachedWithoutAFetch
  TestAURLTakenDownIsRefusedRatherThanReAttached
  TestAFetchFailureIsRecordedWithTheRightRetryPolicy
  TestTheRealExtractorIngestsARealPage
  TestTheRealExtractorReportsA404AsGoneAndAForbiddenAsUnsupported
)

fail=0
pass=0

out="$(go test -tags integration \
  -run 'TestSubmitContentUrl|TestANamedTarget|TestAMatchingFingerprint|TestAURL|TestAFetch|TestTheReal' \
  ./internal/api/ -v 2>&1)"
rc=$?

echo "=== the pipeline's integration suite ==="
if [[ $rc -ne 0 ]]; then
  echo "FAIL  the integration run exited $rc"
  echo "$out" | grep -E '^(--- FAIL|    --- FAIL|FAIL|panic)' | head -20 | sed 's/^/      /'
  fail=$((fail + 1))
else
  echo "ok    the integration run exited 0"
  pass=$((pass + 1))
fi

# A SKIP means the harness or the database was not there. It exits 0 and prints PASS counts,
# which is why it is checked for explicitly rather than inferred from the exit code.
if grep -q -- '--- SKIP' <<<"$out"; then
  echo "FAIL  something was SKIPPED -- the database or the harness is missing, so the same"
  echo "      run would report success without testing anything"
  grep -- '--- SKIP' <<<"$out" | sed 's/^/      /'
  fail=$((fail + 1))
else
  echo "ok    nothing was skipped"
  pass=$((pass + 1))
fi

if grep -q 'no tests to run' <<<"$out"; then
  echo "FAIL  'no tests to run' -- the -run filter matched nothing, which also exits 0"
  fail=$((fail + 1))
else
  echo "ok    the filter matched tests"
  pass=$((pass + 1))
fi

echo
echo "=== every named test reported PASS ==="
for name in "${EXPECTED[@]}"; do
  if grep -q -- "--- PASS: $name " <<<"$out"; then
    echo "ok    $name"
    pass=$((pass + 1))
  else
    echo "FAIL  $name did not report PASS"
    fail=$((fail + 1))
  fi
done

# The strongest single check in this file: the real extractor, over a real socket, against a
# real database. If this one passes and every rule test above it passes, the fake is
# defensible; without it, the whole rule suite could be green while the shipped extractor
# parses nothing.
if grep -q -- '--- PASS: TestTheRealExtractorIngestsARealPage ' <<<"$out"; then
  echo "ok    the production extractor read a real page and the record it made is readable"
  pass=$((pass + 1))
fi

echo
echo "=== the unit tests beneath it ==="
# Same binary shape as the integration run minus the database: the pure functions (the
# normaliser, the decision table) and the extractor's parse and status mapping.
for pkg in ./internal/service/ingest/ ./internal/service/ingest/extract/; do
  if unit_out="$(go test "$pkg" 2>&1)" && ! grep -q 'no test files' <<<"$unit_out"; then
    echo "ok    $pkg"
    pass=$((pass + 1))
  else
    echo "FAIL  $pkg"
    echo "$unit_out" | tail -5 | sed 's/^/      /'
    fail=$((fail + 1))
  fi
done

echo
echo "=== $pass passed, $fail failed ==="
[[ $fail -eq 0 ]]
