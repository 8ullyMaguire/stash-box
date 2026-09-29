-- Webhook queries (SPEC §7.11, phase 3 step 3).

-- name: CreateWebhookEndpoint :one
INSERT INTO webhook_endpoints (id, user_id, secret_hash, target_url, event_types)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: FindWebhookEndpoint :one
SELECT * FROM webhook_endpoints WHERE id = $1;

-- name: FindWebhookEndpointByUser :one
-- Scoped by user on purpose. An endpoint id is a UUID and a GraphQL client cannot
-- be assumed to pass the caller's own, so a lookup that does not check ownership
-- is a way to read another user's webhook configuration -- including the target
-- URL, which is often an internal address.
SELECT * FROM webhook_endpoints WHERE id = $1 AND user_id = $2;

-- name: ListWebhookEndpoints :many
SELECT * FROM webhook_endpoints
WHERE user_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: SetWebhookEndpointDisabled :one
UPDATE webhook_endpoints SET disabled = $2, updated_at = now() WHERE id = $1
RETURNING *;

-- name: RotateWebhookSecret :one
-- Replaces the hash. The OLD secret stops working immediately, which is the point:
-- a rotation is what a user does after suspecting a leak, and a rotation that
-- left the old secret valid would give no protection at all.
UPDATE webhook_endpoints SET secret_hash = $2, updated_at = now() WHERE id = $1
RETURNING *;

-- name: DeleteWebhookEndpoint :one
-- Returns the row so the caller can report WHICH endpoint was removed.
DELETE FROM webhook_endpoints WHERE id = $1 AND user_id = $2 RETURNING *;

-- name: ListWebhookEndpointsForEvent :many
-- The dispatch set: live endpoints subscribed to this event type.
--
-- The overlap operator again, and the reason is the same as the directory's array
-- filters: an endpoint subscribed to ["scene.added", "scene.updated"] wants
-- scene.added, and containment (@>) would require an exact list match.
SELECT * FROM webhook_endpoints
WHERE NOT disabled AND event_types && ARRAY[$1]::TEXT[];

-- name: CreateWebhookDelivery :one
INSERT INTO webhook_deliveries (id, endpoint_id, event_type, payload)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListDueWebhookDeliveries :many
-- The queue read: pending, due, oldest first.
--
-- `FOR UPDATE SKIP LOCKED` and NOT a plain SELECT. Two dispatchers running at once
-- -- a second instance, or an overlapping tick -- must not both claim the same
-- row, or the same event is delivered twice. SKIP LOCKED makes the second one step
-- over the locked row and take the next, which turns "two dispatchers" into "one
-- dispatcher and one slightly behind", with no coordination and no failure.
--
-- A plain SELECT here is the single most common way a queue double-delivers, and
-- the symptom -- a consumer seeing every event twice -- is usually blamed on the
-- consumer.
SELECT * FROM webhook_deliveries
WHERE delivered_at IS NULL AND next_attempt_at <= $1
ORDER BY next_attempt_at ASC
LIMIT $2
FOR UPDATE SKIP LOCKED;

-- name: MarkWebhookDelivered :one
UPDATE webhook_deliveries
SET delivered_at = now(), last_error = NULL
WHERE id = $1
RETURNING *;

-- name: MarkWebhookFailed :one
-- Records a failed attempt and schedules the next one.
--
-- next_attempt_at is computed by the service and passed in, not by the database.
-- The backoff schedule is a POLICY -- changing it should not be a migration --
-- and the service is where the policy lives. The database's job is only to store
-- when.
UPDATE webhook_deliveries
SET attempt = $2, next_attempt_at = $3, last_error = $4
WHERE id = $1
RETURNING *;

-- name: AbandonWebhookDelivery :exec
-- Gives up permanently. The row is KEPT, not deleted, because "this endpoint
-- failed four times and was abandoned" is the answer to the question a user asks
-- when their integration silently stopped working.
UPDATE webhook_deliveries
SET last_error = $2
WHERE id = $1 AND delivered_at IS NULL;

-- name: ListWebhookDeliveriesForEndpoint :many
-- A user's delivery history for one endpoint, newest first.
SELECT * FROM webhook_deliveries
WHERE endpoint_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountWebhookDeliveries :one
-- How many are still pending for an endpoint. Shown on the settings page so a user
-- can see their queue is backing up BEFORE deliveries start failing.
SELECT count(*)::int FROM webhook_deliveries
WHERE endpoint_id = $1 AND delivered_at IS NULL;
