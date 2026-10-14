package api

import (
	"context"
	"fmt"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service/completeness"
)

// StudioCompleteness answers "what do the sources claim this studio lists, what do we hold, and can
// that record still be checked" (SPEC §7.24.2, aggregator phase 9 slice 9f).
//
// Nothing is recomputed here. The percentages come from the verdict's own methods, which are the only
// place the rules live: the flooring, the cap, and "a claim of nothing has no ratio". A resolver that
// did its own arithmetic would be a second answer to the same question, and the two would drift the
// first time either rule changed.
func (r *queryResolver) StudioCompleteness(ctx context.Context, studioID uuid.UUID) (*models.CompletenessVerdict, error) {
	verdict, err := r.services.Completeness().StudioVerdict(ctx, studioID)
	if err != nil {
		return nil, err
	}

	out := &models.CompletenessVerdict{
		Claims:       make([]models.CompletenessClaim, 0, len(verdict.Totals)),
		Expected:     int(verdict.Expected),
		Observed:     int(verdict.Observed),
		Percent:      verdict.Percent(),
		Score:        verdict.Score(),
		HasClaim:     verdict.HasClaim(),
		Unverifiable: verdict.Unverifiable(),
	}

	for _, total := range verdict.Totals {
		claim, err := completenessClaim(total)
		if err != nil {
			return nil, err
		}
		out.Claims = append(out.Claims, claim)
	}

	// Present only when something is actually lost. `Unverifiable` without `which claim` is not
	// actionable, and an empty claim object would be worse than null: it would read as a claim whose
	// own fields happen to be blank.
	if lost, ok := verdict.LostSource(); ok {
		claim, err := completenessClaim(lost)
		if err != nil {
			return nil, err
		}
		out.LostSource = &claim
	}

	return out, nil
}

// completenessClaim converts one denominator. The status is nil unless the instance has a health
// record for the page: absence is not a status, and the enum has no name for it.
func completenessClaim(total completeness.Total) (models.CompletenessClaim, error) {
	claim := models.CompletenessClaim{
		Kind:         total.Kind,
		Expected:     int(total.Expected),
		Observed:     int(total.Observed),
		Missing:      int(total.Missing),
		SourceURL:    total.SourceURL,
		Unverifiable: total.Unverifiable(),
	}

	if stored := total.SourceStatus(); stored != "" {
		status, ok := contentSourceStatusEnum(stored)
		if !ok {
			return claim, fmt.Errorf("the source of %s has status %q, which has no name in the API's vocabulary", total.SourceURL, stored)
		}
		claim.Status = &status
	}

	return claim, nil
}
