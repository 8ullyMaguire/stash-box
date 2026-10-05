package api

import (
	"context"

	"github.com/stashapp/stash-box/internal/models"
)

// ArchiveEntityCounts backs the "State of the Archive" page (SPEC §7.7, growth
// item 9).
//
// The whole point is that the numerator and the denominator come from one place.
// The page shows "142 of 400 performers catalogued", and the two halves of that
// sentence are computed by two different calls. If they are read at different
// moments under READ COMMITTED, the page can render a percentage nobody could
// reproduce — 97.6% on one read and 98.1% on the next, with no edit in between.
//
// So nothing here recomputes anything. The counts come from the completion
// service, which is also where `countIncompleteEntities` gets its numbers from,
// and the weights live there too. Duplicating any of it in this resolver would be
// a second source of truth for the same figure.
func (r *queryResolver) ArchiveEntityCounts(ctx context.Context) ([]models.ArchiveEntityCount, error) {
	counts, err := r.services.Completion().CountEntities(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]models.ArchiveEntityCount, 0, len(counts))
	for _, c := range counts {
		out = append(out, models.ArchiveEntityCount{
			EntityType: models.EntityType(c.EntityType),
			Count:      c.Count,
		})
	}

	return out, nil
}
