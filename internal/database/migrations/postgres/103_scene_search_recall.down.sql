-- SPEC §7.25.1 / §7.25.2 rollback.
--
-- Reverses migration 103 only. It does NOT revert the three column declarations
-- added to migration 61's CREATE TABLE: those exist solely because sqlc ignores
-- an ALTER against a table whose shape came from a DROP+CREATE, so removing them
-- would break code generation while leaving every real database correct. A
-- database that has applied 103 keeps the three columns as ordinary TEXT/TEXT[]
-- columns that nothing reads -- `scene_search` is a derived table, so an empty
-- or stale column here is a rebuild away, not data loss.
--
-- Order matters: the triggers go before the columns, because they reference
-- `scene_search.tag_names` in their function bodies. Dropping the columns first
-- would leave functions referring to a column that no longer exists.

DROP TRIGGER IF EXISTS trg_scene_search_on_st_insert ON scene_tags;
DROP TRIGGER IF EXISTS trg_scene_search_on_st_delete ON scene_tags;
DROP TRIGGER IF EXISTS trg_scene_search_on_tag_rename ON tags;

DROP FUNCTION IF EXISTS trg_scene_tags_inserted();
DROP FUNCTION IF EXISTS trg_scene_tags_deleted();
DROP FUNCTION IF EXISTS trg_tag_renamed_scenes();

-- upsert_scene_search is REPLACED rather than dropped. Migration 61 created it
-- and every scene and studio trigger depends on it; dropping it would remove the
-- trigger functions' target as well. The replacement is 61's original body, so
-- after this migration the table is maintained exactly as it was before 103 --
-- which is the correct definition of a rollback, and re-applying 103 restores
-- the recall columns.
CREATE OR REPLACE FUNCTION upsert_scene_search(sid UUID) RETURNS VOID AS $$
BEGIN
    DELETE FROM scene_search WHERE scene_id = sid
        AND EXISTS (SELECT 1 FROM scenes WHERE id = sid AND deleted = true);

    INSERT INTO scene_search (scene_id, scene_title, scene_date, studio_name,
        network_name, studio_aliases, network_aliases, performer_names,
        scene_code)
    SELECT S.id, S.title, S.date::TEXT, T.name, TP.name,
           COALESCE(ARRAY_AGG(DISTINCT SA.alias) FILTER (WHERE SA.alias IS NOT NULL), '{}'),
           COALESCE(ARRAY_AGG(DISTINCT NA.alias) FILTER (WHERE NA.alias IS NOT NULL), '{}'),
           COALESCE(ARRAY_AGG(DISTINCT P.name) FILTER (WHERE P.name IS NOT NULL), '{}') ||
           COALESCE(ARRAY_AGG(DISTINCT PS."as") FILTER (WHERE PS."as" IS NOT NULL), '{}'),
           S.code
    FROM scenes S
    LEFT JOIN scene_performers PS ON PS.scene_id = S.id
    LEFT JOIN performers P ON PS.performer_id = P.id
    LEFT JOIN studios T ON T.id = S.studio_id
    LEFT JOIN studio_aliases SA ON SA.studio_id = T.id
    LEFT JOIN studios TP ON T.parent_studio_id = TP.id
    LEFT JOIN studio_aliases NA ON NA.studio_id = TP.id
    WHERE S.id = sid AND S.deleted = false
    GROUP BY S.id, T.name, TP.name
    ON CONFLICT (scene_id) DO UPDATE SET
        scene_title = EXCLUDED.scene_title, scene_date = EXCLUDED.scene_date,
        studio_name = EXCLUDED.studio_name, network_name = EXCLUDED.network_name,
        studio_aliases = EXCLUDED.studio_aliases, network_aliases = EXCLUDED.network_aliases,
        performer_names = EXCLUDED.performer_names, scene_code = EXCLUDED.scene_code;
END;
$$ LANGUAGE plpgsql;

-- The BM25 index is rebuilt without the three fields rather than dropped, so the
-- index that migration 56/61 established still exists afterwards. A DROP here
-- would leave scene_search with no index at all on a rollback, which is a worse
-- state than the one being rolled back from.
DROP INDEX IF EXISTS scene_search_bm25_idx;

CREATE INDEX scene_search_bm25_idx ON scene_search
USING bm25 (scene_id, scene_title, scene_date, studio_name, network_name,
            studio_aliases, network_aliases, performer_names, scene_code)
WITH (key_field='scene_id');

ALTER TABLE scene_search DROP COLUMN IF EXISTS tag_names;
ALTER TABLE scene_search DROP COLUMN IF EXISTS scene_director;
ALTER TABLE scene_search DROP COLUMN IF EXISTS scene_details;