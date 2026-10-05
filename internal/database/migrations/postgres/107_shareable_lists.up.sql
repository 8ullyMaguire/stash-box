-- Shareable lists (growth item 28).
--
-- THE POLICY, and it is decided rather than deferred: a list is PRIVATE until its owner
-- explicitly publishes it, and publishing is itself auditable. There is no implicit
-- publication and no way to discover a draft. That is the smallest policy that makes
-- user-generated public content safe without shipping a moderation queue, which is a
-- separate project and is not claimed here.
--
-- `published_at` NULL means private. NOT a separate boolean column: a boolean and a
-- timestamp can disagree (`published = false` with a `published_at` set), and that
-- disagreement is exactly the state in which a draft leaks. One nullable column cannot
-- be inconsistent with itself.
--
-- CHECK (published_at IS NULL OR published_by IS NOT NULL) is the referential half: a
-- publication with no actor cannot be audited, and an audit trail that can contain
-- unattributed entries is not one.

CREATE TABLE lists (
  id uuid NOT NULL DEFAULT gen_random_uuid(),
  -- The owner. ON DELETE CASCADE: a list is meaningless without an owner, and a row
  -- attributing content to a deleted user is worse than losing the content.
  owner_id uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  name varchar(255) NOT NULL,
  description text,

  -- NULL = private (a draft). Set = published and world-readable at READ.
  published_at timestamp with time zone,
  -- WHO published, which is not derivable from owner_id: a moderator may publish on
  -- someone's behalf, and "who pressed the button" is the auditable fact.
  published_by uuid REFERENCES users (id) ON DELETE SET NULL,

  -- Denormalised, because browsing published lists sorts by this on every page and a
  -- join to users for each row is the obvious way to make that slow. The owner_id above
  -- remains the authority; this is for the browse listing only.
  owner_name varchar(255) NOT NULL DEFAULT '',

  created_at timestamp with time zone NOT NULL DEFAULT now(),
  updated_at timestamp with time zone NOT NULL DEFAULT now(),

  PRIMARY KEY (id),
  CONSTRAINT lists_published_by_required
    CHECK (published_at IS NULL OR published_by IS NOT NULL),
  CONSTRAINT lists_owner_name_required CHECK (owner_name <> '')
);

CREATE UNIQUE INDEX lists_unique_name_per_owner
  ON lists (owner_id, name);

-- The browse path: published lists, newest first. A PARTIAL index, so drafts cost
-- nothing here and cannot be reached through this index at all -- which is the point of
-- making "not published" mean "not in the index", rather than relying on a WHERE clause
-- someone might forget.
CREATE INDEX lists_published_at_desc
  ON lists (published_at DESC)
  WHERE published_at IS NOT NULL;

CREATE INDEX lists_owner_id_idx ON lists (owner_id);

-- Membership. `position` is an explicit ordinal rather than relying on insert order or
-- an array column: a list is ordered, users reorder entries, and an array makes a reorder
-- a whole-row rewrite with no way to address one entry.
CREATE TABLE list_items (
  id uuid NOT NULL DEFAULT gen_random_uuid(),
  list_id uuid NOT NULL REFERENCES lists (id) ON DELETE CASCADE,
  -- Nullable because a list may hold any archive entity, and a NOT NULL here would mean
  -- a separate join table per entity type -- a schema decision no list feature should
  -- make on its users' behalf.
  entity_type varchar(50),
  entity_id uuid,
  position integer NOT NULL DEFAULT 0,
  created_at timestamp with time zone NOT NULL DEFAULT now(),

  PRIMARY KEY (id),

  CONSTRAINT list_items_entity_required
    CHECK (entity_type IS NOT NULL AND entity_id IS NOT NULL),
  -- The same entity cannot appear twice in one list. Without this, a client that
  -- double-submits silently doubles an entry and the list's length becomes a lie.
  --
  -- The key is (list_id, entity_id) and DELIBERATELY IGNORES entity_type. I first wrote
  -- (list_id, entity_type, entity_id), which permits one uuid to appear in a single list as
  -- both a SCENE and a PERFORMER -- and then a client resolving "item 3" has two candidates
  -- and no rule to choose between them. A list is a set of archive ENTITIES, so an
  -- ambiguous id inside one list is a bug waiting for a client.
  --
  -- The cost is that a list cannot hold a scene and a performer sharing a uuid. That cannot
  -- occur in a well-formed archive -- uuids are generated per table, so a scene id is never
  -- a performer id -- which makes the stricter key free in practice and correct by
  -- construction.
  CONSTRAINT list_items_unique_entity
    UNIQUE (list_id, entity_id)
);

CREATE INDEX list_items_list_id_position ON list_items (list_id, position);

-- The audit trail the policy promises. Separate from `lists` because a list's current
-- state is one row while its publication history is many, and overwriting one destroys
-- the other -- which is how "publishing is auditable" becomes "publishing happened, once,
-- with no record of who".
--
-- `action` is deliberately a text field rather than an enum: a moderation queue is the
-- follow-on and it will want actions this table has never heard of, and an enum here
-- would mean a migration for every new one.
CREATE TABLE list_audit (
  id uuid NOT NULL DEFAULT gen_random_uuid(),
  list_id uuid NOT NULL REFERENCES lists (id) ON DELETE CASCADE,
  -- SET NULL, not CASCADE: the audit trail must survive the deletion of the actor, or
  -- deleting a user erases the record of what they did, which is the one thing an audit
  -- log must not permit.
  actor_id uuid REFERENCES users (id) ON DELETE SET NULL,
  action varchar(50) NOT NULL,
  created_at timestamp with time zone NOT NULL DEFAULT now(),

  PRIMARY KEY (id)
);

CREATE INDEX list_audit_list_id_created ON list_audit (list_id, created_at DESC);