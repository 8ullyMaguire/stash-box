#!/usr/bin/env bash
# Would a data carry into main's schema be mechanical, or a project?
# The blockers are: (1) main-side NOT NULL columns with no default that a carried row cannot fill,
# (2) NOT NULL columns in the live schema that main does not have (dropped values), (3) FK ordering.
set -uo pipefail
PG="docker exec stashbox-pg"
SCRATCH=stash_box_main_scratch
L=dash
M=main

# shared tables that actually hold rows
while read -r t; do
  [ -z "$t" ] && continue
  n=$($PG psql -U stashbox -d stash-box -tAc "select count(*) from \"$t\"" 2>/dev/null || echo 0)
  [ "$n" != "0" ] && echo "$t" >> /tmp/tables-with-rows.txt
done < /tmp/tables-live.txt

echo "=== tables with rows that exist in BOTH schemas ==="
while read -r t; do
  grep -qx "$t" /tmp/tables-main.txt && echo "$t"
done < /tmp/tables-with-rows.txt > /tmp/carry.txt
wc -l < /tmp/carry.txt

echo
echo "=== (1) BLOCKER: main-side NOT NULL, no default, not in the live table ==="
: > /tmp/blocker1.txt
while read -r t; do
  [ -z "$t" ] && continue
  $PG psql -U stashbox -d "$SCRATCH" -tAc \
    "select column_name||' '||data_type from information_schema.columns c
      where table_schema='public' and table_name='$t' and is_nullable='NO' and column_default is null
        and column_name not in (select column_name from information_schema.columns x
                                 where x.table_schema='public' and x.table_name='$t')
      " > /dev/null 2>&1
  # main-side required columns minus live-side columns of the same name
  $PG psql -U stashbox -d "$SCRATCH" -tAc \
    "select table_name||'.'||column_name||' ('||data_type||')' from information_schema.columns
      where table_schema='public' and table_name='$t' and is_nullable='NO' and column_default is null
      order by column_name" > /tmp/m_req.txt
  $PG psql -U stashbox -d stash-box -tAc \
    "select column_name from information_schema.columns where table_schema='public' and table_name='$t' order by column_name" > /tmp/l_cols.txt
  while read -r c; do
    [ -z "$c" ] && continue
    name=${c%%.*}; col=${c#*.}; col=${col%% *}
    grep -qx "$col" /tmp/l_cols.txt || echo "$c" >> /tmp/blocker1.txt
  done < /tmp/m_req.txt
done < /tmp/carry.txt
sort -u /tmp/blocker1.txt | tee /tmp/blocker1.sorted | head -30
echo "   count: $(wc -l < /tmp/blocker1.sorted)"

echo
echo "=== (2) values that would be DROPPED: live columns main does not have, NOT NULL, in carry tables ==="
sort -u /tmp/cols-only-live.txt | awk -F'|' '{print $1"."$2}' | sort -u > /tmp/live_only_cols.txt
: > /tmp/blocker2.txt
while read -r t; do
  [ -z "$t" ] && continue
  grep "^$t\." /tmp/live_only_cols.txt | sed "s/^$t\./  $t./" >> /tmp/blocker2.txt
done < /tmp/carry.txt
head -25 /tmp/blocker2.txt
echo "   count: $(wc -l < /tmp/blocker2.txt)"

echo
echo "=== (3) type conflicts on same-named columns in carry tables ==="
join -t'|' -1 1 -2 1 \
  <(sed 's/|/TH|/' /dev/null; awk -F'|' '{print $1"|"$2"|"$3}' /tmp/cols-live.txt | sort) \
  <(awk -F'|' '{print $1"|"$2"|"$3}' /tmp/cols-main.txt | sort) 2>/dev/null \
  | awk -F'|' '{ if ($3 != $4) print $1"."$2"  live=" $3 "  main=" $4 }' | head -20
echo "   (compared by name+type; a mismatch here needs a cast)"

echo
echo "=== (4) FK count on the carry tables (load order matters) ==="
$PG psql -U stashbox -d "$SCRATCH" -tAc \
  "select count(*) from information_schema.table_constraints where constraint_type='FOREIGN KEY' and table_schema='public'"
echo "=== is stashbox superuser (needed to suspend FK triggers)? ==="
$PG psql -U stashbox -d "$SCRATCH" -tAc "select rolsuper from pg_roles where rolname = current_user"
