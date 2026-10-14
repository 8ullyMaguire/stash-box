-- Reverse of 114_scene_freshness.up.sql.
--
-- The view and the index it added to `list_items` go; the signal rows themselves (and the
-- fingerprints, list items and reviews they record) are untouched -- this migration only ever
-- derived a score from them.

DROP MATERIALIZED VIEW IF EXISTS scene_freshness;

DROP INDEX IF EXISTS list_items_entity_created_at;
