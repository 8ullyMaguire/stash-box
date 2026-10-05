DROP TRIGGER IF EXISTS scenes_refresh_collage_duration ON scenes;

DROP FUNCTION IF EXISTS refresh_collage_current_duration();

-- Nothing else to undo: current_duration_ms keeps whatever value the rows hold.
-- It is a cache of scenes.duration, so dropping the trigger that maintains it
-- leaves the last known value in place rather than NULLing it -- and the next
-- collage generation rewrites it from the scene anyway. Nulling every row would
-- turn "the trigger is gone" into "every collage now reports unknown duration",
-- which reads as data loss rather than as a reverted migration.