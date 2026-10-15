-- Saved searches. Every read that a user can reach is scoped by user_id in the WHERE clause:
-- a lookup that is able to return somebody else's row is a leak waiting for one caller to
-- forget the check, and the check belongs in the query that cannot be called without it.

-- name: FindSavedSearchesByUser :many
SELECT * FROM saved_searches WHERE user_id = $1 ORDER BY updated_at DESC;

-- name: FindSavedSearchForUser :one
SELECT * FROM saved_searches WHERE id = $1 AND user_id = $2;

-- name: FindSavedSearchByUserAndName :one
SELECT * FROM saved_searches WHERE user_id = $1 AND name = $2;

-- name: CreateSavedSearch :one
INSERT INTO saved_searches (user_id, name, query, notify)
VALUES (@user_id, @name, @query, @notify)
RETURNING *;

-- name: UpdateSavedSearch :one
UPDATE saved_searches
SET name = @name, query = @query, notify = @notify, updated_at = now()
WHERE id = @id AND user_id = @user_id
RETURNING *;

-- name: DeleteSavedSearch :execrows
DELETE FROM saved_searches WHERE id = @id AND user_id = @user_id;

-- The notification job's read. Ordered oldest-updated first so that a run which cannot finish
-- still makes progress on a stable order rather than restarting at the same rows.
-- name: FindNotifyingSavedSearches :many
SELECT * FROM saved_searches WHERE notify ORDER BY updated_at, id;

-- name: SetSavedSearchNotifiedAt :exec
UPDATE saved_searches SET last_notified_at = @last_notified_at WHERE id = @id;

-- The database's OWN clock, in the same terms as the column the cursor is compared against
-- (`scenes.created_at`, a `timestamp` stamped by `now()`).
--
-- The cursor must come from this clock and not from the Go process. `timestamp` has no zone, so
-- a process in +02:00 writing its local wall clock stores two hours ahead of the rows it will be
-- compared with: every run finds nothing, forever, with no error anywhere. Measured, not
-- assumed -- the first version of the notification job had exactly that bug and the integration
-- test went red on it.
-- name: SavedSearchNow :one
SELECT now()::timestamp;

-- The feed token (plan slice 13b). Only the SHA-256 is ever written, and the WHERE clauses carry
-- user_id for the same reason every other write here does: a token is minted for one member's search,
-- and a statement that could touch somebody else's row is a statement one caller away from doing it.

-- name: SetSavedSearchFeedToken :execrows
UPDATE saved_searches SET feed_token_hash = @feed_token_hash, updated_at = now()
WHERE id = @id AND user_id = @user_id;

-- name: ClearSavedSearchFeedToken :execrows
UPDATE saved_searches SET feed_token_hash = NULL, updated_at = now()
WHERE id = @id AND user_id = @user_id;

-- The feed's ONLY lookup, and it is the token's hash that identifies the search. A JOIN rather than a
-- second round trip: the request has to run the search AS ITS OWNER (the filter can depend on who is
-- asking), so the owner's id and name come back with the row that named them.
-- name: FindSavedSearchByFeedToken :one
SELECT saved_searches.*, users.name AS owner_name
FROM saved_searches
JOIN users ON users.id = saved_searches.user_id
WHERE saved_searches.feed_token_hash = @feed_token_hash;

-- Notifications a saved search produced, found by the payload they carry. Deleting them with
-- the search is not cosmetic: the notification list renders every unread row it returns, and a
-- row naming a search that no longer exists has nothing to render.
-- name: DeleteSavedSearchMatchNotifications :exec
DELETE FROM notifications
WHERE type = 'SAVED_SEARCH_MATCH'
  AND data ->> 'saved_search_id' = @saved_search_id::text;
