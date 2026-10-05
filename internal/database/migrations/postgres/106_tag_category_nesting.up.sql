ALTER TABLE tag_categories
  ADD COLUMN IF NOT EXISTS parent_id uuid REFERENCES tag_categories(id) ON DELETE SET NULL;

-- The index is load-bearing: every descendant and ancestor query walks this column, and
-- without it each step of the recursive CTE is a sequential scan.
CREATE INDEX IF NOT EXISTS tag_categories_parent_id_idx ON tag_categories(parent_id);

CREATE OR REPLACE FUNCTION tag_categories_no_cycle()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.parent_id IS NULL THEN
    RETURN NEW;
  END IF;

  IF NEW.parent_id = NEW.id THEN
    RAISE EXCEPTION 'tag category % cannot be its own parent', NEW.id
      USING ERRCODE = 'check_violation';
  END IF;

  IF EXISTS (
    WITH RECURSIVE ancestors AS (
      SELECT id, parent_id FROM tag_categories WHERE id = NEW.parent_id
      UNION ALL
      SELECT c.id, c.parent_id
        FROM tag_categories c
        JOIN ancestors a ON c.id = a.parent_id
    )
    SELECT 1 FROM ancestors WHERE id = NEW.id
  ) THEN
    RAISE EXCEPTION 'tag category % would create a cycle in tag_categories', NEW.id
      USING ERRCODE = 'check_violation';
  END IF;

  RETURN NEW;
END;
$$;

DO $$
BEGIN
    -- EVERYTHING THAT NAMES THE TABLE LIVES IN HERE, not because it is tidier but
    -- because `sqlc generate` replays this schema in LEXICOGRAPHIC order, where
    -- '106_tag_category_nesting' sorts BEFORE '10_tag_categories' -- so this file runs
    -- before the table exists and every statement naming it fails.
    --
    -- golang-migrate (the applier that actually matters) sorts numerically and never
    -- hits this. The alternative was renumbering the file, which would collide with an
    -- already-applied 106 on every deployed database. The guard costs nothing and keeps
    -- codegen working.
    IF to_regclass('public.tag_categories') IS NULL THEN
        RETURN;
    END IF;

    ALTER TABLE tag_categories
      ADD COLUMN IF NOT EXISTS parent_id uuid REFERENCES tag_categories(id) ON DELETE SET NULL;

    CREATE INDEX IF NOT EXISTS tag_categories_parent_id_idx ON tag_categories(parent_id);

    -- DROP IF EXISTS then CREATE, so re-applying this migration is safe. A bare CREATE
    -- TRIGGER fails on the second run with "trigger already exists", which reads as a
    -- broken migration rather than as a re-run.
    DROP TRIGGER IF EXISTS tag_categories_no_cycle_trg ON tag_categories;
    CREATE TRIGGER tag_categories_no_cycle_trg
      BEFORE INSERT OR UPDATE OF parent_id ON tag_categories
      FOR EACH ROW EXECUTE FUNCTION tag_categories_no_cycle();

    COMMENT ON COLUMN tag_categories.parent_id IS
      'Parent category, for nesting. NULL means top level. Deleting a parent promotes its children to top level rather than cascading, so reorganising vocabulary cannot silently destroy it.';
END
$$;

CREATE OR REPLACE FUNCTION tag_categories_no_cycle()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.parent_id IS NULL THEN
    RETURN NEW;
  END IF;

  IF NEW.parent_id = NEW.id THEN
    RAISE EXCEPTION 'tag category % cannot be its own parent', NEW.id
      USING ERRCODE = 'check_violation';
  END IF;

  IF EXISTS (
    WITH RECURSIVE ancestors AS (
      SELECT id, parent_id FROM tag_categories WHERE id = NEW.parent_id
      UNION ALL
      SELECT c.id, c.parent_id
        FROM tag_categories c
        JOIN ancestors a ON c.id = a.parent_id
    )
    SELECT 1 FROM ancestors WHERE id = NEW.id
  ) THEN
    RAISE EXCEPTION 'tag category % would create a cycle in tag_categories', NEW.id
      USING ERRCODE = 'check_violation';
  END IF;

  RETURN NEW;
