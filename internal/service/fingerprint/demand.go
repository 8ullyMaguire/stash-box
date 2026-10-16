package fingerprint

import (
	"context"

	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
)

const (
	// demandMinHolders is the floor the query enforces whatever the caller asks
	// for: the board is exposed THRESHOLDED, so a member cannot ask for the
	// unthresholded one. Two is the smallest number that means "more than one
	// person holds this", which is the only reading of a demand board that is
	// about demand rather than about one submission.
	demandMinHolders = 2

	// demandDefaultHolders and demandDefaultLimit are what an unparameterised
	// call gets.
	demandDefaultHolders = 5
	demandDefaultLimit   = 50

	// demandMaxLimit is the ceiling on one response, so the board cannot be
	// used to pull the whole table a page at a time.
	demandMaxLimit = 200
)

// FingerprintDemand returns SPEC §7.24.5's demand board: hashes many members
// hold, aggregated by hash and count only.
//
// It is a read of a LOWER BOUND. See the query's comment for why each of those
// two facts is load-bearing rather than descriptive.
func (s *Fingerprint) FingerprintDemand(ctx context.Context, minHolders, limit int) (*models.FingerprintDemand, error) {
	// Asking for less than the floor gets the DEFAULT, not the floor: a caller
	// who wants everything asks for 0, and 0 is the request this boundary exists
	// to refuse. Asking for 1 and asking for 2 are the same request.
	floor := demandDefaultHolders
	if minHolders >= demandMinHolders {
		floor = minHolders
	}

	if limit <= 0 {
		limit = demandDefaultLimit
	}
	if limit > demandMaxLimit {
		limit = demandMaxLimit
	}

	rows, err := s.queries.GetFingerprintDemand(ctx, queries.GetFingerprintDemandParams{
		MinHolders: floor,
		RowLimit:   limit,
	})
	if err != nil {
		return nil, err
	}

	entries := make([]models.FingerprintDemandEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, models.FingerprintDemandEntry{
			Algorithm:        models.FingerprintAlgorithm(row.Algorithm),
			Hash:             models.FingerprintHash(row.Hash),
			HolderLowerBound: int(row.HolderCount),
		})
	}

	return &models.FingerprintDemand{
		Entries:    entries,
		MinHolders: floor,
	}, nil
}
