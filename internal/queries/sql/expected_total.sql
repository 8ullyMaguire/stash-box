-- Expected-total denominators and what they imply is missing (SPEC §7.24.2).
--
-- `expected_totals` exists (migration 97) and nothing read it: only the sqlc
-- model struct referenced the table. So the studio "completeness race" and the
-- per-studio "what's missing" are not GraphQL exposure work, they are a real
-- read path with a real join.
--
-- Two ideas that must not be confused, because the schema deliberately separates
-- them:
--
--   * `expected` — a CLAIM about the world: "the studio's site lists 412 scenes".
--     Sourced and asserted by a user. Absence means nobody has counted, which is
--     harmless.
--   * `observed` — what this instance actually holds, counted by SQL.
--
-- The score is observed/expected, and it is only meaningful when a denominator
-- exists. A studio with 3 of 400 scenes and no total scores 100%, which is
-- precisely the failure this table was added to prevent -- so an absent total
-- yields an absent score, never 100 and never 0. §7.24.2's argument is that an
-- unsourced total "silently deflates every completion score on the instance".
--
-- Over-count is real and intended: if the archive holds MORE than the source
-- claims, `missing` clamps to 0 and the caller reports the surplus. The claim is
-- stale, and the honest reading is "we have more than they list".
--
-- RELATIONSHIPS, verified against the live schema rather than assumed:
--   scenes.studio_id            -- studio -> scenes is direct
--   studio_images.studio_id     -- studio -> images is via a JOIN table
--   performers have NO studio link. There is no performer_studio table, so
--   "performers at this studio" is derived through scene_performers and is
--   therefore only as good as the scene links. `ListObservedCounts` says so in a
--   comment rather than presenting the number as a studio's performer roster.

-- name: GetExpectedTotals :many
-- All denominators asserted for one entity, oldest assertion first.
SELECT *
FROM expected_totals
WHERE entity_type = $1
  AND entity_id = $2
ORDER BY asserted_at ASC;

-- name: GetExpectedTotal :one
-- One entity's total for one kind ('scenes', 'images', 'performers'). NULL when
-- nobody has asserted one, which the caller must render as "no denominator"
-- rather than as a score.
SELECT *
FROM expected_totals
WHERE entity_type = $1
  AND entity_id = $2
  AND kind = $3;

-- name: CreateExpectedTotal :one
-- Assert a denominator. The trust gate is NOT here: `source_url` and
-- `asserted_by` are NOT NULL in the table, so an unsourced claim cannot be
-- stored at all. Authorisation (only a trusted contributor or moderator may
-- assert) belongs in the service, where the caller's trust level is readable.
--
-- Re-asserting the same (entity, kind) REPLACES the claim rather than failing:
-- a source that revises its count must be correctable, and two competing rows
-- for one kind would make "the" denominator ambiguous.
INSERT INTO expected_totals (
    entity_type, entity_id, kind, total,
    source_entity_type, source_url, asserted_by, asserted_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, now())
ON CONFLICT (entity_type, entity_id, kind) DO UPDATE
    SET total = EXCLUDED.total,
        source_entity_type = EXCLUDED.source_entity_type,
        source_url = EXCLUDED.source_url,
        asserted_by = EXCLUDED.asserted_by,
        asserted_at = now()
RETURNING *;

-- name: DeleteExpectedTotal :one
-- Retract a denominator, returning the removed row so the caller can log whose
-- claim was withdrawn and what it claimed. :one rather than :exec for that
-- reason: deleting a claim that was never there and deleting a real one look
-- identical otherwise, and the second is worth recording.
DELETE FROM expected_totals
WHERE entity_type = $1
  AND entity_id = $2
  AND kind = $3
RETURNING *;

