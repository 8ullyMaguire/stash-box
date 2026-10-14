package api

import (
	"context"
	"sort"
	"time"

	"github.com/stashapp/stash-box/internal/dataloader"
	"github.com/stashapp/stash-box/internal/models"
)

type tagResolver struct{ *Resolver }

func (r *tagResolver) ID(ctx context.Context, obj *models.Tag) (string, error) {
	return obj.ID.String(), nil
}
func (r *tagResolver) Description(ctx context.Context, obj *models.Tag) (*string, error) {
	return obj.Description, nil
}
func (r *tagResolver) Aliases(ctx context.Context, obj *models.Tag) ([]string, error) {
	aliases, err := dataloader.For(ctx).TagAliasesByID.Load(obj.ID)
	if err != nil {
		return nil, err
	}

	sort.Strings(aliases)

	return aliases, nil
}

func (r *tagResolver) Edits(ctx context.Context, obj *models.Tag) ([]models.Edit, error) {
	return dataloader.For(ctx).EditsByTagID.Load(obj.ID)
}

func (r *tagResolver) Category(ctx context.Context, obj *models.Tag) (*models.TagCategory, error) {
	if obj.CategoryID.Valid {
		return dataloader.For(ctx).TagCategoryByID.Load(obj.CategoryID.UUID)
	}
	return nil, nil
}

func (r *tagResolver) Created(ctx context.Context, obj *models.Tag) (*time.Time, error) {
	return &obj.Created, nil
}

func (r *tagResolver) Updated(ctx context.Context, obj *models.Tag) (*time.Time, error) {
	return &obj.Updated, nil
}

// The ontology. These are service reads rather than dataloader reads: a tag page asks for
// them once, per tag, and the graph is not a bulk field a list page walks (which is what the
// dataloaders exist for).
func (r *tagResolver) Implies(ctx context.Context, obj *models.Tag) ([]models.Tag, error) {
	return r.services.TagOntology().Implies(ctx, obj.ID)
}

func (r *tagResolver) ImpliedBy(ctx context.Context, obj *models.Tag) ([]models.Tag, error) {
	return r.services.TagOntology().ImpliedBy(ctx, obj.ID)
}

func (r *tagResolver) ImpliesTransitive(ctx context.Context, obj *models.Tag) ([]models.TagAtDepth, error) {
	return r.services.TagOntology().ImpliesTransitive(ctx, obj.ID)
}

func (r *tagResolver) ImpliedByTransitive(ctx context.Context, obj *models.Tag) ([]models.TagAtDepth, error) {
	return r.services.TagOntology().ImpliedByTransitive(ctx, obj.ID)
}

// SceneCount is how much is under this tag: what a filter on it returns, through the
// implications below it. Deliberately NOT a stored counter -- the ontology changes by edit, and
// a materialised count would be wrong the moment an implication is added, with nothing to tell
// a reader that the number they are looking at is stale.
func (r *tagResolver) SceneCount(ctx context.Context, obj *models.Tag) (int, error) {
	return r.services.Tag().SceneCount(ctx, obj.ID)
}
