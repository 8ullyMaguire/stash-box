#!/usr/bin/env bash
# Build the carry plan: for each table that holds rows in the live DB and exists in main's schema,
# the column intersection by NAME, what the types are, and what main's migrations have pre-seeded.
set -uo pipefail
PG="docker exec stashbox-pg"
S=stash_box_main_scratch
PLAN=/tmp/carry-plan.txt
: > "$PLAN"

# carry tables: rows in live, present in main's schema
: > /tmp/carry.txt
while read -r t; do
  [ -z "$t" ] && continue
  grep -qx "$t" /tmp/tables-main.txt || continue
  n=$($PG psql -U stashbox -d stash-box -tAc "select count(*) from \"$t\"" 2>/dev/null || echo 0)
  [ "$n" != "0" ] && echo "$t" >> /tmp/carry.txt
done < /tmp/tables-live.txt

echo "=== carry tables: $(wc -l < /tmp/carry.txt) ==="
printf "%-32s %10s %10s %s\n" TABLE LIVE SEEDED_BY_MAIN TYPES
while read -r t; do
  [ -z "$t" ] && continue
  live=$($PG psql -U stashbox -d stash-box -tAc "select count(*) from \"$t\"")
  seeded=$($PG psql -U stashbox -d "$S" -tAc "select count(*) from \"$t\"")

  # column intersection by name, with both types
  $PG psql -U stashbox -d stash-box -tAc \
    "select column_name||' '||data_type from information_schema.columns where table_schema='public' and table_name='$t' order by column_name" > /tmp/lc.txt
  $PG psql -U stashbox -d "$S" -tAc \
    "select column_name||' '||data_type from information_schema.columns where table_schema='public' and table_name='$t' order by column_name" > /tmp/mc.txt
  cols=$(comm -12 /tmp/lc.txt /tmp/mc.txt | cut -d' ' -f1 | paste -sd,)
  mismatch=$(comm -12 /tmp/lc.txt /tmp/mc.txt >/dev/null; join -j1 <(sort /tmp/lc.txt) <(sort /tmp/mc.txt) 2>/dev/null | awk '{ if ($2 != $3) print $1": "$2 " -> " $3 }' | paste -sd'; ')
  count=$(echo "$cols" | tr ',' '\n' | grep -c .)
  printf "%-32s %10s %10s %s\n" "$t" "$live" "$seeded" "${mismatch:-none}"
  echo "$t|$cols" >> "$PLAN"
done < /tmp/carry.txt

echo
echo "=== plan written to $PLAN ==="
echo "=== tables whose main-side starts NON-empty (migrations seed them; the carry must replace): ==="
while read -r t; do
  [ -z "$t" ] && continue
  seeded=$($PG psql -U stashbox -d "$S" -tAc "select count(*) from \"$t\"")
  [ "$seeded" != "0" ] && echo "  $t: $seeded"
done < /tmp/carry.txt
