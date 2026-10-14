-- Session state: "where I left off" (discovery SPEC section 11, aggregator phase 7 slice 7).
--
-- THE SERVER HOLDS A CURSOR ONLY WHEN THE USER OPTED IN. The rule lives in the table's CHECK
-- (migration 116): a `local` row has no payload, and this file never tries to give it one -- the
-- upsert below writes NULL for a local write, whatever the client sent, so a client cannot make
-- the server store what it said it was keeping to itself.

-- name: UpsertSessionState :exec
-- The cursor is written ONLY for a synced row. `CASE WHEN ... THEN $4::jsonb END` rather than a
-- service-side branch, because the schema's CHECK would reject the other shape anyway: the two
-- places agree, and the one that runs is the one that decides.
INSERT INTO session_states ("user_id", "kind", "key", "cursor", "storage", "updated_at")
VALUES (
    sqlc.arg(user_id),
    sqlc.arg(kind),
    sqlc.arg(key),
    CASE WHEN sqlc.arg(storage)::text = 'synced' THEN sqlc.arg(cursor)::jsonb END,
    sqlc.arg(storage),
    now()
)
ON CONFLICT ("user_id", "kind", "key") DO UPDATE SET
    "cursor" = EXCLUDED."cursor",
    "storage" = EXCLUDED."storage",
    "updated_at" = now();

-- name: FindSessionState :one
-- A SYNCED cursor, addressed by what it is for. A `local` row is skipped by the predicate rather
-- than filtered afterwards, so there is no code path in which the payload is read and then
-- discarded: "nothing reads it unless the user turned it on" is this WHERE clause.
SELECT "user_id", "kind", "key", "cursor", "storage", "updated_at"
FROM session_states
WHERE "user_id" = sqlc.arg(user_id)
  AND "kind" = sqlc.arg(kind)
  AND "key" = sqlc.arg(key)
  AND "storage" = 'synced';

-- name: FindSessionStatesByKind :many
-- Everything this user syncs for one kind of surface: what a second device reads on arrival, so
-- it does not need one request per key.
SELECT "user_id", "kind", "key", "cursor", "storage", "updated_at"
FROM session_states
WHERE "user_id" = sqlc.arg(user_id)
  AND "kind" = sqlc.arg(kind)
  AND "storage" = 'synced'
ORDER BY "key";

-- name: DeleteSessionState :execrows
-- Explicit, because there is no expiry (migration 116 says why). Deleting a row that does not
-- exist is not an error; the caller learns whether anything was there from the row count.
DELETE FROM session_states
WHERE "user_id" = sqlc.arg(user_id) AND "kind" = sqlc.arg(kind) AND "key" = sqlc.arg(key);
