#!/usr/bin/env bash
# The gap, measured with the CONTAINER'S client (the host's pg_dump is 16 against a server 18 and
# refuses, which is why the first attempt produced empty diffs).
set -uo pipefail

D=/home/alvaro/stash-box-deploy
STAMP=$(date +%Y%m%d-%H%M%S)
SCRATCH=stash_box_main_scratch
PG="docker exec stashbox-pg"

mkdir -p "$D/backups"

echo "=== 1. snapshot the live database (container's pg_dump) ==="
$PG pg_dump -U stashbox -Fc -d stash-box > "$D/backups/stash-box-pre-rebuild-$STAMP.dump"
ls -lh "$D/backups/stash-box-pre-rebuild-$STAMP.dump"

echo "=== 2. schema-only dumps ==="
$PG pg_dump -U stashbox --schema-only --no-owner --no-acl -d stash-box > /tmp/schema-live.sql
$PG pg_dump -U stashbox --schema-only --no-owner --no-acl -d "$SCRATCH" > /tmp/schema-main.sql
wc -l /tmp/schema-live.sql /tmp/schema-main.sql

echo "=== 3. table sets ==="
$PG psql -U stashbox -d stash-box -tAc "select tablename from pg_tables where schemaname='public' order by 1" > /tmp/tables-live.txt
$PG psql -U stashbox -d "$SCRATCH" -tAc "select tablename from pg_tables where schemaname='public' order by 1" > /tmp/tables-main.txt
echo "live tables: $(wc -l < /tmp/tables-live.txt)   main tables: $(wc -l < /tmp/tables-main.txt)"
echo "--- present in BOTH (these are the tables a data carry would move):"
comm -12 /tmp/tables-live.txt /tmp/tables-main.txt > /tmp/tables-both.txt
wc -l < /tmp/tables-both.txt
echo "--- ONLY in the live database (live's own lineage; no home in main's schema):"
comm -23 /tmp/tables-live.txt /tmp/tables-main.txt | tr '\n' ' '; echo
echo "--- ONLY in main (main's new tables; empty until data is created):"
comm -13 /tmp/tables-live.txt /tmp/tables-main.txt | tr '\n' ' '; echo

echo "=== 4. column-level difference, for the tables in BOTH ==="
$PG psql -U stashbox -d stash-box -tAc \
  "select table_name||'|'||column_name||'|'||data_type||'|'||is_nullable from information_schema.columns where table_schema='public' order by 1,2" \
  | sort > /tmp/cols-live.txt
$PG psql -U stashbox -d "$SCRATCH" -tAc \
  "select table_name||'|'||column_name||'|'||data_type||'|'||is_nullable from information_schema.columns where table_schema='public' order by 1,2" \
  | sort > /tmp/cols-main.txt
echo "--- columns only in MAIN (a carried row would need these to be nullable or defaulted):"
comm -13 /tmp/cols-live.txt /tmp/cols-main.txt | head -40
echo "   total: $(comm -13 /tmp/cols-live.txt /tmp/cols-main.txt | wc -l)"
echo "--- columns only in the LIVE database (would be DROPPED by a carry):"
comm -23 /tmp/cols-live.txt /tmp/cols-main.txt | head -25
echo "   total: $(comm -23 /tmp/cols-live.txt /tmp/cols-main.txt | wc -l)"

echo "=== 5. row counts for the shared tables (the carry's payload) ==="
: > /tmp/rows-live.txt
while read -r t; do
  [ -z "$t" ] && continue
  n=$($PG psql -U stashbox -d stash-box -tAc "select count(*) from \"$t\"" 2>/dev/null || echo "ERR")
  echo "$t $n" >> /tmp/rows-live.txt
done < /tmp/tables-both.txt
sort -k2 -rn /tmp/rows-live.txt | head -20
echo "--- total rows to carry: $(awk '{s+=$2} END {print s}' /tmp/rows-live.txt)"
echo "--- tables with errors (shape mismatch that blocks a plain COPY):"
grep " ERR$" /tmp/rows-live.txt | head -20
