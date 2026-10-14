-- content_sources.url_normalized -- the pipeline's dedup key, stored (plan Phase 2, spec §1).
--
-- WHY A COLUMN. The queue dedups on the Go normaliser's output (strip tracking
-- parameters, lowercase the host, sort the query, drop the fragment). Stage 3 of the
-- pipeline asks the same question of the ROWS -- "does anything already claim this URL?" --
-- and without the normalised form stored, the two answers disagree: a URL submitted with
-- `?utm_source=nl` matches nothing in content_sources, so the pipeline creates a SECOND
-- scene for a page it already has. The normaliser's whole reason for existing is that this
-- does not happen, and a lookup that cannot use it makes it decorative.
--
-- WHY NULLABLE, AND WHY NULL IS NOT A BUG. A row written by the ingest path carries the
-- normalised key. A row written by the scene_urls mirror trigger CANNOT: the trigger is
-- SQL, and normalisation is Go (it sorts parameters, drops a named set of them, and
-- refuses non-http schemes -- all of which would have to be reimplemented against
-- Postgres's regex dialect, and a second implementation that disagrees with the first is
-- worse than an absent one). So NULL means "written by the edit path, or by a migration
-- that predates this column", and the lookup matches those rows on LOWER(url) instead.
--
-- The lookup therefore has two arms and both are indexed:
--   url_normalized = $1                     -- ingest-written rows, exact key
--   LOWER(url) = LOWER($1)                  -- edit-written rows, via content_sources_url_lower_idx
-- The second arm cannot see through a tracking parameter, which is a known and bounded
-- cost: a re-ingested edit-written URL with different tracking parameters adds a second
-- SOURCE row to one scene, not a second scene, because scene_urls/content_sources
-- uniqueness is per (entity, url) and the scene itself is found by fingerprint.
ALTER TABLE "content_sources" ADD COLUMN IF NOT EXISTS "url_normalized" TEXT;

-- Partial, because the edit-written rows have no key to index and will be the majority
-- on any instance that predates the aggregator.
CREATE INDEX IF NOT EXISTS "content_sources_url_normalized_idx"
    ON "content_sources" ("url_normalized")
    WHERE "url_normalized" IS NOT NULL;

COMMENT ON COLUMN "content_sources"."url_normalized" IS
    'The Go normaliser''s key for this URL, written only by the ingest path. NULL means the row came from the edit path (scene_urls mirror) or predates this column; match those on LOWER(url).';

-- ---------------------------------------------------------------------------------
-- ingest_queue.target_entity_id
-- ---------------------------------------------------------------------------------
--
-- WHY THE QUEUE NEEDS TO KNOW. A scene page's "add a source" form submits a URL for THE
-- SCENE THE READER IS LOOKING AT. Without somewhere to record that, the pipeline has to
-- infer the destination from the URL alone -- and an inference is exactly what the
-- submitter just overrode by naming a scene. The row carries the instruction; the
-- pipeline attaches there.
--
-- What it does NOT do is cause a fetch. A named target is attached from what the database
-- already knows -- the scene exists, the source is a URL -- and the pipeline only reads the
-- page on the path that might have to CREATE a record. The first cut of this column's
-- documentation claimed the opposite ("a named target still gets its URL fetched, because
-- the source record wants a health status"), and implementing the takedown path is what
-- showed it was wrong: fetching on every attach means re-requesting URLs this instance
-- already has, including ones a human has taken down. Health is the checker's job
-- (phase 9); the queue row records a decision, not a probe.
--
-- CASCADE, not SET NULL, and the difference is the point of the column: a target that no
-- longer exists makes the queue row meaningless rather than merely anonymous. SET NULL
-- would quietly turn "attach this to scene X" into "attach this wherever it seems to
-- belong", which is a silent change of instruction.
ALTER TABLE "ingest_queue"
    ADD COLUMN IF NOT EXISTS "target_entity_id" UUID REFERENCES "scenes" ("id") ON DELETE CASCADE;

COMMENT ON COLUMN "ingest_queue"."target_entity_id" IS
    'The scene a submission named, when a reader submitted the URL from that scene''s page. NULL means the submitter named no target and the pipeline decides from the URL and its fingerprint.';

