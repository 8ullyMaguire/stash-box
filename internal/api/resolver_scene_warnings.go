package api

import (
	"context"

	"github.com/stashapp/stash-box/internal/models"
)

// Warnings is the Scene.warnings field. The visibility decision lives in the service (a warning about
// a scene the reader cannot see is a disclosure), so this resolver is only the wiring -- but it is
// still a field resolver rather than a column, because the answer costs a query per scene.
func (r *sceneResolver) Warnings(ctx context.Context, obj *models.Scene) ([]models.ContentWarning, error) {
	return r.services.Scene().Warnings(ctx, obj.ID)
}
