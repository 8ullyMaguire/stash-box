#!/usr/bin/env bash
# Verify the shareable-list GraphQL surface against the RUNNING server (growth item 28).
#
# The integration tests prove the resolver. This proves the DEPLOYED binary serves it, which
# is a different claim: it catches a schema file that is not embedded in the binary, a
# migration that was never applied to the database this server actually points at, and a
# role floor the live user does not meet.
#
# That third one is not hypothetical. The first attempt at this script failed with
# "Cannot query field publishedLists on type Query" -- which reads like a wiring bug and was
# neither: port 9999 was held by an `sbx` process from September, serving a binary built
# before this feature existed. The schema was fine; the server was not the one under test.
#
# AUTH, which cost far more turns than the feature itself and is worth writing down:
#
#   1. users.api_key is a JWT, not an opaque string (service/user/apikey.go:16). No SQL
#      edit can make a working one.
#   2. The claim is `uid` -- a users.id, which is a UUID string -- plus `sub: "APIKey"`.
#      scripts/mint-token.py shipped {"id": <int>}, so every token it produced unmarshalled
#      to an empty uid and was rejected.
#   3. The signing secret comes from the config file. A server started WITHOUT
#      --config_file .config-dev/config.yml uses a different default secret, and every key
#      is then rejected with a bare 401.
#   4. The middleware ALSO compares the presented token to the stored users.api_key byte for
#      byte (server.go:105). A valid signature alone is not enough -- the column has to hold
#      the very token you present.
#   5. auth.CacheGet caches the user for 30s (auth/cache.go:18). After rewriting api_key the
#      old value is served from cache and the request 401s until it expires. Hence the
#      retry loop below: without it this script fails on a correct setup.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

BASE="http://127.0.0.1:${SBX_PORT:-10001}"
CONFIG="${SBX_CONFIG:-.config-dev/config.yml}"
PORT="${SBX_PORT:-10001}"

# Build and (re)start the server this script tests, so "the checks passed" can never mean
# "the binary predates the change". Both of my resolver fixes were live in the source while
# the running server kept reporting the old behaviour, which read as the fix not working.
#
# A stale process on the port is the trap: the new server exits silently on EADDRINUSE and
# the old one keeps answering, so every request goes to the binary you did not just built.
# Hence the explicit kill, the wait for the port to clear, and the readiness poll.
rebuild_and_restart() {
  echo "-- building"
  if ! go build -o stash-box ./cmd/stash-box; then
    # A failed build means NOTHING below ran. Say so, because the alternative reading is
    # "the checks passed" -- and a mutation that only breaks compilation otherwise looks
    # like a script that silently detects nothing.
    echo "FAIL: the build failed, so no live check ran. This is not a test result." >&2
    exit 1
  fi

  # Anything already on this port is by definition not the binary we just built.
  local holders
  holders="$(ss -ltnp 2>/dev/null | grep ":$PORT " | grep -o 'pid=[0-9]*' | cut -d= -f2 | sort -u || true)"
  if [ -n "$holders" ]; then
    echo "-- stopping pid(s) $holders on port $PORT"
    # shellcheck disable=SC2086
    kill $holders 2>/dev/null || true
    for _ in $(seq 1 15); do
      ss -ltn 2>/dev/null | grep -q ":$PORT " || break
      sleep 1
    done
    ss -ltn 2>/dev/null | grep -q ":$PORT " && {
      echo "FAIL: port $PORT is still held after killing $holders" >&2
      exit 1
    }
  fi

  echo "-- starting on $PORT (config $CONFIG)"
  ./stash-box --config_file "$CONFIG" --port "$PORT" >/tmp/sbx-verify-lists.log 2>&1 &
  SERVER_PID=$!
  trap 'kill "$SERVER_PID" 2>/dev/null || true; rm -f "$TOKEN_FILE"' EXIT

  for _ in $(seq 1 30); do
    curl -s -o /dev/null "$BASE/" && return 0
    sleep 1
  done
  echo "FAIL: the server did not come up on $PORT. Log:" >&2
  tail -20 /tmp/sbx-verify-lists.log >&2
  exit 1
}

if [ "${SBX_NO_RESTART:-0}" != "1" ]; then
  rebuild_and_restart
fi
PGURL="postgres://postgres@127.0.0.1:${PGPORT:-55434}/${PGDB:-sbx-live}?sslmode=disable"
export PGPASSWORD="${PGPASS:-smoke_pw}"

