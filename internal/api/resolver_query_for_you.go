package api

import (
	"context"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service/taste"
)

// For You GraphQL resolver (feature-discovery SPEC section 9, phase 7 slice 6).
//
// The interesting decisions are in the schema file and in internal/service/taste: the filter is
// the existing query language, affinity is derived at read time, and serendipity is a slot rule
// the caller sets rather than a policy the server imposes. What is left here is loading the tags
// the contributions name, in ONE query rather than one per contribution.

// ForYou returns scenes ranked by the viewer's own taste.
func (r *queryResolver) ForYou(ctx context.Context, input *models.ForYouInput) ([]models.ForYouScene, error) {
	currentUser := auth.GetCurrentUser(ctx)

	feedInput := taste.FeedInput{
		UserID: currentUser.ID,
	}
	if input != nil {
		if input.Limit != nil {
			feedInput.Limit = *input.Limit
		}
		if input.Serendipity != nil {
			feedInput.Serendipity = *input.Serendipity
		}
		if input.Seed != nil {
			feedInput.Seed = int64(*input.Seed)
		}
		feedInput.Filter.Query = input.Query
	}

	feed, err := r.services.Taste().Feed(ctx, feedInput)
	if err != nil {
		return nil, err
	}

	// Every tag any contribution names, in one lookup. A per-contribution FindByID is five
	// queries per row and twenty rows per page, which is the shape of an N+1 that only shows up
	// under load.
	tagIDs := make([]uuid.UUID, 0, len(feed))
	seen := make(map[uuid.UUID]struct{}, len(feed))
	for _, f := range feed {
		for _, c := range f.Contributions {
			if _, ok := seen[c.TagID]; ok {
				continue
			}
			seen[c.TagID] = struct{}{}
			tagIDs = append(tagIDs, c.TagID)
		}
	}

	tags := make(map[uuid.UUID]*models.Tag, len(tagIDs))
	if len(tagIDs) > 0 {
		found, err := r.services.Tag().FindByIds(ctx, tagIDs)
		if err != nil {
			return nil, err
		}
		for _, tag := range found {
			tags[tag.ID] = tag
		}
	}

	out := make([]models.ForYouScene, 0, len(feed))
	for _, f := range feed {
		contributions := make([]models.AffinityContribution, 0, len(f.Contributions))
		for _, c := range f.Contributions {
			// A tag between the scoring query and this lookup cannot go missing in a single
			// transaction, but the row is skipped rather than panicked on: a recommendation
			// with one fewer reason is still a recommendation, and a nil dereference in a
			// read path is an outage.
			tag, ok := tags[c.TagID]
			if !ok {
				continue
			}
			contributions = append(contributions, models.AffinityContribution{
				Tag:    tag,
				Weight: c.Weight,
			})
		}
		out = append(out, models.ForYouScene{
			Scene:         f.Scene,
			Affinity:      f.Affinity,
			Contributions: contributions,
		})
	}
	return out, nil
}
