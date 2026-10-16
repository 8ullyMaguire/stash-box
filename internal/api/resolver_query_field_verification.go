package api

import (
	"context"

	"github.com/stashapp/stash-box/internal/models"
)

// The reason registry, for the edit form's dropdown (SPEC §7.24.1).
//
// It reads the registry rather than hard-coding the codes the frontend offers, which is the
// whole point of migration 96 making it a table: adding a reason is an INSERT, and a UI
// carrying its own copy of the list would keep offering the old set. The order comes from the
// query (by code), so the dropdown does not move between requests.
//
// This returns the registry and NOT the assertions themselves. Reading an entity's assertions
// is a per-entity question that belongs beside the entity, and 7.24.1's surface -- the control
// that ASSERTS one and the missing list that reflects it -- needs the codes and the score, not
// a second list of rows that could drift from what the score used.
func (r *queryResolver) FieldVerificationReasons(ctx context.Context) ([]models.FieldVerificationReason, error) {
	rows, err := r.services.Verification().Reasons(ctx)
	if err != nil {
		return nil, err
	}

	reasons := make([]models.FieldVerificationReason, 0, len(rows))
	for _, row := range rows {
		reasons = append(reasons, models.FieldVerificationReason{
			Code:          row.Code,
			Description:   row.Description,
			SuppressesGap: row.SuppressesGap,
		})
	}
	return reasons, nil
}
