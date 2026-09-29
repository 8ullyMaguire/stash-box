//go:build integration

package api_test

import (
	"context"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/review"
)

// Reviews against the real database (SPEC §7.10, Phase 3 step 1).
//
// The unit tests pin validation and the rating pointer. None of that reaches the
// parts that only exist as SQL: the unique constraint, the average's FILTER, and
// the difference between a rated review and a total one.

// newReviewService builds the review service over the test database.
func newReviewService(t *testing.T) *review.Service {
	t.Helper()
	return dbtest.Factory().Review()
}

func authorUser(t *testing.T, name string) uuid.UUID {
	t.Helper()
	return createUserForQuest(t, name)
}

func rating(n int) *int { return &n }

// Submitting twice is an UPDATE, not a second review.
//
// The one-per-author-per-entity rule is enforced by a CONSTRAINT, so the test that
// matters is that two writes produce one row -- and that the second carries the
// new body rather than being rejected. A service that only checked and returned an
// error would pass a "cannot duplicate" test and fail this one.
func TestSubmittingTwiceUpdatesRatherThanDuplicates(t *testing.T) {
	svc := newReviewService(t)
	ctx := context.Background()
	author := authorUser(t, "A Reviewer With Opinions")
	entity := uuid.Must(uuid.NewV7())

	first, err := svc.Submit(ctx, review.SubmitInput{
		AuthorID: author, EntityType: review.EntitySite, EntityID: entity,
		Rating: rating(2), Body: "Mediocre.",
	})
	require.NoError(t, err)

	second, err := svc.Submit(ctx, review.SubmitInput{
		AuthorID: author, EntityType: review.EntitySite, EntityID: entity,
		Rating: rating(5), Body: "Revised: actually excellent.",
	})
	require.NoError(t, err)

	assert.Equal(t, first.ID, second.ID,
		"the second submission UPDATED the first. A second row would let one "+
			"person rate a site five times and outrank three honest reviewers")
	require.NotNil(t, second.Rating)
	assert.Equal(t, 5, *second.Rating)
	assert.Equal(t, "Revised: actually excellent.", second.Body)

	list, err := svc.ListForEntity(ctx, review.EntitySite, entity, 100, 0)
	require.NoError(t, err)
	assert.Len(t, list, 1, "and the entity still has exactly one review")

	// created_at is PRESERVED across the edit, so "how long has this person held
	// this opinion" is answerable even though the intermediate rating is not.
	assert.Equal(t, first.CreatedAt, second.CreatedAt,
		"an edit must not reset created_at; it is the only part of the history "+
			"that survives the in-place update decision")
}

// An author's edit must not carry the moderator's verification verdict with it.
func TestAnEditDoesNotResetOrForgeVerification(t *testing.T) {
	svc := newReviewService(t)
	ctx := context.Background()
	author := authorUser(t, "A Reviewer Who Is Verified")
	entity := uuid.Must(uuid.NewV7())

	created, err := svc.Submit(ctx, review.SubmitInput{
		AuthorID: author, EntityType: review.EntitySite, EntityID: entity,
		Rating: rating(4), Body: "Genuinely used this for a year.",
	})
	require.NoError(t, err)

	// A moderator verifies the usage claim.
	verified, err := svc.Queries().SetReviewVerified(ctx,
		queries.SetReviewVerifiedParams{ID: created.ID, Verified: true})
	require.NoError(t, err)
	require.True(t, verified.Verified)

	// The author then edits their prose.
	edited, err := svc.Submit(ctx, review.SubmitInput{
		AuthorID: author, EntityType: review.EntitySite, EntityID: entity,
		Rating: rating(4), Body: "Genuinely used this for a year. (edited)",
	})
	require.NoError(t, err)

	assert.True(t, edited.Verified,
		"the verification survives the author's edit. UpdateReview's SET list "+
			"excludes it, so changing your prose cannot silently drop a "+
			"moderator's judgement")
}

// The average separates RATED from TOTAL, and that is the number a directory lies
// with if it does not.
func TestTheAverageCountsRatedAndTotalSeparately(t *testing.T) {
	svc := newReviewService(t)
	ctx := context.Background()
	entity := uuid.Must(uuid.NewV7())

	// Two rated reviews (5 and 4) and one with no rating at all.
	for i, r := range []*int{rating(5), rating(4), nil} {
		_, err := svc.Submit(ctx, review.SubmitInput{
			AuthorID:   authorUser(t, "A Rater "+string(rune('A'+i))),
			EntityType: review.EntityStudio,
			EntityID:   entity,
			Rating:     r,
			Body:       "A review.",
		})
		require.NoError(t, err)
	}

	avg, err := svc.AverageFor(ctx, review.EntityStudio, entity)
	require.NoError(t, err)

	assert.InDelta(t, 4.5, avg.Value, 0.001,
		"the mean is over the two reviews that HAVE a rating. Dividing 9 by 3 "+
			"gives 3.0, which reads as a mediocre studio when it is a good one")
	assert.Equal(t, 2, avg.Rated)
	assert.Equal(t, 3, avg.Total,
		"the unrated review is still a review and still counts toward the total. "+
			"A count that excluded it would make an entity with many comments look "+
			"barely reviewed")
}

