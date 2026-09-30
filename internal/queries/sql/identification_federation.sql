-- Foreign identification evidence — a peer's answer to one of our queries.
--
-- SPEC F2, and this file is where that decision is enforced: the only INSERT
-- here targets identification_foreign_candidates. There is deliberately no
-- query in this file that writes identification_candidates, and no query that
-- deletes or updates a local candidate, so there is no code path from a peer's
-- answer into the local vote table. The test for that is
-- TestForeignEvidenceCannotReachLocalVotePath.

-- name: CreateForeignCandidate :one
INSERT INTO identification_foreign_candidates
    (query_id, peer_id, entity_type, remote_entity_id, remote_entity_name, remote_vote_count)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (query_id, peer_id, entity_type, remote_entity_id) DO UPDATE
SET remote_entity_name = EXCLUDED.remote_entity_name,
    remote_vote_count  = EXCLUDED.remote_vote_count,
    fetched_at         = now()
RETURNING *;
--
-- ON CONFLICT DO UPDATE rather than DO NOTHING, because re-asking a peer is
-- normal (the same query goes out on the next broadcast) and a peer's answer
-- can legitimately have moved: more of its users may now agree. DO NOTHING
-- would keep the first answer forever, which is the stale-evidence problem
-- SPEC F3 exists to prevent -- just on a single row instead of a whole query.

-- name: ListForeignCandidatesByQuery :many
SELECT * FROM identification_foreign_candidates
WHERE query_id = $1
ORDER BY peer_id, remote_vote_count DESC, remote_entity_name;

-- name: GetForeignCandidate :one
SELECT * FROM identification_foreign_candidates WHERE id = $1;

-- name: DeleteForeignCandidatesByQuery :exec
-- Used when a query is resolved or abandoned, so a peer cannot keep answering a
-- question that no longer exists.
DELETE FROM identification_foreign_candidates WHERE query_id = $1;

-- name: DeleteForeignCandidatesByPeer :exec
-- Used when a peer is removed from the registry. Without this, deleting a peer
-- would either fail on the FK or leave orphaned evidence pointing at a peer the
-- operator believes they have removed.
DELETE FROM identification_foreign_candidates WHERE peer_id = $1;

-- name: CountForeignCandidatesByQuery :one
SELECT count(*) FROM identification_foreign_candidates WHERE query_id = $1;
