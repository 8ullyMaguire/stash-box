-- Identification board queries (SPEC §5, migration 79).
--
-- Three tables with one rule running through all of them: a vote is EVIDENCE, not
-- authority. Nothing here writes to scenes/performers/etc, and the resolution path
-- records what a human decided rather than inferring it from a tally.

-- name: CreateIdentificationQuery :one
INSERT INTO identification_queries
    (id, target_type, target_id, description, collage_id, snapshot_id, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetIdentificationQuery :one
SELECT * FROM identification_queries WHERE id = $1;

-- name: ListOpenIdentificationQueries :many
-- The board's queue: open queries, newest first.
--
-- Bounded by the caller and defaulted in the service. An unbounded queue is a
-- denial-of-service vector, and no UI renders more than a few hundred.
--
-- The partial index on status='open' covers exactly this, so the filter is not
-- costing a scan of solved and abandoned queries.
SELECT * FROM identification_queries
WHERE status = 'open'
ORDER BY created_at DESC, id
LIMIT $1;

-- name: ListIdentificationQueriesByStatus :many
-- Every query in a state, for moderation and for §5's "solved" archive view.
SELECT * FROM identification_queries
WHERE status = $1
ORDER BY created_at DESC, id
LIMIT $2;

-- name: ResolveIdentificationQuery :one
-- Records what a HUMAN decided a query was.
--
-- The CHECK constraint enforces that a solved query names its resolution, so
-- there is no way to mark one solved with nothing attached -- which is the
-- failure that would make every consumer of the "solved" view re-verify it.
--
-- Guarded on status='open' so a second resolution attempt is a no-op returning no
-- rows rather than an overwrite. Two people clicking "accept" on different
-- candidates at the same time is a real race, and last-write-wins would silently
-- discard one person's work.
UPDATE identification_queries
SET status = 'solved',
    resolved_type = $2,
    resolved_id = $3,
    resolved_by = $4,
    resolved_at = NOW(),
    updated_at = NOW()
WHERE id = $1 AND status = 'open'
RETURNING *;

-- name: AbandonIdentificationQuery :one
-- Marks a query dead. Distinct from leaving it open: an abandoned query is one the
-- community voted down, and re-surfacing it in the queue forever is how a board
-- fills with questions nobody wants.
UPDATE identification_queries
SET status = 'abandoned', updated_at = NOW()
WHERE id = $1 AND status = 'open'
RETURNING *;

-- name: ListResolvedQueriesForEntity :many
-- "Everything the community has worked out about this performer."
--
-- This is the query that turns a solved query into a CANONICAL LINK, per §5: the
-- answer is that the board's conclusions are indexed against real metadata, so
-- finding an entity also finds what was learned about it.
SELECT * FROM identification_queries
WHERE resolved_type = $1 AND resolved_id = $2 AND status = 'solved'
ORDER BY resolved_at DESC
LIMIT $3;

-- name: AddIdentificationCandidate :one
-- Proposes a candidate.
--
-- The unique (query_id, entity_type, entity_id) means one suggestion per entity
-- per query: re-suggesting is not more signal, and allowing it would let one
-- person weight the vote.
INSERT INTO identification_candidates
    (id, query_id, entity_type, entity_id, note, suggested_by)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListIdentificationCandidates :many
-- A query's candidates WITH their tallies.
--
-- LEFT JOIN plus count, so a candidate nobody has voted for still appears with
-- zero. An INNER JOIN would silently hide every freshly-suggested candidate,
-- which is exactly when someone needs to see it.
--
-- count(DISTINCT v.user_id) rather than count(v.*): the vote table's primary key
-- already makes the rows distinct, but the DISTINCT documents that the tally is
-- of PEOPLE, which is the number §5's leaderboards are built from.
SELECT c.id, c.query_id, c.entity_type, c.entity_id, c.note, c.suggested_by,
       c.created_at, count(v.user_id) AS vote_count
FROM identification_candidates c
LEFT JOIN identification_candidate_votes v ON v.candidate_id = c.id
WHERE c.query_id = $1
GROUP BY c.id
ORDER BY count(v.user_id) DESC, c.created_at ASC
LIMIT $2;

-- name: VoteForIdentificationCandidate :exec
-- One vote. The composite primary key on the vote table is the rule: a second
-- vote is a constraint violation rather than a silently doubled tally.
INSERT INTO identification_candidate_votes (candidate_id, user_id)
VALUES ($1, $2);

-- name: UnvoteIdentificationCandidate :exec
DELETE FROM identification_candidate_votes
WHERE candidate_id = $1 AND user_id = $2;

-- name: HasVotedForCandidate :one
-- Whether THIS user already voted, so the UI can render a vote button as a state
-- rather than as an action that silently does nothing.
SELECT EXISTS (
    SELECT 1 FROM identification_candidate_votes
    WHERE candidate_id = $1 AND user_id = $2
);

-- name: CountIdentificationVotesForUser :one
-- How many candidates this user has voted on, anywhere.
--
-- Backs §5's "Detective" leaderboard. Counting through the candidate table rather
-- than straight at the votes, so a vote on a candidate whose query has been
-- deleted does not count: the vote is evidence about a question that no longer
-- exists.
SELECT count(*)
FROM identification_candidate_votes v
JOIN identification_candidates c ON c.id = v.candidate_id
JOIN identification_queries q ON q.id = c.query_id
WHERE v.user_id = $1 AND q.status = 'open';

-- name: GetIdentificationCandidate :one
-- A single candidate, for the vote path.
--
-- Joins the query so the caller can check the query is still open without a
-- second round trip -- voting on a resolved query is meaningless and the vote
-- would be counted for nothing.
SELECT c.*, q.status AS query_status, q.target_type AS query_target_type
FROM identification_candidates c
JOIN identification_queries q ON q.id = c.query_id
WHERE c.id = $1;

-- name: ListIdentificationQueriesByCreator :many
-- A user's own questions, so they can see what they asked and what got solved.
SELECT * FROM identification_queries
WHERE created_by = $1
ORDER BY created_at DESC
LIMIT $2;

-- name: CountVotesForCandidate :one
-- A single candidate's tally.
--
-- Counted here rather than joined into GetIdentificationCandidate because a vote
-- mutation needs this one row and the grouped per-query query would be a scan of
-- every candidate on the query to answer "how many votes does this one have".
SELECT count(user_id) FROM identification_candidate_votes
WHERE candidate_id = $1;
