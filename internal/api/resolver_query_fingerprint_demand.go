package api

import (
	"context"

	"github.com/stashapp/stash-box/internal/models"
)

func (r *queryResolver) FingerprintDemand(ctx context.Context, input *models.FingerprintDemandInput) (*models.FingerprintDemand, error) {
	minHolders, limit := 0, 0
	if input != nil {
		if input.MinHolders != nil {
			minHolders = *input.MinHolders
		}
		if input.Limit != nil {
			limit = *input.Limit
		}
	}
	return r.services.Fingerprint().FingerprintDemand(ctx, minHolders, limit)
}
