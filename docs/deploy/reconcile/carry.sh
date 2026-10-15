#!/usr/bin/env bash
# Carry the deployment's real data into main's freshly-migrated schema.
#
# What this does and why each choice was made:
#   * 24 tables in the live database hold rows AND exist in main's schema. The rest of the live
#     database (30 tables) is the OLD lineage's own feature set -- instances, themes, reputation,
#     preservation, storage, metadata_changes, the singular field_verification_state -- and every one
#     of them is EMPTY (measured), so nothing is lost by leaving them behind.
#   * image_types / image_type_groups / image_type_conflicts are SEEDED IDENTICALLY by main's own
#     migrations (md5-compared), so they are skipped rather than duplicated.
#   * users / user_roles / sites are seeded by main's migrations with DIFFERENT ids for the same
#     names, so main's seed rows are deleted and the live ones carried -- otherwise every reference
#     into them would point at the wrong id.
#   * FKs and triggers are suspended for the load (session_replication_role = replica; the role is
#     superuser, measured), and re-enabled for the verification, which re-checks referential
#     integrity rather than trusting the load.
set -uo pipefail
# WHERE -i GOES AND WHY IT GOES ONLY THERE. The receiving COPY reads the pipe on the container's
# stdin, so THAT call needs -i; without it docker attaches the container's stdin to nothing, big
# tables die on SIGPIPE and small ones silently copy zero rows (run 1). But an -i call ALSO consumes
# the host-side stdin, and inside this loop the host-side stdin IS the plan file being read -- so
# putting -i on every call made the loop stop after its first table (run 2). Only the receiver gets
# it, and the sender gets </dev/null so it cannot eat the plan either.
PG="docker exec stashbox-pg"
PGRECV="docker exec -i stashbox-pg"
LIVE=stash-box
NEW=stash_box_main_scratch
PLAN=/tmp/carry-plan.txt
LOG=/tmp/carry.log
SKIP="schema_migrations image_types image_type_groups image_type_conflicts"
REPLACE="users user_roles sites"

: > "$LOG"
say() { echo "$*" | tee -a "$LOG"; }

say "=== carry starting $(date -Is) ==="

# ---- phase 0: clear the tables main's migrations seeded with their own ids --------------------
say "--- phase 0: replacing main's seeded rows in: $REPLACE"
for t in $REPLACE; do
  # replica role, so deleting a seeded user that a seeded row references cannot block the delete
  $PG psql -U stashbox -d "$NEW" -q -c "SET session_replication_role = replica; DELETE FROM \"$t\";" \
    && say "    cleared $t"
done

# ---- phase 1: the carry ----------------------------------------------------------------------
carried=0
while IFS='|' read -r t cols; do
  [ -z "$t" ] && continue
  case " $SKIP " in *" $t "*) say "--- skip $t (main's schema seeds it identically)"; continue;; esac
  start=$(date +%s)
  live_n=$($PG psql -U stashbox -d "$LIVE" -tAc "select count(*) from \"$t\"" 2>/dev/null || echo 0)
  # COPY out of the live database, straight into the new one, with the shared column list only.
  # The pipe is the whole migration: no intermediate file, so a partial write is visible as a
  # non-zero exit here rather than as a truncated CSV nobody checks.
  if $PG psql -U stashbox -d "$LIVE" -c "\copy (select $cols from \"$t\") to stdout" < /dev/null 2>>"$LOG" \
     | $PGRECV psql -U stashbox -d "$NEW" -q -c "SET session_replication_role = replica" \
         -c "\copy \"$t\" ($cols) from stdin" 2>>"$LOG"
  then
    new_n=$($PG psql -U stashbox -d "$NEW" -tAc "select count(*) from \"$t\"" 2>/dev/null || echo ERR)
    elapsed=$(( $(date +%s) - start ))
    if [ "$live_n" = "$new_n" ]; then
      say "    OK    $t: $live_n rows in ${elapsed}s"
    else
      say "    MISMATCH $t: live=$live_n new=$new_n"
    fi
    carried=$((carried+1))
  else
    say "    FAILED $t (see $LOG)"
  fi
done < "$PLAN"

# ---- phase 2: sequences ----------------------------------------------------------------------
say "--- phase 2: sequences"
$PG psql -U stashbox -d "$NEW" -tAc \
  "select table_name||'.'||column_name from information_schema.columns
    where table_schema='public' and column_default like 'nextval%'" | while read -r seq; do
  [ -z "$seq" ] && continue
  t=${seq%%.*}; c=${seq#*.}
  max=$($PG psql -U stashbox -d "$NEW" -tAc "select coalesce(max(\"$c\"),0) from \"$t\"")
  $PG psql -U stashbox -d "$NEW" -q -c "select setval(pg_get_serial_sequence('\"$t\"','$c'), greatest($max,1))" >/dev/null 2>&1
  say "    $seq -> setval($max)"
done

# ---- phase 3: verification -------------------------------------------------------------------
say "--- phase 3: verification (FKs re-enabled, so these are real reference checks)"
$PG psql -U stashbox -d "$NEW" -tAc "select count(*) from scene_urls su left join scenes s on s.id = su.scene_id where s.id is null" > /tmp/orphan1
$PG psql -U stashbox -d "$NEW" -tAc "select count(*) from scene_urls su left join sites s on s.id = su.site_id where s.id is null" > /tmp/orphan2
$PG psql -U stashbox -d "$NEW" -tAc "select count(*) from performers p left join performers_aliases a on a.performer_id = p.id where a.performer_id is null" > /tmp/orphan3 2>/dev/null
say "    orphan scene_urls -> scenes:    $(cat /tmp/orphan1)"
say "    orphan scene_urls -> sites:     $(cat /tmp/orphan2)"
say "--- final counts, live vs main-schema:"
for t in scenes scene_urls performers studios tags sites users invite_keys user_roles scene_search; do
  a=$($PG psql -U stashbox -d "$LIVE" -tAc "select count(*) from \"$t\"")
  b=$($PG psql -U stashbox -d "$NEW" -tAc "select count(*) from \"$t\"")
  [ "$a" = "$b" ] && st="OK  " || st="DIFF"
  say "    $st $t: live=$a new=$b"
done
say "=== carry finished $(date -Is); tables carried: $carried ==="
