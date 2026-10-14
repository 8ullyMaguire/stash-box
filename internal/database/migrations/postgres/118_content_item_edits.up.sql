-- The edit -> content item join, mirroring `tag_edits` from migration 05.
--
-- WHY IT EXISTS: an edit stores WHAT changed as JSON, but nothing else in the schema can
-- answer "which edits touched this item" or "what is this edit's target" -- `edits` has a
-- target_type and no target id (migration 05's shape, kept). GetEditTargetID resolves the
-- target by joining the per-type join table for the edit's type, so a new target type
-- without a join table is a type whose edits can never be linked to their entity: the edit
-- applies, the item exists, and `edit.target` is null forever.
--
-- PLAIN FOREIGN KEYS, no ON DELETE CASCADE, exactly as tag_edits/scene_edits have them.
-- Content items are soft-deleted, so the cascade would never fire, and copying the
-- existing shape means a reader comparing the four tables is not asked to decide whether
-- the difference means something.
CREATE TABLE "content_item_edits" (
  "edit_id" uuid not null,
  "content_item_id" uuid not null,
  foreign key ("edit_id") references "edits" ("id"),
  foreign key ("content_item_id") references "content_items" ("id")
);

-- The reverse direction -- "every edit of this item" -- is a page, and it is the one this
-- index exists for. The forward direction is answered by the primary-ish pair of the
-- join's own columns and by GetEditTargetID joining on edit_id.
CREATE INDEX "content_item_edits_item_idx" ON "content_item_edits" ("content_item_id");

-- `edits.target_type` was varchar(10), which holds SCENE, STUDIO, PERFORMER and TAG -- and
-- does not hold CONTENT_ITEM (12 characters). Migration 05 chose the width for the four
-- types it knew about, and this is the first type to exceed it.
--
-- FOUND BY WRITING, not by reading: the vocabulary tests all passed, because a closed
-- vocabulary in Go and a varchar width in Postgres are different things, and the only way to
-- see this one is to insert the row. Every other entity_type column is wide enough already
-- (reviews is TEXT, list_items varchar(50), elo_ratings varchar(32), expected_totals and
-- content_sources varchar(20)), which is exactly why the narrow one looked like all the
-- others until an edit was actually written.
--
-- WIDENING is non-destructive for the four existing spellings: every value in the column
-- today is at most 9 characters, so the change rewrites no data and cannot fail on an
-- existing row.
ALTER TABLE "edits" ALTER COLUMN "target_type" TYPE varchar(20);
