package api

import (
	"context"
	"errors"
	"fmt"

	"github.com/gofrs/uuid"
	"github.com/vektah/gqlparser/v2/gqlerror"

	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service/errutil"
	"github.com/stashapp/stash-box/internal/service/review"
	"github.com/stashapp/stash-box/internal/service/user"
)

// Structured reviews over GraphQL (SPEC §7.10, growth item 29).
//
// Almost entirely wiring. The `reviews` table, 11 sqlc queries, the service and its
// tests already existed -- the tracker was wrong that no review type existed. The
// design work is in three decisions, each a place where the obvious wiring is wrong:
//
//   - A review is NOT an edit. Edits go through moderation and a diff; a review is
//     opinion and is public the moment it is written. Routing reviews through the edit
//     machinery would mean every opinion needs moderator approval and the directory
//     stays empty.
//   - Flagged and removed reviews are filtered IN THE QUERY, not afterwards, so their
//     absence from a listing leaks nothing. An author still sees their own as FLAGGED.
//   - No reviews is an empty list and a null average, never an error and never a zero.
//     Every entity page asks this question, so an error here is a broken page on a
//     brand-new site.

// reviewPtr returns a pointer to a converted review.
//
// Local rather than reusing a generic helper: the repo's existing `ptr` helpers are
// build-tagged into test files, so using one here would not compile outside tests.
func reviewPtr(r models.Review) *models.Review { return &r }

// toReviewStatus maps the service's stored status to the GraphQL enum.
//
// The service keeps status as a plain string so the database CHECK is the single
// definition (see review.go). An unrecognised value therefore falls back to REMOVED
// rather than PUBLISHED: if a fourth status is added to the migration and not here,
// defaulting to PUBLISHED would show a taken-down review as visible. The restrictive
// default is the safe direction to be wrong in.
func toReviewStatus(s string) models.ReviewStatusEnum {
	switch s {
	case review.StatusPublished:
		return models.ReviewStatusEnumPublished
	case review.StatusFlagged:
		return models.ReviewStatusEnumFlagged
	case review.StatusRemoved:
		return models.ReviewStatusEnumRemoved
	}
	return models.ReviewStatusEnumRemoved
}

// entityTypeString maps the GraphQL enum to the service's string constant. Explicit
// rather than a cast so an enum member cannot silently drift from the service constant.
func entityTypeString(t models.ReviewEntityTypeEnum) string {
	switch t {
	case models.ReviewEntityTypeEnumPerformer:
		return review.EntityPerformer
	case models.ReviewEntityTypeEnumStudio:
		return review.EntityStudio
	case models.ReviewEntityTypeEnumSite:
		return review.EntitySite
	}
	// Unreachable: the enum has three members and gqlgen validates before here.
	return review.EntitySite
}

// reviewToModel converts for GraphQL.
//
// The rating pointer is preserved rather than defaulted: a null rating is a real case,
// and flattening it to 0 would put a zero into the average.
// gqlgen reads `author` straight off models.Review (`return obj.Author, nil`), so there
// is no ReviewResolver to hang a lazy loader on and the author has to be resolved while
// the review is converted. That is why this takes a loader: doing it per review would be
// N queries for one page of results.
//
// A review whose author has been deleted degrades to a nil Author rather than failing
// the listing -- one orphaned author should not blank a page of reviews.
func reviewToModel(r *review.Review, author *models.User) models.Review {
	return models.Review{
		ID:         r.ID,
		Author:     author,
		EntityType: models.ReviewEntityTypeEnum(r.EntityType),
		EntityID:   r.EntityID,
		Rating:     r.Rating,
		Body:       r.Body,
		Verified:   r.Verified,
		Status:     toReviewStatus(r.Status),
		CreatedAt:  r.CreatedAt,
		UpdatedAt:  r.UpdatedAt,
	}
}

