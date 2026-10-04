-- SPEC §7.24.1 defect fix -- verified-unknown assertions outlived their entity.
--
-- THE DEFECT, reproduced before this migration was written. `field_verification_states`
-- stores a POLYMORPHIC (entity_type, entity_id) pair rather than four nullable FKs, so
-- PostgreSQL cannot cascade: there is no foreign key to hang ON DELETE CASCADE on. The
-- header of 96 claims
--
--   "ON DELETE CASCADE, unlike most of the edit machinery. An assertion about a deleted
--    entity is not a historical record anyone needs: ... keeping it would let a
--    re-created entity of the same name inherit confidence nobody gave it."
--
-- and the only cascade in 96 is on `asserted_by` (delete the AUTHOR, not the SUBJECT).
-- Measured on a real database with 96-100 applied:
--
--   insert performer P, insert assertion for P   -> assertion count 1
--   delete from performers where id = P          -> DELETE 1
--   assertion count                              -> 1     <-- the claim is false
--
-- The same gap exists in 76's `trust_events` and 79's identification board, so this is
-- the shape this schema uses for polymorphic references, not a mistake unique to 96.
--
-- WHY A TRIGGER AND NOT FOUR PARTIAL FKs. Four nullable FK columns (performer_id,
-- scene_id, ...) would cascade for free, but they permit a row claiming a performer AND
-- a scene simultaneously, which is not a fact about the world -- the exact objection 96's
-- header raises against a four-column shape. A CHECK enforcing "exactly one is non-null"
-- fixes that, at the cost of a migration per new entity type and a service that must
-- remember which column to populate. The trigger keeps the closed set in one place.
--
-- FAIL-LOUD on an unrecognised entity table, BEFORE deleting anything. If a sixth entity
-- type is added and this trigger is not updated, its assertions would never be cleaned up
-- -- a row that silently suppresses a completion gap forever, which is the one outcome
-- §7.24.1 exists to prevent. So the guard runs first and RAISEs, rather than skipping.

CREATE OR REPLACE FUNCTION "field_verification_states_cascade_entity"()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    -- Guard first: on an unknown table this raises and the entity delete fails loudly,
    -- instead of quietly leaving assertions behind.
    IF TG_TABLE_NAME NOT IN ('performers', 'scenes', 'studios', 'sites', 'tags') THEN
        RAISE EXCEPTION
            'field_verification_states_cascade_entity: unknown entity table % -- assertions '
            'of this entity_type would never be cleaned up. Add the table to this trigger.',
            TG_TABLE_NAME
            USING ERRCODE = 'raise_exception';
    END IF;

    -- TG_TABLE_NAME is the plural table; entity_type is the singular name the service
    -- writes ('performer', not 'performers'). Derive it rather than maintaining a second
    -- mapping that can drift from the trigger list above.
    DELETE FROM "field_verification_states"
     WHERE entity_type = left(TG_TABLE_NAME, length(TG_TABLE_NAME) - 1)
       AND entity_id = OLD.id;

    RETURN NULL;   -- AFTER trigger: return value is ignored
END;
$$;

COMMENT ON FUNCTION "field_verification_states_cascade_entity"() IS
'Removes verified-unknown assertions when the entity they describe is deleted. Exists '
'because entity_id is polymorphic, so no foreign key can carry ON DELETE CASCADE.';

-- AFTER, not BEFORE: the assertion rows are gone by the time the entity delete returns,
-- so a caller reading the assertion count immediately after the DELETE sees post-delete
-- truth, and a savepoint rollback of an unrelated later failure restores both together.
--
-- DROP IF EXISTS so re-running this file is a no-op rather than an error. golang-migrate
-- applies each file once, but a hand-run `psql -f` during debugging is routine, and a
-- migration that errors on its second application is one nobody re-runs to check a fix.
DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['performers', 'scenes', 'studios', 'sites', 'tags'] LOOP
        EXECUTE format('DROP TRIGGER IF EXISTS %I ON %I',
                       'field_verification_states_cascade_' || t, t);
        EXECUTE format('DROP TRIGGER IF EXISTS %I ON %I',
                       'field_verification_states_cascade_truncate_' || t, t);
    END LOOP;
