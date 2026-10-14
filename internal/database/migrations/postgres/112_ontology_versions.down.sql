-- Undo 112_ontology_versions.
--
-- The trigger and its function go first for the same reason as 111's down: the function
-- inserts into the table, so dropping the table while the trigger exists leaves a trigger
-- that fails on the next change to tag_implications.
DROP TRIGGER IF EXISTS ontology_version_trg ON tag_implications;
DROP FUNCTION IF EXISTS record_ontology_version();

DROP TABLE IF EXISTS ontology_versions;
