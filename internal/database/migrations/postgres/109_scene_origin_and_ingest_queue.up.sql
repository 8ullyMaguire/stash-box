-- scene origin + the ingest queue -- the aggregator's intake (plan Phase 2, SPEC §7.1-7.3).
--
-- TWO THINGS, ONE THEME. Phase 1 gave a scene somewhere to record where its content
-- lives, but nothing could put content there that no member owns -- and no column said
-- which scenes those were. This migration adds the provenance fact (`scenes.origin`) and
-- the work queue that will populate it.
--
-- ---- why `origin` is a column and not a derived fact ------------------------------
--
-- "This scene came from aggregation" looks derivable: a scene with sources and no
-- fingerprints was not uploaded. It is not derivable, and the first counterexample is
-- the fork's own plan. Phase 2 fingerprints aggregated content deliberately, for the
-- dedup in stage 5 of the pipeline -- so after that lands, an aggregated scene HAS
-- fingerprints and the proxy inverts. The second counterexample is the ordinary one: a
-- member uploads a file and also links the tube site they found it on. That scene has
-- fingerprints AND sources, and it exists because a person made it. A filter phrased as
-- "no fingerprints" would call it aggregate; a filter phrased as "has sources" would too.
--
-- So origin records WHY THE ROW EXISTS, which is the question moderation and discovery
-- actually ask ("show me the content nobody here vouched for"), and it cannot go stale
-- when the fingerprint pipeline changes its mind.
--
-- ---- why the default is 'library', and why that is not the NOT-NULL-DEFAULT trap ----
--
-- Every existing row predates aggregation, so `library` is not a placeholder standing in
-- for "unset" -- it is the true value, and it stays true. (The trap that rule guards
-- against is a column where the default is indistinguishable from a deliberate setting a
-- reader is supposed to honour; nothing reads origin as a preference down a fallback
-- chain.) A row's origin is written at insert time and only down-migrated away.
--
-- ---- why the queue is a table and not a channel -----------------------------------
--
-- The pipeline is resumable, rate-limited per host, and its failures are read after the
-- fact by a person deciding whether to retry. A channel gives up all three. See
-- docs/spec/feature-ingestion.md §3 for the state machine; the constraints below are the
-- parts of it that the database can hold.

-- ---------------------------------------------------------------------------------
-- scenes.origin
-- ---------------------------------------------------------------------------------
ALTER TABLE "scenes" ADD COLUMN IF NOT EXISTS "origin" VARCHAR(20) NOT NULL DEFAULT 'library';

-- Postgres has no ADD CONSTRAINT IF EXISTS, and a re-run must not fail here, so the
-- guard is a catalogue probe. Filtered on conrelid as well as conname: a constraint name
-- is unique per TABLE, not per schema, so a name-only probe can match another table's
-- constraint and skip adding this one entirely.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'scenes_origin_check'
          AND conrelid = 'scenes'::regclass
    ) THEN
        ALTER TABLE "scenes" ADD CONSTRAINT "scenes_origin_check"
            CHECK ("origin" IN ('library', 'aggregate'));
    END IF;
END $$;

-- Discovery's hot path is "the newest aggregated content", and it is a partial index
-- rather than a full one because aggregated rows will be a minority of scenes for a long
-- time: a full index on (origin, created_at) is mostly entries the reader skips.
CREATE INDEX IF NOT EXISTS "scenes_origin_aggregate_idx"
    ON "scenes" ("created_at" DESC)
    WHERE "origin" = 'aggregate' AND "deleted" = false;

