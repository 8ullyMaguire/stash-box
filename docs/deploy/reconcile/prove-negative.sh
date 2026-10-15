#!/usr/bin/env bash
# The negative half: a BLOCKED host must be refused rather than fetched. Plus a general egress probe,
# because "the switch is on" is only useful if the host can actually reach the web.
set -uo pipefail
PG="docker exec stashbox-pg"
q() { $PG psql -U stashbox -d stash-box -tAc "$1"; }

echo "=== egress from this host, outside the app (does arbitrary HTTPS work at all?) ==="
for u in https://example.com/robots.txt https://example.org/robots.txt https://stashdb.org/robots.txt; do
  code=$(curl -s -o /dev/null -m 15 -w '%{http_code}' "$u" 2>&1 || echo "curl-error")
  echo "    curl $u -> $code"
done

echo
echo "=== NEGATIVE TEST: block a host, queue a URL on it, and see whether a request is made ==="
host=example.net
q "delete from content_source_block where value='$host'"
q "insert into content_source_block (kind, value, reason) values ('host', '$host', 'operator test, 2026-10-15') returning id, kind, value" | sed 's/^/    block inserted: /'
q "insert into ingest_queue (url, url_normalized) values ('https://$host/pages/abc', 'https://$host/pages/abc') returning id" | sed 's/^/    queued: /'

for i in $(seq 1 12); do
  row=$(q "select status||' | '||coalesce(outcome,'-')||' | reason='||coalesce(reason,'-')||' | err='||coalesce(last_error,'-') from ingest_queue where url_normalized='https://$host/pages/abc'")
  echo "    t+$((i*10))s: $row"
  case "$row" in rejected*|failed*|done*) break;; esac
  sleep 10
done

echo
echo "=== both sides side by side, from the same queue ==="
q "select '  '||url||'  ->  status='||status||' outcome='||coalesce(outcome,'-')||' reason='||coalesce(reason,'-') from ingest_queue where url like 'https://%' and created_at > now() - interval '30 minutes' order by url"

echo
echo "=== cleanup: release the block, drop the test queue rows ==="
q "delete from content_source_block where value='$host'" >/dev/null; echo "    block released"
q "delete from ingest_queue where url_normalized in ('https://example.com/','https://example.org/page','https://$host/pages/abc')" >/dev/null
echo "    test rows left in the queue: $(q "select count(*) from ingest_queue")"
echo "    blocks left: $(q "select count(*) from content_source_block")"
echo "    service: $(systemctl --user is-active stash-box 2>/dev/null || echo n/a)"
