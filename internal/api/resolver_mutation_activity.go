package api

import (
	"context"
	"errors"

	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service/follow"
)

// Social-discovery mutations (feature-discovery SPEC section 10, phase 7 slice 7).

// FollowUser records that the current user follows someone.
//
// The FOLLOWER IS THE AUTHENTICATED USER and not an argument: a client cannot follow on someone
// else's behalf, and an api that let it would make "who follows whom" a thing anyone could write
// about anyone. Idempotent, and it grants NO visibility -- see SetActivityVisibility.
func (r *mutationResolver) FollowUser(ctx context.Context, input models.FollowInput) (bool, error) {
	currentUser := auth.GetCurrentUser(ctx)
	return true, r.services.Follow().Follow(ctx, currentUser.ID, input.UserID)
}

// UnfollowUser removes the relationship. False means there was none, which is not an error: the
// state the caller asked for is the state that holds.
func (r *mutationResolver) UnfollowUser(ctx context.Context, input models.UnfollowInput) (bool, error) {
	currentUser := auth.GetCurrentUser(ctx)
	existed, err := r.services.Follow().Unfollow(ctx, currentUser.ID, input.UserID)
	if err != nil {
		return false, err
	}
	return existed, nil
}

// storedVisibility maps the GraphQL enum to the level the COLUMN stores.
//
// The two spellings differ on purpose and a `string(input.Visibility)` between them is the bug
// this function exists to prevent: the enum is upper case (GraphQL convention) and the column is
// lower case (matching the CHECK in migration 115 and the values the feed compares against), so
// the naive conversion sends 'FOLLOWERS' to a CHECK that accepts only 'followers'. Found by the
// integration test, which is the only thing that runs both halves.
func storedVisibility(visibility models.ActivityVisibility) string {
	switch visibility {
	case models.ActivityVisibilityPrivate:
		return follow.VisibilityPrivate
	case models.ActivityVisibilityFollowers:
		return follow.VisibilityFollowers
	case models.ActivityVisibilityPublic:
		return follow.VisibilityPublic
	default:
		return ""
	}
}

// SetActivityVisibility publishes, restricts or withdraws ONE follower's access to the CURRENT
// USER's activity.
//
// THE CURRENT USER IS THE AUTHOR, and `input.FollowerID` is only ever the other party. This is the
// single line the whole visibility model rests on: if the follower side could set this, every
// follower could grant themselves `public` and the private default would be decoration. The
// service's signature puts the author first for the same reason; this resolver is where the
// authenticated user is bound to it.
func (r *mutationResolver) SetActivityVisibility(ctx context.Context, input models.ActivityVisibilityInput) (bool, error) {
	currentUser := auth.GetCurrentUser(ctx)

	visibility := storedVisibility(input.Visibility)
	if visibility == "" {
		// Unreachable from a GraphQL client (the enum is closed), and mapped anyway: a value the
		// feed cannot interpret must be refused rather than stored, because the stored value is
		// what the feed reads.
		return false, follow.ErrUnknownVisibility
	}

	err := r.services.Follow().SetVisibility(ctx, currentUser.ID, input.FollowerID, visibility)
	if err != nil {
		if errors.Is(err, follow.ErrNotFollowing) {
			// A visibility on a relationship that does not exist. Not silently true: a caller
			// told "that follower now sees your activity" about a follow that was never there
			// would believe they had published something.
			return false, err
		}
		return false, err
	}
	return true, nil
}
