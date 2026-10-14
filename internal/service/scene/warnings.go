package scene

import (
	"context"
	"fmt"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/trust"
)

// Warnings returns the instance's warn-mode notes about a scene.
//
// TWO WAYS A SCENE IS OUT OF THE READER'S VIEW, and both return the empty list rather than the notes:
// the instance denies it, or its tier is above the viewer's ceiling. A warning is an annotation ON a
// view -- if there is no view, there is nothing to annotate -- and "this content is restricted" is
// itself a disclosure about content the reader cannot open. The check is here rather than in the
// resolver so that every caller of this method gets it, which is the lesson the four tier paths taught.
//
// The list is empty and never nil: the GraphQL field is a non-null list, and a nil slice would
// marshall as null while meaning the same thing.
func (s *Scene) Warnings(ctx context.Context, sceneID uuid.UUID) ([]models.ContentWarning, error) {
	visibleTier := int16(0)
	if user := auth.GetCurrentUser(ctx); user != nil {
		tier, err := s.queries.UserVisibleTier(ctx, queries.UserVisibleTierParams{
			UserID:       user.ID,
			CoolingHours: trust.T3CoolingHours,
		})
		if err != nil {
			return nil, fmt.Errorf("reading the viewer's content-tier ceiling: %w", err)
		}
		visibleTier = tier
	}

	denied, err := s.queries.DeniedSceneIDs(ctx, []uuid.UUID{sceneID})
	if err != nil {
		return nil, fmt.Errorf("reading whether the scene is denylisted: %w", err)
	}
	if len(denied) > 0 {
		return []models.ContentWarning{}, nil
	}

	// The effective tier of this one entity, from the same view the batch lookup uses: a scene the
	// viewer's ceiling excludes is not in their view, so it has no warnings to show them either.
	tiers, err := s.queries.SceneEffectiveTiers(ctx, []uuid.UUID{sceneID})
	if err != nil {
		return nil, fmt.Errorf("reading the scene's effective content tier: %w", err)
	}
	for _, row := range tiers {
		if row.Tier > visibleTier {
			return []models.ContentWarning{}, nil
		}
	}

	rows, err := s.queries.SceneWarnings(ctx, sceneID)
	if err != nil {
		return nil, fmt.Errorf("reading the scene's warnings: %w", err)
	}

	warnings := make([]models.ContentWarning, 0, len(rows))
	for _, row := range rows {
		warnings = append(warnings, models.ContentWarning{
			Source: row.Source,
			Reason: row.Reason,
		})
	}
	return warnings, nil
}