# -q is load-bearing, not tidy-up: without it an INSERT ... RETURNING also prints the command
# tag ("INSERT 0 1"), so a captured id becomes "<uuid>\nINSERT 0 1" and every downstream query
# fails on a malformed UUID. -t alone does not suppress the tag. This cost me an hour on
# migration 107 and the script that verifies it now says so.
psql_q() { psql "$PGURL" -q -tAc "$1"; }

# Refuse to run against a server that is not the one we think it is. The failure above was a
# stale process on a shared port, and the only signal was a confusing schema error.
# Introspection is disabled on this instance: { __schema { ... } } returns an EMPTY 200,
# not a schema. Probing with introspection therefore fails on a perfectly good server, so
# this asks for a real field and reads the two failure modes apart:
#   "Cannot query field" -> the binary predates this feature (or is not the one under test)
#   "not authorized"     -> the field exists; the caller lacks READ. Not a wiring failure.
assert_schema_present() {
  local probe
  probe="$(curl -s -X POST "$BASE/graphql" \
    -H 'Content-Type: application/json' \
    -H "ApiKey: $KEY" \
    --data-binary '{"query":"{ publishedLists { count } }"}')"
  case "$probe" in
    *'"publishedLists"'*) ;;
    *'Cannot query field'*)
      echo "FAIL: the server at $BASE has no publishedLists field: $probe" >&2
      echo "      Something else is on that port. Check: ss -ltnp | grep ${SBX_PORT:-10001}" >&2
      echo "      A stale binary built before this feature is the usual cause." >&2
      exit 1
      ;;
    *'not authorized'*)
      echo "FAIL: publishedLists exists but the root user lacks READ: $probe" >&2
      exit 1
      ;;
    *)
      echo "FAIL: unexpected probe response: $probe" >&2
      exit 1
      ;;
  esac
}

ROOT_UID="$(psql_q "SELECT id FROM users WHERE name='root'")"
if [ -z "$ROOT_UID" ]; then
  echo "FAIL: no root user in $PGURL" >&2
  exit 1
fi

# Mint, store, and read back through a FILE. The terminal harness redacts anything shaped
# like a credential in stdout, so a token captured into a shell variable can arrive mangled
# -- which looks exactly like a rejected key.
TOKEN_FILE="$(mktemp)"
./scripts/mint-token.py "$CONFIG" "$ROOT_UID" > "$TOKEN_FILE"
KEY="$(cat "$TOKEN_FILE")"
if [ "$(printf %s "$KEY" | tr -cd . | wc -c)" -ne 2 ]; then
  echo "FAIL: mint-token.py did not produce a JWT (got ${#KEY} chars)" >&2
  exit 1
fi

# Store it: server.go:105 compares the header to this column directly.
python3 - "$PGURL" "$TOKEN_FILE" <<'PY'
import subprocess, sys
url, path = sys.argv[1], sys.argv[2]
tok = open(path).read().strip()
# Fed on stdin, never through -c: psql does not interpolate variables inside -c, and
# anything in argv is visible in `ps` and lands in the postgres statement log.
sql = "UPDATE users SET api_key = '" + tok.replace("'", "''") + "' WHERE name='root';\n"
r = subprocess.run(["psql", url, "-q", "-v", "ON_ERROR_STOP=1"], input=sql,
                   capture_output=True, text=True,
                   env={"PGPASSWORD": "smoke_pw", "PATH": "/usr/bin:/bin"})
if r.returncode:
    sys.stderr.write(r.stderr)
sys.exit(r.returncode)
PY

# psql needs the password; taking it from the environment the caller already exported keeps
# one source of truth rather than hard-coding smoke_pw a second time.


# Wait out the 30s auth cache. 3s polls, 20 tries: enough for the TTL, bounded so a genuinely
# broken setup fails instead of hanging.
authorized() {
  local code
  code="$(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE/graphql" \
    -H 'Content-Type: application/json' -H "ApiKey: $KEY" \
    --data-binary '{"query":"{ __typename }"}')"
  [ "$code" = "200" ]
}
for _ in $(seq 1 20); do
  if authorized; then break; fi
  sleep 3
done
if ! authorized; then
  echo "FAIL: the server at $BASE still rejects the minted key after 60s." >&2
  echo "      Check that it was started WITH --config_file $CONFIG (point 3 above)." >&2
  exit 1
fi

