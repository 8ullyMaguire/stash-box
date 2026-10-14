-- The ontology version. NexusXXX 4.6 asks for the ontology to be a versioned, governed
-- document; the governance half is the edit system (a tag edit is the proposal, its comment
-- thread is the discussion, its votes are the vote, applying it is the implementation), and
-- this table is the version half.
--
-- WHY A TRIGGER RATHER THAN THE APPLIER. The Go applier could write a version row once per
-- applied edit, in the same transaction, and that would give one version per EDIT. This
-- writes one per change STATEMENT instead -- and covers every path that can change the
-- graph: an edit's applier, a merge's retarget, a future migration that seeds an initial
-- ontology, and a tag deletion's cascade. A version counter maintained in Go would silently
-- miss the paths that do not go through it, which is the bug class this project keeps
-- hitting. The cost, stated: a merge that retargets three implications writes three
-- versions, because a trigger cannot see where the surrounding transaction ends.
CREATE TABLE IF NOT EXISTS ontology_versions (
  version    int PRIMARY KEY,
  snapshot   jsonb NOT NULL DEFAULT '[]'::jsonb,
  note       text,
  created_at timestamp NOT NULL DEFAULT now(),

  CONSTRAINT ontology_versions_version_non_negative CHECK (version >= 0),
  -- The snapshot is a LIST of implication pairs. A JSON type check is cheap and it fails
  -- loudly here rather than in whichever client tries to read the document.
  CONSTRAINT ontology_versions_snapshot_is_array CHECK (jsonb_typeof(snapshot) = 'array')
);

COMMENT ON TABLE ontology_versions IS
  'One row per change to the tag ontology, written by ontology_version_trg in the same transaction as the change. snapshot is the full implication set by tag name at that version.';

COMMENT ON COLUMN ontology_versions.snapshot IS
  'The implication graph by tag NAME, ordered, e.g. [{"tag":"cowgirl_position","implies":"woman_on_top"}]. Names and not ids because the readers are people reviewing an ontology change and tags.name is unique. LIMIT: this is the implication graph only -- tag renames and category changes are not in it.';

-- Version 0 is the empty ontology, seeded BEFORE the trigger exists so that seeding it does
-- not itself bump a version. Without it "version 1" would be the first change and "no rows"
-- would also be a legal state, which makes a reader's "which ontology produced this?"
-- unanswerable rather than merely empty.
INSERT INTO ontology_versions (version, snapshot, note)
VALUES (0, '[]'::jsonb, 'the empty ontology')
ON CONFLICT (version) DO NOTHING;

CREATE OR REPLACE FUNCTION record_ontology_version()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  next_version int;
  snap         jsonb;
  head_name    text;
  tail_name    text;
  what         text;
BEGIN
  -- Serialised on purpose. Ontology changes are rare and a lost race here is a
  -- duplicate-key failure on ontology_versions_pkey, which reads as a broken feature
  -- rather than as a retry. The lock is cheap because nothing else locks this table.
  LOCK TABLE ontology_versions IN EXCLUSIVE MODE;

  SELECT COALESCE(MAX(version), 0) + 1 INTO next_version FROM ontology_versions;

  SELECT COALESCE(
           jsonb_agg(
             jsonb_build_object('tag', t.name, 'implies', i.name)
             ORDER BY t.name, i.name
           ),
           '[]'::jsonb
         )
    INTO snap
    FROM tag_implications ti
    JOIN tags t ON t.id = ti.tag_id
    JOIN tags i ON i.id = ti.implied_tag_id;

  IF TG_OP = 'DELETE' THEN
    what := 'remove';
    SELECT name INTO head_name FROM tags WHERE id = OLD.tag_id;
    SELECT name INTO tail_name FROM tags WHERE id = OLD.implied_tag_id;
  ELSE
    SELECT name INTO head_name FROM tags WHERE id = NEW.tag_id;
    SELECT name INTO tail_name FROM tags WHERE id = NEW.implied_tag_id;
    IF TG_OP = 'UPDATE' THEN
      what := 'retarget';
    ELSE
      what := 'add';
    END IF;
  END IF;

  INSERT INTO ontology_versions (version, snapshot, note)
  VALUES (
    next_version,
    snap,
    what || ' ' || COALESCE(head_name, '(deleted tag)') || ' -> ' ||
      COALESCE(tail_name, '(deleted tag)')
  );

  RETURN NULL;
END;
$$;

DROP TRIGGER IF EXISTS ontology_version_trg ON tag_implications;
CREATE TRIGGER ontology_version_trg
  AFTER INSERT OR UPDATE OR DELETE ON tag_implications
  FOR EACH ROW EXECUTE FUNCTION record_ontology_version();
