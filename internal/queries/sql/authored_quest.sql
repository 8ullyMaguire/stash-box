-- Authored quests, bounties and claiming (SPEC §7.7).
--
-- GENERATED quests (internal/service/quest) are a pure function of the archive and
-- deliberately never stored. These are the AUTHORED ones: a quest a person
-- promised, carrying a bounty a generator must not be able to manufacture.

-- name: CreateAuthoredQuest :one
INSERT INTO authored_quests (id, entity_type, field, target, bounty_points, reason, authored_by, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: AddAuthoredQuestItem :one
INSERT INTO authored_quest_items (id, quest_id, entity_type, entity_id)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: FindAuthoredQuest :one
SELECT * FROM authored_quests WHERE id = $1;

-- name: FindAuthoredQuestByAuthor :many
SELECT * FROM authored_quests
WHERE authored_by = $1
ORDER BY created_at DESC;

-- The quest board. Active quests only, because an expired quest is not a quest
-- anybody acts on, and the count of expired ones grows forever.
--
-- The expiry filter lives HERE rather than in the client so "active" has one
-- definition. A client-side expiry filter and a server-side one disagree the
-- moment a clock is involved.
-- name: FindActiveAuthoredQuests :many
SELECT * FROM authored_quests
WHERE expires_at IS NULL OR expires_at > now()
ORDER BY bounty_points DESC, created_at DESC;

-- The items of a quest, with the claim inlined.
--
-- The LEFT JOIN to the claim is the "reconcile on read" the migration comment
-- promises: a filled field does not DELETE the row, it just makes the item
-- stop counting. Keeping the row is what lets the quest show "you did this one",
-- which is most of why a curator comes back.
-- name: FindAuthoredQuestItems :many
SELECT
    i.id,
    i.quest_id,
    i.entity_type,
    i.entity_id,
    i.claimed_by,
    i.claimed_at,
    i.created_at,
    u.name AS claimed_by_name
FROM authored_quest_items i
LEFT JOIN users u ON u.id = i.claimed_by
WHERE i.quest_id = $1
ORDER BY i.claimed_at NULLS FIRST, i.created_at, i.id;

-- THE CLAIM. A guarded UPDATE, never a check-then-write.
--
-- Two curators claiming the last item in a quest must not both get it, and
-- SELECT-then-UPDATE cannot express that: both read "unclaimed", both write. The
-- WHERE claimed_by IS NULL is the whole concurrency control, and it holds the gap
-- shut because the UPDATE takes the row lock before evaluating it.
--
-- An item already claimed by SOMEONE ELSE returns no rows (ErrNoRows), which the
-- service reports as "already claimed" rather than as a failure. An item already
-- claimed by the SAME curator returns the row, so claiming twice is idempotent --
-- a retried request must not tell a curator they lost a race they won.
-- name: ClaimAuthoredQuestItem :one
UPDATE authored_quest_items
SET claimed_by = $2,
    claimed_at = now()
WHERE id = $1
  AND (claimed_by IS NULL OR claimed_by = $2)
RETURNING *;

-- Releasing a claim. Restricted to the claimer's own rows by the WHERE, so one
-- curator cannot release another's work.
-- name: ReleaseAuthoredQuestItem :one
UPDATE authored_quest_items
SET claimed_by = NULL,
    claimed_at = NULL
WHERE id = $1
  AND claimed_by = $2
RETURNING *;

-- Expiring stale claims. A curator who abandoned work releases it themselves;
-- this is the backstop for the ones who did not.
--
-- Scoped by AGE as well as by the cutoff so it cannot strand a recent claim
-- because the batch ran slowly.
-- name: ExpireStaleQuestClaims :many
UPDATE authored_quest_items
SET claimed_by = NULL,
    claimed_at = NULL
WHERE claimed_by IS NOT NULL
  AND claimed_at < $1 - INTERVAL '1 hour'
RETURNING *;

-- A curator's in-progress work, newest claim first. The "what am I working on"
-- list, and the query an operator runs to find claims worth expiring.
-- name: FindClaimsByUser :many
SELECT
    i.id,
    i.quest_id,
    i.entity_type,
    i.entity_id,
    i.claimed_by,
    i.claimed_at,
    q.field,
    q.target,
    q.bounty_points
FROM authored_quest_items i
JOIN authored_quests q ON q.id = i.quest_id
WHERE i.claimed_by = $1
ORDER BY i.claimed_at DESC;

-- "Show me everything known about this entity" -- how an authored quest turns
-- into a canonical link back to the entity's page.
-- name: FindAuthoredQuestItemsByEntity :many
SELECT * FROM authored_quest_items
WHERE entity_type = $1
  AND entity_id = $2;

-- The count of items a quest holds, used to refuse authoring more than the
-- target rather than silently truncating.
-- name: CountAuthoredQuestItems :one
SELECT count(*) FROM authored_quest_items WHERE quest_id = $1;

-- name: DeleteAuthoredQuest :exec
DELETE FROM authored_quests WHERE id = $1;
