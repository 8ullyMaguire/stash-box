package api

import (
	"context"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/dataloader"
	"github.com/stashapp/stash-box/internal/models"
)

type tagEditResolver struct{ *Resolver }

func (r *tagEditResolver) Category(ctx context.Context, obj *models.TagEdit) (*models.TagCategory, error) {
	if obj.CategoryID == nil {
		return nil, nil
	}

	return r.services.Tag().FindCategory(ctx, *obj.CategoryID)
}

func (r *tagEditResolver) Aliases(ctx context.Context, obj *models.TagEdit) ([]string, error) {
	return r.services.Edit().GetMergedStudioAliases(ctx, obj.EditID)
}

// The implication change an edit carries, as tags rather than ids, because the surface that
// reads it is a reviewer's diff: "this edit adds woman_on_top" is legible and a bare uuid is
// not.
func (r *tagEditResolver) AddedImplications(ctx context.Context, obj *models.TagEdit) ([]models.Tag, error) {
	return tagsByIDs(ctx, obj.AddedImplications)
}

func (r *tagEditResolver) RemovedImplications(ctx context.Context, obj *models.TagEdit) ([]models.Tag, error) {
	return tagsByIDs(ctx, obj.RemovedImplications)
}

// tagsByIDs loads tags in the order asked for and drops the ones that no longer exist: an
// implication whose tail was deleted must not turn an edit card into an error.
func tagsByIDs(ctx context.Context, ids []uuid.UUID) ([]models.Tag, error) {
	tags, err := loadByIDs(ids, dataloader.For(ctx).TagByID.LoadAll)
	if err != nil {
		return nil, err
	}

	out := make([]models.Tag, 0, len(tags))
	for _, tag := range tags {
		if tag != nil {
			out = append(out, *tag)
		}
	}

	return out, nil
}
