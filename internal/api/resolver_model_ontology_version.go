package api

import (
	"context"
	"time"

	"github.com/stashapp/stash-box/internal/models"
)

type ontologyVersionResolver struct{ *Resolver }

func (r *ontologyVersionResolver) Created(ctx context.Context, obj *models.OntologyVersion) (*time.Time, error) {
	return &obj.CreatedAt, nil
}
