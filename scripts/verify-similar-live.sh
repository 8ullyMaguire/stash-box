#!/usr/bin/env bash
# Verify the similar-performers surface against the RUNNING server.
#
# Not the same as scripts/verify-similar.sh, which proves the scoring against a
# hand-built fixture. This proves the deployed binary actually serves the feature:
# that the fields resolve, that the arguments reach the query rather than being
# dropped, and that the exclusions hold on live data.
#
# Why live data is seeded at all: the deployed instance had 8 performers, 9 scenes and
# 6 scene_performers -- not one pair of performers sharing a scene. Every field would
# have returned an empty list, and an empty list is indistinguishable from the two bugs
# this milestone fixed. So scripts/seed-similar-live.sql creates a cast whose correct
# ranking is derivable by hand, and this asserts the server produces it.
#
# Auth is the users.api_key column, sent in the custom ApiKey header (server.go:48).
# It is read from the database rather than passed as an argument so it never lands in a
# shell history or a process listing.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

BASE="http://127.0.0.1:${SBX_PORT:-9999}"
PGURL="postgres://postgres@127.0.0.1:${PGPORT:-55434}/${PGDB:-sbx-live}?sslmode=disable"
PGPASS="${PGPASS:-smoke_pw}"

psql_q() { PGPASSWORD="$PGPASS" psql "$PGURL" -tAc "$1"; }

ANA="f0000000-0000-0000-0000-000000000051"
KEY="$(psql_q "SELECT api_key FROM users WHERE name='root'")"
if [ -z "$KEY" ]; then
  echo "FAIL: no root api_key in $PGURL" >&2
  exit 1
fi

# Variables, not an interpolated id: inline ids need a second layer of escaping inside
# the JSON body, and getting that wrong produced a JSON decode error that looks like a
# server fault rather than a quoting mistake.
ask() {
  local query="$1" vars="$2"
  curl -s -X POST "$BASE/graphql" \
    -H 'Content-Type: application/json' \
    -H "ApiKey: $KEY" \
    --data-binary "$(jq -cn --arg q "$query" --argjson v "$vars" '{query:$q, variables:$v}')"
}

names() { jq -r '[.data.findPerformer.similarPerformers[].performer.name] | join(",")'; }

# Apply the fixture first, so this script is self-contained and re-runnable.
PGPASSWORD="$PGPASS" psql "$PGURL" -q -f scripts/seed-similar-live.sql

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

BASE_QUERY='query($id: ID!, $min: Int, $lim: Int) {
  findPerformer(id: $id) {
    similarPerformerCount(minShared: $min)
    similarPerformers(minShared: $min, limit: $lim) {
      performer { name } scenesShared targetScenes coPerformers score
    }
  }
}'

echo "=== live: default arguments ==="
RESP="$(ask "$BASE_QUERY" "$(jq -cn --arg id "$ANA" '{id:$id}')")"
if echo "$RESP" | jq -e '.errors' >/dev/null 2>&1; then
  echo "  FAIL GraphQL error: $(echo "$RESP" | jq -c '.errors')"
  fails=$((fails + 1))
fi
# BEN shares 3 of ANA's 5 scenes with a circle of 3 (0.96); CLEO shares 2 with 1 (0.48).
check "ranking is BEN then CLEO" "$(echo "$RESP" | names)" "BEN,CLEO"
check "count matches the list"   "$(echo "$RESP" | jq -r '.data.findPerformer.similarPerformerCount')" "2"
check "BEN's evidence"            "$(echo "$RESP" | jq -r '.data.findPerformer.similarPerformers[0] | "\(.scenesShared)/\(.targetScenes) co=\(.coPerformers)"')" "3/5 co=3"
check "score is fractional"      "$(echo "$RESP" | jq -r '.data.findPerformer.similarPerformers[0].score')" "0.96"
check "ANA is not recommended to herself" \
  "$(echo "$RESP" | jq -r '[.data.findPerformer.similarPerformers[].performer.name] | map(select(.=="ANA")) | length')" "0"

echo "=== live: the arguments reach the query ==="
check "minShared=3 drops CLEO" \
  "$(ask "$BASE_QUERY" "$(jq -cn --arg id "$ANA" '{id:$id, min:3}')" | names)" "BEN"
check "minShared=99 drops everyone" \
  "$(ask "$BASE_QUERY" "$(jq -cn --arg id "$ANA" '{id:$id, min:99}')" | names)" ""
check "count at minShared=99 is 0" \
  "$(ask "$BASE_QUERY" "$(jq -cn --arg id "$ANA" '{id:$id, min:99}')" | jq -r '.data.findPerformer.similarPerformerCount')" "0"
check "limit=1 caps the list" \
  "$(ask "$BASE_QUERY" "$(jq -cn --arg id "$ANA" '{id:$id, lim:1}')" | names)" "BEN"
check "an absurd limit is clamped, not refused" \
  "$(ask "$BASE_QUERY" "$(jq -cn --arg id "$ANA" '{id:$id, lim:100000}')" | names)" "BEN,CLEO"

echo "=== live: an unscened performer has nobody similar ==="
check "DAVE has no similar performers" \
  "$(ask "$BASE_QUERY" "$(jq -cn --arg id "f0000000-0000-0000-0000-000000000054" '{id:$id}')" | names)" ""
check "DAVE's count is 0, not an archive-wide census" \
  "$(ask "$BASE_QUERY" "$(jq -cn --arg id "f0000000-0000-0000-0000-000000000054" '{id:$id}')" | jq -r '.data.findPerformer.similarPerformerCount')" "0"

echo
if [ "$fails" -eq 0 ]; then
  echo "LIVE OK: all assertions passed"
else
  echo "LIVE FAILURES: $fails"
  exit 1
fi