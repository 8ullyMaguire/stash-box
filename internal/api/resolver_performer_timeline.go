package api

import (
	"context"

	"github.com/stashapp/stash-box/internal/models"
)

// Performer timeline resolver (growth item 21).
//
// A straight pass-through. The bucketing, the ordering and the undated count are all
// decided in the service, because each of those is a decision a reader could reasonably
// question -- and a resolver that recomputed any of them would be a second place to
// disagree.

// Timeline resolves the performer's year-bucketed appearance history.
func (r *performerResolver) Timeline(ctx context.Context, obj *models.Performer) (*models.PerformerTimeline, error) {
	timeline, err := r.services.Performer().Timeline(ctx, obj.ID)
	if err != nil {
		return nil, err
	}

	// gqlgen generated []models.PerformerTimelineEntry for a `[Type!]!` field, so this is
	// a value slice. Built non-nil so the field serialises as [] rather than null: a
	// client should not have to special-case "no history" apart from "no such performer".
	entries := make([]models.PerformerTimelineEntry, 0, len(timeline.Entries))
	for _, e := range timeline.Entries {
		entries = append(entries, models.PerformerTimelineEntry{
			Year:       e.Year,
			SceneCount: int(e.SceneCount),
		})
	}

	return &models.PerformerTimeline{
		Entries:      entries,
		UndatedCount: int(timeline.UndatedCount),
		// From the service, NOT summed here. Two implementations of "total" is how a
		// profile's scene count and its timeline total drift apart, and the drift is
		// invisible until someone notices the two numbers disagree on the same page.
		SceneCount: int(timeline.TotalSceneCount()),
	}, nil
}
