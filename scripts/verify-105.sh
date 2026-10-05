#!/usr/bin/env bash
# Verify migration 105: collages.current_duration_ms tracks scenes.duration.
#
# The bug being verified is a silent one. Migration 78 created
# current_duration_ms next to source_duration_ms so a client could tell a collage
# generated against a corrected duration from one generated against the current
# one. Nothing ever updated the column after the collage INSERT, so
# current_duration_ms == source_duration_ms forever and `stale` was permanently
# false -- no error, no warning, just a diagnostic that could never fire.
#
# This script asserts the OBSERVED behaviour after a duration correction, and then
# drops the trigger to confirm the assertion genuinely depends on it. A test that
# passes with and without the fix proves nothing.
set -euo pipefail

# Explicit -h/-p/-U: a bare connection string falls back to the local socket, and
# there is no server there. psql needs all three on this host.
DBNAME="${1:-sbx-live}"
PSQL=(psql -h 127.0.0.1 -p 55434 -U postgres -d "$DBNAME" -v ON_ERROR_STOP=1)

q() { "${PSQL[@]}" -qtA -c "$1"; }

echo "=== 1. apply the migration ==="
"${PSQL[@]}" -qf internal/database/migrations/postgres/105_refresh_collage_current_duration.up.sql

echo "=== 2. fixture: a scene at 100s with a collage sampled against it ==="
SCENE=$(q "SELECT gen_random_uuid()")
q "INSERT INTO scenes (id, duration, created_at, updated_at)
   VALUES ('$SCENE', 100, now(), now())" >/dev/null
COLLAGE=$(q "INSERT INTO collages (id, scene_id, frame_count, source_duration_ms, current_duration_ms, generated_at)
   VALUES (gen_random_uuid(), '$SCENE', 12, 100000, 100000, now()) RETURNING id")
echo "    scene=$SCENE collage=$COLLAGE"

echo "=== 3. correct the duration to 200s ==="
q "UPDATE scenes SET duration = 200 WHERE id = '$SCENE'" >/dev/null

CUR=$(q "SELECT current_duration_ms FROM collages WHERE id = '$COLLAGE'")
SRC=$(q "SELECT source_duration_ms FROM collages WHERE id = '$COLLAGE'")
echo "    source_duration_ms=$SRC current_duration_ms=$CUR"

if [ "$CUR" != "200000" ]; then
  echo "FAIL: current_duration_ms should be 200000 (200s * 1000), got '$CUR'"
  exit 1
fi
if [ "$SRC" != "100000" ]; then
  echo "FAIL: source_duration_ms must stay 100000 -- the sampler used 100s. Got '$SRC'."
  echo "      Overwriting it would destroy the only evidence of what the collage was made against."
  exit 1
fi
echo "    OK: current tracks the scene, source preserves what the sampler believed"

echo "=== 4. MUTATION: drop the trigger, confirm the assertion fails without it ==="
q "DROP TRIGGER scenes_refresh_collage_duration ON scenes" >/dev/null
q "UPDATE scenes SET duration = 300 WHERE id = '$SCENE'" >/dev/null
CUR2=$(q "SELECT current_duration_ms FROM collages WHERE id = '$COLLAGE'")
echo "    with the trigger dropped, current_duration_ms=$CUR2 (still 200000 if unmaintained)"
if [ "$CUR2" = "300000" ]; then
  echo "FAIL: dropping the trigger changed nothing -- the assertion is not testing the trigger."
  exit 1
fi
echo "    OK: without the trigger the column goes stale. The test above is load-bearing."

echo "=== 5. restore the trigger and re-verify ==="
"${PSQL[@]}" -qf internal/database/migrations/postgres/105_refresh_collage_current_duration.up.sql
q "UPDATE scenes SET duration = 400 WHERE id = '$SCENE'" >/dev/null
CUR3=$(q "SELECT current_duration_ms FROM collages WHERE id = '$COLLAGE'")
if [ "$CUR3" != "400000" ]; then
  echo "FAIL: after restoring the trigger, current_duration_ms should be 400000, got '$CUR3'"
  exit 1
fi
echo "    OK: 400000"

echo "=== 6. a scene with NO duration maps to NULL, not 0 ==="
q "UPDATE scenes SET duration = NULL WHERE id = '$SCENE'" >/dev/null
CURN=$(q "SELECT coalesce(current_duration_ms::text, 'NULL') FROM collages WHERE id = '$COLLAGE'")
if [ "$CURN" != "NULL" ]; then
  echo "FAIL: no duration should read NULL. Got '$CURN' -- 0 would report every"
  echo "      durationless scene as stale, which is the bug this mapping avoids."
  exit 1
fi
echo "    OK: NULL"

q "DELETE FROM collages WHERE id = '$COLLAGE'" >/dev/null
q "DELETE FROM scenes WHERE id = '$SCENE'" >/dev/null
echo
echo "=== migration 105 VERIFIED ==="