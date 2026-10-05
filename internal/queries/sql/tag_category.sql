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

-- name: SetTagCategoryParent :one
-- Move a category under a parent, or promote it to top level with a NULL parent.
--
-- A DEDICATED QUERY rather than an extra field on UpdateTagCategory: re-parenting is a
-- different act from renaming, and folding it into the general update would let every
-- caller that can rename silently restructure the hierarchy.
--
-- A CYCLE IS NOT CHECKED HERE. It is a trigger (migration 106) so the constraint holds for
-- every writer, not only this path -- a check that can be bypassed by any other writer is
-- not a check.
UPDATE tag_categories SET parent_id = $2, updated_at = now()
WHERE id = $1
RETURNING *;

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
-- The seed is the subject's PARENT -- resolved by a SCALAR SUBQUERY, not by seeding on
-- $1 and filtering afterwards. Both reasons matter:
--
--  1. Correctness. `WHERE tc.parent_id = $1` selects the rows whose parent IS the subject,
--     i.e. the subject's CHILDREN. For a leaf that is the empty set and the whole walk
--     returns nothing. That is the version I wrote first, and it fails silently rather
--     than loudly -- every ancestor list just came back empty.
--
--  2. sqlc. Excluding the subject afterwards needs `WHERE id <> $1` on a WITH
--     RECURSIVE reference, which sqlc reports as an ambiguous column and accepts no
--     alias for -- bare, CTE-named or otherwise. A scalar subquery has no outer column
--     reference, so it parses, and a top-level category returns no rows for free: its
--     parent_id is NULL, the subquery yields NULL, and NULL matches nothing.
--
-- Ordered nearest-parent FIRST (depth 0 is the immediate parent, the last row is the
-- root), so a client renders breadcrumbs by reading this list in reverse.
WITH RECURSIVE chain AS (
    SELECT tc.*, 0 AS depth
      FROM tag_categories tc
     WHERE tc.id = (SELECT p.parent_id FROM tag_categories p WHERE p.id = sqlc.arg('id')::uuid)
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
