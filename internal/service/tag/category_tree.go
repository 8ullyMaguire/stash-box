package tag

import (
	"context"
	"fmt"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/converter"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
)

// Tag category nesting (growth item 24).
//
// The category LAYER already existed -- table, GraphQL type, create/update/delete
// mutations, a service -- and had zero rows. What was missing was any way to say one
// category is a child of another, which is what "hierarchy" means.
//
// Why the tree walks live here rather than in the resolvers: every one of them is a
// recursive query whose result is a row type carrying a depth column, and the conversion
// from that row type to the GraphQL shape is the same three lines each time. Putting it
// here means the resolver cannot accidentally ship a walk without its depth.

// TreeNode is a category in a tree walk, with how far from the category asked about.
//
// Depth is the reason this type exists. A flat list of category names cannot draw a
// hierarchy: a client cannot distinguish a child from a grandchild without rebuilding the
// tree itself, which is exactly the work the database already did.
type TreeNode struct {
	Category *models.TagCategory
	// Depth is 0 for an immediate child (or the immediate parent, in Ancestors), 1 for
	// the next level out, and so on.
	Depth int
}

// Children returns the direct children of a category.
//
// One level only, for a tree that loads as the user expands it. For a known whole
// subtree, Descendants is one round trip instead of one per level.
//
// An empty list for a leaf is normal and not an error.
func (s *Tag) Children(ctx context.Context, parentID uuid.UUID) ([]*models.TagCategory, error) {
	rows, err := s.queries.FindTagCategoryChildren(ctx, uuid.NullUUID{UUID: parentID, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("finding children of tag category %s: %w", parentID, err)
	}
	out := make([]*models.TagCategory, 0, len(rows))
	for _, r := range rows {
		out = append(out, converter.TagCategoryToModelPtr(queries.TagCategory{
			ID:          r.ID,
			Group:       r.Group,
			Name:        r.Name,
			Description: r.Description,
			CreatedAt:   r.CreatedAt,
			UpdatedAt:   r.UpdatedAt,
			ParentID:    r.ParentID,
		}))
	}
	return out, nil
}

// Descendants returns every category below this one, at any depth.
//
// Ordered nearest-first (depth ascending), so a client can render a tree in one pass.
// The cycle guard in migration 106 is what makes this terminate: with a cycle present it
// would recurse until the database exhausted its stack, and the data would already be
// corrupt so there would be nothing left to detect at read time.
func (s *Tag) Descendants(ctx context.Context, id uuid.UUID) ([]TreeNode, error) {
	rows, err := s.queries.FindTagCategoryDescendants(ctx, uuid.NullUUID{UUID: id, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("finding descendants of tag category %s: %w", id, err)
	}
	out := make([]TreeNode, 0, len(rows))
	for _, r := range rows {
		out = append(out, TreeNode{
			Category: converter.TagCategoryToModelPtr(queries.TagCategory{
				ID:          r.ID,
				Group:       r.Group,
				Name:        r.Name,
				Description: r.Description,
				CreatedAt:   r.CreatedAt,
				UpdatedAt:   r.UpdatedAt,
				ParentID:    r.ParentID,
			}),
			Depth: int(r.Depth),
		})
	}
	return out, nil
}

// Ancestors returns every category above this one, up to the root.
//
// Ordered NEAREST PARENT FIRST (depth 0 is the immediate parent), so a breadcrumb renders
// by reading the list in reverse.
func (s *Tag) Ancestors(ctx context.Context, id uuid.UUID) ([]TreeNode, error) {
	rows, err := s.queries.FindTagCategoryAncestors(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("finding ancestors of tag category %s: %w", id, err)
	}
	out := make([]TreeNode, 0, len(rows))
	for _, r := range rows {
		out = append(out, TreeNode{
			Category: converter.TagCategoryToModelPtr(queries.TagCategory{
				ID:          r.ID,
				Group:       r.Group,
				Name:        r.Name,
				Description: r.Description,
				CreatedAt:   r.CreatedAt,
				UpdatedAt:   r.UpdatedAt,
				ParentID:    r.ParentID,
			}),
			Depth: int(r.Depth),
		})
	}
	return out, nil
}

// SetParent moves a category under a parent, or promotes it to top level when
// parentID.Valid is false.
//
// NO CYCLE CHECK HERE. That is deliberate: the check is a database trigger (migration
// 106), and duplicating it here would be two implementations of one rule that could
// disagree -- and a check that can be bypassed by any other writer is not a check. The
// database error is surfaced as-is, which is why the error text is passed through rather
// than rewritten: it names the category and says "cycle".
func (s *Tag) SetParent(ctx context.Context, id uuid.UUID, parentID uuid.NullUUID) (*models.TagCategory, error) {
	row, err := s.queries.SetTagCategoryParent(ctx, queries.SetTagCategoryParentParams{
		ID:       id,
		ParentID: parentID,
	})
	if err != nil {
		// NOT wrapped in a generic message: the trigger's text is the only thing that
		// says WHICH category would have created a cycle, and a caller relinking a deep
		// tree needs to know which link failed.
		return nil, fmt.Errorf("setting parent of tag category %s: %w", id, err)
	}
	return converter.TagCategoryToModelPtr(row), nil
}

// Roots returns the top-level categories: the entry point for browsing the hierarchy.
//
// A category whose parent was deleted is a root, because ON DELETE SET NULL promotes it.
// That is deliberate rather than a side effect -- see the migration.
func (s *Tag) CategoryRoots(ctx context.Context) ([]*models.TagCategory, error) {
	rows, err := s.queries.FindTagCategoryRoots(ctx)
	if err != nil {
		return nil, fmt.Errorf("finding root tag categories: %w", err)
	}
	out := make([]*models.TagCategory, 0, len(rows))
	for _, r := range rows {
		out = append(out, converter.TagCategoryToModelPtr(r))
	}
	return out, nil
}
