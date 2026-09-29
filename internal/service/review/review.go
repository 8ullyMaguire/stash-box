// Package review implements user reviews of directory entities (SPEC §7.10).
//
// Reviews are the first user-generated content on this instance that is neither an
// edit nor a vote, and that shapes three decisions in here.
//
// The one that is easiest to get wrong: A REVIEW IS NOT AN EDIT. An edit corrects
// the archive and goes through moderation, a diff and an approval; a review is
// opinion ABOUT the archive, authored by a user, and is public the moment it is
// written. Reusing the edit machinery for reviews would mean every opinion needs
// moderator approval, and the directory would be empty.
package review

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/stashapp/stash-box/internal/queries"
)

// Status is a review's moderation state.
//
// A string rather than a named type so the database CHECK is the single
// definition -- a Go enum that could drift from the migration's IN (...) list is
// two sources of truth, and the value that matters for behaviour is only ever
// compared, never switched on exhaustively.
const (
	StatusPublished = "published"
	StatusFlagged   = "flagged"
	StatusRemoved   = "removed"
)

// Entity types that can be reviewed.
//
// A closed set in code even though the column is free text, and the asymmetry is
// deliberate. The COLUMN is free text so a new entity type is a code change rather
// than a migration; the CHECK is here so a typo in a GraphQL argument produces a
// clear error rather than an orphan review attached to an entity type nothing will
// ever query. The schema cannot know this list without becoming the thing that has
// to change when the product changes.
const (
	EntitySite      = "SITE"
	EntityStudio    = "STUDIO"
	EntityPerformer = "PERFORMER"
)

// ErrNotFound is returned when a review id does not resolve.
var ErrNotFound = errors.New("review not found")

// ErrNotAuthor is returned when someone tries to edit or delete a review that is
// not theirs.
//
// A distinct error from ErrNotFound and not a generic "unauthorized", because the
// two mean different things to a client: not-found means the review is gone (or
// was never there), not-author means it exists and is not yours. Collapsing them
// would tell an author their own review vanished when a moderator removed it.
var ErrNotAuthor = errors.New("not the author of this review")

// ErrInvalidRating is returned for a rating outside 1..5.
//
// Checked in the service as well as by the schema CHECK, because the service is
// what a GraphQL mutation reaches first and a constraint violation arriving as
// "duplicate key value violates unique constraint" is not a usable error message.
// The CHECK is not redundant: it is what makes the rule true for anything that
// writes the table without going through here.
var ErrInvalidRating = errors.New("rating must be between 1 and 5, or absent")

// ErrEmptyBody is returned for a review with no text.
var ErrEmptyBody = errors.New("review body must not be empty")