-- ---------------------------------------------------------------------------------
-- ingest_queue
-- ---------------------------------------------------------------------------------
--
-- No CREATE/ALTER pairing for existing tables here, unlike the columns above: this table
-- is created by this migration, so there is no earlier version of it for
-- `CREATE TABLE IF NOT EXISTS` to skip past.
CREATE TABLE IF NOT EXISTS "ingest_queue" (
    "id"             UUID NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
    -- The URL as submitted, kept verbatim: normalisation is a claim about what a URL
    -- MEANS, and when that claim is wrong the raw value is the only way to find out what
    -- the submitter actually sent.
    "url"            TEXT NOT NULL,
    -- The dedup key. Parsed once at insert, never recomputed at read: a normaliser whose
    -- behaviour changes must not silently re-partition the queue.
    "url_normalized" TEXT NOT NULL,
    -- WHERE THE WORK IS. `running` rows whose claim is stale are reclaimed by the sweep
    -- in phase 9, not by a timeout in the claim statement.
    "status"         VARCHAR(20) NOT NULL DEFAULT 'pending',
    -- WHAT THE PIPELINE DECIDED. Null until it decides. Kept separate from status because
    -- "how many of the last hundred ingestions were duplicates" is the number that says
    -- whether the dedup rules work, and it would otherwise be a string comparison across
    -- separate status values.
    "outcome"        VARCHAR(20),
    -- Incremented at CLAIM time, not at failure time: a worker killed mid-job is the case
    -- the counter exists for, and a counter that only moves on a clean failure never
    -- counts it.
    "attempts"       INTEGER NOT NULL DEFAULT 0,
    -- The extractor's own confidence, 0..1, as it reported it. Not a verdict -- the
    -- auto-approve decision reads it alongside the site's trust.
    "confidence"     REAL,
    "result_scene_id" UUID REFERENCES "scenes" ("id") ON DELETE SET NULL,
    -- Null means a crawler or the scheduler submitted it, which is the normal case for
    -- this table, and is why the column is nullable rather than pointing at a system user
    -- row invented by a migration.
    "submitted_by"   UUID REFERENCES "users" ("id") ON DELETE SET NULL,
    -- Why the pipeline refused it (denylist, login wall, unsupported kind). A refusal a
    -- human cannot read is a refusal they will re-queue.
    "reason"         TEXT,
    "last_error"     TEXT,
    -- Backoff as a column so a failing host is retried later without a retry loop
    -- occupying a worker.
    "next_attempt_at" TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    "claimed_at"     TIMESTAMPTZ,
    "started_at"     TIMESTAMPTZ,
    "finished_at"    TIMESTAMPTZ,
    "created_at"     TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT "ingest_queue_status_check"
        CHECK ("status" IN ('pending', 'running', 'done', 'failed', 'rejected')),
    CONSTRAINT "ingest_queue_outcome_check"
        CHECK ("outcome" IS NULL OR "outcome" IN ('created', 'attached', 'merged')),
    -- Status and outcome cannot disagree about whether the pipeline finished. A `done`
    -- row without an outcome is a row nothing can report on; an outcome on a failed or
    -- pending row is a decision attributed to a run that did not decide anything.
    CONSTRAINT "ingest_queue_done_has_outcome"
        CHECK (("status" = 'done') = ("outcome" IS NOT NULL))
);

-- The claim's own index: the queue is drained by `status='pending' AND next_attempt_at <=
-- now()` ordered by next_attempt_at, and without this it is a sequential scan of every
-- row ever ingested on every claim.
CREATE INDEX IF NOT EXISTS "ingest_queue_claimable_idx"
    ON "ingest_queue" ("next_attempt_at", "created_at")
    WHERE "status" = 'pending';

-- A URL that is queued, in flight, or successfully ingested cannot be queued again, and a
-- URL that FAILED PERMANENTLY can be. Both halves matter: without the partial predicate a
-- transient failure could never be retried, and without the uniqueness a popular URL is
-- ingested once per submission.
--
-- Verified behaviourally in scripts/verify-109.sh rather than by reading indexdef: the
-- predicate is rendered by the catalogue in a form a text probe does not match, and a
-- probe written from the shape I WROTE instead of the shape the catalogue RENDERS calls
-- a correct index missing.
CREATE UNIQUE INDEX IF NOT EXISTS "ingest_queue_active_url_idx"
    ON "ingest_queue" ("url_normalized")
    WHERE "status" IN ('pending', 'running', 'done');

COMMENT ON TABLE "ingest_queue" IS
    'Aggregation intake queue: one row per submitted URL, claimed with FOR UPDATE SKIP LOCKED. See docs/spec/feature-ingestion.md.';
