package streak

import (
	"context"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/stashapp/stash-box/internal/queries"
)

// The contribution heatmap (plan phase 6 slice 6e).
//
// It lives in the streak service because the streak already owns the one derivation
// that matters here: which calendar day a contribution fell on. A heatmap with its
// own answer to that question would eventually show a member a grid that contradicts
// the run displayed beside it, and the disagreement would be invisible until someone
// compared them by hand.
//
// What it adds is the COUNT. A streak measures showing up; a heatmap measures how
// much, and those are different numbers about the same log -- forty approvals in one
// afternoon is one active day and forty contributions.

// HeatmapDay is one day's contribution count.
type HeatmapDay struct {
	Day   time.Time
	Count int
}

const (
	// HeatmapDefaultDays is the window a caller gets by asking for nothing: a year,
	// because a contribution heatmap is read as a year at a glance.
	HeatmapDefaultDays = 365
	// HeatmapMaxDays is the longest window a caller can ask for.
	//
	// A year and a day, so the default is reachable exactly and a leap year fits.
	// Longer windows are CLAMPED rather than refused: the cost of the query is the
	// window, and a client that asks for ten years is asking for a grid nobody can
	// see. The clamped answer is still the truth about the days it covers, so a
	// caller that wants more can page by asking again with a later window.
	HeatmapMaxDays = 366
)

// clampHeatmapDays narrows a requested window into the range the endpoint serves.
//
// Zero and negatives become the default rather than an error, because an absent
// argument and an omitted one are the same thing on the wire: a GraphQL `days: 0`
// is what a client sends when it has no value, not a request for an empty grid.
func clampHeatmapDays(days int) int {
	if days <= 0 {
		return HeatmapDefaultDays
	}
	if days > HeatmapMaxDays {
		return HeatmapMaxDays
	}
	return days
}

// heatmapSince is the first day the window includes, given the day it ends on.
//
// The window is inclusive of `today` and `days-1` days long, so asking for 1 returns
// today alone. Computed from a day already truncated in the DATABASE's zone by the
// caller, and truncated again here in the same frame: an instant would let the
// boundary land mid-day and hand back a partial first day.
func heatmapSince(today time.Time, days int) time.Time {
	truncated := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, today.Location())
	return truncated.AddDate(0, 0, -(days - 1))
}

// ContributionHeatmap returns the member's per-day contribution counts, newest
// first, over a window ending today.
//
// "Today" is the DATABASE's day, not the caller's and not the server's: the streak
// reads its notion of the current day from the database for the same reason, and a
// heatmap whose last column was a day off from the streak's would be the exact
// confusion this service exists to prevent.
//
// Only days WITH contributions are returned. The grid's empties are the client's to
// draw -- it knows the window it asked for, and a year of zero-rows would be 365
// objects saying nothing.
func (s *Service) ContributionHeatmap(ctx context.Context, userID uuid.UUID, days int) ([]HeatmapDay, error) {
	window := clampHeatmapDays(days)

	today, err := s.queries.DatabaseNow(ctx)
	if err != nil {
		return nil, err
	}

	rows, err := s.queries.ListUserContributionDays(ctx, queries.ListUserContributionDaysParams{
		UserID: userID,
		Since:  pgTimestamp(heatmapSince(today, window)),
	})
	if err != nil {
		return nil, err
	}

	out := make([]HeatmapDay, 0, len(rows))
	for _, row := range rows {
		out = append(out, HeatmapDay{Day: row.Day, Count: int(row.Count)})
	}
	return out, nil
}

// pgTimestamp bridges the service's time.Time to the generated param type.
//
// The window is always set -- there is no "no cutoff" query, because a heatmap with no
// cutoff is the whole life of the account -- so Valid is true by construction rather
// than by a caller's choice.
func pgTimestamp(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}
