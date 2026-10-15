package api

import (
	"context"
	"fmt"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stashapp/stash-box/internal/dataloader"

	"github.com/stashapp/stash-box/internal/models"
)

type editCommentResolver struct{ *Resolver }

func (r *editCommentResolver) ID(ctx context.Context, obj *models.EditComment) (string, error) {
	return obj.ID.String(), nil
}

func (r *editCommentResolver) Comment(ctx context.Context, obj *models.EditComment) (string, error) {
	return obj.Text, nil
}

func (r *editCommentResolver) Date(ctx context.Context, obj *models.EditComment) (*time.Time, error) {
	return &obj.CreatedAt, nil
}

func (r *editCommentResolver) Updated(ctx context.Context, obj *models.EditComment) (*time.Time, error) {
	return obj.UpdatedAt, nil
}

func (r *editCommentResolver) Hidden(ctx context.Context, obj *models.EditComment) (bool, error) {
	return obj.IsHidden, nil
}

func (r *editCommentResolver) User(ctx context.Context, obj *models.EditComment) (*models.User, error) {
	if obj.UserID.UUID.IsNil() {
		return nil, nil
	}

	return dataloader.For(ctx).UserByID.Load(obj.UserID.UUID)
}

func (r *editCommentResolver) Edit(ctx context.Context, obj *models.EditComment) (*models.Edit, error) {
	return dataloader.For(ctx).EditByID.Load(obj.EditID)
}

// Kind reports what the writer was doing, BY NAME.
//
// The mapping is forced rather than cast for the reason slice 6g's seasonal status is: the column
// holds a lower-case word and the enum's values are the upper-case spelling a client matches on, so
// a cast would compile and then report an enum value that does not exist. An unrecognised value is
// an ERROR rather than a blank.
func (r *editCommentResolver) Kind(ctx context.Context, obj *models.EditComment) (models.EditCommentKind, error) {
	switch obj.Kind {
	case "discussion":
		return models.EditCommentKindDiscussion, nil
	case "source_info":
		return models.EditCommentKindSourceInfo, nil
	case "performer_id":
		return models.EditCommentKindPerformerID, nil
	case "timestamp":
		return models.EditCommentKindTimestamp, nil
	case "correction":
		return models.EditCommentKindCorrection, nil
	case "request":
		return models.EditCommentKindRequest, nil
	case "review":
		return models.EditCommentKindReview, nil
	default:
		return "", fmt.Errorf("edit comment %s has an unrecognised kind %q", obj.ID, obj.Kind)
	}
}

// PerformerID is the structured field of the PERFORMER_ID kind, and is null for every other kind --
// which the database refuses to let be otherwise.
func (r *editCommentResolver) PerformerID(ctx context.Context, obj *models.EditComment) (*uuid.UUID, error) {
	if !obj.PerformerID.Valid {
		return nil, nil
	}
	id := obj.PerformerID.UUID
	return &id, nil
}
