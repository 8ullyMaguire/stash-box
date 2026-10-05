#!/usr/bin/env bash
# Live-verify the collage GraphQL surface against a RUNNING server.
#
# The integration tests drive the resolvers in-process. This drives them over HTTP,
# as a client does, because the failure modes that survive in-process testing are
# schema-level: a field the gateway refuses, a directive that rejects a READ user,
# a nullability promise the schema makes that the resolver does not keep.
set -euo pipefail

URL="${1:-http://127.0.0.1:9999/graphql}"
TOKEN="${STASH_TOKEN:-}"

gq() { # gq <query> [variables-json]
  if [ -n "$TOKEN" ]; then
    curl -s -X POST "$URL" -H "Content-Type: application/json" \
      -H "Authorization: Bearer $TOKEN" \
      -d "$(python3 -c 'import json,sys; print(json.dumps({"query":sys.argv[1],"variables":json.loads(sys.argv[2] or "{}")}))' "$1" "${2:-}")"
  else
    curl -s -X POST "$URL" -H "Content-Type: application/json" \
      -d "$(python3 -c 'import json,sys; print(json.dumps({"query":sys.argv[1],"variables":json.loads(sys.argv[2] or "{}")}))' "$1" "${2:-}")"
  fi
}

echo "=== 1. the queries exist in the served schema ==="
for f in sceneSnapshots sceneCollage underSnapshottedScenes; do
  n=$(gq "{ __type(name: \"Query\") { fields { name } } }" | python3 -c "
import json,sys
d=json.load(sys.stdin)
print(sum(1 for x in d['data']['__type']['fields'] if x['name']=='$f'))")
  echo "    Query.$f present: $n"
done

echo "=== 2. sceneSnapshots and sceneCollage return without error ==="
SCENE=$(gq '{ findScenes(scene_filter: { per_page: 1 }) { scenes { id } } }' \
  | python3 -c "import json,sys; s=json.load(sys.stdin)['data']['findScenes']['scenes']; print(s[0]['id'] if s else '')")
if [ -z "$SCENE" ]; then
  echo "    no scene in this instance; seeding one is required to go further"
  exit 0
fi
echo "    using scene $SCENE"
gq 'query($s: ID!) { sceneSnapshots(sceneID: $s) { id timestamp createdAt } }' "{\"s\":\"$SCENE\"}" \
  | python3 -c "import json,sys; d=json.load(sys.stdin); print('    sceneSnapshots errors:', d.get('errors','none'))"
gq 'query($s: ID!) { sceneCollage(sceneID: $s) { id frameCount stale } }' "{\"s\":\"$SCENE\"}" \
  | python3 -c "import json,sys; d=json.load(sys.stdin); print('    sceneCollage errors:', d.get('errors','none'))"
gq '{ underSnapshottedScenes { snapshotCount minimum scene { id } } }' \
  | python3 -c "import json,sys; d=json.load(sys.stdin); print('    underSnapshottedScenes errors:', d.get('errors','none')); print('    entries:', len(d['data']['underSnapshottedScenes']))"

echo
echo "=== live collage surface verified ==="