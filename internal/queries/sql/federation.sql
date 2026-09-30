-- Federation peer registry queries.
--
-- Read-side only. The federation service owns creating and deleting peers; this
-- file is what it reads through, and nothing outside the federation package
-- should import these -- a peer list is an operator surface, not a public one.

-- name: CreateFederationPeer :one
INSERT INTO federation_peers (name, base_url, instance_id, trust_weight, enabled)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: UpdateFederationPeer :one
UPDATE federation_peers
SET name = $2,
    base_url = $3,
    trust_weight = $4,
    enabled = $5,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteFederationPeer :exec
DELETE FROM federation_peers WHERE id = $1;

-- name: GetFederationPeer :one
SELECT * FROM federation_peers WHERE id = $1;

-- name: GetFederationPeerByInstanceId :one
-- The lookup that makes a reply attributable: an inbound answer is matched to
-- a peer by the instance id it declares, so an unknown instance is rejected
-- rather than silently recorded against nobody.
SELECT * FROM federation_peers WHERE instance_id = $1;

-- name: ListFederationPeers :many
-- Operator surface, so ordering is by name rather than by id: a list whose
-- order changes between calls is a list nobody can scan.
SELECT * FROM federation_peers ORDER BY name;

-- name: ListEnabledFederationPeers :many
-- The candidate set for a broadcast. Filters `enabled` in SQL rather than in Go
-- so the broadcast path cannot accidentally read a disabled peer.
SELECT * FROM federation_peers WHERE enabled = TRUE ORDER BY name;

-- name: ListStaleFederationPeers :many
-- Peers that have not been seen inside the cutoff, for the operator's stale
-- list. Never-contacted peers have a NULL last_seen_at and are returned by
-- `last_seen_at IS NULL OR last_seen_at < $1` -- an unknown peer and a peer
-- that has gone quiet are both "do not ask", but they are different facts and
-- the operator surface shows them differently.
SELECT * FROM federation_peers
WHERE last_seen_at IS NULL OR last_seen_at < $1
ORDER BY last_seen_at NULLS FIRST;

-- name: TouchFederationPeer :exec
-- Records a successful contact. Separate from UpdateFederationPeer so a
-- successful fetch cannot be confused with an operator edit, and so the
-- updated_at churn of an edit does not look like liveness.
UPDATE federation_peers SET last_seen_at = now(), updated_at = now() WHERE id = $1;