-- name: GetObservedCount :one
-- What this instance actually holds for one (studio, kind).
--
-- The CASE guards every kind, so an unknown kind returns 0 rather than NULL: a
-- count of nothing is a real answer, whereas NULL would make the ratio
-- undefined and silently drop the row.
--
-- DISTINCT everywhere, because these are all MANY-to-MANY in practice: a scene
-- joined to two performers, or an image linked twice, would inflate the observed
-- count and report a well-linked studio as over-claimed.
--
-- The performers branch is a DERIVED number and is documented as such in the
-- comment above. It counts performers seen in at least one of this studio's
-- scenes; performers with no scene link are invisible to it, which is a property
-- of the data model and not something this query can fix.
SELECT
    CASE sqlc.arg(kind)::varchar
        WHEN 'scenes' THEN (
            SELECT count(DISTINCT s.id)
            FROM scenes s
            WHERE s.studio_id = sqlc.arg(studio_id)::uuid
        )
        WHEN 'images' THEN (
            SELECT count(DISTINCT si.image_id)
            FROM studio_images si
            WHERE si.studio_id = sqlc.arg(studio_id)::uuid
        )
        WHEN 'performers' THEN (
            SELECT count(DISTINCT sp.performer_id)
            FROM scene_performers sp
            JOIN scenes s ON s.id = sp.scene_id
            WHERE s.studio_id = sqlc.arg(studio_id)::uuid
        )
        ELSE 0
    END::bigint AS observed_count;

-- name: ListStudioCompleteness :many
-- The studio race: every studio that HAS a denominator, with what we hold and
-- what is claimed, ranked by the size of the gap.
--
-- Ordered by `missing` DESC because a race has a leader, and the leader is the
-- studio furthest from its claimed total. Ordering by ratio instead would put a
-- studio that is 3 of 400 (99.25% missing) below one that is 40 of 60 (33%
-- missing), which reads as the smaller opportunity when it is the larger one.
--
-- The inner JOIN is deliberate: a studio with no denominator has nothing to race
-- against, and a LEFT JOIN would put every uncounted studio on the board with a
-- NULL score, where it looks like a participant rather than an absence. Those are
-- listed separately by ListUncountedStudios.
--
-- Deleted studios are excluded: a merged-away studio's denominator is a claim
-- about a record that no longer exists, and leaving it on the board would put a
-- permanent, unclosable gap at the top of a race nobody can win.
SELECT
    st.id AS studio_id,
    st.name AS studio_name,
    t.kind,
    t.total AS expected,
    CASE t.kind
        WHEN 'scenes' THEN (SELECT count(DISTINCT s.id) FROM scenes s WHERE s.studio_id = st.id)
        WHEN 'images' THEN (SELECT count(DISTINCT si.image_id) FROM studio_images si WHERE si.studio_id = st.id)
        WHEN 'performers' THEN (
            SELECT count(DISTINCT sp.performer_id)
            FROM scene_performers sp JOIN scenes s ON s.id = sp.scene_id
            WHERE s.studio_id = st.id
        )
        ELSE 0
    END::bigint AS observed,
    GREATEST(
        t.total - CASE t.kind
            WHEN 'scenes' THEN (SELECT count(DISTINCT s.id) FROM scenes s WHERE s.studio_id = st.id)
            WHEN 'images' THEN (SELECT count(DISTINCT si.image_id) FROM studio_images si WHERE si.studio_id = st.id)
            WHEN 'performers' THEN (
                SELECT count(DISTINCT sp.performer_id)
                FROM scene_performers sp JOIN scenes s ON s.id = sp.scene_id
                WHERE s.studio_id = st.id
            )
            ELSE 0
        END,
        0
    )::bigint AS missing,
    t.source_url,
    t.asserted_at
FROM studios st
JOIN expected_totals t
  ON t.entity_type = 'studio'
 AND t.entity_id = st.id
WHERE NOT st.deleted
ORDER BY missing DESC, st.name ASC
LIMIT sqlc.arg(limit_count)::int OFFSET sqlc.arg(limit_offset)::int;

-- name: ListUncountedStudios :many
-- Studios holding scenes but carrying NO denominator. This is the actionable
-- list: each row is one sourced claim away from being on the board, and a claim
-- is cheap to make.
--
-- Only studios that already hold scenes are listed. A studio with nothing and no
-- denominator is not a gap anyone closes by counting -- it simply has not been
-- started -- and putting those here would bury the real rows under every empty
-- shell in the instance.
SELECT
    st.id AS studio_id,
    st.name AS studio_name,
    count(DISTINCT s.id)::bigint AS observed
FROM studios st
JOIN scenes s ON s.studio_id = st.id
WHERE NOT st.deleted
  AND NOT EXISTS (
      SELECT 1 FROM expected_totals t
      WHERE t.entity_type = 'studio' AND t.entity_id = st.id
  )
GROUP BY st.id, st.name
ORDER BY observed DESC, st.name ASC
LIMIT sqlc.arg(limit_count)::int OFFSET sqlc.arg(limit_offset)::int;
