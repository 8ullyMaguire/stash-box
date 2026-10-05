package api

import (
	"context"

	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service/performer"
)

// Similar-performer GraphQL resolvers (SPEC §7.5, growth item 27).
//
// The interesting part of this feature is not the plumbing, it is that the score is
// DECOMPOSED into observable facts (`scenesShared`, `targetScenes`, `coPerformers`)
// rather than exposed as a bare number. A client can therefore check the claim, and a
// developer can tell a scoring change from a data change when the ranking shifts.
// The bare `score` is still there, for ordering -- and the schema says plainly that it
// must not be rendered as a percentage.

// SimilarPerformers returns performers who appear alongside this one.
//
// An empty list is a legitimate answer, not a failure: a performer with no scenes, or
// one who has never shared two scenes with anybody, has no similar performers on this
// instance. Returning an error would make a sparse archive look broken.
func (r *performerResolver) SimilarPerformers(ctx context.Context, obj *models.Performer, minShared, limit *int) ([]models.SimilarPerformer, error) {
	results, err := r.services.Performer().FindSimilar(ctx, performer.SimilarPerformerInput{
		PerformerID: obj.ID,
		MinShared:   derefInt(minShared),
		Limit:       derefInt(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]models.SimilarPerformer, 0, len(results))
	for _, s := range results {
		p := s.Performer
		out = append(out, models.SimilarPerformer{
			Performer:    &p,
			ScenesShared: s.ScenesShared,
			TargetScenes: s.TargetScenes,
			CoPerformers: s.CoPerformers,
			Score:        s.Score,
		})
	}
	return out, nil
}

// SimilarPerformerCount reports how many performers clear the floor.
func (r *performerResolver) SimilarPerformerCount(ctx context.Context, obj *models.Performer, minShared *int) (int, error) {
	return r.services.Performer().CountSimilar(ctx, obj.ID, derefInt(minShared))
}

// derefInt reads an optional GraphQL argument.
//
// Zero for an absent argument, which every caller here treats as "use the default" —
// so an omitted argument and an explicit 0 mean the same thing, and the defaults live
// in one place (the service) rather than being duplicated per resolver.
func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
