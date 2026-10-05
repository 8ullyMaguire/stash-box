-- SPEC §7.25.1: make scene.details, scene.director and scene TAGS searchable.
--
-- scene_search has been rebuilt three times -- migration 35 (tsvector), 56
-- (ParadeDB BM25) and 61 (the alias arrays) -- and none of them added these
-- three fields. They are where broad-stroke recall lives: `details` is free
-- text ("hotel room, rainy night"), `director` is a half-remembered-name cue,
-- and a tag name is the single most common thing a person remembers about a
-- scene they cannot place.
--
-- This migration is ADDITIVE, and that is a deliberate departure from the
-- migrations it follows. 35 and 56 both DROP and re-CREATE scene_search. Doing
-- that a third time drops every index and rewrites every row to achieve what
-- three columns and three index fields accomplish, and on a populated instance
-- it is an unbounded rebuild inside a migration.
--
-- The tag triggers are the load-bearing part, and they are new rather than
-- amended. scene_tags has carried NO trigger since 01_initial: scenes, studios,
-- performers and scene_performers all fire into upsert_scene_search or
-- upsert_tag_search, and scene_tags fires nothing at all. So a tag_names column
-- without these triggers would be correct only for scenes that happened to be
-- written after the fact -- an index that looks populated and is wrong, which is
-- worse than no column because it cannot be noticed.

-- ---------------------------------------------------------------------------
-- Columns
-- ---------------------------------------------------------------------------

ALTER TABLE scene_search
    ADD COLUMN IF NOT EXISTS scene_details TEXT,
    ADD COLUMN IF NOT EXISTS scene_director TEXT,
    ADD COLUMN IF NOT EXISTS tag_names TEXT[];

-- ---------------------------------------------------------------------------
-- Backfill
--
-- The rows that exist now were built by migration 61's upsert, which had no
-- these three fields. Rebuilding from the base tables is a one-off; after this,
-- upsert_scene_search below keeps them current.
--
-- The tag aggregate is a correlated subquery rather than another LEFT JOIN on a
-- GROUP BY, because the surrounding statement already groups and adding a
-- second many-to-many join to it multiplies the performer array. The
-- correlated form is the one that cannot do that.
-- ---------------------------------------------------------------------------

UPDATE scene_search ss
SET scene_details = S.details,
    scene_director = S.director,
    tag_names = COALESCE(tg.agg, '{}')
FROM scenes S
LEFT JOIN (
    SELECT ST.scene_id, ARRAY_AGG(DISTINCT T.name) AS agg
    FROM scene_tags ST
    JOIN tags T ON T.id = ST.tag_id
    GROUP BY ST.scene_id
) tg ON tg.scene_id = S.id
WHERE ss.scene_id = S.id;

-- ---------------------------------------------------------------------------
-- BM25 index
--
-- Dropped and recreated because ParadeDB requires every indexed column to be
-- named in the USING bm25 list -- there is no way to add a field to an existing
-- index. tag_names joins performer_names in text_fields with fieldnorms off and
-- record "basic", the same treatment the alias arrays get: these are
-- keyword-style identifier lists where a term appearing in many rows should not
-- be discounted by field length.
-- ---------------------------------------------------------------------------

DROP INDEX IF EXISTS scene_search_bm25_idx;

CREATE INDEX scene_search_bm25_idx ON scene_search
USING bm25 (
    scene_id,
    scene_title,
    scene_date,
    studio_name,
    network_name,
    studio_aliases,
    network_aliases,
    performer_names,
    scene_code,
    scene_details,
    scene_director,
    tag_names
)
WITH (
    key_field='scene_id',
    text_fields='{
        "performer_names": {"fieldnorms": false, "record": "basic"},
        "studio_aliases": {"fieldnorms": false, "record": "basic"},
        "network_aliases": {"fieldnorms": false, "record": "basic"},
        "tag_names": {"fieldnorms": false, "record": "basic"}
    }'
);

-- ---------------------------------------------------------------------------
-- upsert_scene_search
--
-- This is the step that is easiest to omit and that makes the whole feature a
-- silent no-op if it is: without the three new columns in the INSERT and in the
-- ON CONFLICT DO UPDATE, the next scene write blanks every row's details,
-- director and tags -- so the backfill above would be correct for a few minutes
-- and the feature would appear to work in a manual test and be dead in
-- production.
--
-- The tag aggregate is folded into the existing GROUP BY rather than joined as
-- a second grouped subquery, for the same reason the backfill used a correlated
-- form: two many-to-many joins in one grouped statement cross-multiply.
-- ---------------------------------------------------------------------------

