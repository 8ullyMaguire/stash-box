-- User queries

-- name: CreateUser :one
INSERT INTO users (id, name, password_hash, email, api_key, api_calls, invite_tokens, invited_by, last_api_call, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW(), NOW(), NOW())
RETURNING *;

-- name: UpdateUser :one
UPDATE users 
SET name = $2, password_hash = $3, email = $4, updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = $1;

-- name: FindUser :one
SELECT * FROM users WHERE id = $1;

-- name: GetUsers :many
SELECT * FROM users WHERE id = ANY($1::UUID[]);

-- name: FindUserWithRoles :one
SELECT sqlc.embed(users),
  ARRAY(SELECT role FROM user_roles WHERE user_id = users.id)::TEXT[] AS roles
FROM users WHERE users.id = $1;

-- name: FindUserByName :one
SELECT * FROM users WHERE UPPER(name) = UPPER(sqlc.arg(name)::text);

-- name: FindUserByEmail :one
SELECT * FROM users WHERE UPPER(email) = UPPER($1);

-- name: CountUsers :one
SELECT COUNT(*) FROM users;

-- name: UpdateUserAPIKey :exec
UPDATE users
SET api_key = $2, updated_at = NOW()
WHERE id = $1;

-- name: UpdateUserPassword :exec
UPDATE users
SET password_hash = $2, updated_at = NOW()
WHERE id = $1;

-- name: GetUserInvitedBy :one
-- Who invited this member, for the referral payment (plan phase 11 slice 11b).
--
-- ONE COLUMN, deliberately: the payment needs exactly this fact, and reading a whole user row
-- to get it would make the referral depend on the shape of a row that belongs to another
-- service -- the coupling that makes a column rename break a package meant to know nothing
-- about it.
--
-- NULL is the normal answer (an account an operator created has no inviter) and it is a
-- DIFFERENT answer from "invited by a named member", so it is read as a nullable uuid rather
-- than coalesced away. The column is ON DELETE SET NULL, so a deleted referrer is an absent
-- one: there is nobody left to pay, which is the truth rather than an error.
SELECT invited_by FROM users WHERE id = $1;

-- name: UpdateUserInviteTokenCount :exec
UPDATE users
SET invite_tokens = $2
WHERE id = $1;

-- name: UpdateUserEmail :exec
UPDATE users
SET email = $2, updated_at = NOW()
WHERE id = $1;

-- User roles

-- name: CreateUserRoles :copyfrom
INSERT INTO user_roles (user_id, role) VALUES ($1, $2);

-- name: DeleteUserRoles :exec
DELETE FROM user_roles WHERE user_id = $1;

-- name: GetUserRoles :many
SELECT role FROM user_roles WHERE user_id = $1;

-- name: GetUserRolesByUserIDs :many
SELECT user_id, role FROM user_roles WHERE user_id = ANY($1::UUID[]);

-- name: CountVotesByType :many
SELECT vote, COUNT(*) as count FROM edit_votes WHERE user_id = $1 GROUP BY vote;

-- name: CountUserEditsByStatus :many
SELECT status, bot, COUNT(*) as count FROM edits WHERE user_id = $1 GROUP BY status, bot;

-- name: GetUserNotificationSubscriptions :many
SELECT type FROM user_notifications WHERE user_id = $1;

-- name: RecordVouch :one
-- Records an accepted vouch. ON CONFLICT DO NOTHING makes a second attempt against
-- the partial unique index report no rows, which the service reads as "already
-- vouched" rather than as an error -- the same idiom RecordTrustEvent uses.
INSERT INTO member_vouches (id, voucher_id, target_id, expires_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT DO NOTHING
RETURNING *;

-- name: ListActiveVouchesForTarget :many
-- A target's vouches that are neither withdrawn nor expired, newest first.
SELECT * FROM member_vouches
WHERE target_id = $1 AND withdrawn_at IS NULL AND expires_at > $2
ORDER BY created_at DESC;

-- name: CountActiveVouchesForTarget :one
-- The cap's instrument. Counts exactly what the list above returns, so a refusal
-- and a displayed list cannot disagree about how full a target is.
SELECT count(*) FROM member_vouches
WHERE target_id = $1 AND withdrawn_at IS NULL AND expires_at > $2;

-- name: ListVouchesForTarget :many
-- Every vouch a target has ever held, active or not, newest first. The audit view:
-- withdrawn vouches are part of the record, not noise.
SELECT * FROM member_vouches WHERE target_id = $1 ORDER BY created_at DESC;

-- name: WithdrawVouch :one
-- The voucher ends their own vouch, which frees the slot it occupied.
--
-- Scoped to the voucher's own row: withdrawing somebody else's vouch is not a
-- thing a member can do, and a query that took only the vouch id would make it one.
UPDATE member_vouches
SET withdrawn_at = $3
WHERE voucher_id = $1 AND target_id = $2 AND withdrawn_at IS NULL
RETURNING *;

-- name: LockUserForVouch :one
-- Takes a row lock on the member being vouched for, for the length of the
-- transaction that checks and records a vouch.
--
-- The cap is "at most three ACTIVE", and a count-then-insert without a lock can be
-- raced by two simultaneous vouches, each seeing two and both inserting. A unique
-- index cannot express "at most three", so the serialisation has to come from
-- somewhere: this is the somewhere, and it is a single row lock rather than a
-- table lock because the contention is per member by construction.
SELECT id FROM users WHERE id = $1 FOR UPDATE;
