-- content_sources -- the aggregator's first brick (SPEC §7.27, plan Phase 1).
--
-- WHAT THIS IS FOR. Until now every URL in this database records where a MEMBER'S
-- OWN FILE came from: `scene_urls (scene_id, url, site_id)`, written by the edit
-- path when a contributor submits a scene. There is no record of content this
-- instance knows about and nobody here owns, which is the entire premise of the
-- aggregator programme (NexusXXX §3.2 `sources[]`). This table is that record.
--
-- WHY A NEW TABLE RATHER THAN COLUMNS ON scene_urls. Two reasons, and the second
-- is the deciding one:
--
--   1. `scene_urls` is an UPSTREAM table. This fork's §0 scope policy is that
--      upstream's tables stay upstream-shaped so that every future upstream merge
--      is a merge and not a conflict resolution. Adding `status`, `last_checked`,
--      `archived_url` and four more columns to it would put this fork in a
--      permanent conflict on a table 30 code paths read.
--   2. Health is a property of a (thing, url) PAIR for things that are not scenes.
--      Phase 3 adds five non-video content kinds, and their URLs have no
--      `scene_urls` row to hang off. A scene-only table would be re-derived as a
--      polymorphic one later, which is the second migration nobody remembers.
--
-- THE SINGLE-WRITER RULE, and the one thing to get right here. URL membership
-- keeps being written to `scene_urls` by the existing edit path -- one statement
-- of truth, one trigger, one direction:
--
--     scene_urls  --(trigger)-->  content_sources
--
-- A second trigger in the reverse direction was considered and rejected. Two
-- mirrors converge only if both are idempotent, and the usual guard for that,
-- `pg_trigger_depth() > 1`, does not prevent the disagreement -- it hides it. A
-- direction that is only ever one way has no such failure mode.
--
-- The consequence, stated so nobody has to discover it: for scene-kind rows the
-- write path is still `scene_urls`. `content_sources` owns the health columns and
-- nothing else owns them. Phase 1's Go half reads the new table; Phase 3's
-- non-scene kinds write it directly, because they have no legacy table to mirror.

CREATE TABLE "content_sources" (
    "id" UUID NOT NULL PRIMARY KEY DEFAULT gen_random_uuid(),
    -- 'scene' today; 'content_item' arrives with Phase 3. An enum type would need
    -- ALTER TYPE on every future kind and the repo's entity_type columns
    -- (reviews, completion, favorites, lists, expected_totals) are all VARCHAR,
    -- so this matches the shape that already exists rather than inventing a
    -- second one.
    "entity_type" VARCHAR(20) NOT NULL,
    "entity_id" UUID NOT NULL,
    -- text, not varchar(n). A URL longer than 255 characters is normal on the
    -- sites this programme indexes, and scene_urls' own column is unbounded
    -- varchar for the same reason.
    "url" TEXT NOT NULL,
    -- Nullable: an aggregated source can be submitted before its site exists in
    -- the directory, and requiring a site would make the directory a
    -- precondition for indexing anything.
    "site_id" UUID REFERENCES "sites" ("id") ON DELETE SET NULL,
    -- The platform slug from the draft's §3.2 enum, kept free-form because the
    -- list of sites is longer than any enum written today and a CHECK here would
    -- reject the next tube site as invalid data.
    "platform" VARCHAR(50),
    -- 'unknown', NOT 'live', as the default: nothing has checked this URL yet,
    -- and a row claiming 'live' would be asserting something no measurement
    -- supports. The first health sweep is what moves it.
    "status" VARCHAR(20) NOT NULL DEFAULT 'unknown',
    "canonical" BOOLEAN NOT NULL DEFAULT FALSE,
    "first_seen" TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- Null means "never checked", which is different from "checked and failed" --
    -- the same distinction migration 96 had to introduce for metadata fields, and
    -- for the same reason: an absent value must not read as an answer.
    "last_checked" TIMESTAMPTZ,
    "last_ok" TIMESTAMPTZ,
    "check_failures" INTEGER NOT NULL DEFAULT 0,
    "archived_url" TEXT,
    "archived_at" TIMESTAMPTZ,
    -- Who submitted it, when that is a person. Null for rows that arrived through
    -- the scene_urls trigger, which has no actor to attribute.
    "created_by" UUID REFERENCES "users" ("id") ON DELETE SET NULL,

    -- The status machine from §7.3, as a constraint rather than a convention:
    --   live -> intermittent (3+ consecutive failures) -> dead (7+ days unreachable)
    --   dead -> resurrected is NOT a status; a resurrected source is 'live' again
    --           with its failure count reset, because the row's identity is the URL
    --   any -> dmca, which is terminal as far as serving is concerned
    CONSTRAINT "content_sources_status_check" CHECK (
        "status" IN ('unknown', 'live', 'intermittent', 'dead', 'dmca', 'archived')
    ),
    CONSTRAINT "content_sources_entity_type_check" CHECK (
        "entity_type" IN ('scene', 'content_item')
    ),
    CONSTRAINT "content_sources_failures_nonneg" CHECK ("check_failures" >= 0),
    -- Partial archives are not a state: an archived URL without a timestamp
    -- cannot be aged, and a timestamp without a URL cannot be fetched.
    CONSTRAINT "content_sources_archive_pair" CHECK (
        ("archived_url" IS NULL) = ("archived_at" IS NULL)
    ),
    -- One row per (thing, url). Two rows for one URL on one entity would make the
    -- health sweep report two answers for one question.
    CONSTRAINT "content_sources_unique" UNIQUE ("entity_type", "entity_id", "url")
);

