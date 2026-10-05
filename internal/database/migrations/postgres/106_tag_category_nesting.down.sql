-- Undo 106_tag_category_nesting.
--
-- The trigger and function go first: they reference parent_id, so dropping the column
-- while the trigger exists leaves a trigger whose function no longer compiles, and the
-- next INSERT on tag_categories fails with an obscure error.
DROP TRIGGER IF EXISTS tag_categories_no_cycle_trg ON tag_categories;
DROP FUNCTION IF EXISTS tag_categories_no_cycle();

DROP INDEX IF EXISTS tag_categories_parent_id_idx;

-- IF EXISTS because a partially-applied 106 may have added the column without the rest.
ALTER TABLE tag_categories DROP COLUMN IF EXISTS parent_id;