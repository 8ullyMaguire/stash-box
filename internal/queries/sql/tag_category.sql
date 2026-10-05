-- Tag category queries

-- name: CreateTagCategory :one
INSERT INTO tag_categories (id, "group", name, description, created_at, updated_at)
VALUES ($1, $2, $3, $4, now(), now())
RETURNING *;

-- name: UpdateTagCategory :one
UPDATE tag_categories 
SET "group" = $2, name = $3, description = $4, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteTagCategory :exec
DELETE FROM tag_categories WHERE id = $1;

-- name: FindTagCategory :one
SELECT * FROM tag_categories WHERE id = $1;

-- name: GetAllTagCategories :many
SELECT * FROM tag_categories ORDER BY name ASC;

-- name: GetTagCategoriesByIds :many
SELECT * FROM tag_categories WHERE id = ANY($1::UUID[]);

-- Nesting queries (growth item 24). Migration 106 added parent_id; these read it.
--
-- EVERY TREE QUERY CARRIES A DEPTH COLUMN, and that is not decoration. A flat list of
-- names cannot draw a hierarchy -- the client cannot tell a child from a grandchild
-- without the distance. Carrying depth also means the client does not have to
-- reconstruct the tree by repeated round trips.
--
-- `depth` starts at 0 for DIRECT children of the requested category. The requested
-- category itself is not included in its own descendants, so a client asking "what is
-- under this?" gets exactly that and no self-reference.

-- name: FindTagCategoryChildren :many
-- Direct children only. One level, for a tree that loads lazily as the user expands.
SELECT tc.*, 0 AS depth
FROM tag_categories tc
WHERE tc.parent_id = $1
ORDER BY tc.name ASC;

-- name: FindTagCategoryDescendants :many
-- Everything below a category, at any depth.
--
-- The cycle guard in migration 106 is what makes this terminate. Without it a cycle
-- makes this CTE recurse until the database runs out of stack, and there is no cycle
-- here to detect at read time because the data would already be corrupt.
WITH RECURSIVE tree AS (
    SELECT tc.*, 0 AS depth
      FROM tag_categories tc
     WHERE tc.parent_id = $1
    UNION ALL
    SELECT tc.*, t.depth + 1
      FROM tag_categories tc
      JOIN tree t ON tc.parent_id = t.id
)
SELECT * FROM tree ORDER BY depth ASC, name ASC;

-- name: FindTagCategoryAncestors :many
-- Everything ABOVE a category: parents, grandparents, to the root.
--
-- Built from the subject's PARENT and walking upward, not from the subject and walking
-- up. That shape is deliberate: an earlier version started at the subject and filtered
-- `WHERE id <> $1` on the CTE reference, which is exactly the form sqlc cannot parse --
-- it reports `id` as ambiguous and accepts no alias, bare or CTE-named. Starting from the
-- parent sidesteps the self-reference entirely, and it is also marginally cheaper: the
-- subject's own row is never materialised only to be discarded.
--
-- Ordered nearest-parent FIRST (depth 0 is the immediate parent, the last row is the
-- root), so a client renders breadcrumbs by reading this list in reverse.
WITH RECURSIVE chain AS (
    SELECT tc.*, 0 AS depth
      FROM tag_categories tc
     WHERE tc.parent_id = $1
    UNION ALL
    SELECT tc.*, c.depth + 1
      FROM tag_categories tc
      JOIN chain c ON tc.id = c.parent_id
)
SELECT * FROM chain ORDER BY depth ASC, name ASC;

-- name: FindTagCategoryRoots :many
-- Top-level categories. The entry point for browsing.
--
-- `OR parent_id IS NULL` rather than a NOT EXISTS: a category whose parent row was
-- deleted has parent_id set to NULL by ON DELETE SET NULL, so it is a root either way.
SELECT * FROM tag_categories
WHERE parent_id IS NULL
ORDER BY name ASC;