CREATE OR REPLACE FUNCTION upsert_scene_search(sid UUID) RETURNS VOID AS $$
BEGIN
    DELETE FROM scene_search WHERE scene_id = sid
        AND EXISTS (SELECT 1 FROM scenes WHERE id = sid AND deleted = true);

    INSERT INTO scene_search (scene_id, scene_title, scene_date, studio_name,
        network_name, studio_aliases, network_aliases, performer_names,
        scene_code, scene_details, scene_director, tag_names)
    SELECT S.id, S.title, S.date::TEXT, T.name, TP.name,
           COALESCE(ARRAY_AGG(DISTINCT SA.alias) FILTER (WHERE SA.alias IS NOT NULL), '{}'),
           COALESCE(ARRAY_AGG(DISTINCT NA.alias) FILTER (WHERE NA.alias IS NOT NULL), '{}'),
           COALESCE(ARRAY_AGG(DISTINCT P.name) FILTER (WHERE P.name IS NOT NULL), '{}') ||
           COALESCE(ARRAY_AGG(DISTINCT PS."as") FILTER (WHERE PS."as" IS NOT NULL), '{}'),
           S.code,
           S.details,
           S.director,
           COALESCE(ARRAY_AGG(DISTINCT TG.name) FILTER (WHERE TG.name IS NOT NULL), '{}')
    FROM scenes S
    LEFT JOIN scene_performers PS ON PS.scene_id = S.id
    LEFT JOIN performers P ON PS.performer_id = P.id
    LEFT JOIN studios T ON T.id = S.studio_id
    LEFT JOIN studio_aliases SA ON SA.studio_id = T.id
    LEFT JOIN studios TP ON T.parent_studio_id = TP.id
    LEFT JOIN studio_aliases NA ON NA.studio_id = TP.id
    LEFT JOIN scene_tags ST ON ST.scene_id = S.id
    LEFT JOIN tags TG ON TG.id = ST.tag_id
    WHERE S.id = sid AND S.deleted = false
    GROUP BY S.id, T.name, TP.name
    ON CONFLICT (scene_id) DO UPDATE SET
        scene_title = EXCLUDED.scene_title, scene_date = EXCLUDED.scene_date,
        studio_name = EXCLUDED.studio_name, network_name = EXCLUDED.network_name,
        studio_aliases = EXCLUDED.studio_aliases, network_aliases = EXCLUDED.network_aliases,
        performer_names = EXCLUDED.performer_names, scene_code = EXCLUDED.scene_code,
        scene_details = EXCLUDED.scene_details,
        scene_director = EXCLUDED.scene_director,
        tag_names = EXCLUDED.tag_names;
END;
$$ LANGUAGE plpgsql;

-- ---------------------------------------------------------------------------
-- Triggers on scene_tags
--
-- STATEMENT-level, matching the shape already proven for scene_performers at
-- 56_paradedb_search.up.sql:130-146. A row-level trigger here would call
-- upsert_scene_search once per tag per scene, so a bulk tag import -- the way
-- tags actually arrive, via a COPY -- would rewrite the same scene_search row
-- thousands of times inside one statement. The statement-level form is one
-- rewrite regardless of batch size.
-- ---------------------------------------------------------------------------

CREATE OR REPLACE FUNCTION trg_scene_tags_inserted() RETURNS TRIGGER AS $$
BEGIN
    PERFORM upsert_scene_search(scene_id)
    FROM (SELECT DISTINCT scene_id FROM new_rows) affected;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

-- Postgres has no CREATE TRIGGER IF NOT EXISTS, and the repo already guards
-- this way where it matters (61:83-84, 102:76-79). Unguarded, re-running this
-- migration by hand dies with `trigger ... already exists` halfway through --
-- which is how I lost the mutation-3 restore the first time, and it left the
-- scratch database carrying a deliberately broken upsert function with a
-- migration that reported success.
DROP TRIGGER IF EXISTS trg_scene_search_on_st_insert ON scene_tags;

CREATE TRIGGER trg_scene_search_on_st_insert
AFTER INSERT ON scene_tags
REFERENCING NEW TABLE AS new_rows
FOR EACH STATEMENT EXECUTE FUNCTION trg_scene_tags_inserted();

CREATE OR REPLACE FUNCTION trg_scene_tags_deleted() RETURNS TRIGGER AS $$
BEGIN
    PERFORM upsert_scene_search(scene_id)
    FROM (SELECT DISTINCT scene_id FROM old_rows) affected;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_scene_search_on_st_delete ON scene_tags;

CREATE TRIGGER trg_scene_search_on_st_delete
AFTER DELETE ON scene_tags
REFERENCING OLD TABLE AS old_rows
FOR EACH STATEMENT EXECUTE FUNCTION trg_scene_tags_deleted();

-- ---------------------------------------------------------------------------
-- A tag's own rename must reach every scene carrying it
--
-- The third trigger, and the one that closes the loop. tags already has
-- trg_tag_search_on_tag firing into upsert_tag_search, so renaming a tag
-- updates the TAG's search row and leaves every scene carrying it holding the
-- old name in tag_names -- a search for the new spelling would miss every scene
-- that has it, which is the exact recall failure this migration exists to fix,
-- introduced by the fix.
--
-- AFTER UPDATE with NO column list, and that is forced rather than chosen.
-- PostgreSQL refuses a transition table on a trigger with a column list:
-- "transition tables cannot be specified for triggers with column lists". So
-- `AFTER UPDATE OF name ... REFERENCING NEW TABLE` does not compile, and the
-- first draft of this migration was exactly that.
--
-- The corollary is that NEW is not usable either -- in a statement-level trigger
-- NEW does not refer to the updated rows, which is why the ids come from the
-- transition table. `SELECT DISTINCT id FROM new_rows` is the same shape
-- trg_studio_aliases_inserted_scenes uses at 61:86-101, so it is a form already
-- proven in this schema rather than a new one.
--
-- The cost of dropping the column list is that this also fires when a tag's
-- description changes and nothing in tag_names moves. Recomputing it anyway is
-- correct, tag updates are not a hot path, and the alternative -- a row-level
-- trigger with a WHEN clause -- trades a cheap redundant rewrite for a
-- per-row upsert on bulk tag edits.
-- ---------------------------------------------------------------------------

CREATE OR REPLACE FUNCTION trg_tag_renamed_scenes() RETURNS TRIGGER AS $$
BEGIN
    PERFORM upsert_scene_search(ST.scene_id)
    FROM scene_tags ST
    JOIN (SELECT DISTINCT id FROM new_rows) renamed ON renamed.id = ST.tag_id;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_scene_search_on_tag_rename ON tags;

CREATE TRIGGER trg_scene_search_on_tag_rename
AFTER UPDATE ON tags
REFERENCING NEW TABLE AS new_rows
FOR EACH STATEMENT EXECUTE FUNCTION trg_tag_renamed_scenes();