#!/usr/bin/env bash
# Decisions the carry needs: are the seeded lookup tables identical, and who are the seeded users?
set -uo pipefail
PG="docker exec stashbox-pg"
S=stash_box_main_scratch
q() { $PG psql -U stashbox -d "$1" -tAc "$2"; }

echo "=== image_types: identical id sets? ==="
q stash-box "select md5(string_agg(id::text||name, ',' order by id))" > /tmp/it-live
q "$S"       "select md5(string_agg(id::text||name, ',' order by id))" > /tmp/it-main
echo "live: $(cat /tmp/it-live)   main: $(cat /tmp/it-main)"
diff -q /tmp/it-live /tmp/it-main >/dev/null && echo "IDENTICAL -> the main-side seed is the same vocabulary; skip the carry" || echo "DIFFERENT -> replace"

echo "=== image_type_groups / image_type_conflicts: identical? ==="
for t in image_type_groups image_type_conflicts; do
  a=$(q stash-box "select md5(string_agg(t::text, ',' order by t::text)) from (select * from \"$t\") t")
  b=$(q "$S" "select md5(string_agg(t::text, ',' order by t::text)) from (select * from \"$t\") t")
  echo "$t  live=$(echo "$a" | cut -c1-8)  main=$(echo "$b" | cut -c1-8)  $([ "$a" = "$b" ] && echo SAME || echo DIFFERENT)"
done

echo "=== sites: live (first 5) vs main's 2 ==="
q stash-box "select id||' '||name from sites order by name limit 5"
echo "--- main:"
q "$S" "select id||' '||name from sites order by name"

echo "=== users: live vs main ==="
echo "--- live:"
q stash-box "select id||' '||name||' '||coalesce(email,'-') from users order by name"
echo "--- main:"
q "$S" "select id||' '||name||' '||coalesce(email,'-') from users order by name"

echo "=== user_roles: live vs main ==="
echo "--- live:"; q stash-box "select user_id||' '||role from user_roles order by 1"
echo "--- main:"; q "$S" "select user_id||' '||role from user_roles order by 1"

echo "=== sequence-backed columns among the carry tables (need setval after) ==="
q "$S" "select table_name||'.'||column_name from information_schema.columns where table_schema='public' and column_default like 'nextval%' order by 1"

echo "=== tables main's schema has that the live DB also has, for reference: carried columns per table ==="
wc -l /tmp/carry-plan.txt; head -4 /tmp/carry-plan.txt | cut -c1-140
