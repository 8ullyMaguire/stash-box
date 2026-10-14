-- name: FindSceneSources :many
-- The scene page's source list: membership from scene_urls, health from
-- content_sources, joined by the view migration 108 creates.
--
-- Read through the VIEW and not through content_sources directly. A health row can
-- outlive its scene_urls row on purpose (the edit path replaces the URL list by
-- deleting every row and re-inserting it, and the health history must survive that),
-- so querying the table directly would show URLs the scene no longer claims.
--
-- Canonical first, then newest: the canonical source is the one a reader wants, and
-- among the rest the recently-added ones are more likely to still be alive than a
-- source that has been sitting there since the scene was created.
SELECT * FROM scene_sources
WHERE scene_id = $1
ORDER BY canonical DESC, first_seen DESC NULLS LAST, url;

-- name: FindSceneSourcesByIds :many
-- The batched form, for the dataloader that serves Scene.sources when a page lists
-- many scenes. Same view as FindSceneSources: one query for the whole page rather
-- than one per scene.
--
-- Ordered by scene_id first so the grouping the dataloader does preserves the
-- display order WITHIN each scene (canonical first), and is deterministic rather
-- than whatever the planner returns.
SELECT * FROM scene_sources
WHERE scene_id = ANY(sqlc.arg(scene_ids)::UUID[])
ORDER BY scene_id, canonical DESC, first_seen DESC NULLS LAST, url;

-- name: FindContentSourcesForEntity :many
-- The polymorphic read, for every entity kind that is not a scene. Phase 3's
-- content_items use this; scenes use FindSceneSources above, because only scenes have
-- a membership table to join.
SELECT * FROM content_sources
WHERE entity_type = $1 AND entity_id = $2
ORDER BY canonical DESC, first_seen DESC NULLS LAST, url;

-- name: FindContentSourceByEntityAndURL :one
-- "Have we seen this URL before?" -- the first question the ingestion pipeline asks
-- (plan Phase 2). Scoped to the entity, because the same URL on two different scenes
-- is two sources: the URL's health is one fact, but the membership is per entity.
SELECT * FROM content_sources
WHERE entity_type = $1 AND entity_id = $2 AND url = $3;

-- name: FindContentSourcesByURL :many
-- The same URL across every entity that claims it. This is what makes a single health
-- check answer for all of them: check once, write every row.
SELECT * FROM content_sources WHERE url = $1;

-- name: FindContentSourcesByNormalizedURL :many
-- "Has anything already claimed this URL?" (spec §1 stage 3) -- the question the
-- pipeline asks before it creates a record.
--
-- TWO ARMS, and the second is not redundant. `url_normalized` is written only by the
-- ingest path; a row written by the scene_urls mirror carries NULL, because the mirror
-- is a SQL trigger and normalisation is Go (migration 110 says why at length). Matching
-- only the first arm would report "nothing claims this URL" for every row the edit path
-- ever wrote, and the pipeline would then create a second scene for a page that is
-- already on one -- the exact failure the normaliser exists to prevent.
--
-- Both arms are indexed (content_sources_url_normalized_idx, content_sources_url_lower_idx),
-- so this is a bitmap OR of two index scans rather than a sequential scan.
--
-- The entity_type filter is a parameter rather than a constant: phase 3's content_items
-- are the same question with a different answer set, and a hardcoded 'scene' here would
-- be the second place to change when they land.
SELECT * FROM content_sources
WHERE entity_type = @entity_type
  AND (url_normalized = @url_normalized OR LOWER(url) = LOWER(@url_normalized))
ORDER BY (url_normalized = @url_normalized) DESC NULLS LAST, first_seen;

-- name: InsertContentSource :one
-- The pipeline's attach: record that an entity is reachable at a URL.
--
-- ON CONFLICT DO NOTHING against the (entity_type, entity_id, url) constraint, so an
-- attach that has already happened is a no-op rather than an error: re-attaching is the
-- ordinary outcome of a duplicate submission, and the caller cannot tell (nor should it)
-- whether the row was there before. ErrNoRows here means "already attached".
--
-- `canonical` is NOT set. Electing a canonical source is a separate decision with its
-- own statement and a partial unique index behind it (phase 1), and a pipeline that
-- silently claimed canonicality for whatever it happened to ingest first would make the
-- reader's destination depend on queue order.
INSERT INTO content_sources (
    entity_type, entity_id, url, url_normalized, site_id, platform, created_by
) VALUES (
    @entity_type, @entity_id, @url, @url_normalized, @site_id, @platform, @created_by
)
ON CONFLICT (entity_type, entity_id, url) DO NOTHING
RETURNING *;

-- name: FindSiteForURL :one
-- Resolve a URL to a directory entry, so an ingested source carries its site.
--
-- Longest regex wins: the directory holds both `example.com` and
-- `example.com/studio/`-shaped patterns, and the more specific one is the better answer.
-- Both are POSIX extended regular expressions as far as Postgres is concerned, so a
-- pattern written for the frontend's JavaScript engine may not match here -- that is a
-- MISS, not an error: site_id is nullable, the source is still recorded, and a source
-- with no site is a normal state rather than a failure.
--
-- The regex is user data (imported with the directory), so this is evaluated by
-- Postgres's own engine and never by the Go layer.
SELECT id, name FROM sites
WHERE regex IS NOT NULL AND regex <> '' AND @url ~ regex
ORDER BY length(regex) DESC
LIMIT 1;

