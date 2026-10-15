#!/usr/bin/env bash
# Switch the deployment from the old lineage's database+binary to main's.
#
# The database NAME is preserved (the old one is renamed aside) so the DSN in the config keeps
# working and a rollback is one rename away. The JWT and session keys are untouched: they are not
# carried data, they are what makes an existing session or token still valid.
set -uo pipefail
D=/home/alvaro/stash-box-deploy
STAMP=$(date +%Y%m%d-%H%M%S)
export XDG_RUNTIME_DIR=/run/user/1000
PG="docker exec stashbox-pg"
say() { echo "$*"; }

say "=== switchover $STAMP ==="
say "--- 1. stop the service"
systemctl --user stop stash-box
sleep 3
say "    is-active: $(systemctl --user is-active stash-box 2>&1)"

say "--- 2. rename databases (the deployment's own is preserved, dated)"
$PG psql -U stashbox -d postgres -q -c "ALTER DATABASE \"stash-box\" RENAME TO \"stash-box-pre-main-$STAMP\"" \
  && say "    old  -> stash-box-pre-main-$STAMP"
$PG psql -U stashbox -d postgres -q -c "ALTER DATABASE stash_box_main_scratch RENAME TO \"stash-box\"" \
  && say "    new  -> stash-box (main's schema, the data carried)"
say "    databases now:"
$PG psql -U stashbox -d postgres -tAc "select datname from pg_database where datistemplate=false order by 1" | sed 's/^/      /'

say "--- 3. config: the DSN is unchanged, the switch goes ON"
cp -a "$D/config/stash-box-config.yml" "$D/config/stash-box-config.yml.bak-before-main-$STAMP"
grep -v "^content_fetch_sources:" "$D/config/stash-box-config.yml" > /tmp/cfg.new
cat >> /tmp/cfg.new <<'EOF'

# content_fetch_sources: whether this instance may make outbound HTTP requests to
# third-party content sources -- the ingest drain's page fetch, the health sweep, and
# Wayback "Save Page Now". ON 2026-10-15 by owner instruction, with the guardrails in
# docs/spec/feature-content-aggregator.md: every request obeys the host's robots.txt and
# a per-host pace (content_fetch_min_interval), a URL or a whole host can be taken out of
# service by an operator takedown, and this instance serves no media at all.
content_fetch_sources: true
EOF
mv /tmp/cfg.new "$D/config/stash-box-config.yml"
chmod 600 "$D/config/stash-box-config.yml"
tail -9 "$D/config/stash-box-config.yml" | sed 's/^/    /'

say "--- 4. main's binary becomes the deployed one"
cp -a "$D/bin/stash-box" "$D/bin/stash-box.pre-main-$STAMP"
cp -a "$D/bin/stash-box.main" "$D/bin/stash-box"
say "    deployed revision: $(go version -m "$D/bin/stash-box" | awk '/vcs.revision/{print $2}')"
say "    rollback binary:   bin/stash-box.pre-main-$STAMP"

say "--- 5. start and watch"
wc -l < "$D/stash-box.log" > /tmp/mark
systemctl --user start stash-box
sleep 25
say "    is-active: $(systemctl --user is-active stash-box)"
say "    new log lines:"
tail -n +$(( $(cat /tmp/mark) + 1 )) "$D/stash-box.log" | tail -14 | sed 's/^/      /'

say "--- 6. does it serve?"
say "    nginx 8011: $(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8011/)"
say "    graphql:    $(curl -s -X POST http://127.0.0.1:9998/graphql -H 'Content-Type: application/json' -d '{"query":"{ version { version hash } }"}' | head -c 120)"
say "    data:       $($PG psql -U stashbox -d stash-box -tAc "select 'scenes=' || count(*) from scenes") $($PG psql -U stashbox -d stash-box -tAc "select 'performers=' || count(*) from performers")"
say "=== switchover done $(date -Is) ==="
