-- Undo 111_tag_implications.
--
-- The trigger and its function go first: the function reads the table, so dropping the
-- table while the trigger exists leaves a trigger whose function fails on the next insert.
DROP TRIGGER IF EXISTS tag_implications_no_cycle_trg ON tag_implications;
DROP FUNCTION IF EXISTS tag_implications_no_cycle();

DROP INDEX IF EXISTS tag_implications_implied_idx;
DROP TABLE IF EXISTS tag_implications;