// Review is a review as the service returns it.
//
// A distinct type from queries.Review rather than returning the generated row,
// because two of the generated fields are storage concerns a client should not
// see or set: `status` is exposed as a typed field below, and nothing outside this
// package should be able to write it.
type Review struct {
	ID         uuid.UUID
	AuthorID   uuid.UUID
	EntityType string
	EntityID   uuid.UUID
	// Rating is a POINTER because a nullable rating is a real case, not an
	// absence. A review of a studio's ethics has no 1-to-5 value, and
	// flattening that to 0 would put a zero into the average.
	Rating *int
	Body   string
	// Verified is the moderator's usage confirmation. Read-only to authors --
	// Create accepts it only on the internal path, and Submit (the author-facing
	// entry point) never sets it.
	Verified bool
	Status   string
	// CreatedAt is preserved across an edit, so "how long has this person held
	// this opinion" is answerable even though the intermediate ratings are not.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Average is an entity's rating summary.
//
// RatedCount and Total are SEPARATE and must stay separate: a directory showing
// "4.2" over a single review is indistinguishable from a consensus, and the count
// is the only thing that tells them apart. Total includes unrated reviews because
// they are still reviews; Rated excludes them because they cannot contribute to a
// mean.
type Average struct {
	Value float64
	// Rated is how many reviews carried a rating.
	Rated int
	// Total is how many published reviews exist, rated or not.
	Total int
}

// Service implements reviews.
type Service struct {
	queries *queries.Queries
}

// NewService builds a review service.
func NewService(q *queries.Queries) *Service {
	return &Service{queries: q}
}

// SubmitInput is a create-or-update request from an author.
type SubmitInput struct {
	AuthorID   uuid.UUID
	EntityType string
	EntityID   uuid.UUID
	Rating     *int
	Body       string
}

// Submit creates a review, or updates the author's existing review of the same
// entity.
//
// AN UPSERT, decided by the service rather than by the database, and the reason is
// that the unique constraint's error is not usable at this layer: it arrives as
// "duplicate key value violates unique constraint reviews_one_per_author_per_
// entity", from which a client cannot tell which of the two things it did wrong.
// Read-then-write it is, and the race is closed by the constraint -- if two
// submissions for the same author and entity arrive together, one of them gets
// the constraint error and the caller sees a real failure rather than two
// reviews.
//
// The read-then-write is a genuine race and the comment above is the honest
// version of that: the constraint is the authority, this read is a courtesy. That
// ordering matters -- checking first and letting the constraint arbitrate is
// correct, and treating the read as a guarantee is not.
func (s *Service) Submit(ctx context.Context, in SubmitInput) (*Review, error) {
	if err := validate(in); err != nil {
		return nil, err
	}

	existing, err := s.queries.FindReviewByAuthorAndEntity(ctx,
		queries.FindReviewByAuthorAndEntityParams{
			AuthorID:   in.AuthorID,
			EntityType: in.EntityType,
			EntityID:   in.EntityID,
		})
	switch {
	case err == nil:
		// An edit. `verified` is not carried through: an author changing their
		// prose must not be able to carry a moderator's verdict with them, and
		// UpdateReview's SET list does not include it.
		row, err := s.queries.UpdateReview(ctx, queries.UpdateReviewParams{
			ID:     existing.ID,
			Rating: in.Rating,
			Body:   in.Body,
		})
		if err != nil {
			return nil, err
		}
		return toReview(&row), nil

	case errors.Is(err, pgx.ErrNoRows):
		row, err := s.queries.CreateReview(ctx, queries.CreateReviewParams{
			ID:         uuid.Must(uuid.NewV7()),
			AuthorID:   in.AuthorID,
			EntityType: in.EntityType,
			EntityID:   in.EntityID,
			Rating:     in.Rating,
			Body:       in.Body,
			Verified:   false,
			// Published, not "pending". Reviews are public content and a
			// moderation queue on every review would leave the directory empty;
			// the flag path exists for the failure case, which is a different
			// mechanism from requiring approval up front.
			Status: StatusPublished,
		})
		if err != nil {
			return nil, err
		}
		return toReview(&row), nil

	default:
		return nil, err
	}
}

// validate checks a submission before it reaches the database.
//
// Body is trimmed BEFORE the emptiness test, and that is the whole reason this
// function exists rather than a NOT NULL constraint: a review of "   " satisfies
// NOT NULL, renders as nothing, and averages as though a person had said
// something. The database cannot express "not empty" without a CHECK that
// contradicts the plan's own note about leaving length rules to the form.
func validate(in SubmitInput) error {
	if strings.TrimSpace(in.Body) == "" {
		return ErrEmptyBody
	}
	if in.Rating != nil && (*in.Rating < 1 || *in.Rating > 5) {
		return ErrInvalidRating
	}
	switch in.EntityType {
	case EntitySite, EntityStudio, EntityPerformer:
	default:
		return fmt.Errorf("cannot review entity type %q", in.EntityType)
	}
	return nil
}

// Queries exposes the generated query handle.
//
// For the two MODERATION calls -- SetReviewStatus and SetReviewVerified -- which
// this service deliberately does not wrap. They are moderator actions, and a
// wrapper here would be a second, unauthenticated path to them: the trust check
// belongs in the resolver that knows who is asking, and a service method that
// takes an actor id and then has to trust it is worse than no method at all.
//
// The author-facing surface is Submit, Get, ListForEntity, AverageFor and Delete,
// and those are the five with rules in them.
func (s *Service) Queries() *queries.Queries {
	return s.queries
}

// Get returns one review, published or not.
//
// NOT filtered by status: the author resolving their own removed review and a
// moderator resolving a flagged one both need to see it, and the callers that must
// not see it are the listing and average queries, which filter in SQL.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Review, error) {
	row, err := s.queries.FindReview(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return toReview(&row), nil
}

// ListForEntity returns an entity's published reviews, newest first.
//
// The caller supplies the limit and offset rather than this method defaulting
// them, because the shared integration database is not isolated and a hard-coded
// default of 25 is exactly how a test's fixtures get pushed off the end of an
// unfiltered collection (#829, #1007). The server clamps to 100.
func (s *Service) ListForEntity(
	ctx context.Context, entityType string, entityID uuid.UUID, limit, offset int32,
) ([]Review, error) {
	rows, err := s.queries.ListReviewsForEntity(ctx,
		queries.ListReviewsForEntityParams{
			EntityType: entityType,
			EntityID:   entityID,
			Limit:      limit,
			Offset:     offset,
		})
	if err != nil {
		return nil, err
	}
	out := make([]Review, 0, len(rows))
	for _, r := range rows {
		out = append(out, *toReview(&r))
	}
	return out, nil
}

// AverageFor returns an entity's rating summary.
//
// An entity with no reviews is a ZERO Average and no error, because every entity
// page asks this question and an error on a brand-new site is a broken page
// rather than an exceptional condition.
func (s *Service) AverageFor(
	ctx context.Context, entityType string, entityID uuid.UUID,
) (Average, error) {
	row, err := s.queries.GetReviewAverage(ctx,
		queries.GetReviewAverageParams{EntityType: entityType, EntityID: entityID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Average{}, nil
		}
		return Average{}, err
	}
	return Average{
		Value: row.Average,
		Rated: int(row.RatedCount),
		Total: int(row.TotalCount),
	}, nil
}

// Delete removes a review.
//
// Author-only, and the check is here rather than in the resolver so that any other
// caller gets it too. Returns ErrNotAuthor rather than a boolean, so the
// distinction between "gone" and "not yours" survives to the client.
func (s *Service) Delete(ctx context.Context, actorID, id uuid.UUID) error {
	row, err := s.queries.FindReview(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if row.AuthorID != actorID {
		return ErrNotAuthor
	}
	if _, err := s.queries.DeleteReview(ctx, id); err != nil {
		return err
	}
	return nil
}

// toReview converts a generated row to the service type.
//
// A helper, and the standing rule from the plan applies to it: a test of THIS
// function is not a test of Submit, and the wiring is what has to be asserted.
func toReview(r *queries.Review) *Review {
	out := &Review{
		ID:         r.ID,
		AuthorID:   r.AuthorID,
		EntityType: r.EntityType,
		EntityID:   r.EntityID,
		Body:       r.Body,
		Verified:   r.Verified,
		Status:     r.Status,
	}
	if r.Rating != nil {
		rating := *r.Rating
		out.Rating = &rating
	}
	if r.CreatedAt.Valid {
		out.CreatedAt = r.CreatedAt.Time
	}
	if r.UpdatedAt.Valid {
		out.UpdatedAt = r.UpdatedAt.Time
	}
	return out
}
