-- Elo / Glicko-2 queries (SPEC §9, migration 77).
--
-- elo_votes is the source of truth and is append-only; elo_ratings is a cache
-- with a defined rebuild path. These queries are the only places allowed to
-- write either table, for the same reason trust.sql owns trust_events and
-- user_trust.

-- name: GetEloRating :one
SELECT * FROM elo_ratings WHERE entity_type = $1 AND entity_id = $2;

-- name: GetEloRatingsByIDs :many
-- Takes a type plus a batch of ids, because ranking reads are always
-- per-entity-kind ("the ratings for these 50 performers"), never across kinds.
SELECT * FROM elo_ratings WHERE entity_type = $1 AND entity_id = ANY($2::UUID[]);

-- name: UpsertEloRating :one
-- Creates the row on first use, which is what makes a performer created AFTER
-- the migration work without a separate backfill: the seeding INSERT covers
-- performers that predate it, and this covers everyone after.
--
-- last_rated_at is set on update as well as insert, so a rating that is
-- re-asserted with unchanged numbers still counts as "rated now" for the Glicko
-- time constant.
INSERT INTO elo_ratings (entity_type, entity_id, rating, deviation, last_rated_at)
VALUES ($1, $2, $3, $4, NOW())
ON CONFLICT (entity_type, entity_id) DO UPDATE
SET rating = EXCLUDED.rating,
    deviation = EXCLUDED.deviation,
    last_rated_at = NOW()
RETURNING *;

-- name: RecordEloVote :one
-- No ON CONFLICT: elo_votes has no natural key beyond its own id, and a
-- duplicate matchup is a legitimate thing for a user to do (they may change
-- their mind about the same pair). Deduplicating identical votes would discard
-- that signal, and the "don't double count" property comes from Glicko being
-- applied to the rating, not from refusing to store the vote.
--
-- Returning the row lets the service report the resulting rating to the voter
-- without a second round trip.
INSERT INTO elo_votes (id, user_id, winner_id, loser_id, winner_type, loser_type, picked_side)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: CountEloVotesForEntity :one
-- How many votes an entity has taken part in, across both sides. Feeds the
-- leaderboard's "needs more votes" marker and any minimum-confidence filter.
SELECT count(*)
FROM elo_votes
WHERE (winner_type = sqlc.arg(entity_type) AND winner_id = sqlc.arg(entity_id))
   OR (loser_type = sqlc.arg(entity_type) AND loser_id = sqlc.arg(entity_id));

-- name: GetEloVotesForUser :many
-- The user's own votes, newest first. Backs the taste vector (SPEC §2) and the
-- voting-consistency signal in SPEC §6.
--
-- Ordered by created_at so a rebuild is deterministic: two rebuilds of the same
-- vote set must produce the same vector, and an unordered scan would let
-- floating-point rounding differ between runs.
SELECT * FROM elo_votes WHERE user_id = $1 ORDER BY created_at DESC;

-- name: GetTasteVector :one
SELECT * FROM taste_vectors WHERE user_id = $1;

-- name: UpsertTasteVector :one
INSERT INTO taste_vectors (user_id, vector, vote_count, updated_at)
VALUES ($1, $2, $3, NOW())
ON CONFLICT (user_id) DO UPDATE
SET vector = EXCLUDED.vector,
    vote_count = EXCLUDED.vote_count,
    updated_at = NOW()
RETURNING *;

-- name: DeleteTasteVector :exec
-- Only used by a rebuild, so the row can be recreated from scratch rather than
-- merged. Merging a recomputed vector into an existing one would double-count
-- every feature.
DELETE FROM taste_vectors WHERE user_id = $1;
