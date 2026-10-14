// Package completeness owns the expected-total READ path: what a source claims a studio
// lists, what this instance actually holds, and whether the claim's evidence is still there.
//
// Phase 3a built the table, its constraints and the queries, and nothing read them -- only
// the sqlc struct referenced `expected_totals` (see the note at the top of
// internal/queries/sql/expected_total.sql). This is that read path, for one entity, which is
// what the aggregator's slice 9f needs: a denominator whose source page is gone is a claim
// nobody can check any more, and the instance should say so rather than present the number as
// if it were still verifiable.
package completeness

import (
	"context"
	"fmt"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/content"
	"github.com/stashapp/stash-box/internal/service/ingest"
)

// Service reads denominators and their evidence.
type Service struct {
	queries *queries.Queries
}

// NewService builds the service.
func NewService(q *queries.Queries) *Service {
	return &Service{queries: q}
}

// Total is one denominator with what this instance holds against it, and what is known about
// the page it came from.
type Total struct {
	Kind string

	// Expected is the source's claim. Observed is what the instance holds. Missing is the gap,
	// floored at zero: over-count is real and intended (the instance may hold MORE than the
	// source lists), and reporting a negative gap would read as an error rather than as surplus.
	Expected int64
	Observed int64
	Missing  int64

	// SourceURL is the claim's evidence, exactly as it was asserted -- not normalised, because
	// this is the string a curator wrote.
	SourceURL string

	// Source is the health record for that page, when the instance tracks it. NIL IS THE
	// ORDINARY CASE AND NOT A PROBLEM: most source pages are not URLs this instance aggregates,
	// and "nobody has checked this page" is not "this page is gone".
	Source *models.ContentSource
}

// SourceStatus is the health record's status, or "" when there is no record.
func (t Total) SourceStatus() string {
	if t.Source == nil {
		return ""
	}
	return t.Source.Status
}

// Unverifiable reports whether the claim's evidence is GONE.
//
// Only a status the instance actually observed counts: `dead`, `dmca` and `archived` are
// conclusions, while an absent record and an `unknown` one are both "no evidence either way"
// and must not be reported as a lost source. The distinction is the whole reason this is a
// method on a struct that carries the record, rather than a boolean computed at the call site.
func (t Total) Unverifiable() bool {
	switch t.SourceStatus() {
	case content.StatusDead, content.StatusDMCA, content.StatusArchived:
		return true
	default:
		return false
	}
}

// StudioTotals reads a studio's denominators, the counts held against them, and the health of
// each claim's source page.
//
// The URL is normalised with the ingest path's own canonicaliser before the lookup, which is
// the difference between resolving a curator's hand-typed URL and not: the health record is
// keyed by the canonical form. A source_url that cannot be normalised at all resolves to no
// record, which is the honest answer -- it is not a URL this instance could be tracking.
func (s *Service) StudioTotals(ctx context.Context, studioID uuid.UUID) ([]Total, error) {
	rows, err := s.queries.GetExpectedTotals(ctx, queries.GetExpectedTotalsParams{
		EntityType: "studio",
		EntityID:   studioID,
	})
	if err != nil {
		return nil, fmt.Errorf("reading the studio's denominators: %w", err)
	}

	totals := make([]Total, 0, len(rows))
	for _, row := range rows {
		observed, err := s.queries.GetObservedCount(ctx, queries.GetObservedCountParams{
			Kind:     row.Kind,
			StudioID: studioID,
		})
		if err != nil {
			return nil, fmt.Errorf("counting what is held for %q: %w", row.Kind, err)
		}

		expected := int64(row.Total)
		missing := expected - observed
		if missing < 0 {
			missing = 0
		}

		total := Total{
			Kind:      row.Kind,
			Expected:  expected,
			Observed:  observed,
			Missing:   missing,
			SourceURL: row.SourceUrl,
		}

		if normalized, err := ingest.NormalizeURL(row.SourceUrl); err == nil {
			// ACROSS ENTITY TYPES, not filtered to the total's own: the question is about the PAGE.
			// A studio's denominator is asserted from a page about that studio, and that page is
			// tracked -- when it is tracked at all -- as a URL of an entity this instance
			// aggregates; `content_sources` allows 'scene' and 'content_item' and never 'studio',
			// so a lookup filtered to the total's entity type could not match anything.
			sources, err := s.queries.FindContentSourcesForURL(ctx, &normalized)
			if err != nil {
				return nil, fmt.Errorf("reading the health of %s: %w", row.SourceUrl, err)
			}
			if len(sources) > 0 {
				// The query ranks an alive page above a dead one and an exact normalised match
				// above the fallback, so the first row is the best answer rather than an arbitrary
				// one -- and "best" is what a reader wants: a page one scene has given up on is
				// still a page another scene is serving.
				source := content.ContentSourceToModel(sources[0])
				total.Source = &source
			}
		}

		totals = append(totals, total)
	}

	return totals, nil
}