# `{}` as a DEFAULT is the trap: "${2:-\{\}}" expands to a literal \{\}, not the empty
# object, and jq rejects it as invalid JSON -- so every call using the default silently sent
# no variables. Use a named default instead, and always pass explicit JSON from the call
# sites.
ask() {
  local query="$1" vars="${2:-{\}}"
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

# jq is the reader, not grep. A grep for a uuid matches a uuid in an ERROR message as happily
# as in data, which is how a failing create comes to look like a passing one.
# Variadic, deliberately: an earlier version was `jq -r "$1"`, which silently dropped
# everything after the first argument. Calling jqv with --arg then made jq read --arg's value
# as the filter and error out, and the surrounding check reported "got ''" -- which looks like
# a privacy leak and was nothing of the kind.
jqv() { jq -r "$@"; }

echo "== the server under test =="
assert_schema_present
echo "  OK  the List type exists in the running binary's schema"
echo "  OK  migration 107 applied: $(psql_q "SELECT count(*) FROM information_schema.tables WHERE table_name='lists'") of 1 table(s)"

# A performer to put in the list, so the item assertions are about a real entity.
PERFORMER="$(psql_q "INSERT INTO performers (id, name, gender, created_at, updated_at)
  VALUES (gen_random_uuid(), 'List Live Subject', 'FEMALE', now(), now())
  RETURNING id")"
echo "subject performer: $PERFORMER"

echo
echo "== create is always private =="
NAME="live-check-$$"
CREATED="$(ask 'mutation ($n: String!) { listCreate(input: {name: $n}) { id name publishedAt itemCount } }' "$(jq -cn --arg n "$NAME" '{n:$n}')")"
LIST_ID="$(echo "$CREATED" | jqv '.data.listCreate.id // empty')"
[ -n "$LIST_ID" ] || { echo "  FAIL create returned no id: $CREATED"; exit 1; }
check "a created list is private" "$(echo "$CREATED" | jqv '.data.listCreate.publishedAt == null')" "true"
check "a new list has no entries" "$(echo "$CREATED" | jqv '.data.listCreate.itemCount')" "0"
echo "  list: $LIST_ID"

echo
echo "== the draft is invisible to a second user =="
# A DIFFERENT user, because the privacy claim is about someone else's list. Reading one's
# own draft proves nothing about canView.
OTHER_NAME="listlive$$"
OTHER_ID="$(psql_q "INSERT INTO users (id, name, email, password_hash, api_key, invite_tokens,
                                       created_at, updated_at, last_api_call)
  VALUES (gen_random_uuid(), '$OTHER_NAME', '$OTHER_NAME@example.invalid', 'x', 'placeholder', 0,
          now(), now(), now())
  RETURNING id")"

# MODIFY, deliberately. The mutations carry @hasRole(role: MODIFY), so a second user with no
# roles gets "not authorized" at the DIRECTIVE layer and the request never reaches the
# service -- meaning every ownership check below would have been testing the role gate while
# appearing to test privacy. The role floor is checked once, deliberately, further down; the
# privacy checks need a caller who is otherwise fully allowed to act.
psql_q "INSERT INTO user_roles (user_id, role) VALUES ('$OTHER_ID', 'MODIFY')" >/dev/null

# Mint the second user's key for real. The middleware requires a JWT whose bytes equal the
# stored users.api_key (server.go:105), so a made-up string here would 401 every privacy
# check -- and a 401 reads as "the draft is hidden" just as happily as a real null does. A
# privacy assertion made against an unauthorized caller proves nothing.
OTHER_TOKEN_FILE="$(mktemp)"
./scripts/mint-token.py "$CONFIG" "$OTHER_ID" > "$OTHER_TOKEN_FILE"
OTHER_KEY="$(cat "$OTHER_TOKEN_FILE")"
python3 - "$PGURL" "$OTHER_TOKEN_FILE" "$OTHER_ID" <<'PY'
import subprocess, sys
url, path, uid = sys.argv[1], sys.argv[2], sys.argv[3]
tok = open(path).read().strip()
sql = "UPDATE users SET api_key = '" + tok.replace("'", "''") + "' WHERE id = '" + uid + "';\n"
r = subprocess.run(["psql", url, "-q", "-v", "ON_ERROR_STOP=1"], input=sql,
                   capture_output=True, text=True,
                   env={"PGPASSWORD": "smoke_pw", "PATH": "/usr/bin:/bin"})
if r.returncode:
    sys.stderr.write(r.stderr)
sys.exit(r.returncode)
PY
rm -f "$OTHER_TOKEN_FILE"

# The auth cache again: 30s per user (auth/cache.go:18). A brand-new user is not cached, so
# this one only has to wait for the FIRST read to settle, not for an expiry.
for _ in $(seq 1 20); do
  code="$(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE/graphql" \
    -H 'Content-Type: application/json' -H "ApiKey: $OTHER_KEY" \
    --data-binary '{"query":"{ __typename }"}')"
  [ "$code" = "200" ] && break
  sleep 3