END;
$$;

DO $$
BEGIN
    -- EVERYTHING THAT NAMES THE TABLE LIVES IN HERE, not because it is tidier but
    -- because `sqlc generate` replays this schema in LEXICOGRAPHIC order, where
    -- '106_tag_category_nesting' sorts BEFORE '10_tag_categories' -- so this file runs
    -- before the table exists and every statement naming it fails.
    --
    -- golang-migrate (the applier that actually matters) sorts numerically and never
    -- hits this. The alternative was renumbering the file, which would collide with an
    -- already-applied 106 on every deployed database. The guard costs nothing and keeps
    -- codegen working.
    IF to_regclass('public.tag_categories') IS NULL THEN
        RETURN;
    END IF;

    ALTER TABLE tag_categories
      ADD COLUMN IF NOT EXISTS parent_id uuid REFERENCES tag_categories(id) ON DELETE SET NULL;

    CREATE INDEX IF NOT EXISTS tag_categories_parent_id_idx ON tag_categories(parent_id);

    -- DROP IF EXISTS then CREATE, so re-applying this migration is safe. A bare CREATE
    -- TRIGGER fails on the second run with "trigger already exists", which reads as a
    -- broken migration rather than as a re-run.
    DROP TRIGGER IF EXISTS tag_categories_no_cycle_trg ON tag_categories;
    CREATE TRIGGER tag_categories_no_cycle_trg
      BEFORE INSERT OR UPDATE OF parent_id ON tag_categories
      FOR EACH ROW EXECUTE FUNCTION tag_categories_no_cycle();

    COMMENT ON COLUMN tag_categories.parent_id IS
      'Parent category, for nesting. NULL means top level. Deleting a parent promotes its children to top level rather than cascading, so reorganising vocabulary cannot silently destroy it.';
END
$$;

-- Refuse a cycle, and refuse self-parenting.
--
-- The check walks UPWARD from the proposed parent. If it reaches the row being written,
-- following parent links from the new parent would eventually arrive back at the row
-- itself -- which is the definition of a cycle.
--
-- The guard is on INSERT and UPDATE OF parent_id, and it compares ids so it also catches
-- a category being made its own parent. Without the id comparison, self-parenting is the
-- simplest possible cycle and would slip through a walk that starts from the parent.
--
-- Written as a function rather than inline in the constraint because a recursive CTE is
-- not allowed in a CHECK expression.
CREATE OR REPLACE FUNCTION tag_categories_no_cycle()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.parent_id IS NULL THEN
    RETURN NEW;
  END IF;

  IF NEW.parent_id = NEW.id THEN
    RAISE EXCEPTION 'tag category % cannot be its own parent', NEW.id
      USING ERRCODE = 'check_violation';
  END IF;

  IF EXISTS (
    WITH RECURSIVE ancestors AS (
      SELECT id, parent_id FROM tag_categories WHERE id = NEW.parent_id
      UNION ALL
      SELECT c.id, c.parent_id
        FROM tag_categories c
        JOIN ancestors a ON c.id = a.parent_id
    )
    SELECT 1 FROM ancestors WHERE id = NEW.id
  ) THEN
    RAISE EXCEPTION 'tag category % would create a cycle in tag_categories', NEW.id
      USING ERRCODE = 'check_violation';
  END IF;

  RETURN NEW;
END;
$$;

-- DROP IF EXISTS then CREATE, so re-applying this migration is safe. A bare CREATE
-- TRIGGER fails on the second run with "trigger already exists", which reads as a broken
-- migration rather than a re-run.
DROP TRIGGER IF EXISTS tag_categories_no_cycle_trg ON tag_categories;
CREATE TRIGGER tag_categories_no_cycle_trg
  BEFORE INSERT OR UPDATE OF parent_id ON tag_categories
  FOR EACH ROW EXECUTE FUNCTION tag_categories_no_cycle();

-- Comments, because `parent_id` on its own does not say which way the edge points.
COMMENT ON COLUMN tag_categories.parent_id IS
  'Parent category, for nesting. NULL means top level. Deleting a parent sets its children''s parent to NULL (promotes them) rather than cascading, so reorganising vocabulary cannot silently destroy it.';