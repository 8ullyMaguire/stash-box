-- The takedown path, and the host-level instrument (plan phase 20; SPEC §7.23 D5's siblings).
--
-- THE GAP THIS CLOSES. Migration 108 gave `content_sources` a `status` and the health state
-- machine made `dmca` absorbing: the sweep cannot move a taken-down row back, the mirror does not
-- offer one, the archiver does not submit one, and the pipeline refuses to re-attach one. Every one
-- of those rules exists and every one of them is tested -- with a raw `UPDATE content_sources SET
-- status = 'dmca'` written inside the test itself. The instance could honour a takedown it had been
-- told about by hand and could not be TOLD. An operator handling a notice had exactly one instrument:
-- psql. That is the difference between a mechanism and a product, and it is the whole reason this
-- migration exists.
--
-- TWO KEYS, ONE TABLE, because a notice names whatever the complainant sent. A DMCA notice names
-- URLs; a CSAM report names a site. Both are the same kind of fact -- "nothing on this instance may
-- point at this" -- and both are consulted at the same moment, before anything is recorded. What
-- differs is only the key, so the key is a column and not a second table.
--
-- WHY A BLOCK AND NOT ONLY A STATUS. The status answers "is this recorded link still served?" and it
-- cannot answer "may this URL ever enter?" -- because the URL in a notice may not be recorded here
-- at all. Without a block, the sequence "notice about a URL we do not have, then a member submits
-- that URL, then the pipeline indexes it" ends with the instance serving the link it was told to
-- remove, and no code in the tree notices. The block is what makes the refusal precede the record.
--
-- A SECOND NOTICE IS NOT AN ERROR, and the schema has to be able to say so. The service reads a key
-- before it writes one: an existing block is reused and reported (`already_blocked` on the mutation),
-- the mark statement runs again regardless (it is idempotent), and the new notice is recorded in
-- `mod_audit` against the same block. The alternative -- letting the unique constraint reject the
-- fifth letter about a URL the instance already removed -- answers a legal workflow that worked with
-- a failure message, and an operator reads that as "not handled". The constraint stays as the
-- backstop against two takedowns racing, which is the case a read-then-write can lose.
--
-- RELEASING A BLOCK DELETES THE ROW. This table is the CURRENT policy, not a history: the history is
-- `mod_audit`, which records the block, the release and who did either, in the same transaction. Two
-- histories would eventually disagree, and the disagreement would be about whether a URL is blocked.
CREATE TABLE "content_source_block" (
    "id" UUID NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
    -- 'url' -- the value is a normalized URL, the same key `content_sources.url_normalized` holds
    --          and the same function (`ingest.NormalizeURL`) produces, so a notice's URL and an
    --          ingest job's URL are compared as one string and not as two texts that look alike.
    -- 'host' -- the value is a host: lowercase, no port, no scheme, no trailing dot. `sites.regex`
    --          is deliberately NOT the instrument here: a regex is how a block becomes a block that
    --          does not match, and matching is the entire job.
    "kind" VARCHAR(8) NOT NULL,
    "value" TEXT NOT NULL,
    -- Why, in the complainant's words where possible: the notice reference, the report id, the
    -- operator's own reason. This is the field an instance answers a question with.
    "reason" TEXT NOT NULL,
    -- Nullable: an instance migrating a denylist in, or a row whose author was later deleted, has no
    -- user to name. ON DELETE SET NULL rather than CASCADE, because losing the block because its
    -- author was deleted would silently unblock a host.
    "created_by_id" UUID REFERENCES "users" ("id") ON DELETE SET NULL,
    "created_at" TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT "content_source_block_kind" CHECK ("kind" IN ('url', 'host')),
    -- The lookup is always by (kind, value) and the pair must be unique, or a second block for the
    -- same key would be a row nobody reads and a release that removes only one of two.
    CONSTRAINT "content_source_block_key" UNIQUE ("kind", "value")
);

COMMENT ON TABLE "content_source_block" IS
    'URLs and hosts nothing on this instance may point at. Current policy; mod_audit is the history.';

-- The operator's own audit actions. One value per DECISION rather than per direction: an operator
-- reading the trail wants "was this URL blocked?" answered by a row, and the direction is in the
-- data. Releasing a block is recorded under the action that created it, with the release in `data`.
ALTER TYPE mod_audit_action ADD VALUE IF NOT EXISTS 'SOURCE_TAKEDOWN';
ALTER TYPE mod_audit_action ADD VALUE IF NOT EXISTS 'SOURCE_BLOCK';

-- NO STORED `host` COLUMN, and the reason is the opposite of migration 110's. There, the dedup key
-- was written on every ingest job and read on every ingest job, so it became a column. A host block
-- is an operator action taken a handful of times in an instance's life, and the pipeline's own
-- per-job host check compares the host it already parsed out of the URL against this table's value
-- directly -- nothing needs an index on the sources side to do that. What the mark-existing-rows
-- statement below does need is to find rows by a host that is in the URL and nowhere else, which is
-- one anchored, case-insensitive regex over `content_sources.url` on the rare occasion a host is
-- blocked. A backfill of 100% of rows to serve a statement that runs twice is the wrong trade, and a
-- backfill that mis-parses a malformed URL would silently produce a host block that misses rows.
--
-- The pattern the service builds is anchored at the authority component:
--     ^[a-z][a-z0-9+.-]*://([^/?#@]*@)?HOST(:[0-9]+)?([/?#].*)?$
-- with HOST passed through regexp.QuoteMeta, matched with `~*`. Anchoring matters: a bare
-- `LIKE '%host%'` would take down every source on `host.example.com` when `example.com` was blocked,
-- and every source on `notexample.com` too.