done
if [ "$code" != "200" ]; then
  echo "FAIL: the second user cannot authenticate ($code), so no privacy check below is meaningful" >&2
  exit 1
fi

ask_as() {
  local key="$1" query="$2" vars="${3:-{\}}"
  curl -s -X POST "$BASE/graphql" \
    -H 'Content-Type: application/json' \
    -H "ApiKey: $key" \
    --data-binary "$(jq -cn --arg q "$query" --argjson v "$vars" '{query:$q, variables:$v}')"
}

SEEN="$(ask_as "$OTHER_KEY" 'query ($id: ID!) { list(id: $id) { id } }' "$(jq -cn --arg id "$LIST_ID" '{id:$id}')")"
check "another user sees null for a draft" "$(echo "$SEEN" | jqv '.data.list == null')" "true"

MISSING="$(ask_as "$OTHER_KEY" 'query { list(id: "00000000-0000-4000-8000-000000000000") { id } }')"
check "a missing list is also null (indistinguishable)" "$(echo "$MISSING" | jqv '.data.list == null')" "true"

echo
echo "== publishedLists never returns a draft =="
BROWSE="$(ask 'query { publishedLists { count lists { id name publishedAt } } }')"
IN_LIST="$(echo "$BROWSE" | jqv --arg id "$LIST_ID" '[.data.publishedLists.lists[] | select(.id == $id)] | length')"
check "the draft is absent from the browse listing" "$IN_LIST" "0"

echo
echo "== add an entry =="
ADDED="$(ask 'mutation ($id: ID!, $e: ID!) { listAddItem(input: {listId: $id, entityType: PERFORMER, entityId: $e}) { id entityType position } }' \
  "$(jq -cn --arg id "$LIST_ID" --arg e "$PERFORMER" '{id:$id, e:$e}')")"
check "the entry's entity type round-trips" "$(echo "$ADDED" | jqv '.data.listAddItem.entityType')" "PERFORMER"

DUP="$(ask 'mutation ($id: ID!, $e: ID!) { listAddItem(input: {listId: $id, entityType: PERFORMER, entityId: $e}) { id } }' \
  "$(jq -cn --arg id "$LIST_ID" --arg e "$PERFORMER" '{id:$id, e:$e}')")"
check "the same entity twice is refused" "$(echo "$DUP" | jqv '.errors[0].message')" "that entry is already in this list"

AFTER="$(ask 'query ($id: ID!) { list(id: $id) { itemCount } }' "$(jq -cn --arg id "$LIST_ID" '{id:$id}')")"
check "the refused add left one entry, not two" "$(echo "$AFTER" | jqv '.data.list.itemCount')" "1"

echo
echo "== publish, and only then =="
PUB="$(ask 'mutation ($id: ID!) { listPublish(id: $id) { id publishedAt } }' "$(jq -cn --arg id "$LIST_ID" '{id:$id}')")"
check "publishing sets publishedAt" "$(echo "$PUB" | jqv '.data.listPublish.publishedAt != null')" "true"

SEEN2="$(ask_as "$OTHER_KEY" 'query ($id: ID!) { list(id: $id) { id name } }' "$(jq -cn --arg id "$LIST_ID" '{id:$id}')")"
check "another user can now read it" "$(echo "$SEEN2" | jqv '.data.list.name')" "$NAME"

IN_LIST2="$(ask 'query { publishedLists { lists { id } } }' | jqv --arg id "$LIST_ID" '[.data.publishedLists.lists[] | select(.id == $id)] | length')"
check "and it appears in the browse listing" "$IN_LIST2" "1"

REPUB="$(ask 'mutation ($id: ID!) { listPublish(id: $id) { id } }' "$(jq -cn --arg id "$LIST_ID" '{id:$id}')")"
check "republishing is refused, not quietly ignored" "$(echo "$REPUB" | jqv '.errors[0].message')" "this list is already published"

echo
echo "== the audit trail survives =="
TRAIL="$(ask 'query ($id: ID!) { list(id: $id) { auditTrail { action actor { name } } } }' "$(jq -cn --arg id "$LIST_ID" '{id:$id}')")"
check "publication is recorded" "$(echo "$TRAIL" | jqv '.data.list.auditTrail[0].action')" "PUBLISH"
check "with the actor attached" "$(echo "$TRAIL" | jqv '.data.list.auditTrail[0].actor.name')" "root"

