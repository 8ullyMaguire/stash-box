#!/usr/bin/env bash
# Is the instance actually healthy on main's schema with the carried data, or merely up?
set -uo pipefail
PG="docker exec stashbox-pg"
L=/home/alvaro/stash-box-deploy/stash-box.log
q() { $PG psql -U stashbox -d stash-box -tAc "$1"; }
say() { echo "$*"; }

say "=== migration state (should be main's tip, not dirty) ==="
say "    $($PG psql -U stashbox -d stash-box -tAc "select version||' dirty='||dirty from schema_migrations")"

say "=== the public site ==="
say "    nginx: $(curl -s -o /tmp/home.html -w '%{http_code}' http://127.0.0.1:8011/)  bytes=$(wc -c < /tmp/home.html)"
say "    title: $(grep -o '<title>[^<]*' /tmp/home.html | head -1)"
say "    contains the frontend bundle: $(grep -c 'assets/' /tmp/home.html)"

say "=== the public API answers with the carried data ==="
curl -s -X POST http://127.0.0.1:9998/graphql -H 'Content-Type: application/json' \
  -d '{"query":"{ queryScenes(input: { per_page: 1 }) { count scenes { id title } } }"}' | head -c 300; echo
curl -s -X POST http://127.0.0.1:9998/graphql -H 'Content-Type: application/json' \
  -d '{"query":"{ queryPerformers(input: { per_page: 1 }) { count performers { id name } } }"}' | head -c 300; echo

say "=== is the crawl-hygiene/aggregator machinery live in the running binary? ==="
say "    outbound jobs registered: $(grep -c 'content_fetch_sources is ON' "$L")"
say "    errors since the switchover: $(tail -400 "$L" | grep -c 'level=error')"
say "    migrations attempted since switchover: $(tail -400 "$L" | grep -ci 'migrat')"

say "=== the data is intact where the app reads it ==="
for t in scenes scene_urls performers studios tags sites users; do
  say "    $t = $(q "select count(*) from $t")"
done
say "    scene_urls with a site: $(q "select count(*) from scene_urls where site_id is not null")"
say "    sources (phase 1 table, carried from the old lineage): $(q "select count(*) from content_sources")"

say "=== disks ==="
df -h /home/alvaro | tail -1 | sed 's/^/    /'
say "    db size: $($PG psql -U stashbox -d stash-box -tAc "select pg_size_pretty(pg_database_size('stash-box'))")"
say "    old database preserved: $($PG psql -U stashbox -d postgres -tAc "select pg_size_pretty(pg_database_size(datname))||'  '||datname from pg_database where datname like 'stash-box-pre-main%'" | sed 's/^ *//')"