-- name: EnsureIngestSite :one
-- The aggregator's directory entry for a host it has never seen (plan phase 2).
--
-- WHY THE INGEST PATH CREATES SITES AT ALL. scene_urls.site_id is NOT NULL, so a source
-- with no site cannot be recorded as a MEMBERSHIP -- and the scene page, the completion
-- score and every other reader of a scene's URLs read scene_urls, not content_sources. A
-- pipeline that skipped the membership for an uncatalogued host produced sources that
-- existed in one table and appeared in no view; asking a created record for its source list
-- returned an empty list.
--
-- So the pipeline grows the directory, one row per HOST, and the row is a fact rather than
-- a judgement: the name is the host, the url is where it was found, valid_types is what was
-- observed (a page with a scene on it), and `highlighted` is passed in as FALSE --
-- deliberately overriding the column's true default, because the directory's highlighted
-- sites are a curated list and a crawler that highlighted every host it touched would fill
-- it with whatever it crawled.
--
-- ON CONFLICT (name) DO UPDATE is the upsert-that-returns-the-EXISTING-row trick:
-- sites.name is UNIQUE and NOT NULL, so it is the only safe key, and DO NOTHING would
-- return no row at all -- leaving the caller to re-select, which is exactly the race (two
-- workers draining two URLs from the same new host) this statement closes. The no-op UPDATE
-- writes the name the row already has.
--
-- `regex` is what makes this run ONCE per host: FindSiteForURL matches on regex only, so a
-- row with a NULL regex resolves nothing and every URL from that host would come back here.
INSERT INTO sites (id, name, url, regex, valid_types, highlighted, created_at, updated_at)
VALUES (@id, @name, @url, @regex, @valid_types, @highlighted, now(), now())
ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name
RETURNING *;

-- name: RecordSourceCheck :one
-- The health sweep's write, and the only statement that may move `status`.
--
-- `last_ok` is set ONLY when the check succeeded. Passing NULL leaves the previous
-- value alone via COALESCE, because "when was this last alive" is a fact about the
-- past that a failure does not change -- overwriting it with NULL would erase the
-- only evidence that the source was ever reachable.
--
-- check_failures is passed in rather than incremented, so the caller owns the
-- transition rule (3 consecutive failures -> intermittent, 7 days -> dead) and this
-- statement stays a write and not a policy.
UPDATE content_sources SET
    status = @status,
    last_checked = now(),
    last_ok = COALESCE(@last_ok::timestamptz, last_ok),
    check_failures = @check_failures
WHERE id = @id
RETURNING *;

-- name: ClearCanonicalSourceExcept :exec
-- Half of the canonical election. This one and PromoteSourceToCanonical below are two
-- statements on purpose, and they MUST run in one transaction.
--
-- Measured, because the obvious single-statement form does not work: a data-modifying
-- CTE that clears the incumbent while the outer UPDATE promotes the successor fails with
-- `duplicate key value violates unique constraint "content_sources_one_canonical"`. The
-- outer UPDATE does not see the CTE's change -- a data-modifying CTE's effects are not
-- visible to the rest of the same command -- so the unique index evaluates both rows as
-- canonical at once. Two statements inside a transaction are visible to each other, and
-- the transaction is what removes the "no canonical source if the second fails" window.
--
-- scripts/verify-108.sh keeps the CTE form as a REJECT case, so this decision cannot be
-- refactored back into one statement without a test going red.
UPDATE content_sources SET canonical = false
WHERE entity_type = $1 AND entity_id = $2 AND canonical AND id <> $3;

-- name: PromoteSourceToCanonical :exec
-- The other half. Separate from the clear, and ordered after it by the caller.
UPDATE content_sources SET canonical = true WHERE id = $1;

-- name: ClearCanonicalSource :exec
-- Demote without promoting a replacement: the case where the canonical source is
-- known to be dead and no other source has been chosen yet. An entity with no
-- canonical source is a legitimate state and must be reachable, or the only way to
-- remove a bad canonical would be to nominate a different one.
UPDATE content_sources SET canonical = false WHERE entity_type = $1 AND entity_id = $2;

-- name: FindContentSourceByID :one
-- The health sweep's read. It goes to the TABLE rather than the scene_sources view on purpose: the
-- sweep acts on the health record, and a URL a scene has stopped claiming still has one -- a row the
-- view deliberately hides. Checking it here is what keeps health history independent of membership.
SELECT * FROM "content_sources" WHERE "id" = @id;

-- name: FindContentSourcesDueForCheck :many
-- The health sweep's candidate list (slice 9b).
--
-- Whether a candidate is DUE is a function of the row -- reliability lengthens the interval,
-- popularity shortens it -- and that rule lives in Go (`content.CheckInterval`) where it is a
-- tested function rather than an expression in SQL. So this query answers the narrower question
-- "which rows are worth looking at", most overdue first, and the caller skips the ones whose
-- interval has not elapsed.
--
-- SKIPPING CANNOT STARVE ANYTHING, and that is why the ordering is what it is: rows come back
-- oldest-checked first, so a row that is not due yet is only reached after every row that is.
--
-- DEAD is absent on purpose: resurrecting a dead source is the resurrection queue's job (slice
-- 9c), on a cadence of its own, and `content_sources_sweep_idx` (migration 108) is a partial index
-- over exactly the three statuses below -- the query and the index are the same list.
SELECT cs.*, (
    SELECT count(*) FROM scene_urls su WHERE su.url = cs.url
)::int AS claimant_count
FROM content_sources cs
WHERE cs.status IN ('unknown', 'live', 'intermittent')
ORDER BY cs.last_checked ASC NULLS FIRST, cs.first_seen ASC
LIMIT @lim;
