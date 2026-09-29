package review

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/queries"
)

// aRow builds a generated review row, with a rating only when asked for.
func aRow(rating *int) queries.Review {
	return queries.Review{
		ID:         uuid.Must(uuid.NewV7()),
		AuthorID:   uuid.Must(uuid.NewV7()),
		EntityType: EntitySite,
		EntityID:   uuid.Must(uuid.NewV7()),
		Rating:     rating,
		Body:       "body",
		Status:     StatusPublished,
	}
}

// Validation is pure, so it is tested without a database. These are the cases the
// schema CANNOT express and therefore the only place they are pinned: a NOT NULL
// body is satisfied by "   ", and a rating CHECK is bypassed entirely by NULL --
// which is a legitimate value here, so the boundary is what needs a test.

func ptr[T any](v T) *T { return &v }

func validInput() SubmitInput {
	rating := 4
	return SubmitInput{
		AuthorID:   uuid.Must(uuid.NewV7()),
		EntityType: EntitySite,
		EntityID:   uuid.Must(uuid.NewV7()),
		Rating:     &rating,
		Body:       "Good catalogue, slow uploads.",
	}
}

// A body of whitespace satisfies NOT NULL and renders as nothing.
func TestAWhitespaceOnlyBodyIsRejected(t *testing.T) {
	cases := []string{"", "   ", "\t", "\n\n", " \t\n "}
	for _, body := range cases {
		in := validInput()
		in.Body = body
		err := validate(in)

		require.ErrorIs(t, err, ErrEmptyBody,
			"body %q satisfies NOT NULL and would render as a blank review that "+
				"still counts toward the total", body)
	}
}

// The rating boundary, and NULL as a legitimate value rather than a missing one.
func TestRatingBoundaries(t *testing.T) {
	rating := func(n int) *int { return &n }

	t.Run("in range", func(t *testing.T) {
		for _, n := range []int{1, 2, 3, 4, 5} {
			in := validInput()
			in.Rating = rating(n)
			assert.NoError(t, validate(in), "rating %d is in range", n)
		}
	})

	t.Run("out of range", func(t *testing.T) {
		for _, n := range []int{0, -1, 6, 100} {
			in := validInput()
			in.Rating = rating(n)
			assert.ErrorIs(t, validate(in), ErrInvalidRating,
				"rating %d must be rejected. A 0 is the value a flattened nullable "+
					"rating would produce, and a 6 survives an average looking "+
					"merely high", n)
		}
	})

	t.Run("absent is valid, not zero", func(t *testing.T) {
		in := validInput()
		in.Rating = nil
		assert.NoError(t, validate(in),
			"a review of a studio's ethics has no 1-to-5 value. Rejecting NULL "+
				"here would force a fake rating that then gets averaged")
	})
}

// An unknown entity type is rejected with a message naming the type.
//
// The COLUMN is free text so adding an entity type is not a migration, but a
// typo in a GraphQL argument would otherwise produce an orphan review that no
// listing query will ever return -- written, invisible, and averaging nothing.
func TestAnUnknownEntityTypeIsRejectedAndNamed(t *testing.T) {
	for _, entityType := range []string{"PERFORMERS", "site", "LIST", "", "SITE "} {
		in := validInput()
		in.EntityType = entityType
		err := validate(in)

		require.Error(t, err, "entity type %q", entityType)
		assert.Contains(t, err.Error(), entityType,
			"the error must name the offending type; a generic failure sends the "+
				"client looking for a different bug")
	}
}

// Every entity type the service accepts is a real one, so the closed set cannot
// drift into accepting something the listings never query.
func TestTheAcceptedEntityTypesAreTheDeclaredOnes(t *testing.T) {
	for _, entityType := range []string{EntitySite, EntityStudio, EntityPerformer} {
		in := validInput()
		in.EntityType = entityType
		assert.NoError(t, validate(in), "%s must be reviewable", entityType)
	}
}

// toReview is a helper and the plan's rule 2 applies: this is NOT a test of
// Submit. It is here to pin the one conversion that can silently lose data --
// the nullable rating pointer.
func TestToReviewPreservesANilRatingAsNil(t *testing.T) {
	got := toReview(ptr(aRow(nil)))

	assert.Nil(t, got.Rating,
		"a nil rating must stay nil. Flattening it to 0 puts a zero into the "+
			"entity's average, and a 0 is a value a reader cannot distinguish from "+
			"a real one-star review")
}

func TestToReviewCopiesTheRatingValue(t *testing.T) {
	five := 5
	row := aRow(&five)

	got := toReview(ptr(row))

	require.NotNil(t, got.Rating)
	assert.Equal(t, 5, *got.Rating)

	// And it must be a COPY, not an alias: toReview's caller may retain the row,
	// and a shared *int means a later mutation of one is visible in the other.
	*got.Rating = 1
	assert.Equal(t, 5, *row.Rating, "the rating pointer is aliased to the row's")
}
