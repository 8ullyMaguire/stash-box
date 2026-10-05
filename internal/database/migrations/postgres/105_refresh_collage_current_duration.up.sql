-- Keep collages.current_duration_ms in step with the scene's duration.
--
-- Migration 78 added current_duration_ms beside source_duration_ms precisely so a
-- client could tell a collage generated against a since-corrected duration from
-- one generated against the current one -- "a collage generated against a
-- duration that has since been corrected produces frames bunched at the end, and
-- without these two numbers that is undiagnosable".
--
-- It did not work. The column was written once by the collage INSERT and never
-- updated, and nothing else touched it: no trigger, no updater, no query. So
-- current_duration_ms always equalled source_duration_ms, `stale` was
-- permanently false, and the diagnostic it exists for was unreachable. Both
-- integration tests that assert staleness read false.
--
-- This trigger is the missing half. It fires on the scene rather than on the
-- collage, because the scene's duration is the thing that changes and collages
-- are what must learn about it -- updating collages when a collage changes would
-- be circular.
--
-- Why a trigger and not application code: the correction happens in many places
-- (edits, imports, federation, the API, direct SQL by an operator), and this is
-- the only mechanism that catches all of them. A trigger in the service would
-- leave a path that silently forgets, which is precisely the failure already
-- recorded here.

CREATE OR REPLACE FUNCTION refresh_collage_current_duration()
RETURNS TRIGGER AS $$
BEGIN
    -- duration is in SECONDS on scenes and milliseconds on collages. The two
    -- columns are both integer-ish and nothing would catch a missed conversion,
    -- so the multiply lives in exactly one place: here.
    --
    -- NULL scenes.duration means "no duration recorded", which is a real state and
    -- distinct from a duration of zero. It maps to NULL here rather than 0, because
    -- 0 would read as "stale" for every scene that simply has no duration yet.
    UPDATE collages
       SET current_duration_ms = NEW.duration * 1000
     WHERE scene_id = NEW.id
       -- Only write when the value actually changes. Without this the trigger
       -- fires a write on every scene update that touches no duration, which turns
       -- an unrelated edit into a collage rewrite and its generated_at-adjacent
       -- indexes into churn.
       AND current_duration_ms IS DISTINCT FROM NEW.duration * 1000;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

COMMENT ON FUNCTION refresh_collage_current_duration() IS
'Keeps collages.current_duration_ms equal to scenes.duration * 1000, so a collage '
'generated before a duration correction reports stale instead of silently '
'lying about which duration its frames were sampled against.';

-- AFTER, so the collage update sees the committed scene row rather than racing the
-- statement that changed it.
--
-- DROP first, then CREATE: a migration must be REPLAYABLE. Applying this file by
-- hand -- an operator, a debugging session, a verification script -- leaves the
-- trigger in place with no row in schema_migrations, so the server's own replay
-- then dies at startup and the application does not boot at all. That happened
-- here: `scripts/verify-105.sh` applies the migration directly, so running the
-- server afterwards failed with "trigger already exists" and nothing listened on
-- the port. An idempotent migration is not a nicety; it is the difference between
-- a recoverable mistake and an instance that will not start.
DROP TRIGGER IF EXISTS scenes_refresh_collage_duration ON scenes;

CREATE TRIGGER scenes_refresh_collage_duration
AFTER UPDATE OF duration ON scenes
FOR EACH ROW
WHEN (OLD.duration IS DISTINCT FROM NEW.duration)
EXECUTE FUNCTION refresh_collage_current_duration();

-- Existing rows are wrong by construction: every collage currently has
-- current_duration_ms = source_duration_ms from its INSERT. Backfill, or the bug
-- would persist for every collage already in the database and only new corrections
-- would look right.
UPDATE collages c
   SET current_duration_ms = s.duration * 1000
  FROM scenes s
 WHERE s.id = c.scene_id
   AND c.current_duration_ms IS DISTINCT FROM s.duration * 1000;