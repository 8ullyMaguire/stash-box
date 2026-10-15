#!/usr/bin/env bash
# Behavioural proof of the switch: does a queued URL actually get fetched, and does the takedown
# refuse a blocked one? A log line saying "ON" is a claim; a request that happened is evidence.
set -uo pipefail
PG="docker exec stashbox-pg"
L=/home/alvaro/stash-box-deploy/stash-box.log
q() { $PG psql -U stashbox -d stash-box -tAc "$1"; }

echo "=== the takedown's storage (find it by name, do not assume) ==="
q "select table_name from information_schema.tables where table_schema='public' and (table_name like '%block%' or table_name like '%takedown%' or table_name like '%deny%')"
echo "--- its columns:"
for t in $(q "select table_name from information_schema.tables where table_schema='public' and (table_name like '%block%' or table_name like '%takedown%')"); do
  echo "  $t:"
  q "select '    '||column_name||' '||data_type||' null='||is_nullable from information_schema.columns where table_schema='public' and table_name='$t' order by ordinal_position"
done

echo
echo "=== 1. queue a URL that nobody has blocked ==="
q "insert into ingest_queue (url, url_normalized) values ('https://example.com/', 'https://example.com/') returning id, status" | sed 's/^/    inserted /'

echo "=== 2. the cron's schedule for the drain (from the running binary's own log if present) ==="
grep -iE "ingest|drain|sweep" "$L" | tail -3 | sed 's/^/    /'

echo "=== 3. watching for the request to be made (up to 150s) ==="
for i in $(seq 1 15); do
  status=$(q "select status from ingest_queue where url_normalized='https://example.com/'")
  outcome=$(q "select coalesce(outcome,'-') from ingest_queue where url_normalized='https://example.com/'")
  attempts=$(q "select attempts from ingest_queue where url_normalized='https://example.com/'")
  echo "    t+$((i*10))s: status=$status outcome=$outcome attempts=$attempts"
  [ "$status" != "pending" ] && [ "$status" != "running" ] && break
  sleep 10
done

echo "=== 4. what the row says happened ==="
q "select 'status='||status||' outcome='||coalesce(outcome,'-')||' attempts='||attempts||' error='||coalesce(last_error,'-')||' reason='||coalesce(reason,'-') from ingest_queue where url_normalized='https://example.com/'"

echo "=== 5. the log's own account of it ==="
grep -iE "example.com|ingest|drain" "$L" | tail -8 | sed 's/^/    /'

echo "=== 6. NEGATIVE: a blocked host must be refused, not fetched ==="
echo "--- block example.org, then queue it"
q "insert into content_source_blocks (host_or_url) values ('example.org')" 2>/dev/null \
  || q "select 'no single-column block table; columns are: '||string_agg(column_name, ',') from information_schema.columns where table_schema='public' and table_name='content_source_blocks'"
q "insert into ingest_queue (url, url_normalized) values ('https://example.org/page', 'https://example.org/page') returning id" | sed 's/^/    inserted /'
sleep 45
q "select 'blocked-row: status='||status||' outcome='||coalesce(outcome,'-')||' error='||coalesce(last_error,'-')||' reason='||coalesce(reason,'-') from ingest_queue where url_normalized='https://example.org/page'"