-- At most one canonical source per entity. Enforced here rather than in a service
-- because "the canonical source changed" is a claim about a set, and a service
-- that forgets to clear the old one leaves two rows both asserting it.
CREATE UNIQUE INDEX "content_sources_one_canonical"
    ON "content_sources" ("entity_type", "entity_id")
    WHERE "canonical";

-- The health sweep's query: everything not checked since T, worst first. A partial
-- index on the rows the sweep can act on, so the dead and dmca rows -- which are
-- terminal and are the majority over time -- do not sit in the index at all.
CREATE INDEX "content_sources_sweep_idx"
    ON "content_sources" ("last_checked")
    WHERE "status" IN ('unknown', 'live', 'intermittent');

-- URL dedup: "have we seen this URL before, on anything?" is the first question the
-- ingestion pipeline asks (plan Phase 2). Lowered, because the same URL arrives
-- with different casing from different submitters.
CREATE INDEX "content_sources_url_lower_idx" ON "content_sources" (LOWER("url"));

-- Entity lookups, which is how a scene page loads its sources.
CREATE INDEX "content_sources_entity_idx" ON "content_sources" ("entity_type", "entity_id");

-- ---------------------------------------------------------------------------
-- Backfill
--
-- Existing scene URLs become sources with NO health claim: status stays 'unknown'
-- because no check has ever run against them, and inventing 'live' here would make
-- the first sweep a correction of this migration rather than a measurement. No
-- reader reads `status` yet, so nothing regresses by leaving it unknown.
--
-- `canonical` is left FALSE for every backfilled row. Electing a canonical source
-- is a decision (highest quality, best metadata); defaulting to the lowest id
-- would be this migration making that decision badly.
-- ---------------------------------------------------------------------------
INSERT INTO "content_sources" ("entity_type", "entity_id", "url", "site_id", "first_seen")
SELECT 'scene', su."scene_id", su."url", su."site_id", NOW()
FROM "scene_urls" su
ON CONFLICT ("entity_type", "entity_id", "url") DO NOTHING;

-- ---------------------------------------------------------------------------
-- The one-way mirror
--
-- Statement-level with a transition table, not row-level: the performer and
-- scene_tags paths in this schema already prove the shape (migrations 61, 67, 103),
-- and a scene's URLs can arrive in a bulk COPY for many scenes at once, where a
-- row trigger is one upsert per row per scene.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION content_sources_from_scene_urls() RETURNS TRIGGER AS $$
BEGIN
    -- The UPDATE branch deliberately touches ONLY site_id. A member re-submitting a
    -- URL that already exists must not reset that URL's health: the obvious
    -- `DO UPDATE SET site_id = EXCLUDED.site_id` form is exactly the bug this WHERE
    -- clause avoids, because it would clear a 'dead' status on every edit.
    INSERT INTO content_sources ("entity_type", "entity_id", "url", "site_id")
    SELECT 'scene', "scene_id", "url", "site_id" FROM new_rows
    ON CONFLICT ("entity_type", "entity_id", "url") DO UPDATE
        SET "site_id" = EXCLUDED."site_id"
        WHERE content_sources."site_id" IS DISTINCT FROM EXCLUDED."site_id";
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

-- Postgres has no CREATE TRIGGER IF NOT EXISTS and this repo guards with DROP
-- IF EXISTS (103:176, 61:83): unguarded, re-running by hand dies halfway with
-- `trigger ... already exists` and leaves a migration that reported success.
DROP TRIGGER IF EXISTS trg_content_sources_from_scene_urls_ins ON "scene_urls";

