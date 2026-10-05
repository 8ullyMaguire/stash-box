#!/usr/bin/env bash
# Behavioural proof that migration 103 does what it claims.
#
# Every assertion here is a ROUND TRIP: seed rows, then read the value back out
# of scene_search. Object existence is checked too, but existence is the weaker
# claim -- a trigger that was created and never fires passes every catalogue
# probe there is.
#
#   scene_details   a scene's details reach scene_search
#   scene_director  a scene's director reaches scene_search
#   tag_names       ADDING A TAG to a scene updates scene_search   <- the trigger
#   tag_names       REMOVING A tag updates scene_search           <- the trigger
#   tag_names       RENAMING a tag updates every scene carrying it <- the trigger
#   performer_names survives the second many-to-many join uncrossed
#
# Run against a provisioned database:  scripts/verify-103.sh [dbname]
set -euo pipefail

DB="${1:-sbx-scratch}"
P="psql -h 127.0.0.1 -p 55434 -U postgres -d $DB -v ON_ERROR_STOP=1 --quiet"
export PGPASSWORD=smoke_pw

fail=0
ok()   { echo "  PASS  $1"; }
bad()  { echo "  FAIL  $1"; fail=1; }
check() { # check <label> <expected> <actual>
  if [ "$2" = "$3" ]; then ok "$1"; else bad "$1 (expected [$2], got [$3])"; fi
}

echo "== catalogue =="
# Sorted, not ordinal_position order. Column order in a table is not a property
# anything depends on, and asserting it made this check fail the moment the three
# new columns were declared in migration 61 ahead of scene_code -- a false alarm
# about a change that was correct. The check that matters is the SET.
cols=$($P -tAc "select string_agg(column_name,',' order by column_name) from information_schema.columns where table_name='scene_search';")
check "scene_search has 12 columns" \
  "network_aliases,network_name,performer_names,scene_code,scene_date,scene_details,scene_director,scene_id,scene_title,studio_aliases,studio_name,tag_names" \
  "$cols"

n=$($P -tAc "select count(*) from pg_indexes where indexname='scene_search_bm25_idx';")
check "bm25 index present" "1" "$n"

# Behavioural, not textual: a probe written from the indexdef we EXPECTED can
# miss a correct index because pg_get_indexdef parenthesises and quotes the
# predicate. Counting the field the index actually knows about cannot.
f=$($P -tAc "select count(*) from pg_index i join pg_attribute a on a.attrelid=i.indrelid and a.attnum=any(i.indkey) where i.indexrelid='scene_search_bm25_idx'::regclass and a.attname='scene_details';")
check "bm25 index covers scene_details" "1" "$f"

for t in trg_scene_search_on_st_insert trg_scene_search_on_st_delete trg_scene_search_on_tag_rename; do
  n=$($P -tAc "select count(*) from pg_trigger where tgname='$t';")
  check "trigger $t present" "1" "$n"
done

# --- fixture ---------------------------------------------------------------
$P -q <<'SQL'
DELETE FROM scene_tags; DELETE FROM scene_performers; DELETE FROM scene_search; DELETE FROM scenes;
DELETE FROM tags; DELETE FROM performers; DELETE FROM studios;
INSERT INTO studios (id, name, created_at, updated_at)
  VALUES ('11111111-1111-1111-1111-111111111111', 'Probe Studio', now(), now());
INSERT INTO performers (id, name, created_at, updated_at)
  VALUES ('22222222-2222-2222-2222-222222222222', 'Probe Performer', now(), now()),
         ('33333333-3333-3333-3333-333333333333', 'Second Performer', now(), now());
INSERT INTO tags (id, name, created_at, updated_at)
  VALUES ('44444444-4444-4444-4444-444444444444', 'anal', now(), now()),
         ('55555555-5555-5555-5555-555555555555', 'outdoors', now(), now());
