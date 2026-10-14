-- The tag graph. One row says: anything tagged `tag_id` is effectively also tagged
-- `implied_tag_id`.
--
-- DIRECTION. The relation points from the specific to the general -- NexusXXX 4.2:
-- "Tagging cowgirl_position automatically applies woman_on_top, vaginal_sex, sex". A
-- reader filtering by a general tag must find the specific things, so the search walks
-- this table BACKWARDS from the tag that was asked for.
--
-- ONE OWNING TABLE, NOTHING MATERIALISED. An implied tag is NOT written to `scene_tags`.
-- `scene_tags` has no source column, so a materialised implication is indistinguishable
-- from a person's tag: "was this applied by a human" becomes unanswerable, and the row
-- cannot be removed when the implication is, because nothing records which rows a rule
-- produced. Derived at read time, adding an implication applies to every existing item
-- with no backfill, and removing one takes effect immediately. See
-- docs/spec/feature-tag-ontology.md section 2 for the rejected alternative.
CREATE TABLE IF NOT EXISTS tag_implications (
  tag_id        uuid NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
  implied_tag_id uuid NOT NULL REFERENCES tags (id) ON DELETE CASCADE,
  created_at    timestamp NOT NULL DEFAULT now(),

  PRIMARY KEY (tag_id, implied_tag_id),

  -- A tag implying itself is either a mistake or a cycle of length one; both are refused
  -- here so the trigger below only has to reason about longer cycles.
  CONSTRAINT tag_implications_no_self CHECK (tag_id <> implied_tag_id)
);

-- The reverse direction is the one every search takes ("what implies this tag?"), so it
-- needs its own index: the primary key already covers "what does this tag imply".
CREATE INDEX IF NOT EXISTS tag_implications_implied_idx
  ON tag_implications (implied_tag_id);

COMMENT ON TABLE tag_implications IS
  'The tag DAG: tag_id implies implied_tag_id (specific -> general). Read through a recursive CTE, never materialised onto scene_tags. Cycles are refused by tag_implications_no_cycle_trg.';

-- The DAG guarantee, at the database.
--
-- The same recursive shape as tag_categories_no_cycle() (migration 106), deliberately not
-- a second invention: a service-level check protects only the paths that remember to call
-- it, and this graph is written by edits, merges, seeds and future migrations.
--
-- SEARCH DIRECTION: adding A -> B creates a cycle only if B can already reach A, so the
-- walk starts at the proposed TAIL and follows its implications outwards. (Walking the
-- head's ancestors would answer a different question and pass a cycle through.)
CREATE OR REPLACE FUNCTION tag_implications_no_cycle()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.tag_id = NEW.implied_tag_id THEN
    RAISE EXCEPTION 'tag % cannot imply itself', NEW.tag_id
      USING ERRCODE = 'check_violation';
  END IF;

  IF EXISTS (
    WITH RECURSIVE reachable AS (
      SELECT ti.implied_tag_id
        FROM tag_implications ti
       WHERE ti.tag_id = NEW.implied_tag_id
      UNION
      SELECT ti.implied_tag_id
        FROM tag_implications ti
        JOIN reachable r ON ti.tag_id = r.implied_tag_id
    )
    SELECT 1 FROM reachable WHERE implied_tag_id = NEW.tag_id
  ) THEN
    RAISE EXCEPTION 'tag implication % -> % would create a cycle in tag_implications',
      NEW.tag_id, NEW.implied_tag_id
      USING ERRCODE = 'check_violation';
  END IF;

  RETURN NEW;
END;
$$;

-- DROP IF EXISTS then CREATE, so re-applying this migration is safe: a bare CREATE TRIGGER
-- fails on the second run with "trigger already exists", which reads as a broken migration
-- rather than as a re-run.
DROP TRIGGER IF EXISTS tag_implications_no_cycle_trg ON tag_implications;
CREATE TRIGGER tag_implications_no_cycle_trg
  BEFORE INSERT OR UPDATE OF tag_id, implied_tag_id ON tag_implications
  FOR EACH ROW EXECUTE FUNCTION tag_implications_no_cycle();
