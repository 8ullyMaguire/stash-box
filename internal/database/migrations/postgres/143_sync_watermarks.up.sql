-- Where an incremental sync remembers how far it has read (plan: docs/plan/feature-sync-service.md, step 1).
--
-- WHY A TABLE AND NOT A FILE. The watermark is derived state about a run, and the run writes records to this
-- database. A file beside the config would be a second thing to keep consistent across a crash -- and the
-- failure it produces is the quiet one: a mark that moved past records that were never written, so the next
-- run skips them forever. One database, one place where "the run finished this entity" is decided.
--
-- WHY THE KEY IS (entity, source) AND NOT JUST entity. Two sources would share one mark, so whichever ran
-- second would read the first one's position and skip everything older. `source` is the source's host, taken
-- from the configured endpoint rather than hard-coded, so this instance's own second source does not collide
-- with its first.
--
-- SEMANTICS, which the store's own comments repeat because they are the whole reason the table is useful:
-- a row exists only for an entity whose walk COMPLETED. No row means "never synced", which the runner reads as
-- a full walk. A row is written once per entity, after that entity's walk returns, and never mid-walk.
CREATE TABLE sync_watermarks (
    entity     text        NOT NULL,
    source     text        NOT NULL,
    watermark  timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (entity, source)
);

-- A comment on the table and on the two columns an operator will look at first: this is the kind of table
-- someone reads six months later through \d and has to interpret.
COMMENT ON TABLE sync_watermarks IS
    'High-water marks for the incremental sync from an external stash-box source (internal/sdbimport). One row per completed entity walk.';
COMMENT ON COLUMN sync_watermarks.entity IS
    'The entity whose walk completed: performers, studios, scenes, tags, sites.';
COMMENT ON COLUMN sync_watermarks.source IS
    'The source host the mark belongs to (e.g. stashdb.org). Part of the key so two sources cannot share a mark.';
COMMENT ON COLUMN sync_watermarks.watermark IS
    'The `updated` value of the oldest record the last completed walk read. The next walk re-reads a margin behind it on purpose.';
