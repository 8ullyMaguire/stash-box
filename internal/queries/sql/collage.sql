-- Snapshot collage queries (SPEC §8, migration 78).
--
-- Two tables with a deliberate split: scene_snapshots is the source of truth
-- (individual timestamped frames) and collages is a derived selection over them.
-- See migration 78 for why the split exists and why a snapshot is a timestamp
-- rather than a stored image.

-- name: CreateSceneSnapshot :one
-- Adding a snapshot by hand or via the API. collage_id is left NULL: SPEC §8
-- describes collages as GENERATED, so a snapshot exists in the pool before
-- anything selects from it.
--
-- The unique (scene_id, timestamp_ms) means a re-add of the same instant is a
-- constraint violation rather than a duplicate frame that renders as one and
-- counts twice toward the frame budget. Surfaced as ErrDuplicateSnapshot rather
-- than a raw 23505 so the caller can say something useful.
INSERT INTO scene_snapshots (id, scene_id, timestamp_ms, created_by)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetSceneSnapshot :one
SELECT * FROM scene_snapshots WHERE id = $1;

-- name: ListSceneSnapshots :many
-- Every snapshot for a scene, in order, whether or not it is in a collage.
--
-- Ordered by timestamp so a caller rendering a scene's visual index gets frames in
-- playback order for free, and so a client computing "how far apart are these"
-- does not have to sort.
SELECT * FROM scene_snapshots
WHERE scene_id = $1
ORDER BY timestamp_ms;

-- name: ListUnassignedSnapshots :many
-- The pool a collage generation samples from.
--
-- Excludes snapshots already committed to a collage. On a REgeneration the old
-- collage's frames are freed first (see ClearCollage), so this returns the whole
-- pool again -- which is what makes a re-roll able to pick different frames rather
-- than only from whatever the last generation left behind.
SELECT * FROM scene_snapshots
WHERE scene_id = $1 AND collage_id IS NULL
ORDER BY timestamp_ms;

-- name: CountSceneSnapshots :one
SELECT count(*) FROM scene_snapshots WHERE scene_id = $1;

-- name: DeleteSceneSnapshot :exec
DELETE FROM scene_snapshots WHERE id = $1;

-- name: GetCollageForScene :one
SELECT * FROM collages WHERE scene_id = $1;

-- name: CreateCollage :one
-- Creates the collage row. source_duration_ms is the duration the sampler
-- believed; the scene's current duration is read by the caller and stored
-- alongside it so a stale collage is diagnosable rather than merely wrong-looking.
INSERT INTO collages (id, scene_id, frame_count, strategy, source_duration_ms, current_duration_ms)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: DeleteCollage :exec
-- Removing a collage. The snapshots it referenced are NOT deleted: the FK is ON
-- DELETE SET NULL, so they return to the unassigned pool and a later generation can
-- reuse them. Deleting a user's curated frames because someone re-rolled a collage
-- would be data loss dressed up as a cascade.
DELETE FROM collages WHERE scene_id = $1;

-- name: AssignSnapshotsToCollage :execrows
-- Claims a set of snapshots for a collage.
--
-- The WHERE clause on collage_id IS NULL is a claim, not a filter: if a snapshot
-- was claimed by a concurrent generation between the SELECT and this UPDATE, the
-- row does not match and is not taken. That is the optimistic-concurrency guard.
--
-- :execrows rather than :exec, and that is load-bearing. Without the returned count
-- the caller cannot tell a complete claim from a partial one, and a partial claim
-- produces a collage with fewer frames than its own frame_count says -- a broken
-- strip that renders as though it were fine. The caller compares the count against
-- the number it asked for and rolls back on a shortfall.
--
-- array_unnest over a UUID[] is how sqlc passes a set; there is no variadic form
-- in Postgres and unnest is the idiomatic one.
UPDATE scene_snapshots
SET collage_id = $1
WHERE id = ANY(sqlc.arg(snapshot_ids)::UUID[])
  AND collage_id IS NULL;

-- name: ListCollageSnapshots :many
-- The frames of a collage, in playback order.
--
-- The read path for rendering, covered by a partial index on
-- (collage_id, timestamp_ms) so this is an index scan and not a sort of the
-- scene's entire snapshot set.
SELECT s.*
FROM scene_snapshots s
JOIN collages c ON c.id = s.collage_id
WHERE c.scene_id = sqlc.arg(scene_id)
  AND s.collage_id IS NOT NULL
ORDER BY s.timestamp_ms;

-- name: DeleteCollageForScene :exec
-- Frees a collage's frames without removing the collage row, for the regeneration
-- path: clear the assignment, sample again, reassign.
--
-- Separate from DeleteCollage because the two are used at different times and
-- conflating them is how a regeneration ends up deleting curated snapshots.
UPDATE scene_snapshots
SET collage_id = NULL
WHERE scene_id = $1 AND collage_id IS NOT NULL;

-- name: ListScenesWithInsufficientSnapshots :many
-- Scenes whose visual index is too thin to identify from -- the input to SPEC §8's
-- curation and preservation quests ("this scene has only 2 snapshots").
--
-- Counts snapshots per scene rather than collages: a scene with 40 snapshots and
-- no collage is better identified than one with 3 and a 12-frame collage, because
-- the snapshots are what a user can browse and the collage is a selection over
-- them.
--
-- Deleted scenes are excluded, matching every other performer/scene query.
SELECT s.id, count(sn.id) AS snapshot_count
FROM scenes s
LEFT JOIN scene_snapshots sn ON sn.scene_id = s.id
WHERE s.deleted = FALSE
GROUP BY s.id
HAVING count(sn.id) < sqlc.arg(min_snapshots)::INT
ORDER BY count(sn.id) ASC, s.id
LIMIT sqlc.arg(limit_count);


-- name: FindSceneDuration :one
-- The scene's duration, for spacing a collage's frames.
--
-- Returns only `duration`, deliberately. A sampler needs the length and nothing
-- else, and selecting the whole scene row here would hand the caller a struct it
-- has no business modifying.
--
-- Deleted scenes are excluded so Generate can tell "this scene is gone" from "this
-- scene has no duration" -- two different errors with two different fixes, and the
-- distinction is worth a row.
SELECT duration FROM scenes WHERE id = $1 AND deleted = FALSE;
