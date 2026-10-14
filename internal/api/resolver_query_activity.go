package api

import (
	"context"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service/activity"
	"github.com/stashapp/stash-box/internal/service/follow"
)

// Social discovery GraphQL resolvers (feature-discovery SPEC section 10, phase 7 slice 7).
//
// Thin on purpose. The two decisions worth knowing are in the schema file and the services: the
// feed is chronological with no ranking input, and following grants no visibility -- the author
// sets that, per follower. What is left here is translation, plus one place where the translation
// is a permission: SetActivityVisibility passes the AUTHENTICATED user as the author of the
// activity, never the id the client sent.

// ActivityFeed returns what the people this viewer follows have been doing, newest first.
func (r *queryResolver) ActivityFeed(ctx context.Context, input *models.ActivityFeedInput) ([]models.ActivityItem, error) {
	currentUser := auth.GetCurrentUser(ctx)

	feedInput := activity.FeedInput{ViewerID: currentUser.ID}
	if input != nil {
		if input.Limit != nil {
			feedInput.Limit = *input.Limit
		}
		feedInput.Before = input.Before
	}

	items, err := r.services.Activity().Feed(ctx, feedInput)
	if err != nil {
		return nil, err
	}

	out := make([]models.ActivityItem, 0, len(items))
	for _, item := range items {
		row := models.ActivityItem{
			Kind:      models.ActivityKind(item.Kind),
			CreatedAt: item.CreatedAt,
			Actor: &models.ActivityActor{
				ID:   item.ActorID,
				Name: item.ActorName,
			},
			Scene:  item.Scene,
			Rating: item.Rating,
		}
		// Absent rather than empty for a private list: a client rendering `listName` as a link
		// must not render an empty one, and the field's absence is the signal that the reader is
		// not being told which list it was.
		if item.ListID.Valid {
			listID := item.ListID.UUID
			row.ListID = &listID
		}
		row.ListName = item.ListName
		out = append(out, row)
	}
	return out, nil
}

// Following returns who this viewer follows.
func (r *queryResolver) Following(ctx context.Context) ([]models.Follow, error) {
	currentUser := auth.GetCurrentUser(ctx)
	return r.followersOrFollowing(ctx, currentUser.ID, true)
}

// Followers returns who follows this viewer: the read the visibility editor is built on.
func (r *queryResolver) Followers(ctx context.Context) ([]models.Follow, error) {
	currentUser := auth.GetCurrentUser(ctx)
	return r.followersOrFollowing(ctx, currentUser.ID, false)
}

// followersOrFollowing is the two directions in one place, because they differ only in which
// service call produces the rows and both render the same shape.
func (r *queryResolver) followersOrFollowing(ctx context.Context, userID uuid.UUID, following bool) ([]models.Follow, error) {
	var (
		rows []follow.Follow
		err  error
	)
	if following {
		rows, err = r.services.Follow().Following(ctx, userID)
	} else {
		rows, err = r.services.Follow().Followers(ctx, userID)
	}
	if err != nil {
		return nil, err
	}

	out := make([]models.Follow, 0, len(rows))
	for _, row := range rows {
		out = append(out, models.Follow{
			User: &models.ActivityActor{
				ID:   row.UserID,
				Name: row.UserName,
			},
			Visibility: models.ActivityVisibility(visibilityEnum(row.Visibility)),
			CreatedAt:  row.CreatedAt,
		})
	}
	return out, nil
}

// visibilityEnum maps the stored level to the GraphQL enum value.
//
// An unknown stored value maps to PRIVATE, which is the direction that cannot leak: a value the
// service does not recognise (a row written by a newer version, a manual edit) must not be
// reported as a grant. The schema's CHECK makes this unreachable today, and the mapping is here
// because "unreachable" is a property of today's data rather than of this function.
func visibilityEnum(visibility string) string {
	switch visibility {
	case follow.VisibilityFollowers:
		return string(models.ActivityVisibilityFollowers)
	case follow.VisibilityPublic:
		return string(models.ActivityVisibilityPublic)
	default:
		return string(models.ActivityVisibilityPrivate)
	}
}
