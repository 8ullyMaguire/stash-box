package api

import (
	"context"
	"time"

	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service/streak"
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

	return streakModel(result), nil
}

// streakModel maps the service's streak onto the schema's, in one place so the two
// callers (the query and the freeze mutation) cannot disagree about what a streak is.
//
// The DateTime scalar is a string here (gqlgen's default binding), so `lastActiveDay`
// is formatted rather than passed through.
//
// The VALUE is a calendar date, not an instant: it came back from SQL as a
// `timestamp` truncated to the database's day. Rendering it as UTC is a formatting
// choice that would SHIFT it -- 2026-10-01 local midnight is 2026-09-30 22:00 UTC, and
// the client would show the day before the one the database matched against. A plain
// YYYY-MM-DD is what the field actually is, and re-parsing it in the client's zone
// cannot move it.
func streakModel(result streak.Streak) *models.Streak {
	out := &models.Streak{
		CurrentStreak:    result.CurrentStreak,
		LongestStreak:    result.LongestStreak,
		TotalActiveDays:  result.TotalActiveDays,
		ActiveToday:      result.ActiveToday,
		FreezesEarned:    result.FreezesEarned,
		FreezesUsed:      result.FreezesUsed,
		FreezesAvailable: result.FreezesAvailable,
	}
	if result.LastActiveDay != nil {
		day := result.LastActiveDay.Format("2006-01-02")
		out.LastActiveDay = &day
	}
	return out
}

// SpendStreakFreeze covers the most recent missed day and returns the repaired streak.
//
// The mutation returns the STREAK rather than a confirmation, because the thing a
// member wants to see after repairing a run is the run: a boolean would make every
// client follow it with the query it already knew how to make, and would let the
// two answers be read at different moments.
func (r *mutationResolver) SpendStreakFreeze(ctx context.Context) (*models.Streak, error) {
	user := auth.GetCurrentUser(ctx)
	if user == nil {
		return nil, auth.ErrUnauthorized
	}
	result, err := r.services.Streak().SpendFreeze(ctx, user.ID, time.Now())
	if err != nil {
		return nil, err
	}
	return streakModel(result), nil
}

// ContributionHeatmap returns the member's per-day contribution counts.
//
// The @isUserOwner directive has already rejected anybody but the member, so there is
// no check here -- the same shape as the owner-gated trust object. The counters rule
// is why: the level a member has is public, and the work behind it is not.
func (r *userResolver) ContributionHeatmap(ctx context.Context, user *models.User, days *int) ([]models.ContributionDay, error) {
	window := 0
	if days != nil {
		window = *days
	}

	rows, err := r.services.Streak().ContributionHeatmap(ctx, user.ID, window)
	if err != nil {
		return nil, err
	}

	out := make([]models.ContributionDay, 0, len(rows))
	for _, row := range rows {
		out = append(out, models.ContributionDay{
			// The same rendering the streak's own day uses: a calendar day in the
			// database's zone, not an instant for a client to interpret in its own.
			Day:   row.Day.Format("2006-01-02"),
			Count: row.Count,
		})
	}
	return out, nil
}