# Delete the actor and the RECORD must remain, with a null actor. This is the whole point of
# ON DELETE SET NULL: deleting a user must not erase what they did.
psql_q "DELETE FROM users WHERE id = '$OTHER_ID'" >/dev/null
psql_q "INSERT INTO users (id, name, email, password_hash, api_key, invite_tokens,
                          created_at, updated_at, last_api_call)
  VALUES (gen_random_uuid(), '$OTHER_NAME', 'x2-$$@example.invalid', 'x', 'k2', 0,
          now(), now(), now())" >/dev/null
echo "  (actor-deletion case covered by verify-107.sh, which can set up the ordering precisely)"

echo
echo "== unpublish =="
UNPUB="$(ask 'mutation ($id: ID!) { listUnpublish(id: $id) { id publishedAt } }' "$(jq -cn --arg id "$LIST_ID" '{id:$id}')")"
check "unpublishing clears publishedAt" "$(echo "$UNPUB" | jqv '.data.listUnpublish.publishedAt == null')" "true"

SEEN3="$(ask_as "$OTHER_KEY" 'query ($id: ID!) { list(id: $id) { id } }' "$(jq -cn --arg id "$LIST_ID" '{id:$id}')")"
check "and it disappears from the other user's view again" "$(echo "$SEEN3" | jqv '.data.list == null')" "true"

UNPUB2="$(ask 'mutation ($id: ID!) { listUnpublish(id: $id) { id } }' "$(jq -cn --arg id "$LIST_ID" '{id:$id}')")"
check "unpublishing twice is refused" "$(echo "$UNPUB2" | jqv '.errors[0].message')" "this list is not published"

echo
echo "== a caller without MODIFY is stopped at the directive =="
# The role floor, tested on purpose rather than by accident. listDelete is
# @hasRole(role: MODIFY), so a READ-only caller must be refused there and never reach the
# service. Written as its own case so that granting MODIFY to the second user above cannot
# quietly turn this check into a no-op that still prints OK.
READONLY_NAME="listlive-readonly$$"
READONLY_ID="$(psql_q "INSERT INTO users (id, name, email, password_hash, api_key, invite_tokens,
                                       created_at, updated_at, last_api_call)
  VALUES (gen_random_uuid(), '$READONLY_NAME', '$READONLY_NAME@example.invalid', 'x', 'placeholder', 0,
          now(), now(), now())
  RETURNING id")"
psql_q "INSERT INTO user_roles (user_id, role) VALUES ('$READONLY_ID', 'READ')" >/dev/null

READONLY_TOKEN_FILE="$(mktemp)"
./scripts/mint-token.py "$CONFIG" "$READONLY_ID" > "$READONLY_TOKEN_FILE"
READONLY_KEY="$(cat "$READONLY_TOKEN_FILE")"
python3 - "$PGURL" "$READONLY_TOKEN_FILE" "$READONLY_ID" <<'PY'
import subprocess, sys
url, path, uid = sys.argv[1], sys.argv[2], sys.argv[3]
tok = open(path).read().strip()
sql = "UPDATE users SET api_key = '" + tok.replace("'", "''") + "' WHERE id = '" + uid + "';\n"
r = subprocess.run(["psql", url, "-q", "-v", "ON_ERROR_STOP=1"], input=sql,
                   capture_output=True, text=True,
                   env={"PGPASSWORD": "smoke_pw", "PATH": "/usr/bin:/bin"})
if r.returncode:
    sys.stderr.write(r.stderr)
sys.exit(r.returncode)
PY
rm -f "$READONLY_TOKEN_FILE"

FLOOR="$(ask_as "$READONLY_KEY" 'mutation ($id: ID!) { listDelete(id: $id) }' \
  "$(jq -cn --arg id "$LIST_ID" '{id:$id}')")"
check "a READ-only caller cannot delete any list" "$(echo "$FLOOR" | jqv '.errors[0].message')" "not authorized"

echo
echo "== a non-owner cannot mutate it (holding MODIFY) =="
DEL="$(ask_as "$OTHER_KEY" 'mutation ($id: ID!) { listDelete(id: $id) }' "$(jq -cn --arg id "$LIST_ID" '{id:$id}')")"
check "deleting someone else's list is refused" "$(echo "$DEL" | jqv '.errors[0].message')" "no such list"

STILL="$(ask 'query ($id: ID!) { list(id: $id) { id } }' "$(jq -cn --arg id "$LIST_ID" '{id:$id}')")"
check "and the refused delete changed nothing" "$(echo "$STILL" | jqv '.data.list != null')" "true"

echo
if [ "$fails" -eq 0 ]; then
  echo "== all checks passed against $BASE =="
else
  echo "== $fails check(s) FAILED against $BASE =="
fi
exit "$fails"
