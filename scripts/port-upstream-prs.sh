#!/usr/bin/env bash
# Port upstream stash-box PRs into this fork, smallest blast radius first.
#
# WHY THIS ORDER: the forks that upstream marks CONFLICTING (#928, #1086, #1076,
# #1183) conflict against upstream master, not against us -- but we have 89
# migrations and schema changes they do not know about, so anything touching
# graphql/ or queries/sql/ is far more likely to break here. Small PRs first
# means an early failure is cheap and the log stays readable.
#
# One commit per PR, so a bad port can be reverted on its own.
set -uo pipefail
cd /home/alvaro/code-local/go/stash-box

export POSTGRES_DB='postgres:smoke_pw@127.0.0.1:5434/stash-box-test?sslmode=disable'
export GIT_MERGE_AUTOEDIT=no

# Ascending by diff size: trivial perf/CSS fixes first, the 29k-line PR last.
PRS=(1242 1224 1086 928 1203 1253 1257 1254 1252 1260 1263 1255 1262
     1265 1271 1274 1223 1227 1219 1241 1268 1272 1123 1275 1273 1249
     1270 1267 1225 1212 1266 1269 1248 1215 1216 1183 1076 1155 878 1278)

results=/tmp/port_results.txt
: > "$results"

for n in "${PRS[@]}"; do
  ref="refs/pr/$n"
  title=$(gh pr view "$n" -R stashapp/stash-box --json title --jq .title 2>/dev/null)
  author=$(gh pr view "$n" -R stashapp/stash-box --json author --jq .author.login 2>/dev/null)

  if ! git merge --no-ff -m "Merge upstream #$n: $title

Ported from stashapp/stash-box#$n by $author." "$ref" >/tmp/merge_$n.log 2>&1; then
    echo "CONFLICT $n | $title" | tee -a "$results"
    git merge --abort 2>/dev/null
    continue
  fi

  # A PR touching the schema needs codegen before it can possibly build.
  if git diff --name-only HEAD^ HEAD | grep -qE '\.graphql$|internal/queries/sql/'; then
    echo "  #$n needs codegen"
    ( cd internal/models && go generate ./... ) >/tmp/gen_$n.log 2>&1
    sqlc generate >>/tmp/gen_$n.log 2>&1
    if ! git diff --quiet; then
      git add -A
      git commit -q --amend --no-edit
    fi
  fi

  if ! go build ./... >/tmp/build_$n.log 2>&1; then
    echo "BUILDFAIL $n | $title" | tee -a "$results"
    git reset --hard HEAD^ >/dev/null
    continue
  fi

  if ! go test -count=1 ./... >/tmp/test_$n.log 2>&1; then
    echo "TESTFAIL $n | $title" | tee -a "$results"
    git reset --hard HEAD^ >/dev/null
    continue
  fi

  echo "OK $n | $title" | tee -a "$results"
done

echo "=== SUMMARY ==="
sort "$results" | awk '{print $1}' | uniq -c
