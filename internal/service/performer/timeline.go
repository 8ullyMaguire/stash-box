package performer

import (
	"context"
	"fmt"

	"github.com/gofrs/uuid"
)

// Performer timeline (growth item 21).
//
// Year-bucketed appearance history. The scope is deliberately narrow and stated in the
// spec: `scenes.date` is the only date the schema attaches to an appearance, so a
// year-bucketed count is the strongest timeline this database supports. A finer one --
// per-appearance credit lines, or a continuous span between first and last appearance --
// would need a new table, and pretending to offer one would be offering a guess.

// TimelineEntry is one year and how many scenes the performer appeared in that year.
type TimelineEntry struct {
	// Year is the calendar year taken from scenes.date. Null dates cannot appear here.
	Year int
	// SceneCount is the number of appearances in that year.
	SceneCount int64
}

// Timeline is a performer's appearance history.
//
// UndatedCount is part of the result rather than an afterthought. `scenes.date` is
// NULLABLE, and an undated appearance belongs to no bucket, so without this number the
// timeline silently under-reports: a performer with 50 scenes, 3 of them dated, renders
// as a 3-scene career and looks like a nearly-unknown performer rather than one whose
// dates were never recorded. Those are very different things to a reader, and the two
// fields together are what tell them apart.
type Timeline struct {
	Entries []TimelineEntry

	// UndatedCount is the number of appearances whose scene has no date. Always
	// reported, including when zero.
	UndatedCount int64
}

// TotalSceneCount is the number of appearances across every bucket plus the undated ones.
//
// Not the sum of the buckets alone: that would silently exclude undated appearances and
// disagree with CountScenesByPerformer, which is the sort of two-numbers-that-should-
// agree-but-don't bug that is nearly impossible to notice and trivial to assert against.
func (t Timeline) TotalSceneCount() int64 {
	total := t.UndatedCount
	for _, e := range t.Entries {
		total += e.SceneCount
	}
	return total
}

// Timeline returns the performer's appearance history, oldest year first.
//
// Empty rather than an error for a performer with no dated appearances. A performer who
// exists and has no history is not an error condition.
func (s *Performer) Timeline(ctx context.Context, id uuid.UUID) (*Timeline, error) {
	rows, err := s.queries.FindPerformerTimeline(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("building timeline for performer %s: %w", id, err)
	}

	// Non-nil empty slice, not nil. gqlgen emits `null` for a nil slice under a
	// `[Type!]!` field, and a timeline that reports null rather than [] is a shape a
	// client has to special-case for no benefit.
	entries := make([]TimelineEntry, 0, len(rows))
	for _, r := range rows {
		entries = append(entries, TimelineEntry{
			Year:       r.Year,
			SceneCount: r.SceneCount,
		})
	}

	undated, err := s.queries.CountUndatedScenesByPerformer(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("counting undated appearances for performer %s: %w", id, err)
	}

	return &Timeline{
		Entries:      entries,
		UndatedCount: undated,
	}, nil
}