#!/usr/bin/env bash
# (a) Do the deployment-only tables hold data? (b) Are the shared tables' columns identical?
set -uo pipefail
PG="docker exec stashbox-pg"
SCRATCH=stash_box_main_scratch

$PG psql -U stashbox -d stash-box -tAc "select tablename from pg_tables where schemaname='public' order by 1" | sort > /tmp/tables-live.txt
$PG psql -U stashbox -d "$SCRATCH" -tAc "select tablename from pg_tables where schemaname='public' order by 1" | sort > /tmp/tables-main.txt
comm -23 /tmp/tables-live.txt /tmp/tables-main.txt > /tmp/tables-only-live.txt
comm -13 /tmp/tables-live.txt /tmp/tables-main.txt > /tmp/tables-only-main.txt

echo "=== (a) row counts in the deployment-ONLY tables (30) ==="
while read -r t; do
  [ -z "$t" ] && continue
  n=$($PG psql -U stashbox -d stash-box -tAc "select count(*) from \"$t\"" 2>/dev/null || echo ERR)
  printf "%-34s %s\n" "$t" "$n"
done < /tmp/tables-only-live.txt

echo "=== (b) columns only in MAIN, for the shared tables (carried rows must tolerate these) ==="
$PG psql -U stashbox -d stash-box -tAc \
  "select table_name||'|'||column_name||'|'||data_type||'|'||is_nullable||'|'||coalesce(column_default,'-') from information_schema.columns where table_schema='public' order by table_name, column_name" \
  | sort > /tmp/cols-live.txt
$PG psql -U stashbox -d "$SCRATCH" -tAc \
  "select table_name||'|'||column_name||'|'||data_type||'|'||is_nullable||'|'||coalesce(column_default,'-') from information_schema.columns where table_schema='public' order by table_name, column_name" \
  | sort > /tmp/cols-main.txt
comm -13 /tmp/cols-live.txt /tmp/cols-main.txt > /tmp/cols-only-main.txt
comm -23 /tmp/cols-live.txt /tmp/cols-main.txt > /tmp/cols-only-live.txt
echo "only in main: $(wc -l < /tmp/cols-only-main.txt)   only in live: $(wc -l < /tmp/cols-only-live.txt)"
echo "--- first 30 of 'only in main' (tables main has columns for that the live DB lacks):"
head -30 /tmp/cols-only-main.txt
echo "--- first 30 of 'only in live' (columns the live DB has that main's schema does not):"
head -30 /tmp/cols-only-live.txt

echo "=== (c) which tables do the column differences touch? ==="
cut -d'|' -f1 /tmp/cols-only-main.txt | sort -u | tr '\n' ' '; echo
echo "--- and:"
cut -d'|' -f1 /tmp/cols-only-live.txt | sort -u | tr '\n' ' '; echo
