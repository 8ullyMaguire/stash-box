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
    status = $2,
    last_checked = now(),
    last_ok = COALESCE($3::timestamptz, last_ok),
    check_failures = $4
WHERE id = $1
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
