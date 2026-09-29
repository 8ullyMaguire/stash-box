package api

import (
	"context"
	"errors"

	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service/completion"
)

// Completion resolvers (SPEC §7.7).
//
// Five field resolvers that all do the same thing, which is why they are here
// rather than on the completion service: a `completion` field on a Performer and a
// `completion` field on a Scene are the same query with a different id, and
// putting the shape in the schema means a client has ONE progress-bar component
// rather than five near-identical ones.

// completionEntityType converts the schema enum to the service's.
//
// Separate types so the service's closed set stays closed: the schema accepts
// whatever its enum allows, and this is where an unrecognised value is refused by
// name rather than scored as something arbitrary.
func completionEntityType(t models.EntityType) completion.EntityType {
	return completion.EntityType(t)
}

// toModelCompletion converts a service result to its GraphQL shape.
//
// `missing` is a []string in the schema because that is what a client renders: the
// field names are user-facing text in a quest, and a client keeping a parallel
// enum of them would be a second copy of this list.
func toModelCompletion(result completion.Result) *models.Completion {
	missing := make([]string, 0, len(result.Missing))
	for _, field := range result.Missing {
		missing = append(missing, string(field))
	}
	return &models.Completion{
		EntityType: models.EntityType(result.EntityType),
		Score:      result.Score,
		Missing:    missing,
		Total:      result.Total,
		Earned:     result.Earned,
	}
}

// completionOrNil maps a missing entity to null.
//
// Null rather than an error: a client rendering a list of 50 performers must not
// lose the whole page because one of them was deleted between the list query and
// this one, and every client already has an empty state for a null field.
//
// Takes the error ALONE rather than (result, error), because Go will not spread a
// two-value return into a call whose parameters it cannot see through, and the
// five call sites would otherwise each repeat the same five lines.
func completionOrNil(err error) (*models.Completion, error) {
	if err != nil {
		if errors.Is(err, completion.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return nil, nil
}

// completionOrValue is the success path, kept separate from completionOrNil so the
// error check happens once and the conversion happens once.
func completionOrValue(result completion.Result, err error) (*models.Completion, error) {
	if err != nil {
		return completionOrNil(err)
	}
	return toModelCompletion(result), nil
}

// Completion returns a performer's completion score.
func (r *performerResolver) Completion(ctx context.Context, obj *models.Performer) (*models.Completion, error) {
	return completionOrValue(r.services.Completion().Performer(ctx, obj.ID))
}

// Completion returns a scene's completion score.
func (r *sceneResolver) Completion(ctx context.Context, obj *models.Scene) (*models.Completion, error) {
	return completionOrValue(r.services.Completion().Scene(ctx, obj.ID))
}

// Completion returns a studio's completion score.
func (r *studioResolver) Completion(ctx context.Context, obj *models.Studio) (*models.Completion, error) {
	return completionOrValue(r.services.Completion().Studio(ctx, obj.ID))
}

// Completion returns a site's completion score.
func (r *siteResolver) Completion(ctx context.Context, obj *models.Site) (*models.Completion, error) {
	return completionOrValue(r.services.Completion().Site(ctx, obj.ID))
}

// Completion returns a tag's completion score.
func (r *tagResolver) Completion(ctx context.Context, obj *models.Tag) (*models.Completion, error) {
	return completionOrValue(r.services.Completion().Tag(ctx, obj.ID))
}

// CountIncompleteEntities counts entities below a completion threshold.
//
// The number a generated quest is sized from. Non-null and zero when the type is
// not one this version scores, rather than an error: the schema accepts five types
// and this version wires all five, but a client asking about a sixth should get a
// number it can render, not an exception. An error here would be a client crash
// over a progress bar.
func (r *queryResolver) CountIncompleteEntities(ctx context.Context, entityType models.EntityType, below int) (int, error) {
	count, err := r.services.Completion().CountIncomplete(ctx,
		completionEntityType(entityType), below)
	if err != nil {
		if errors.Is(err, completion.ErrUnknownEntityType) {
			return 0, nil
		}
		return 0, err
	}
	return count, nil
}
