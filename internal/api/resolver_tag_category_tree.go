package api

import (
	"context"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service/tag"
)

// Tag category hierarchy resolvers (growth item 24).
//
// Thin by design. Every tree walk is a recursive query with a depth column, and the
// service owns both the SQL and the depth convention; a resolver that reimplemented the
// ordering would be a second place for it to be wrong.
//
// Empty results are the normal case and never an error: most categories are leaves, and
// this instance had zero categories before migration 106.

// TagCategorySetParent moves a category under a parent, or promotes it to top level.
//
// The cycle refusal comes from the database trigger and is NOT re-implemented here: see
// the note on Tag.SetParent. A client that tries to close a loop gets the trigger's
// error, which names the category -- more useful than a generic "invalid hierarchy".
func (r *mutationResolver) TagCategorySetParent(ctx context.Context, input models.TagCategoryParentInput) (*models.TagCategory, error) {
	// gqlgen maps a nullable ID to *uuid.UUID, so an OMITTED parentId arrives as nil --
	// which is the promote-to-top-level case, not an error.
	var parent uuid.NullUUID
	if input.ParentID != nil {
		parent = uuid.NullUUID{UUID: *input.ParentID, Valid: true}
	}
	category, err := r.services.Tag().SetParent(ctx, input.ID, parent)
	if err != nil {
		return nil, err
	}
	return category, nil
}

// treeNodes converts service nodes to the GraphQL shape.
//
// One helper for both directions, because the conversion is identical and a resolver that
// did it inline twice would be a resolver that could disagree with itself about the order.
func treeNodes(nodes []tag.TreeNode) []models.TagCategoryTreeNode {
	out := make([]models.TagCategoryTreeNode, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, models.TagCategoryTreeNode{
			Category: n.Category,
			Depth:    n.Depth,
		})
	}
	return out
}

// Parent resolves the parent category, or null for a top-level one.
//
// Null covers two cases that look identical from outside and are both correct: a category
// that was always top-level, and one whose parent was deleted. Deleting a parent promotes
// its children (ON DELETE SET NULL) rather than cascading, so a reorganise cannot destroy
// vocabulary -- and a promoted child correctly reports no parent.
func (r *tagCategoryResolver) Parent(ctx context.Context, obj *models.TagCategory) (*models.TagCategory, error) {
	if !obj.ParentID.Valid {
		return nil, nil
	}
	parent, err := r.services.Tag().FindCategory(ctx, obj.ParentID.UUID)
	if err != nil {
		// A dangling parent_id would be a data-integrity failure, but reporting it as an
		// error would blank the category page. A missing parent renders as null, which
		// is what the row says anyway once the parent is gone.
		return nil, nil
	}
	return parent, nil
}

// Children returns direct children only -- one level, for a tree that loads as the user
// expands it.
// gqlgen generated `[]models.TagCategory` (values, not pointers) for a `[TagCategory!]!`
// field, so the service's pointer slice is converted here rather than at the call site.
func (r *tagCategoryResolver) Children(ctx context.Context, obj *models.TagCategory) ([]models.TagCategory, error) {
	children, err := r.services.Tag().Children(ctx, obj.ID)
	if err != nil {
		return nil, err
	}
	out := make([]models.TagCategory, 0, len(children))
	for _, c := range children {
		if c != nil {
			out = append(out, *c)
		}
	}
	return out, nil
}

// Descendants returns the whole subtree in one query, nearest first.
//
// Preferred over walking Children client-side when the whole subtree is wanted: this is
// one round trip instead of one per level, and the depths come back already correct.
func (r *tagCategoryResolver) Descendants(ctx context.Context, obj *models.TagCategory) ([]models.TagCategoryTreeNode, error) {
	nodes, err := r.services.Tag().Descendants(ctx, obj.ID)
	if err != nil {
		return nil, err
	}
	return treeNodes(nodes), nil
}

// Ancestors returns parents up to the root, nearest first.
func (r *tagCategoryResolver) Ancestors(ctx context.Context, obj *models.TagCategory) ([]models.TagCategoryTreeNode, error) {
	// FindTagCategoryAncestors takes the PARENT id: it starts from `tc.parent_id = $1` and
	// walks upward, because the alternative -- starting at the category and excluding
	// itself -- is the form sqlc cannot parse. Passing obj.ID here silently matched
	// nothing and reported "no ancestors", which is the kind of plausible empty answer
	// that reads as a missing feature rather than a wrong argument.
	nodes, err := r.services.Tag().Ancestors(ctx, obj.ID)
	if err != nil {
		return nil, err
	}
	return treeNodes(nodes), nil
}
