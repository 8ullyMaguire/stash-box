-- Down for 108. Drops the health state WITH the table: content_sources is the only
-- owner of it, so there is nothing to preserve and nowhere to put it. The URLs
-- themselves survive in scene_urls, which this migration reads from and never
-- writes to.
--
-- Order matters twice here.
--
-- The triggers must go BEFORE the table. Left behind, the mirror function would
-- fire on the next scene_urls insert and fail with `relation "content_sources"
-- does not exist` -- and the write that fails is an ordinary member edit, so the
-- symptom reads as a broken edit path rather than a half-applied rollback.
--
-- The view must go before the table for the same reason and it fails differently:
-- a view over a dropped table does not error on DROP, it errors on the next SELECT
-- that touches it, which is a scene page rather than a migration.

DROP VIEW IF EXISTS "scene_sources";

DROP TRIGGER IF EXISTS trg_content_sources_from_scene_urls_ins ON "scene_urls";
DROP TRIGGER IF EXISTS trg_content_sources_from_scene_urls_upd ON "scene_urls";
DROP TRIGGER IF EXISTS trg_content_sources_from_scenes_del ON "scenes";

DROP FUNCTION IF EXISTS content_sources_from_scene_urls();
DROP FUNCTION IF EXISTS content_sources_from_scenes_deleted();

DROP TABLE IF EXISTS "content_sources";
