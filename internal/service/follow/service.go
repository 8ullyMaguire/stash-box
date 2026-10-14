// Package follow records who follows whom and how much of whose activity they may see
// (discovery SPEC section 10, aggregator phase 7 slice 7).
//
// ONE RULE IS THE WHOLE FEATURE: a follow row grants NOTHING until the FOLLOWED user sets a
// visibility on it, and the default is private (migration 115). Following someone is a statement
// about who the follower watches; it is not consent to publish. Everything here exists to keep the
// two apart:
//
//   - Follow is unilateral and idempotent, and it cannot set a visibility.
//   - SetVisibility requires the current user to be the FOLLOWED side, because a follower who
//     could grant themselves `public` would make the default decoration.
//   - The feed reads the grants and nothing else (see internal/service/activity).
package follow

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/errutil"
)

// The three levels, and what each one means for the activity feed. They are strings that match
// the CHECK constraint in migration 115: a value that is in the enum but not in the constraint
// would be rejected by the database at write time rather than silently accepted.
const (
	// VisibilityPrivate is the DEFAULT, and the reason the default is the safe direction: a row
	// nobody has touched shows nothing to anybody.
	VisibilityPrivate = "private"

	// VisibilityFollowers publishes to the ONE follower the row names. Not to the followed
	// user's other followers: the grant is per row, which is what a per-follow column buys.
	VisibilityFollowers = "followers"

	// VisibilityPublic publishes to every reader of the instance, including people who follow
	// nobody. See migration 115 for why a level about the ACTIVITY lives on a row about a
	// RELATIONSHIP.
	VisibilityPublic = "public"
)

var (
	// ErrSelfFollow is returned when a user tries to follow themselves. The database has a CHECK
	// for it; this error exists so the API can say which rule was broken rather than surfacing a
	// constraint name.
	ErrSelfFollow = errors.New("a user cannot follow themselves")

	// ErrUnknownVisibility is returned for a level the feed cannot interpret. Refused rather
	// than defaulted: a typo'd visibility silently becoming 'private' looks like the feature
	// not working, and silently becoming 'public' would be the leak this column exists to
	// prevent.
	ErrUnknownVisibility = errors.New("unknown activity visibility")

	// ErrNotFollowing is returned when a visibility is set on a follow that does not exist.
	// Not a silent success, because a caller told "the grant is now followers" about a row that
	// was never there would believe they had published something.
	ErrNotFollowing = errors.New("no such follow")
)

// Follow is one row: who follows whom, and what they were granted.
type Follow struct {
	// UserID is the OTHER side of the relationship, which is what a caller usually wants: the
	// followed user when listing who you follow, the follower when listing who follows you.
	// Named rather than spelled Follower/Followed so one type serves both directions, and
	// documented here so the ambiguity cannot be misread.
	UserID     uuid.UUID
	UserName   string
	Visibility string
	CreatedAt  time.Time
}

// Service is the follows service.
type Service struct {
	queries *queries.Queries
	withTxn queries.WithTxnFunc
}

// New creates a follows service.
func New(queries *queries.Queries, withTxn queries.WithTxnFunc) *Service {
	return &Service{queries: queries, withTxn: withTxn}
}

// Follow makes followerID follow followedID, and grants nothing.
//
// Idempotent: a second call is a no-op rather than an error, because the effect the caller asked
// for is already true. It does NOT touch the visibility, so re-following cannot reset a grant the
// followed user made.
func (s *Service) Follow(ctx context.Context, followerID, followedID uuid.UUID) error {
	if followerID == followedID {
		return ErrSelfFollow
	}
	_, err := s.queries.UpsertFollow(ctx, queries.UpsertFollowParams{
		FollowerID: followerID,
		FollowedID: followedID,
	})
	if err != nil {
		return fmt.Errorf("following: %w", err)
	}
	return nil
}

// Unfollow removes the relationship. The bool reports whether one was there, so a caller can tell
// "unfollowed" from "you were not following them" without a second read.
func (s *Service) Unfollow(ctx context.Context, followerID, followedID uuid.UUID) (bool, error) {
	rows, err := s.queries.DeleteFollow(ctx, queries.DeleteFollowParams{
		FollowerID: followerID,
		FollowedID: followedID,
	})
	if err != nil {
		return false, fmt.Errorf("unfollowing: %w", err)
	}
	return rows > 0, nil
}

// SetVisibility records the FOLLOWED user's grant to one follower.
//
// followedID is the CURRENT USER and followerID is the other party. The parameter names are
// deliberately in that order at the call site, because the mistake this signature exists to make
// impossible is a follower editing their own access: the service cannot tell who is calling, so
// the resolver must pass the authenticated user as followedID, and the name says so.
func (s *Service) SetVisibility(ctx context.Context, followedID, followerID uuid.UUID, visibility string) error {
	if !ValidVisibility(visibility) {
		return ErrUnknownVisibility
	}
	rows, err := s.queries.SetFollowVisibility(ctx, queries.SetFollowVisibilityParams{
		FollowerID:         followerID,
		FollowedID:         followedID,
		ActivityVisibility: visibility,
	})
	if err != nil {
		return fmt.Errorf("setting activity visibility: %w", err)
	}
	if rows == 0 {
		return ErrNotFollowing
	}
	return nil
}

// ValidVisibility reports whether a level is one the feed can interpret.
func ValidVisibility(visibility string) bool {
	switch visibility {
	case VisibilityPrivate, VisibilityFollowers, VisibilityPublic:
		return true
	default:
		return false
	}
}

// Following lists the users this user follows, newest first, with the visibility each was
// granted.
func (s *Service) Following(ctx context.Context, userID uuid.UUID) ([]Follow, error) {
	rows, err := s.queries.FindFollowing(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("listing follows: %w", err)
	}
	out := make([]Follow, 0, len(rows))
	for _, row := range rows {
		out = append(out, Follow{
			UserID:     row.UserID,
			UserName:   row.UserName,
			Visibility: row.ActivityVisibility,
			CreatedAt:  row.CreatedAt.Time,
		})
	}
	return out, nil
}

// Followers lists the users who follow this user, newest first, with the visibility each was
// granted. This is the read the visibility editor is built on.
func (s *Service) Followers(ctx context.Context, userID uuid.UUID) ([]Follow, error) {
	rows, err := s.queries.FindFollowers(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("listing followers: %w", err)
	}
	out := make([]Follow, 0, len(rows))
	for _, row := range rows {
		out = append(out, Follow{
			UserID:     row.UserID,
			UserName:   row.UserName,
			Visibility: row.ActivityVisibility,
			CreatedAt:  row.CreatedAt.Time,
		})
	}
	return out, nil
}

// IsFollowing reports whether followerID follows followedID.
func (s *Service) IsFollowing(ctx context.Context, followerID, followedID uuid.UUID) (bool, error) {
	_, err := s.queries.FindFollow(ctx, queries.FindFollowParams{
		FollowerID: followerID,
		FollowedID: followedID,
	})
	if err != nil {
		// No rows IS the answer here, not a failure: "is A following B" has a false.
		if errutil.IgnoreNotFound(err) == nil {
			return false, nil
		}
		return false, fmt.Errorf("reading the follow: %w", err)
	}
	return true, nil
}
