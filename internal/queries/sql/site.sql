-- Site queries

-- name: CreateSite :one
INSERT INTO sites (id, name, description, url, regex, valid_types, category_id, highlighted, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now(), now())
RETURNING *;

-- name: UpdateSite :one
UPDATE sites
SET name = $2, description = $3, url = $4, regex = $5, valid_types = $6, category_id = $7, highlighted = $8, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteSite :exec
DELETE FROM sites WHERE id = $1;

-- name: GetSite :one
SELECT * FROM sites WHERE id = $1;

-- name: FindSitesByIds :many
SELECT * FROM sites WHERE id = ANY($1::UUID[]);

-- name: GetSitesByName :many
-- Case-insensitive, matching the Find<entity>ByName convention used for tags,
-- studios and performers. Needed to resolve an id for a site that already
-- exists: site name carries a unique index, so a name lookup is sufficient and
-- avoids inventing a uuid. Returns a slice rather than a single row so a
-- duplicated name (only possible if the index is absent) degrades to "take the
-- first" instead of erroring the caller.
SELECT * FROM sites WHERE UPPER(name) = UPPER($1);