CREATE TRIGGER trg_content_sources_from_scene_urls_ins
AFTER INSERT ON "scene_urls"
REFERENCING NEW TABLE AS new_rows
FOR EACH STATEMENT EXECUTE FUNCTION content_sources_from_scene_urls();

-- An UPDATE trigger too, and it is the same function rather than a second one
-- because the function is already an idempotent upsert: it inserts the pair when it
-- is missing and re-points site_id when it is not.
--
-- Without this, an UPDATE that moves a URL to a different site -- which no code
-- does today, `scene.sql` replaces the list with DELETE + CreateSceneURLs -- would
-- silently leave content_sources pointing at the old site. A desync that nothing
-- detects until the two tables are compared, which is why it is worth one more
-- trigger now rather than a hunt later. UPDATE also fires OLD and NEW, and only the
-- new side is interesting: an orphaned old row is exactly what the read view
-- already handles.
DROP TRIGGER IF EXISTS trg_content_sources_from_scene_urls_upd ON "scene_urls";

CREATE TRIGGER trg_content_sources_from_scene_urls_upd
AFTER UPDATE ON "scene_urls"
REFERENCING NEW TABLE AS new_rows
FOR EACH STATEMENT EXECUTE FUNCTION content_sources_from_scene_urls();

-- ---------------------------------------------------------------------------
-- There is deliberately NO delete trigger on scene_urls, and that is the one
-- design decision in this migration worth arguing about.
--
-- The edit path in this repository replaces a scene's URL list by deleting every
-- row and re-inserting the submitted set (internal/queries/sql/scene.sql:
-- `DELETE FROM scene_urls WHERE scene_id = $1` before CreateSceneURLs). So a
-- trigger that deleted the health row whenever a scene_urls row went away would
-- reset the health of every URL of a scene on every edit -- which is the whole
-- history this table exists to keep, thrown away by a member fixing a typo.
--
-- The cost of not deleting is an orphan: a URL removed from a scene keeps its
-- health row until the scene itself is deleted. Two things make that harmless:
--
--   * Membership stays owned by scene_urls, so readers go through the
--     `scene_sources` view below, which JOINs the two. A removed URL cannot
--     appear on the scene page.
--   * `expected_totals` (migration 97) points at a url row through
--     (source_entity_type, source_url), so an orphaned health row can still
--     answer "is this URL alive?" -- which is a better answer for that table than
--     losing it.
--
-- Scene deletion, which really does end the row's life, is handled directly.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION content_sources_from_scenes_deleted() RETURNS TRIGGER AS $$
BEGIN
    DELETE FROM content_sources cs
    USING (SELECT DISTINCT "id" FROM old_rows) gone
    WHERE cs."entity_type" = 'scene' AND cs."entity_id" = gone."id";
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_content_sources_from_scenes_del ON "scenes";

CREATE TRIGGER trg_content_sources_from_scenes_del
AFTER DELETE ON "scenes"
REFERENCING OLD TABLE AS old_rows
FOR EACH STATEMENT EXECUTE FUNCTION content_sources_from_scenes_deleted();

-- ---------------------------------------------------------------------------
-- The read surface: membership from scene_urls, health from content_sources.
--
-- This view is the answer to "which URLs does this scene have, and are they
-- alive?" and it is a view rather than a denormalized column precisely because the
-- two halves have different owners and different lifetimes. A reader that wants
-- only membership keeps using scene_urls; a reader that wants health joins here.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE VIEW "scene_sources" AS
SELECT
    su."scene_id",
    su."url",
    su."site_id",
    COALESCE(cs."status", 'unknown') AS "status",
    COALESCE(cs."canonical", FALSE) AS "canonical",
    cs."platform",
    cs."first_seen",
    cs."last_checked",
    cs."last_ok",
    COALESCE(cs."check_failures", 0) AS "check_failures",
    cs."archived_url",
    cs."id" AS "source_id"
FROM "scene_urls" su
LEFT JOIN "content_sources" cs
    ON cs."entity_type" = 'scene'
   AND cs."entity_id" = su."scene_id"
   AND cs."url" = su."url";

-- The LEFT JOIN is deliberate: a scene_urls row whose mirror has not been created
-- yet -- the window between this migration's backfill and a trigger firing -- must
-- still appear on the scene page with status 'unknown', not vanish from it.

