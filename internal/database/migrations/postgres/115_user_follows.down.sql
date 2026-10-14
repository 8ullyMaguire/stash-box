-- Reverse of 115_user_follows.up.sql.
--
-- Both feed indexes go with it: they exist for the activity feed, which is the read this
-- migration makes possible, and an index nobody's query needs is a write cost on every
-- fingerprint submission and list addition.
--
-- The signals themselves (fingerprints, list items, reviews) are untouched -- this migration
-- only recorded who watches whom.

DROP INDEX IF EXISTS list_items_list_created_at_idx;
DROP INDEX IF EXISTS scene_fingerprints_user_created_at_idx;
DROP TABLE IF EXISTS "user_follows";
