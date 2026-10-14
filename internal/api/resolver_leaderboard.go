package api

import (
	"context"

	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service/leaderboard"
	"github.com/stashapp/stash-box/internal/service/trust"
)

// leaderboardEntryResolver resolves the fields of a board row that need a lookup.
type leaderboardEntryResolver struct{ *Resolver }

// User is the ranked member.
//
// One lookup per row, bounded by the board's limit -- and that is why the limit is clamped
// rather than merely defaulted. An unbounded board would make this an N+1 over the whole
// membership.
func (r *leaderboardEntryResolver) User(ctx context.Context, obj *models.LeaderboardEntry) (*models.User, error) {
	return r.services.User().FindByID(ctx, obj.UserID)
}

// Leaderboard returns the ranked members.
//
// A nil input is the default board rather than an error: the schema makes it optional
// because "the board" is a complete request, and the defaults are the service's to choose.
func (r *queryResolver) Leaderboard(ctx context.Context, input *models.LeaderboardInput) ([]models.LeaderboardEntry, error) {
	opts := leaderboard.Options{}
	if input != nil {
		if input.Days != nil {
			opts.Days = *input.Days
		}
		if input.Limit != nil {
			opts.Limit = *input.Limit
		}
		if input.Level != nil {
			level := trust.LevelEnum(*input.Level)
			opts.Level = &level
		}
	}

	entries, err := r.services.Leaderboard().Leaderboard(ctx, opts)
	if err != nil {
		return nil, err
	}

	out := make([]models.LeaderboardEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, models.LeaderboardEntry{
			Position: entry.Position,
			UserID:   entry.UserID,
			Score:    entry.Score,
			Level:    int(entry.Level),
		})
	}
	return out, nil
}

// SetLeaderboardListed records the caller's own choice about appearing on the board.
func (r *mutationResolver) SetLeaderboardListed(ctx context.Context, listed bool) (*models.UserTrust, error) {
	user := auth.GetCurrentUser(ctx)
	if user == nil {
		// No @hasRole on this mutation, so an anonymous caller reaches this far. The
		// preference is per-account: there is nothing to record and nothing to return.
		return nil, auth.ErrUnauthorized
	}

	if _, err := r.services.Leaderboard().SetListed(ctx, user.ID, listed); err != nil {
		return nil, err
	}

	// Return the resulting standing, as setContentViewingOptIn does, so the client does not
	// need a second round trip to find out whether the flag took.
	return r.userTrustModel(ctx, user.ID)
}
