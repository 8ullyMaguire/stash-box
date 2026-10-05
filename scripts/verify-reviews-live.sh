#!/usr/bin/env bash
# Verify the review GraphQL surface against the RUNNING server (growth item 29).
#
# The integration tests prove the resolver. This proves the DEPLOYED binary serves it,
# which is a different claim: it catches a schema not embedded in the binary, a
# migration not applied, and a role floor the live user does not meet.
#
# Auth is the users.api_key column in the custom ApiKey header (server.go:48).
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

BASE="http://127.0.0.1:${SBX_PORT:-9999}"
PGURL="postgres://postgres@127.0.0.1:${PGPORT:-55434}/${PGDB:-sbx-live}?sslmode=disable"
export PGPASSWORD="${PGPASS:-smoke_pw}"

# -q is load-bearing, not tidy-up: without it an INSERT ... RETURNING also prints the
# command tag ("INSERT 0 1"), so a captured id became "<uuid>\nINSERT 0 1" and every
# downstream query failed on a malformed UUID. -t alone does not suppress the tag.
psql_q() { psql "$PGURL" -q -tAc "$1"; }

# A moderator: reviewSubmit requires MODERATE, and the role hierarchy is flat, so the
# ROOT user's ADMIN role is what makes this checkable at all.
KEY="$(psql_q "SELECT api_key FROM users WHERE name='root'")"
if [ -z "$KEY" ]; then
  echo "FAIL: no root api_key in $PGURL" >&2
  exit 1
fi

ask() {
  local query="$1" vars="$2"
  curl -s -X POST "$BASE/graphql" \
    -H 'Content-Type: application/json' \
    -H "ApiKey: $KEY" \
    --data-binary "$(jq -cn --arg q "$query" --argjson v "$vars" '{query:$q, variables:$v}')"
}

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

# A fresh performer, so the assertions do not depend on whatever else is seeded.
PERFORMER="$(psql_q "INSERT INTO performers (id, name, gender, created_at, updated_at)
  VALUES (gen_random_uuid(), 'Review Live Subject', 'FEMALE', now(), now())
  RETURNING id")"
echo "subject: $PERFORMER"

# Two DIFFERENT authors, because reviews are unique per (author, entity): one author
# submitting twice upserts into one review and the mean would be that single rating.
#
# Only `root` holds ADMIN on this instance and RoleEnum.Implies is flat, so only root can
# review at all -- which is the live demonstration of the rough edge recorded in
# review.graphql. So this grants MODERATE to a dedicated probe user and revokes it
# afterwards. It deliberately does NOT revoke a role from any pre-existing account to
# clean up: breaking a real user's permissions is a far worse failure than leaving a
# probe row behind.
# Grant MODERATE to the existing `liveverify` account rather than creating a probe user:
# `users.password_hash` is NOT NULL, and fabricating a bcrypt hash to satisfy it is the
# wrong kind of clever.
#
# The pre-existing role set is recorded and restored on exit, so running this script
# leaves the account exactly as it found it. It adds MODERATE only if absent, so an
# account that already has it is left untouched.
PROBE="liveverify"
PROBE_ID="$(psql_q "SELECT id FROM users WHERE name='$PROBE'")"
if [ -z "$PROBE_ID" ]; then
  echo "FAIL: no '$PROBE' user to grant MODERATE to" >&2
  exit 1
fi
PRE_ROLES="$(psql_q "SELECT string_agg(role::text, ',' ORDER BY role::text) FROM user_roles WHERE user_id='$PROBE_ID'")"
cleanup() {
  psql "$PGURL" -q -c "DELETE FROM user_roles WHERE user_id='$PROBE_ID' AND role='MODERATE'" >/dev/null 2>&1 || true
  # Restore the exact prior set rather than assuming what it was: if the account already
  # had MODERATE, the DELETE above removed it and this puts it back.
  if [ -n "$PRE_ROLES" ]; then
    psql "$PGURL" -q -c "
      INSERT INTO user_roles (user_id, role)
      SELECT '$PROBE_ID', unnest(ARRAY['$PRE_ROLES']::role[])
      ON CONFLICT DO NOTHING" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT
psql "$PGURL" -q -c "INSERT INTO user_roles (user_id, role) VALUES ('$PROBE_ID', 'MODERATE') ON CONFLICT DO NOTHING"
PROBE_KEY="$(psql_q "SELECT api_key FROM users WHERE name='$PROBE'")"

for pair in "5:$KEY" "3:$PROBE_KEY"; do
  rating="${pair%%:*}"
  akey="${pair##*:}"
  curl -s -X POST "$BASE/graphql" \
    -H 'Content-Type: application/json' -H "ApiKey: $akey" \
    --data-binary "$(jq -cn --arg e "$PERFORMER" --argjson r "$rating" '{
      query: "mutation($i: ReviewSubmitInput!){ reviewSubmit(input:$i){ status rating } }",
      variables: { i: { entityType: "PERFORMER", entityId: $e, rating: $r, body: "Live check." } }
    }')" >/dev/null
