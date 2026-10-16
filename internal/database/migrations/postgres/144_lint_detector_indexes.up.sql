-- SPEC §7.24.4: the indexes the lint detectors need, added after MEASURING them missing.
--
-- 144, not 142: 142 and 143 were taken while this file was being written (142 by the performer filter
-- indexes, 143 by the sync watermarks), and golang-migrate identifies a migration by its number -- two
-- files with one version is a deployment that refuses to start. The next free number is the only
-- correct one, which is why the number is checked against the directory rather than assumed.
--
-- The measurement, on a real archive (thinkcentre, 112,391 performers and 181,896 alias rows):
--
--   duplicate_performer_url       1.85s
--   duplicate_scene_url           0.67s
--   low_resolution_performer_image 0.08s
--   impossible_scene_date         9.28s
--   performer_alias_collision    37.54s   <-- the board is unusable at this speed
--
-- # Why the alias detector was the slow one
--
-- It joins every alias against every name and every other alias, on `lower(...)` of both sides, because
-- "Nina" and "nina" are the same claim about a person. The indexes that existed -- `index_performers_on_alias`
-- on `performer_aliases(alias)`, and `index_active_performers_on_name` on `performers(name, disambiguation)` --
-- are on the UNLOWERED columns, so no index could answer that join and the planner fell back to comparing
-- pairs.
--
-- # Why expression indexes and not a rewrite
--
-- The query's shape is not the accident; the case-insensitivity is the requirement. So the fix is to give the
-- planner something it can use for the comparison it is actually being asked to make, rather than to change
-- the question to one the old indexes happen to answer -- a detector that stopped reporting "Nina" against
-- "nina" would go quiet on exactly the duplicates a curator is looking for.
--
-- PARTIAL on the performers side, matching `index_active_performers_on_name`: a deleted performer is not a
-- lint subject (every detector filters `NOT deleted`), and an index over the merged-away rows would be built,
-- maintained, and never read.
--
-- CONCURRENTLY is deliberately NOT used. It cannot run inside a transaction, and golang-migrate wraps each
-- migration in one; a migration that failed for that reason would be a deployment that cannot start. The cost
-- is a write lock for the duration of the build, which is seconds on this archive and is the same trade every
-- other index in this directory makes.

CREATE INDEX IF NOT EXISTS "index_performer_aliases_on_lower_alias"
    ON "performer_aliases" (lower("alias"));

CREATE INDEX IF NOT EXISTS "index_active_performers_on_lower_name"
    ON "performers" (lower("name")) WHERE NOT "deleted";
