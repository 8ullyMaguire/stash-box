package api

import (
	"context"
	"fmt"
	"time"

	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service/seasonal"
)

// seasonalStatus maps the service's status onto the schema's enum.
//
// FORCED RATHER THAN CAST, because the two spellings have nothing in common on the wire: the
// service's values are lower-case words and an enum whose values a client matches on is
// upper-case by GraphQL convention. A cast would compile and then report an enum value that
// does not exist, which is the failure mode migration 108's status column is the standing
// example of -- an unnamed value must error, not be reported as UNKNOWN.
func seasonalStatus(s seasonal.Status) (models.SeasonalEventStatus, error) {
	switch s {
	case seasonal.StatusUpcoming:
		return models.SeasonalEventStatusUpcoming, nil
	case seasonal.StatusActive:
		return models.SeasonalEventStatusActive, nil
	case seasonal.StatusEnded:
		return models.SeasonalEventStatusEnded, nil
	default:
		return "", fmt.Errorf("unknown seasonal event status %q", s)
	}
}

// SeasonalEvents returns every declared event with the caller's own progress.
func (r *queryResolver) SeasonalEvents(ctx context.Context) ([]models.SeasonalEventProgress, error) {
	user := auth.GetCurrentUser(ctx)
	if user == nil {
		// The schema's @hasRole is what normally stops this; the check is here because the
		// progress is per-account and there is nothing to compute without one.
		return nil, auth.ErrUnauthorized
	}

	progress, err := r.services.Seasonal().For(ctx, user.ID)
	if err != nil {
		return nil, err
	}

	out := make([]models.SeasonalEventProgress, 0, len(progress))
	for _, p := range progress {
		status, err := seasonalStatus(p.Status)
		if err != nil {
			return nil, err
		}
		out = append(out, models.SeasonalEventProgress{
			Event: &models.SeasonalEvent{
				Slug:        p.Event.Slug,
				Title:       p.Event.Title,
				Description: p.Event.Description,
				// RFC3339: an event's window is two INSTANTS, not a calendar day, which is the
				// same distinction the vouch's timestamps carry.
				StartsAt:  p.Event.StartsAt.Format(time.RFC3339),
				EndsAt:    p.Event.EndsAt.Format(time.RFC3339),
				Threshold: p.Event.Threshold,
				Status:    status,
			},
			Contributions: p.Contributions,
			Reached:       p.Reached,
		})
	}
	return out, nil
}