done

QUERY='query($id: ID!) {
  findPerformer(id: $id) {
    reviews { id body rating status author { name } }
    reviewSummary { average ratedCount totalCount }
  }
}'

echo "=== live: reviews resolve and the summary agrees ==="
RESP="$(ask "$QUERY" "$(jq -cn --arg id "$PERFORMER" '{id:$id}')")"
if echo "$RESP" | jq -e '.errors' >/dev/null 2>&1; then
  echo "  FAIL GraphQL error: $(echo "$RESP" | jq -c '.errors')"
  fails=$((fails + 1))
fi

check "two reviews listed"        "$(echo "$RESP" | jq -r '.data.findPerformer.reviews | length')" "2"
check "mean of 5 and 3 is 4"      "$(echo "$RESP" | jq -r '.data.findPerformer.reviewSummary.average')" "4"
check "both were rated"           "$(echo "$RESP" | jq -r '.data.findPerformer.reviewSummary.ratedCount')" "2"
check "total counts both"         "$(echo "$RESP" | jq -r '.data.findPerformer.reviewSummary.totalCount')" "2"
check "the author is resolved"    "$(echo "$RESP" | jq -r '[.data.findPerformer.reviews[].author.name | select(. != null and . != "")] | length')" "2"
check "all are PUBLISHED"         "$(echo "$RESP" | jq -r '[.data.findPerformer.reviews[].status] | unique | join(",")')" "PUBLISHED"

echo "=== live: an unreviewed entity has NULL average, not zero ==="
EMPTY="$(psql_q "INSERT INTO performers (id, name, gender, created_at, updated_at)
  VALUES (gen_random_uuid(), 'Review Live Unreviewed', 'MALE', now(), now())
  RETURNING id")"
ERESP="$(ask "$QUERY" "$(jq -cn --arg id "$EMPTY" '{id:$id}')")"
check "no reviews"              "$(echo "$ERESP" | jq -r '.data.findPerformer.reviews | length')" "0"
check "average is null"         "$(echo "$ERESP" | jq -r '.data.findPerformer.reviewSummary.average // "null"')" "null"
check "ratedCount is zero"      "$(echo "$ERESP" | jq -r '.data.findPerformer.reviewSummary.ratedCount')" "0"

echo "=== live: a flagged review is not listed, and stays flagged ==="
# Captured from RESP, i.e. BEFORE the flag -- reading it from FRESP afterwards would pick
# whichever review survived the filter, which is the other one.
REVIEW_ID="$(echo "$RESP" | jq -r '.data.findPerformer.reviews[0].id')"
# The surviving mean is the OTHER rating, so read the flagged row's rating rather than
# assuming which row reviews[0] is. Listing is newest-first, so reviews[0] is the rating-3
# review and the survivor is the 5 -- I had it backwards and the server was right.
FLAGGED_RATING="$(echo "$RESP" | jq -r '.data.findPerformer.reviews[0].rating')"
SURVIVING_RATING="$(echo "$RESP" | jq -r '.data.findPerformer.reviews[1].rating')"
psql "$PGURL" -q -c "UPDATE reviews SET status='flagged' WHERE id='$REVIEW_ID'"
FRESP="$(ask "$QUERY" "$(jq -cn --arg id "$PERFORMER" '{id:$id}')")"
check "one listed after flagging" "$(echo "$FRESP" | jq -r '.data.findPerformer.reviews | length')" "1"
check "the mean excludes it"      "$(echo "$FRESP" | jq -r '.data.findPerformer.reviewSummary.average')" "$SURVIVING_RATING"
check "it is the flagged one that is gone" "$FLAGGED_RATING" "3"
check "ratedCount excludes it"    "$(echo "$FRESP" | jq -r '.data.findPerformer.reviewSummary.ratedCount')" "1"

SINGLE="$(ask 'query($id: ID!){ review(id:$id){ status } }' "$(jq -cn --arg id "$REVIEW_ID" '{id:$id}')")"
check "fetched directly it is FLAGGED" "$(echo "$SINGLE" | jq -r '.data.review.status')" "FLAGGED"

psql "$PGURL" -q -c "DELETE FROM reviews WHERE entity_id='$PERFORMER' OR entity_id='$EMPTY'" >/dev/null
psql "$PGURL" -q -c "DELETE FROM performers WHERE id IN ('$PERFORMER','$EMPTY')" >/dev/null

echo
if [ "$fails" -eq 0 ]; then
  echo "LIVE OK: all assertions passed"
else
  echo "LIVE FAILURES: $fails"
  exit 1
fi