INSERT INTO scenes (id, title, details, director, studio_id, created_at, updated_at)
  VALUES ('66666666-6666-6666-6666-666666666666', 'Probe Scene',
          'a cramped hotel room on a rainy night', 'A Director', '11111111-1111-1111-1111-111111111111',
          now(), now());
-- two performers AND two tags on one scene: the cross-multiply trap
INSERT INTO scene_performers (scene_id, performer_id) VALUES
  ('66666666-6666-6666-6666-666666666666', '22222222-2222-2222-2222-222222222222'),
  ('66666666-6666-6666-6666-666666666666', '33333333-3333-3333-3333-333333333333');
INSERT INTO scene_tags (scene_id, tag_id) VALUES
  ('66666666-6666-6666-6666-666666666666', '44444444-4444-4444-4444-444444444444');
SQL

echo "== behaviour =="
d=$($P -tAc "select scene_details from scene_search where scene_id='66666666-6666-6666-6666-666666666666';")
check "scene_details populated on write" "a cramped hotel room on a rainy night" "$d"

di=$($P -tAc "select scene_director from scene_search where scene_id='66666666-6666-6666-6666-666666666666';")
check "scene_director populated on write" "A Director" "$di"

pn=$($P -tAc "select array_to_string(performer_names,',') from scene_search where scene_id='66666666-6666-6666-6666-666666666666';")
check "performer_names uncrossed (2 performers x 1 tag)" "Probe Performer,Second Performer" "$pn"

t1=$($P -tAc "select array_to_string(tag_names,',') from scene_search where scene_id='66666666-6666-6666-6666-666666666666';")
check "tag_names after one tag" "anal" "$t1"

# THE assertion. Nothing but the trigger can do this.
$P -q -c "INSERT INTO scene_tags (scene_id, tag_id) VALUES ('66666666-6666-6666-6666-666666666666','55555555-5555-5555-5555-555555555555');"
t2=$($P -tAc "select array_to_string(tag_names,',') from scene_search where scene_id='66666666-6666-6666-6666-666666666666';")
check "ADDING a tag updates scene_search" "anal,outdoors" "$t2"

$P -q -c "UPDATE tags SET name='outdoor' WHERE id='55555555-5555-5555-5555-555555555555';"
t3=$($P -tAc "select array_to_string(tag_names,',') from scene_search where scene_id='66666666-6666-6666-6666-666666666666';")
check "RENAMING a tag reaches the scene" "anal,outdoor" "$t3"

$P -q -c "DELETE FROM scene_tags WHERE scene_id='66666666-6666-6666-6666-666666666666' AND tag_id='55555555-5555-5555-5555-555555555555';"
t4=$($P -tAc "select coalesce(array_to_string(tag_names,','),'<null>') from scene_search where scene_id='66666666-6666-6666-6666-666666666666';")
check "REMOVING a tag updates scene_search" "anal" "$t4"

$P -q -c "UPDATE scenes SET details='a rooftop at dawn' WHERE id='66666666-6666-6666-6666-666666666666';"
d2=$($P -tAc "select scene_details from scene_search where scene_id='66666666-6666-6666-6666-666666666666';")
check "details update propagates" "a rooftop at dawn" "$d2"

# A scene whose search row was never built is exactly what §7.25.2 reports.
$P -q -c "DELETE FROM scene_search WHERE scene_id='66666666-6666-6666-6666-666666666666';"
sc=$($P -tAc "select count(*) from scenes where deleted=false;")
ix=$($P -tAc "select count(*) from scene_search;")
check "drift is detectable (scenes != indexed)" "1" "$([ "$sc" != "$ix" ] && echo 1 || echo 0)"

$P -q -c "SELECT upsert_scene_search('66666666-6666-6666-6666-666666666666');"
ix2=$($P -tAc "select count(*) from scene_search;")
check "upsert_scene_search repairs the drift" "$sc" "$ix2"

echo
[ "$fail" -eq 0 ] && echo "ALL PASS" || echo "FAILURES PRESENT"
exit "$fail"