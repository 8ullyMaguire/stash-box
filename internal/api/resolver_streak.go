package api

import (
	"context"

	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/models"
)

// UserStreak returns the calling user's activity streak.
//
// The clock comes from the streak service's ForCurrent, not from time.Now() and
// not from a clock read in this file. Activity days are stamped and truncated by
// PostgreSQL in the database's timezone, so the only correct "today" is the one
// the database would use. Getting that from the host instead makes "active
// today" false for an event that landed seconds ago, and the failure looks like
// a streak bug rather than a clock bug.
//
// ForCurrent also means every field in one response is computed against a
// single value of "today". A resolver that read the clock per field could
// disagree with itself across midnight -- reporting a streak that both is and is
// not alive, depending on which field crossed the boundary first.
//
// There is deliberately no `userStreak(id:)`. A streak is a private measure of
// one's own contribution, and making it queryable for arbitrary users turns a
// progress indicator into a public ranking of who has been absent.
func (r *queryResolver) UserStreak(ctx context.Context) (*models.Streak, error) {
	// The @hasRole(READ) directive should have rejected this already. A zero
	// streak rather than an error is deliberate: an unauthenticated or
	// not-yet-registered user seeing "no activity" is the ordinary first-run
	// state, and an error page for it would be worse than the answer.
	user := auth.GetCurrentUser(ctx)
	if user == nil {
		return &models.Streak{}, nil
	}

	result, err := r.services.Streak().ForCurrent(ctx, user.ID)
	if err != nil {
		return nil, err
	}

	out := &models.Streak{
		CurrentStreak:   result.CurrentStreak,
		LongestStreak:   result.LongestStreak,
		TotalActiveDays: result.TotalActiveDays,
		ActiveToday:     result.ActiveToday,
	}

	// The DateTime scalar is a string here (gqlgen's default binding), so the
	// day is formatted rather than passed through. RFC3339 in UTC, matching
	// resolver_federation.go.
	//
	// The VALUE is a calendar date, not an instant: it came back from SQL as a
	// `timestamp` truncated to the database's day. Rendering it as UTC is a
	// formatting choice that would SHIFT it -- 2026-10-01 local midnight is
	// 2026-09-30 22:00 UTC, and the client would show the day before the one the
	// database matched against. So it is formatted as a plain YYYY-MM-DD, which is
	// what the field actually is, and re-parsing it in the client's zone cannot
	// move it.
	if result.LastActiveDay != nil {
		day := result.LastActiveDay.Format("2006-01-02")
		out.LastActiveDay = &day
	}

	return out, nil
}
