-- name: CreateList :one
-- A new list, PRIVATE by construction.
--
-- `published_at` and `published_by` are absent from this statement entirely rather than
-- set to NULL. A create that mentioned them would be a create that could publish, and the
-- policy says publication is a separate, deliberate, auditable act -- so the way to make
-- that structural is for the INSERT to have no column to publish through.
INSERT INTO lists (id, owner_id, name, description, owner_name)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: FindList :one
SELECT * FROM lists WHERE id = $1;

-- name: FindListByName :one
-- Lookup for the "you already have a list called this" check. Scoped to the owner because
-- names are unique per owner, not globally.
SELECT * FROM lists WHERE owner_id = $1 AND name = $2;

-- name: UpdateList :one
-- Rename / re-describe. NOT re-parenting and NOT publishing: publishing is its own
-- statement so it can carry an audit row, and it takes an actor argument.
UPDATE lists SET name = $2, description = $3, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteList :exec
DELETE FROM lists WHERE id = $1;

-- name: PublishList :one
-- Publish, recording WHEN and WHO in the same statement.
--
-- One statement rather than an UPDATE followed by an INSERT into list_audit, because the
-- two must agree: a publication without its audit row is exactly the state the policy
-- forbids, and splitting the write makes that state reachable if the second statement
-- fails.
UPDATE lists SET published_at = now(), published_by = $2, updated_at = now()
WHERE id = $1 AND published_at IS NULL
RETURNING *;

-- name: UnpublishList :one
-- Return to private. The `published_at IS NOT NULL` guard means unpublishing something
-- already private is a no-op returning no rows, so a caller cannot fabricate an
-- unpublish event for a list that was never published.
UPDATE lists SET published_at = NULL, published_by = NULL, updated_at = now()
WHERE id = $1 AND published_at IS NOT NULL
RETURNING *;

-- name: RecordListAudit :exec
INSERT INTO list_audit (list_id, actor_id, action) VALUES ($1, $2, $3);

-- name: FindListAudit :many
-- A list's publication history, newest first.
SELECT * FROM list_audit WHERE list_id = $1 ORDER BY created_at DESC, id DESC;

-- name: FindPublishedLists :many
-- The browse listing: PUBLISHED lists only, newest publication first.
--
-- The WHERE is not decoration. It mirrors the partial index, so the query is answered by
-- the index alone; a draft is not merely excluded from the result, it is not in the index.
SELECT * FROM lists
 WHERE published_at IS NOT NULL
 ORDER BY published_at DESC, id DESC
 LIMIT $1 OFFSET $2;

-- name: CountPublishedLists :one
SELECT count(*) FROM lists WHERE published_at IS NOT NULL;

-- name: FindListsByOwner :many
-- Everything an owner has, drafts included. This is the one listing that shows private
-- lists, and it is reachable only by someone entitled to see that owner's drafts.
SELECT * FROM lists WHERE owner_id = $1 ORDER BY name ASC;

-- name: FindListsByIds :many
-- Batch lookup by id, used to resolve the lists a caller may see.
--
-- Deliberately NOT filtered on published_at. Visibility is decided by the caller with the
-- full row in hand -- a draft is returned with its NULL published_at so the caller can tell
-- "private" from "does not exist" -- and a filter here would erase that distinction.
SELECT * FROM lists WHERE id = ANY($1::UUID[]);

-- name: AddListItem :one
INSERT INTO list_items (list_id, entity_type, entity_id, position)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: RemoveListItem :exec
DELETE FROM list_items WHERE id = $1;

-- name: FindListItems :many
-- A list's contents in display order.
--
-- `position` then `id`: position is client-supplied and ties are common (every item added
-- in one bulk request gets the same position), so id breaks them deterministically. Without
-- the tiebreak, two clients paginating the same list can see different orders.
SELECT * FROM list_items WHERE list_id = $1 ORDER BY position ASC, id ASC;

-- name: RemoveListItemsByEntity :exec
DELETE FROM list_items WHERE list_id = $1 AND entity_id = $2;

-- name: ReorderListItems :exec
-- Set an explicit position for one item. Reordering is one row at a time rather than a bulk
-- swap, because a list has no version column and a concurrent reorder would otherwise be
-- last-write-wins across the whole list with no way to detect it.
UPDATE list_items SET position = $2 WHERE id = $1;

-- name: CountListItems :one
SELECT count(*) FROM list_items WHERE list_id = $1;