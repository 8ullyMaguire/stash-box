-- Trust level queries (SPEC §6, migration 76).
--
-- trust_events is the source of truth; user_trust is a denormalised rollup of
-- it. These queries keep the rollup in step with the events, and the two
-- upserts below are the only places in the codebase allowed to write either
-- table.

-- name: GetUserTrust :one
SELECT * FROM user_trust WHERE user_id = $1;

-- name: GetUserTrustByUserIDs :many
SELECT * FROM user_trust WHERE user_id = ANY($1::UUID[]);

-- name: RecordTrustEvent :one
-- ON CONFLICT DO NOTHING makes a retried or duplicated event a no-op rather
-- than a silent double increment. The matching partial state is a real
-- concern: an edit can be applied twice by a retried request, and without this
-- the contributor's trust would grow twice for one contribution.
--
-- Returning the row means a duplicate insert reports no rows, which the
-- service treats as "already recorded" rather than as an error.
INSERT INTO trust_events (user_id, kind, entity_type, entity_id, delta)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT DO NOTHING
RETURNING *;

-- name: ApplyTrustEvent :one
-- Applies one event's effect to the rollup, creating the row if absent.
--
-- The totals are incremented rather than recomputed by replaying events, so
-- recording a trust event is O(1) regardless of how much history a user has.
-- The signed delta works for both directions: a rejected edit decrements the
-- same column an approval incremented.
--
-- ON CONFLICT (user_id) DO UPDATE is required because a user with no rollup row
-- yet (every new user) has to be created on first event.
-- The per-kind terms are the SIGNED DELTA, not a literal 1.
--
-- They used to be literal 1, which is why every caller passed Delta: 1 and the
-- column could only ever count whole contributions. A bounty needs its magnitude
-- to reach bonus_points, and a reversal needs -1 to come back out, so the
-- magnitude cannot live in the caller and be ignored here.
--
-- Rejected edits are already the sign-flipped term (-delta), which is the one
-- place the sign convention differs: 'edit_rejected' moves rejected_edits, and a
-- REVERSAL of a rejection moves it back.
INSERT INTO user_trust (user_id, approved_edits, rejected_edits, identification_solves, quests_completed, replicas_hosted, bonus_points, updated_at)
VALUES (
    $1,
    CASE WHEN sqlc.arg(event_kind)::TEXT = 'edit_approved' THEN sqlc.arg(event_delta)::INTEGER ELSE 0 END,
    CASE WHEN sqlc.arg(event_kind)::TEXT = 'edit_rejected' THEN sqlc.arg(event_delta)::INTEGER ELSE 0 END,
    CASE WHEN sqlc.arg(event_kind)::TEXT = 'identification_solved' THEN sqlc.arg(event_delta)::INTEGER ELSE 0 END,
    CASE WHEN sqlc.arg(event_kind)::TEXT = 'quest_completed' THEN sqlc.arg(event_delta)::INTEGER ELSE 0 END,
    CASE WHEN sqlc.arg(event_kind)::TEXT = 'replica_hosted' THEN sqlc.arg(event_delta)::INTEGER ELSE 0 END,
    -- A points-ONLY kind: bounty_bonus moves bonus_points and no count at all.
    -- That is the whole reason it is a separate kind -- folding it into
    -- quests_completed would credit 500 completed quests for one bounty.
    CASE WHEN sqlc.arg(event_kind)::TEXT = 'bounty_bonus' THEN sqlc.arg(event_delta)::INTEGER ELSE 0 END,
    NOW()
)
ON CONFLICT (user_id) DO UPDATE SET
    approved_edits = user_trust.approved_edits + EXCLUDED.approved_edits,
    rejected_edits = user_trust.rejected_edits + EXCLUDED.rejected_edits,
    identification_solves = user_trust.identification_solves + EXCLUDED.identification_solves,
    quests_completed = user_trust.quests_completed + EXCLUDED.quests_completed,
    replicas_hosted = user_trust.replicas_hosted + EXCLUDED.replicas_hosted,
    bonus_points = user_trust.bonus_points + EXCLUDED.bonus_points,
    updated_at = NOW()
RETURNING *;

-- name: RecomputeUserTrustTotals :one
-- Rebuilds the totals from the event log.
--
-- This is the recovery path, and the reason trust_events is append-only: if the
-- rollup ever drifts -- a threshold change, a bug in ApplyTrustEvent, a manual
-- database edit -- the truth is still replayable. Coalescing SUMs the signed
-- deltas per kind in one pass rather than replaying row by row.
INSERT INTO user_trust (user_id, approved_edits, rejected_edits, identification_solves, quests_completed, replicas_hosted, bonus_points, updated_at)
SELECT
    $1,
    COALESCE(SUM(CASE WHEN kind = 'edit_approved' THEN delta ELSE 0 END), 0),
    COALESCE(SUM(CASE WHEN kind = 'edit_rejected' THEN -delta ELSE 0 END), 0),
    COALESCE(SUM(CASE WHEN kind = 'identification_solved' THEN delta ELSE 0 END), 0),
    COALESCE(SUM(CASE WHEN kind = 'quest_completed' THEN delta ELSE 0 END), 0),
    COALESCE(SUM(CASE WHEN kind = 'replica_hosted' THEN delta ELSE 0 END), 0),
    COALESCE(SUM(CASE WHEN kind = 'bounty_bonus' THEN delta ELSE 0 END), 0),
    NOW()
FROM trust_events
WHERE user_id = $1
-- No GROUP BY: an aggregate over an empty set still returns exactly one row,
-- with the COALESCE defaults above. GROUP BY made this return NOTHING for a
-- user with no events, which made it unusable for creating a zeroed rollup --
-- and that is exactly what SetContentViewingOptIn needs to do for a user
-- opting in before they are eligible.
ON CONFLICT (user_id) DO UPDATE SET
    approved_edits = EXCLUDED.approved_edits,
    rejected_edits = EXCLUDED.rejected_edits,
    identification_solves = EXCLUDED.identification_solves,
    quests_completed = EXCLUDED.quests_completed,
    replicas_hosted = EXCLUDED.replicas_hosted,
    bonus_points = EXCLUDED.bonus_points,
    updated_at = NOW()
RETURNING *;

-- name: SetUserTrustLevel :one
-- The level is written by the service after deriving it from the thresholds,
-- never by the database. The curve is a product decision and belongs in Go,
-- where it can be changed without a migration.
UPDATE user_trust
SET level = $2, updated_at = NOW()
WHERE user_id = $1
RETURNING *;

-- name: SetContentViewingOptIn :one
-- The one user-writable field in this table (SPEC §6: high-trust users
-- EXPLICITLY opt in to viewing content).
--
-- It is deliberately NOT gated on level here. Eligibility (level >= 4) is
-- derived at read time by the service, because a stored eligibility would go
-- stale the moment a threshold changed; a stored opt-in is just the user's
-- own choice and is safe to keep.
UPDATE user_trust
SET content_viewing_opt_in = $2, updated_at = NOW()
WHERE user_id = $1
RETURNING *;

-- name: ListUserTrustEvents :many
-- One user's history, newest first. Used by the audit view and by tests that
-- assert the event log is the source of truth.
SELECT * FROM trust_events
WHERE user_id = $1
ORDER BY created_at DESC, id DESC;

-- name: CountUserTrustEvents :one
SELECT count(*) FROM trust_events WHERE user_id = $1;

-- name: GetAllUserTrust :many
-- Every rollup row. Used when a threshold changes and every level must be
-- recomputed (see RebuildLevels in internal/service/trust).
SELECT * FROM user_trust;
