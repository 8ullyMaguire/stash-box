-- Reviews (SPEC §7.10, phase 3 step 1).
--
-- EDITING IS AN UPSERT, not a new version, and that is the decision the plan
-- deferred. Recorded here because the alternative was seriously considered and
-- the reason for rejecting it is not obvious.
--
-- The case FOR versioning (a review_edits table, history preserved): a rating that
-- silently changes from 5 to 1 is unfalsifiable, and a contributor who buys
-- goodwill and then edits the review leaves no trace.
--
-- The reason it is REJECTED for now: the plan's own argument is that "verified
-- usage" is much weaker without history, and that is true -- but only if
-- something READS the history. Nothing would. A versioned review with no
-- moderator view, no diff surface and no query is an append-only table that
-- doubles write cost and disk and cannot answer a question anyone is asking yet.
-- Versioning is cheap to add LATER (a history table over the same id), and
-- expensive to add now in the sense that every read path has to be written twice
-- from the start.
--
-- What is NOT given up: created_at is preserved across an edit, so "how long has
-- this person had this opinion" still has an answer, and a rating that changes is
-- visible as a change of value on the entity page. What IS given up, explicitly:
-- the intermediate rating is not recoverable.
--
-- The upsert below therefore updates body/rating/updated_at and leaves created_at
-- alone. It also does NOT touch `verified`, because verification is a moderator's
-- judgement about a claim, and an author editing prose must not be able to edit
-- the moderator's verdict along with it.

-- name: CreateReview :one
INSERT INTO reviews (id, author_id, entity_type, entity_id, rating, body, verified, status)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: UpdateReview :one
-- The author's own review of the same entity, replaced in place. `verified` is
-- deliberately absent from the SET list: see the note above.
UPDATE reviews
SET rating = $2, body = $3, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: FindReview :one
SELECT * FROM reviews WHERE id = $1;

-- name: FindReviewByAuthorAndEntity :one
-- Used by the upsert path to decide create-vs-update, and by the service to turn a
-- duplicate submission into an update rather than a constraint error.
SELECT * FROM reviews
WHERE author_id = $1 AND entity_type = $2 AND entity_id = $3;

-- name: ListReviewsForEntity :many
-- The entity page: published reviews, newest first.
--
-- `status = 'published'` is in the QUERY, not applied afterwards. A flagged review
-- is still visible to its author, so the flag-filtered-out-then-re-added
-- approach leaks moderation state to the client and needs a second scan; and a
-- removed review must not be countable in the average, which the next query
-- depends on being consistent about.
SELECT * FROM reviews
WHERE entity_type = $1 AND entity_id = $2 AND status = 'published'
ORDER BY created_at DESC, id DESC
LIMIT $3 OFFSET $4;

-- name: GetReviewAverage :one
-- The mean rating for an entity, and the count behind it.
--
-- count(*) is returned alongside because an average of one 5-star review and an
-- average of four hundred are both "4.2" at different moments, and a directory
-- that shows a bare number invites reading a single review as a consensus. The
-- caller decides what to do with a low count; the number itself cannot be
-- qualified from inside an aggregate.
--
-- count(*) FILTER (WHERE rating IS NOT NULL) rather than count(*): a review with
-- no rating is a real review and must appear in the total review count, but
-- including it in the mean would divide by a value that does not exist and drag
-- the average toward zero. The two numbers are deliberately different and the
-- struct keeps them apart.
SELECT
    -- CAST OUTSIDE the COALESCE. Inside it, sqlc sees a coalesce() over two
    -- untyped branches and falls back to interface{}, which pushes a type
    -- assertion into the service for a value the schema already knows is a
    -- float. The cast is the outer expression, so the column has one type.
    CAST(COALESCE(avg(rating), 0) AS double precision) AS average,
    count(*) FILTER (WHERE rating IS NOT NULL)::int AS rated_count,
    count(*)::int AS total_count
FROM reviews
WHERE entity_type = $1 AND entity_id = $2 AND status = 'published';

-- name: ListReviewsByAuthor :many
-- A profile page: this author's published reviews, newest first.
SELECT * FROM reviews
WHERE author_id = $1 AND status = 'published'
ORDER BY created_at DESC, id DESC
LIMIT $2 OFFSET $3;

-- name: SetReviewStatus :one
-- Moderation. Not restricted to flagged->published: a review can be flagged
-- directly by a moderator, and a status transition table in SQL would need a
-- trigger to enforce and a migration every time a state is added.
UPDATE reviews SET status = $2, updated_at = now() WHERE id = $1 RETURNING *;

-- name: SetReviewVerified :one
-- The moderator's usage verification, separate from SetReviewStatus so that
-- granting and withdrawing it are different calls and neither can be an
-- accidental side effect of the other.
UPDATE reviews SET verified = $2, updated_at = now() WHERE id = $1 RETURNING *;

-- name: ListFlaggedReviews :many
-- The moderation queue: oldest first, because the queue is worked in arrival
-- order and "newest first" makes an old report invisible under a constant
-- trickle of new ones.
SELECT * FROM reviews
WHERE status = 'flagged'
ORDER BY created_at ASC, id ASC
LIMIT $1 OFFSET $2;

-- name: CountReviewsForEntity :one
-- Total published reviews for an entity, unpaginated. Separate from
-- GetReviewAverage because the paginated list is capped at 100 by the caller and
-- a count taken from a capped list is a count of the cap.
SELECT count(*)::int FROM reviews
WHERE entity_type = $1 AND entity_id = $2 AND status = 'published';

-- name: DeleteReview :one
-- The author deleting their own review, or a moderator removing it outright.
-- Returns the row so the caller can confirm WHICH review went; a DELETE that
-- matches nothing and returns nothing is indistinguishable from success in a
-- mutation resolver, and a GraphQL client cannot tell a failed delete from a
-- deleted review.
DELETE FROM reviews WHERE id = $1 RETURNING *;
