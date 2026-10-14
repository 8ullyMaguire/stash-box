-- The tag ontology: the implication graph (migration 111) and its versions (migration 112).
--
-- Nothing here writes an implied tag onto scene_tags. The graph is the owning table and
-- every read derives from it; see docs/spec/feature-tag-ontology.md section 2.

-- name: CreateTagImplication :exec
-- Idempotent on purpose: an edit that adds an implication the ontology already has is not an
-- error worth failing an apply over, and the caller cannot tell the two apart in a diff.
INSERT INTO tag_implications (tag_id, implied_tag_id)
VALUES ($1, $2)
ON CONFLICT (tag_id, implied_tag_id) DO NOTHING;

-- name: DeleteTagImplication :exec
DELETE FROM tag_implications WHERE tag_id = $1 AND implied_tag_id = $2;

-- name: FindTagImplication :one
SELECT tag_id, implied_tag_id, created_at
FROM tag_implications
WHERE tag_id = $1 AND implied_tag_id = $2;

-- name: ListTagImplications :many
-- Every pair, by name, ordered: the shape the version snapshot is built from and what an
-- operator reads to see the whole ontology at once.
SELECT t.id AS tag_id, t.name AS tag_name,
       i.id AS implied_tag_id, i.name AS implied_name,
       ti.created_at
FROM tag_implications ti
JOIN tags t ON t.id = ti.tag_id
JOIN tags i ON i.id = ti.implied_tag_id
ORDER BY t.name, i.name;

-- name: FindImplicationsByTagID :many
-- Direct: what THIS tag implies.
SELECT i.* FROM tag_implications ti
JOIN tags i ON i.id = ti.implied_tag_id
WHERE ti.tag_id = $1 AND i.deleted = false
ORDER BY i.name;

-- name: FindImpliedByTagID :many
-- Direct: what implies THIS tag.
SELECT t.* FROM tag_implications ti
JOIN tags t ON t.id = ti.tag_id
WHERE ti.implied_tag_id = $1 AND t.deleted = false
ORDER BY t.name;

-- name: FindTransitiveImplicationsByTagID :many
-- Everything below this tag, at any depth. depth 1 is a direct implication.
--
-- A tag reachable by two paths appears ONCE, at its SHALLOWEST depth: UNION dedupes by
-- (tag_id, depth), which does not collapse a diamond, so DISTINCT ON does. The ORDER BY has
-- to lead with the column DISTINCT ON keys on; the caller sorts for display.
--
-- The depth bound is unreachable while the cycle trigger holds. It is there so that a
-- corrupt graph produces a short answer instead of a hung request, which is the failure mode
-- a search surface can survive.
WITH RECURSIVE reach(tag_id, depth) AS (
  SELECT ti.implied_tag_id, 1
    FROM tag_implications ti
   WHERE ti.tag_id = $1
  UNION
  SELECT ti.implied_tag_id, r.depth + 1
    FROM reach r
    JOIN tag_implications ti ON ti.tag_id = r.tag_id
   WHERE r.depth < 64
)
SELECT DISTINCT ON (t.id) t.*, r.depth
FROM reach r
JOIN tags t ON t.id = r.tag_id
WHERE t.deleted = false
ORDER BY t.id, r.depth;

-- name: FindTransitiveImpliedByTagID :many
-- Everything above this tag, at any depth: the tags a reader can filter by to find it.
WITH RECURSIVE reach(tag_id, depth) AS (
  SELECT ti.tag_id, 1
    FROM tag_implications ti
   WHERE ti.implied_tag_id = $1
  UNION
  SELECT ti.tag_id, r.depth + 1
    FROM reach r
    JOIN tag_implications ti ON ti.implied_tag_id = r.tag_id
   WHERE r.depth < 64
)
SELECT DISTINCT ON (t.id) t.*, r.depth
FROM reach r
JOIN tags t ON t.id = r.tag_id
WHERE t.deleted = false
ORDER BY t.id, r.depth;

-- name: ExpandTagIDsByImplications :many
-- The paint brush the search uses: for EVERY requested tag id, every tag that implies it,
-- transitively, INCLUDING the tag itself. One round trip for a whole filter.
--
-- Returning root_id alongside tag_id is what makes it usable: the caller needs to know which
-- REQUESTED tag each result satisfies, because "all of these" is a per-requested-tag
-- question and a flat set of ids cannot answer it.
WITH RECURSIVE reach(root_id, tag_id, depth) AS (
  SELECT u.id, u.id, 0
    FROM unnest(sqlc.arg(tag_ids)::UUID[]) AS u(id)
  UNION
  SELECT r.root_id, ti.tag_id, r.depth + 1
    FROM reach r
    JOIN tag_implications ti ON ti.implied_tag_id = r.tag_id
   WHERE r.depth < 64
)
SELECT root_id::uuid AS root_id, tag_id::uuid AS tag_id FROM reach;

-- The version (migration 112's trigger owns the writes).

-- name: CurrentOntologyVersion :one
SELECT COALESCE(MAX(version), 0)::int AS version FROM ontology_versions;

-- name: ListOntologyVersions :many
-- The history, newest first, WITHOUT the snapshots: a list is not the place to ship every
-- version's whole document, and the caller that wants one asks for it by number.
SELECT version, note, created_at
FROM ontology_versions
ORDER BY version DESC
LIMIT sqlc.arg('limit');

-- name: FindOntologyVersion :one
SELECT version, snapshot, note, created_at
FROM ontology_versions
WHERE version = $1;

-- Merging a tag into another (see edit/tag.go mergeInto).

-- name: DeleteSelfTagImplicationsForMerge :exec
-- The pairs that would become self-implications once source is folded into target. Deleted
-- FIRST, because after the retarget they are indistinguishable from a tag implying itself--
-- which is a different statement (and the CHECK would refuse it, mid-merge).
DELETE FROM tag_implications
WHERE (tag_id = @source_id AND implied_tag_id = @target_id)
   OR (tag_id = @target_id AND implied_tag_id = @source_id);

-- name: DeleteDuplicateTagImplicationTails :exec
-- Rows the tail retarget would collide with. A duplicate is not an error here: it says the
-- surviving tag already implies what the merged-away tag implied, which is the same fact.
DELETE FROM tag_implications d
WHERE d.implied_tag_id = @source_id
  AND EXISTS (
    SELECT 1 FROM tag_implications x
    WHERE x.tag_id = d.tag_id AND x.implied_tag_id = @target_id
  );

-- name: DeleteDuplicateTagImplicationHeads :exec
DELETE FROM tag_implications d
WHERE d.tag_id = @source_id
  AND EXISTS (
    SELECT 1 FROM tag_implications x
    WHERE x.implied_tag_id = d.implied_tag_id AND x.tag_id = @target_id
  );

-- name: RetargetTagImplicationTails :exec
UPDATE tag_implications SET implied_tag_id = @target_id WHERE implied_tag_id = @source_id;

-- name: RetargetTagImplicationHeads :exec
UPDATE tag_implications SET tag_id = @target_id WHERE tag_id = @source_id;