END;
$$;

CREATE TRIGGER "field_verification_states_cascade_performers"
AFTER DELETE ON "performers"
FOR EACH ROW EXECUTE FUNCTION "field_verification_states_cascade_entity"();

CREATE TRIGGER "field_verification_states_cascade_scenes"
AFTER DELETE ON "scenes"
FOR EACH ROW EXECUTE FUNCTION "field_verification_states_cascade_entity"();

CREATE TRIGGER "field_verification_states_cascade_studios"
AFTER DELETE ON "studios"
FOR EACH ROW EXECUTE FUNCTION "field_verification_states_cascade_entity"();

CREATE TRIGGER "field_verification_states_cascade_sites"
AFTER DELETE ON "sites"
FOR EACH ROW EXECUTE FUNCTION "field_verification_states_cascade_entity"();

CREATE TRIGGER "field_verification_states_cascade_tags"
AFTER DELETE ON "tags"
FOR EACH ROW EXECUTE FUNCTION "field_verification_states_cascade_entity"();

-- TRUNCATE fires NO row-level trigger. The integration suite truncates between packages;
-- without this the assertions survive and reference entities that no longer exist.
CREATE OR REPLACE FUNCTION "field_verification_states_cascade_truncate"()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    TRUNCATE "field_verification_states";
    RETURN NULL;
END;
$$;

CREATE TRIGGER "field_verification_states_cascade_truncate_performers"
AFTER TRUNCATE ON "performers"
FOR EACH STATEMENT EXECUTE FUNCTION "field_verification_states_cascade_truncate"();

CREATE TRIGGER "field_verification_states_cascade_truncate_scenes"
AFTER TRUNCATE ON "scenes"
FOR EACH STATEMENT EXECUTE FUNCTION "field_verification_states_cascade_truncate"();

CREATE TRIGGER "field_verification_states_cascade_truncate_studios"
AFTER TRUNCATE ON "studios"
FOR EACH STATEMENT EXECUTE FUNCTION "field_verification_states_cascade_truncate"();

CREATE TRIGGER "field_verification_states_cascade_truncate_sites"
AFTER TRUNCATE ON "sites"
FOR EACH STATEMENT EXECUTE FUNCTION "field_verification_states_cascade_truncate"();

CREATE TRIGGER "field_verification_states_cascade_truncate_tags"
AFTER TRUNCATE ON "tags"
FOR EACH STATEMENT EXECUTE FUNCTION "field_verification_states_cascade_truncate"();

-- CONVERGE A DATABASE THAT HAS ALREADY BEEN RUNNING WITH THE BROKEN BEHAVIOUR.
-- An assertion whose entity is gone suppresses a completion gap for an entity that does
-- not exist; on a re-created entity of the same name it would apply confidence nobody gave
-- it. The condition is (type is known) AND (that type's entity is missing) -- written as a
-- CASE so each known type is deleted only when ITS OWN table lacks the row. An earlier
-- draft chained five AND-ed NOT EXISTS and then AND-ed `entity_type NOT IN (...)`, which
-- deleted precisely nothing: for a known type the final term was false.
DELETE FROM "field_verification_states" s
 WHERE CASE s.entity_type
          WHEN 'performer' THEN NOT EXISTS (SELECT 1 FROM "performers" p WHERE p.id = s.entity_id)
          WHEN 'scene'     THEN NOT EXISTS (SELECT 1 FROM "scenes"     e WHERE e.id = s.entity_id)
          WHEN 'studio'    THEN NOT EXISTS (SELECT 1 FROM "studios"    e WHERE e.id = s.entity_id)
          WHEN 'site'      THEN NOT EXISTS (SELECT 1 FROM "sites"      e WHERE e.id = s.entity_id)
          WHEN 'tag'       THEN NOT EXISTS (SELECT 1 FROM "tags"       e WHERE e.id = s.entity_id)
          -- An unknown type cannot be checked against any table, so it is left alone
          -- rather than deleted on a guess. The service is the only writer and its
          -- vocabulary is closed (internal/service/completion.AllEntityTypes).
          ELSE FALSE
        END;