// reviewAuthors loads the distinct authors of a review list in ONE query.
//
// Per-review loading would make a 25-review page 25 round trips, and the author is
// rendered on every row. Collected first, deduped, then looked up once.
func reviewAuthors(ctx context.Context, svc *user.User, list []review.Review) map[uuid.UUID]*models.User {
	ids := make([]uuid.UUID, 0, len(list))
	seen := make(map[uuid.UUID]struct{}, len(list))
	for _, r := range list {
		if _, ok := seen[r.AuthorID]; !ok {
			seen[r.AuthorID] = struct{}{}
			ids = append(ids, r.AuthorID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	// LoadIds reports one error per requested id rather than one aggregate error, which
	// is what we want: a deleted author is a per-id miss, not a failure of the load. It
	// wraps loadutil.One, so a missing id yields a nil user rather than an error.
	users, errs := svc.LoadIds(ctx, ids)
	out := make(map[uuid.UUID]*models.User, len(users))
	for _, err := range errs {
		// IgnoreNotFound, not errors.Is: the not-found sentinel lives in a different
		// package per service, so testing for it here would bind this file to whichever
		// one LoadIds happened to return.
		if err := errutil.IgnoreNotFound(err); err != nil {
			// A genuine load failure must not fail the review listing: the reviews are
			// real and their bodies are the content, so authors degrade to nil.
			return out
		}
	}
	for _, u := range users {
		if u != nil {
			out[u.ID] = u
		}
	}
	return out
}

// reviewErrors maps the service's sentinel errors onto client-facing errors.
//
// The mapping preserves the distinction the service deliberately keeps between "not
// found" and "not yours": collapsing them would tell an author their own review
// vanished when a moderator removed it.
func reviewErrors(err error) error {
	switch {
	case errors.Is(err, review.ErrNotFound):
		return gqlerror.Errorf("review not found")
	case errors.Is(err, review.ErrNotAuthor):
		return gqlerror.Errorf("not the author of this review")
	case errors.Is(err, review.ErrInvalidRating):
		return gqlerror.Errorf("rating must be between 1 and 5, or absent")
	case errors.Is(err, review.ErrEmptyBody):
		return gqlerror.Errorf("review body must not be empty")
	}
	return nil
}

// reviewPage bounds and defaults the pagination arguments.
//
// Clamped rather than rejected: an oversized perPage is a client that wants results,
// and erroring teaches it to retry smaller for no benefit. The ceiling exists because
// each review embeds an author, so an unbounded page is an unbounded author load.
func reviewPage(perPage, page *int) (limit, offset int32) {
	const maxPerPage = 100
	n := 25
	if perPage != nil && *perPage > 0 {
		n = *perPage
	}
	if n > maxPerPage {
		n = maxPerPage
	}
	pageNum := 1
	if page != nil && *page > 0 {
		pageNum = *page
	}
	return int32(n), int32((pageNum - 1) * n)
}

// --- Performer -------------------------------------------------------------------

func (r *performerResolver) Reviews(ctx context.Context, obj *models.Performer, perPage, page *int) ([]models.Review, error) {
	return reviewsForEntity(ctx, r.services.Review(), r.services.User(), review.EntityPerformer, obj.ID, perPage, page)
}

func (r *performerResolver) ReviewSummary(ctx context.Context, obj *models.Performer) (*models.ReviewSummary, error) {
	return reviewSummaryFor(ctx, r.services.Review(), review.EntityPerformer, obj.ID)
}

// --- Studio ----------------------------------------------------------------------

func (r *studioResolver) Reviews(ctx context.Context, obj *models.Studio, perPage, page *int) ([]models.Review, error) {
	return reviewsForEntity(ctx, r.services.Review(), r.services.User(), review.EntityStudio, obj.ID, perPage, page)
}

func (r *studioResolver) ReviewSummary(ctx context.Context, obj *models.Studio) (*models.ReviewSummary, error) {
	return reviewSummaryFor(ctx, r.services.Review(), review.EntityStudio, obj.ID)
}

// --- shared ----------------------------------------------------------------------

// reviewsForEntity lists published reviews for any entity type.
//
// An empty list is the normal answer, not a failure: most entities have no reviews, and
// returning an error would make a sparse archive look broken.
func reviewsForEntity(
	ctx context.Context, svc *review.Service, users *user.User, entityType string,
	id uuid.UUID, perPage, page *int,
) ([]models.Review, error) {
	limit, offset := reviewPage(perPage, page)
	list, err := svc.ListForEntity(ctx, entityType, id, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("listing %s reviews: %w", entityType, err)
	}
	authors := reviewAuthors(ctx, users, list)
	out := make([]models.Review, 0, len(list))
	for i := range list {
		out = append(out, reviewToModel(&list[i], authors[list[i].AuthorID]))
	}
	return out, nil
}

// reviewSummaryFor returns the rating summary for any entity type.
//
// The nil-versus-zero distinction is the point: an entity with no ratings gets
// average = nil and totalCount = 0, never average = 0, because 0 is not a rating and
// would render as "rated zero by everyone".
func reviewSummaryFor(
	ctx context.Context, svc *review.Service, entityType string, id uuid.UUID,
) (*models.ReviewSummary, error) {
	avg, err := svc.AverageFor(ctx, entityType, id)
	if err != nil {
		return nil, fmt.Errorf("summarising %s reviews: %w", entityType, err)
	}
	var value *float64
	if avg.Rated > 0 {
		v := avg.Value
		value = &v
	}
	return &models.ReviewSummary{
		Average:    value,
		RatedCount: avg.Rated,
		TotalCount: avg.Total,
	}, nil
}

// --- Query -----------------------------------------------------------------------

// Review returns a single review, or null when it does not exist.
func (r *queryResolver) Review(ctx context.Context, id uuid.UUID) (*models.Review, error) {
	rev, err := r.services.Review().Get(ctx, id)
	if err != nil {
		if errors.Is(err, review.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("finding review: %w", err)
	}
	return reviewPtr(reviewToModel(rev, reviewAuthor(ctx, r.services.User(), rev.AuthorID))), nil
}

// --- Mutation --------------------------------------------------------------------

// ReviewSubmit writes or replaces the caller's review of an entity.
//
// One mutation for both, because editing your review is the same act as writing it: a
// separate update mutation would let a client create two reviews where the author
// wanted one.
func (r *mutationResolver) ReviewSubmit(ctx context.Context, input models.ReviewSubmitInput) (*models.Review, error) {
	entityID := input.EntityID
	currentUser := auth.GetCurrentUser(ctx)
	out, err := r.services.Review().Submit(ctx, review.SubmitInput{
		AuthorID:   currentUser.ID,
		EntityType: entityTypeString(input.EntityType),
		EntityID:   entityID,
		Rating:     input.Rating,
		Body:       input.Body,
	})
	if err != nil {
		if mapped := reviewErrors(err); mapped != nil {
			return nil, mapped
		}
		return nil, fmt.Errorf("submitting review: %w", err)
	}
	return reviewPtr(reviewToModel(out, reviewAuthor(ctx, r.services.User(), out.AuthorID))), nil
}

// ReviewDelete removes the caller's own review.
//
// Author-only, enforced in the service rather than here so every caller gets the check.
// A moderator removes reviews by flagging, not by deleting, which is what keeps
// `created_at` meaningful for a review that was taken down.
func (r *mutationResolver) ReviewDelete(ctx context.Context, id uuid.UUID) (bool, error) {
	if err := r.services.Review().Delete(ctx, auth.GetCurrentUser(ctx).ID, id); err != nil {
		if mapped := reviewErrors(err); mapped != nil {
			return false, mapped
		}
		return false, fmt.Errorf("deleting review: %w", err)
	}
	return true, nil
}

// reviewAuthor resolves one author, returning nil rather than an error.
//
// See reviewAuthors for why a missing author degrades rather than failing.
func reviewAuthor(ctx context.Context, svc *user.User, id uuid.UUID) *models.User {
	u, err := svc.LoadIds(ctx, []uuid.UUID{id})
	if err != nil || len(u) == 0 {
		return nil
	}
	return u[0]
}