// A removed review leaves both numbers; a flagged one leaves only the rated count.
func TestRemovedAndFlaggedReviewsAreExcludedFromTheAverage(t *testing.T) {
	svc := newReviewService(t)
	ctx := context.Background()
	entity := uuid.Must(uuid.NewV7())

	kept, err := svc.Submit(ctx, review.SubmitInput{
		AuthorID:   authorUser(t, "A Keeper Of Reviews"),
		EntityType: review.EntitySite, EntityID: entity,
		Rating: rating(4), Body: "Fine.",
	})
	require.NoError(t, err)

	flagged, err := svc.Submit(ctx, review.SubmitInput{
		AuthorID:   authorUser(t, "A Flagged Reviewer"),
		EntityType: review.EntitySite, EntityID: entity,
		Rating: rating(1), Body: "Spam.",
	})
	require.NoError(t, err)
	_, err = svc.Queries().SetReviewStatus(ctx,
		queries.SetReviewStatusParams{ID: flagged.ID, Status: review.StatusFlagged})
	require.NoError(t, err)

	avg, err := svc.AverageFor(ctx, review.EntitySite, entity)
	require.NoError(t, err)
	assert.InDelta(t, 4.0, avg.Value, 0.001, "a flagged review is out of the mean")
	assert.Equal(t, 1, avg.Total, "and out of the total")

	_, err = svc.Queries().SetReviewStatus(ctx,
		queries.SetReviewStatusParams{ID: flagged.ID, Status: review.StatusRemoved})
	require.NoError(t, err)

	avg, err = svc.AverageFor(ctx, review.EntitySite, entity)
	require.NoError(t, err)
	assert.InDelta(t, 4.0, avg.Value, 0.001)
	assert.Equal(t, 1, avg.Total)

	// A FLAGGED review is still visible to its own author -- Get does not filter
	// by status, which is why the listing and the average do it in SQL instead.
	got, err := svc.Get(ctx, flagged.ID)
	require.NoError(t, err)
	assert.Equal(t, review.StatusRemoved, got.Status)
	_ = kept
}

// Two DIFFERENT authors on one entity are two reviews, which is the case the
// unique constraint must not break.
func TestDifferentAuthorsEachGetTheirOwnReview(t *testing.T) {
	svc := newReviewService(t)
	ctx := context.Background()
	entity := uuid.Must(uuid.NewV7())

	for i, name := range []string{"Reviewer One", "Reviewer Two", "Reviewer Three"} {
		_, err := svc.Submit(ctx, review.SubmitInput{
			AuthorID:   authorUser(t, name),
			EntityType: review.EntityPerformer,
			EntityID:   entity,
			Rating:     rating(i + 1),
			Body:       "A review.",
		})
		require.NoError(t, err)
	}

	avg, err := svc.AverageFor(ctx, review.EntityPerformer, entity)
	require.NoError(t, err)
	assert.InDelta(t, 2.0, avg.Value, 0.001, "ratings 1, 2 and 3 average to 2")
	assert.Equal(t, 3, avg.Total, "three authors, three reviews")
}

// An entity with no reviews is a zero average and no error.
func TestAnEntityWithNoReviewsHasAZeroAverage(t *testing.T) {
	svc := newReviewService(t)
	ctx := context.Background()

	avg, err := svc.AverageFor(ctx, review.EntitySite, uuid.Must(uuid.NewV7()))
	require.NoError(t, err, "every entity page asks this; an error on a new site "+
		"is a broken page, not an exceptional condition")
	assert.Equal(t, review.Average{}, avg)
}

// Deletion is author-only, and the two failure modes stay distinguishable.
func TestDeleteIsAuthorOnly(t *testing.T) {
	svc := newReviewService(t)
	ctx := context.Background()
	author := authorUser(t, "The Review Owner")
	other := authorUser(t, "Someone Else Entirely")
	entity := uuid.Must(uuid.NewV7())

	created, err := svc.Submit(ctx, review.SubmitInput{
		AuthorID: author, EntityType: review.EntitySite, EntityID: entity,
		Rating: rating(3), Body: "Mine.",
	})
	require.NoError(t, err)

	err = svc.Delete(ctx, other, created.ID)
	assert.ErrorIs(t, err, review.ErrNotAuthor,
		"someone else's review must not be deletable, and the error must say "+
			"NOT-AUTHOR rather than NOT-FOUND: the two mean different things to a "+
			"client, and collapsing them tells an author their review vanished")

	err = svc.Delete(ctx, author, created.ID)
	require.NoError(t, err)

	_, err = svc.Get(ctx, created.ID)
	assert.ErrorIs(t, err, review.ErrNotFound, "and it is gone afterwards")
}
