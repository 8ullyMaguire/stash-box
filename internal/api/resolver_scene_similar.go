package api

import (
	"context"

	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service/similar"
)

// Scene-similarity GraphQL resolvers (feature-discovery SPEC section 8, phase 7 slice 5).
//
// The plumbing is not the interesting part. What is interesting is that every reason a
// client renders names something a reader can open -- a tag, a person, a studio -- and that
// the score is reported as a RANKING VALUE rather than as a quality, because a heuristic
// wearing a percentage is a claim nobody can check. See the schema file for the reasoning
// behind the three tag kinds.

// SimilarScenes returns scenes related to this one.
//
// An empty list is a legitimate answer: a scene with nothing in common with anything, or one
// whose neighbours all rank below the page, has none. An error would make a sparse archive
// look broken.
func (r *sceneResolver) SimilarScenes(ctx context.Context, obj *models.Scene, limit *int) ([]models.SimilarScene, error) {
	results, err := r.services.Similar().FindSimilarScenes(ctx, similar.SimilarSceneInput{
		Subject: *obj,
		Limit:   derefInt(limit),
	})
	if err != nil {
		return nil, err
	}

	out := make([]models.SimilarScene, 0, len(results))
	for _, s := range results {
		reasons := make([]models.SimilarityReason, 0, len(s.Reasons))
		for _, reason := range s.Reasons {
			label := reason.Label
			via := reason.Via
			reasons = append(reasons, models.SimilarityReason{
				Kind:  models.SimilarityReasonKind(reason.Kind),
				Label: label,
				// Absent rather than empty for the kinds with nothing in between: a client
				// rendering `via` as a link must not render an empty one.
				Via:    optionalString(via),
				Weight: reason.Weight,
			})
		}
		out = append(out, models.SimilarScene{
			Scene:         s.Scene,
			Score:         s.Score,
			Reasons:       reasons,
			SemanticScore: s.SemanticScore,
		})
	}
	return out, nil
}

// optionalString returns nil for an empty string, so an absent GraphQL field is absent
// rather than an empty string a client has to check for.
func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
