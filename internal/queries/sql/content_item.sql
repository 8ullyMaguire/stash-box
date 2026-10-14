-- Content items (aggregator phase 3). The data layer for the five non-video kinds.
--
-- Every read here filters `deleted = false` rather than leaving it to the caller. A soft delete
-- that one query forgets is a row that appears on one page and not another, and the omission is
-- invisible in review -- the same reason migration 117 put the column in the table now instead of
-- adding it later.

-- name: CreateContentItem :one
INSERT INTO "content_items" ("id", "kind", "title", "details", "release_date")
VALUES (@id, @kind, @title, @details, @release_date)
RETURNING *;

-- name: GetContentItem :one
SELECT * FROM "content_items" WHERE "id" = @id AND "deleted" = false;

-- name: GetContentItemIncludingDeleted :one
-- For moderation and for the audit surfaces: "this item was removed" is information a reader of
-- the audit log needs, and the filtered read above cannot answer it.
SELECT * FROM "content_items" WHERE "id" = @id;

-- name: ListContentItems :many
-- Newest first, and `created_at` rather than `release_date` for the reason phase 7 slice 6
-- recorded: a pool ordered by a nullable field never contains the rows that lack it.
SELECT * FROM "content_items"
WHERE "deleted" = false
  AND (sqlc.narg('kind')::varchar IS NULL OR "kind" = sqlc.narg('kind')::varchar)
ORDER BY "created_at" DESC, "id"
LIMIT @row_limit;

-- name: ListContentItemsForTag :many
-- The tag page's direction: items carrying one tag, newest first. Both directions of the link
-- matter and only one of them is covered by the unique index.
SELECT ci.* FROM "content_items" ci
JOIN "content_item_tags" cit ON cit."content_item_id" = ci."id"
WHERE cit."tag_id" = @tag_id AND ci."deleted" = false
ORDER BY ci."created_at" DESC, ci."id"
LIMIT @row_limit;

-- name: UpdateContentItem :one
-- A FULL update of the item's own columns, not a patch: the edit's apply already merged the
-- change into a model (see models.ContentItem.CopyFromContentItemEdit), so a statement that
-- took partial fields would be a second place where "leave this alone" is decided -- and the
-- two would disagree the first time a field is cleared.
UPDATE "content_items"
SET "kind" = @kind,
    "title" = @title,
    "details" = @details,
    "release_date" = @release_date,
    "updated_at" = now()
WHERE "id" = @id AND "deleted" = false
RETURNING *;

-- name: SoftDeleteContentItem :exec
UPDATE "content_items" SET "deleted" = true, "updated_at" = now() WHERE "id" = @id;

-- name: LinkContentItemTag :exec
INSERT INTO "content_item_tags" ("content_item_id", "tag_id")
VALUES (@content_item_id, @tag_id)
ON CONFLICT DO NOTHING;

-- name: UnlinkContentItemTag :exec
DELETE FROM "content_item_tags"
WHERE "content_item_id" = @content_item_id AND "tag_id" = @tag_id;

-- name: ListContentItemTags :many
SELECT t.* FROM "tags" t
JOIN "content_item_tags" cit ON cit."tag_id" = t."id"
WHERE cit."content_item_id" = @content_item_id
ORDER BY t."name";

-- name: CreateContentItemChapter :one
INSERT INTO "content_item_chapters" ("id", "content_item_id", "number", "title", "page_count", "duration_seconds")
VALUES (@id, @content_item_id, @number, @title, @page_count, @duration_seconds)
RETURNING *;

-- name: ListContentItemChapters :many
SELECT * FROM "content_item_chapters"
WHERE "content_item_id" = @content_item_id
ORDER BY "number";

-- name: CreateContentItemMedia :one
INSERT INTO "content_item_media" ("id", "content_item_id", "url", "kind", "width", "height", "duration_seconds")
VALUES (@id, @content_item_id, @url, @kind, @width, @height, @duration_seconds)
RETURNING *;

-- name: ListContentItemMedia :many
SELECT * FROM "content_item_media"
WHERE "content_item_id" = @content_item_id
-- Ordered by what the URL IS, not alphabetically: the `original` is the item itself and
-- a client renders it as the item, so it has to come first, and alphabetical order puts
-- `archive` there instead. Ties break on the url so the order is total -- a page whose
-- order depends on the plan is a page that reorders itself between requests.
ORDER BY CASE "kind"
    WHEN 'original' THEN 0
    WHEN 'preview' THEN 1
    WHEN 'stream' THEN 2
    WHEN 'download' THEN 3
    WHEN 'archive' THEN 4
    ELSE 5
  END, "url";
