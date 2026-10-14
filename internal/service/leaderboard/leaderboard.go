// Package leaderboard ranks members by their contribution over a window.
//
// THE ONE SURFACE THAT PUBLISHES NUMBERS THAT ARE OTHERWISE PRIVATE. Slice 6a settled
// that a member's LEVEL is public and their counters are not, because the counters carry
// the approved/rejected RATIO and a ratio is a performance review. A leaderboard is a page
// of counters in rank order -- which is defensible here for two reasons and only here:
//
//   - What is published is a count of POSITIVE contributions over a window, not the
//     ratio. A member's rejected edits are summed into their LEVEL, in Go, by the same
//     curve the badge uses, and are never shown as a column.
//   - Every member can leave. Participation is OPT-OUT (the plan's word), and opting out
//     costs nothing else: no capability, no level, no badge is affected by the flag, and
//     the phase's own verification is exactly that.
//
// ABSENCE IS AMBIGUOUS, WHICH IS THE POINT. A member who opted out and a member who has
// contributed nothing this month look identical on the board: neither has a row. So the
// board cannot be read as a list of who declined -- only of who is showing.
package leaderboard

import (
	"context"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/trust"
)

const (
	// DefaultDays is the window a caller gets by asking for nothing: a month. Long enough
	// that a board means something, short enough that it is not the same board all year.
	DefaultDays = 30
	// MaxDays bounds the window, a year and a day like the heatmap's, so a leap year fits
	// and the default is reachable exactly.
	MaxDays = 366
	// DefaultLimit is how many members a board shows by default.
	DefaultLimit = 25
	// MaxLimit bounds it. A leaderboard is a page, not an export.
	MaxLimit = 100
)

// Options is a board's request.
type Options struct {
	// Days is the window. Clamped, not refused; zero means the default.
	Days int
	// Level narrows the board to one level, or nil for every level.
	//
	// The narrowing happens AFTER the ranking, so the positions are the members' real
	// standing: a level-scoped board shows position 3, 7, 12 rather than renumbering the
	// handful of members who happen to share a level. It can therefore return fewer rows
	// than the limit, which is the truth about a filter rather than a bug in it.
	Level *trust.LevelEnum
	// Limit is how many rows to return. Clamped, not refused.
	Limit int
}

// Entry is one row of a board.
type Entry struct {
	// Position is 1-based and is the member's rank in the WHOLE ranking, before any level
	// filter, so a filtered board's positions have gaps -- which is what a rank means.
	Position int
	UserID   uuid.UUID
	// Score is the number of contributions in the window. Not points, not the level.
	Score int
	// Level is the member's public standing, derived by the one implementation of the
	// curve from the counters the query returned.
	Level trust.LevelEnum
}

// Service reads and writes leaderboard participation.
type Service struct {
	queries *queries.Queries
}

// NewService builds a leaderboard service.
func NewService(q *queries.Queries) *Service {
	return &Service{queries: q}
}

// Leaderboard returns the ranked members.
//
// "Now" comes from the DATABASE, following the rule the streak and the heatmap already
// follow: a window that ended on the server's day while the rest of the instance used the
// database's would include or exclude a day depending on which process was asked.
func (s *Service) Leaderboard(ctx context.Context, opts Options) ([]Entry, error) {
	days := clampDays(opts.Days)
	limit := clampLimit(opts.Limit)

	today, err := s.queries.DatabaseNow(ctx)
	if err != nil {
		return nil, err
	}

	rows, err := s.queries.ListLeaderboard(ctx, queries.ListLeaderboardParams{
		Since:      pgTimestamp(sinceFor(today, days)),
		LimitCount: int32(limit),
	})
	if err != nil {
		return nil, err
	}

	// THE LEVEL IS READ, NOT RECOMPUTED. Running the curve over the totals here was the
	// first implementation, and a test failed on the first run: a member inside a recovery
	// window has a level the policy HOLDS DOWN, deliberately not what their totals justify
	// (slice 6b-1), so the board disagreed with the badge in exactly the case the policy
	// exists for. The stored level is the one number that has been through the policy, and
	// this is the only reading of it that cannot drift.
	out := make([]Entry, 0, len(rows))
	for i, row := range rows {
		entry := Entry{
			Position: i + 1,
			UserID:   row.UserID,
			Score:    int(row.Score),
			Level:    trust.LevelEnum(row.Level),
		}
		if opts.Level != nil && entry.Level != *opts.Level {
			continue
		}
		out = append(out, entry)
	}
	return out, nil
}

// SetListed records whether the member appears on the board.
//
// The member must already have a user_trust row for this to write: a member who has never
// contributed has no counters to publish, so there is nothing to opt out of, and the
// caller is expected to have handled that -- the API returns the rollup, and a caller with
// no rollup has no board to appear on.
func (s *Service) SetListed(ctx context.Context, userID uuid.UUID, listed bool) (*queries.UserTrust, error) {
	// The upsert, not the update: a member with no rollup row has nothing to update, and
	// "I would rather not be on the board" is a sentence a member with nothing to show
	// should still be able to say. The row this creates is the same zeroed rollup the read
	// path already reports for a member who has none.
	row, err := s.queries.UpsertLeaderboardListed(ctx, queries.UpsertLeaderboardListedParams{
		UserID:            userID,
		LeaderboardListed: listed,
	})
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// clampDays bounds a requested window.
//
// Zero and negatives become the default: a GraphQL `days: 0` is what a client sends when
// it has no value, not a request for an empty board.
func clampDays(days int) int {
	if days <= 0 {
		return DefaultDays
	}
	if days > MaxDays {
		return MaxDays
	}
	return days
}

// clampLimit bounds a requested page size, the same way and for the same reason.
func clampLimit(limit int) int {
	if limit <= 0 {
		return DefaultLimit
	}
	if limit > MaxLimit {
		return MaxLimit
	}
	return limit
}

// sinceFor is the first day a window includes, given the day it ends on.
//
// THE SAME RULE AS THE HEATMAP'S, INCLUDING THE OFF-BY-ONE: the window includes today and
// is `days` long, so it starts `days-1` back. Re-zoning is deliberately absent -- the day
// arrives truncated in the database's frame and leaves in it.
func sinceFor(today time.Time, days int) time.Time {
	truncated := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, today.Location())
	return truncated.AddDate(0, 0, -(days - 1))
}

func pgTimestamp(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}
