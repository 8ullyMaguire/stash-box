//go:build integration

package api_test

import (
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
)

// Reviews over GraphQL (SPEC §7.10, growth item 29).
//
// internal/service/review/review_test.go already pins the validation, the rating
// pointer and the upsert. None of that reaches GraphQL, and the two things this file
// adds are the ones a resolver can get wrong on its own:
//
//   - the NULL-versus-ZERO distinction. An entity with no ratings must return
//     average: null, never 0. Zero is not a rating, and a page rendering "0.0" over
//     zero reviews is a lie that looks like data.
//   - status visibility. Flagged and removed reviews are filtered in the query, so
//     their absence from a listing must leak nothing -- including to their author,
//     who sees FLAGGED on their own review via `review` but never in the list.

const reviewQuery = `
	query($id: ID!) {
		findPerformer(id: $id) {
			reviews(perPage: 25, page: 1) {
				id
				body
				rating
				verified
				status
				author { id name }
			}
			reviewSummary { average ratedCount totalCount }
		}
	}
`

type reviewRow struct {
	ID       string
	Body     string
	Rating   *int
	Verified bool
	Status   string
	Author   struct {
		ID   string
		Name string
	}
}

type reviewSummary struct {
	Average    *float64
	RatedCount int
	TotalCount int
}

type reviewResponse struct {
	FindPerformer struct {
		Reviews       []reviewRow
		ReviewSummary reviewSummary
	}
}

// mustUUID parses an id or fails the test. gqlgen decodes ID to uuid.UUID directly, so
// this only appears where a test needs the id as a query parameter.
func mustUUID(t *testing.T, id string) uuid.UUID {
	t.Helper()
	u, err := uuid.FromString(id)
	require.NoError(t, err)
	return u
}

func askReviews(t *testing.T, r *testRunner, performerID string) reviewResponse {
	t.Helper()
	var resp reviewResponse
	r.client.MustPost(reviewQuery, &resp, client.Var("id", performerID))
	return resp
}

// The whole path for a rated review: it round-trips, the author is resolved, and the
// summary agrees with the arithmetic.
//
// One test rather than one per field, because what is being proved is that the chain is
// connected. A field that resolves but is never queried behaves identically to one that
// works.
func TestReviewRoundTripAndSummary(t *testing.T) {
	admin := asAdmin(t)
	moderate := asModerate(t)

	subject, err := admin.client.createPerformer(models.PerformerCreateInput{Name: "Review Subject"})
	require.NoError(t, err)

	// THREE DIFFERENT AUTHORS, not three submissions by one. Reviews are one-per-author-
	// per-entity, so submitting three times as one user correctly collapses to a single
	// review -- which is what TestReviewSubmitIsAnUpsert pins. I originally wrote this
	// with a single author and it failed with 1 review rather than 3, which was the test
	// being wrong about the domain, not the upsert being broken.
	//
	// One rated review (5) plus one prose-only review: the average is 5, the rated count
	// is 1, and the total is 2. An unrated review counts as a review but cannot
	// contribute to a mean, which is the whole reason `rating` is a pointer.
	//
	// Two reviews, not three: reviews are unique per (author, entity), and only two
	// harness users hold a role that can submit one. Getting the third author is not
	// worth loosening the role floor to make a test pass.
	var out struct {
		ReviewSubmit struct {
			Body   string
			Rating *int
			Status string
		}
	}
	for _, tc := range []struct {
		author *testRunner
		body   string
		rating *int
	}{
		// Probed which roles actually pass reviewSubmit: MODERATE and ADMIN do, EDIT
		// does not. That is RoleEnum.Implies at work -- it models a FLAT hierarchy, so
		// EDIT implies only READ and itself. The harness also gives each test user
		// exactly ONE role, so the two authors here are moderate and admin.
		{asModerate(t), "Excellent work.", intPtr(5)},
		{asAdmin(t), "Their ethics are a separate question.", nil},
	} {
		out.ReviewSubmit = struct {
			Body   string
			Rating *int
			Status string
		}{}
		tc.author.client.MustPost(
			`mutation($input: ReviewSubmitInput!) {
				reviewSubmit(input: $input) { body rating status }
			}`,
			&out,
			client.Var("input", map[string]any{
				"entityType": "PERFORMER",
				"entityId":   subject.ID,
				"rating":     tc.rating,
				"body":       tc.body,
			}))
		assert.Equal(t, "PUBLISHED", out.ReviewSubmit.Status,
			"a review is public the moment it is written; routing it through edit-style moderation would empty the directory")
		assert.Equal(t, tc.body, out.ReviewSubmit.Body)
		if tc.rating == nil {
			assert.Nil(t, out.ReviewSubmit.Rating,
				"a prose-only review must round-trip as null, not as 0")
		} else {
			require.NotNil(t, out.ReviewSubmit.Rating)
			assert.Equal(t, *tc.rating, *out.ReviewSubmit.Rating)
		}
	}

	resp := askReviews(t, moderate, subject.ID)
	require.Len(t, resp.FindPerformer.Reviews, 2, "both reviews are published and must be listed")

	sum := resp.FindPerformer.ReviewSummary
	assert.Equal(t, 2, sum.TotalCount, "total counts every published review, rated or not")
	assert.Equal(t, 1, sum.RatedCount, "rated counts only those that could contribute to a mean")
	require.NotNil(t, sum.Average, "there is a rating, so the average is not null")
	assert.InDelta(t, 5.0, *sum.Average, 0.001, "the mean of a single rating is that rating")

	// The author must be resolved on every row, not left as a blank object.
	for _, row := range resp.FindPerformer.Reviews {
		assert.NotEmpty(t, row.Author.Name,
			"author is resolved at conversion time; a nil here means the whole page renders anonymously")
	}
}

// The null-versus-zero case, which is the one a resolver most easily gets wrong.
func TestReviewSummaryIsNullNotZeroWhenUnrated(t *testing.T) {
	admin := asAdmin(t)

	subject, err := admin.client.createPerformer(models.PerformerCreateInput{Name: "Review Unrated Subject"})
	require.NoError(t, err)

	resp := askReviews(t, asRead(t), subject.ID)
	assert.Empty(t, resp.FindPerformer.Reviews, "an unrated entity has no reviews")
	assert.Nil(t, resp.FindPerformer.ReviewSummary.Average,
		"no ratings means a NULL average, never 0 -- 0 is not a rating")
	assert.Equal(t, 0, resp.FindPerformer.ReviewSummary.RatedCount)
	assert.Equal(t, 0, resp.FindPerformer.ReviewSummary.TotalCount)
}

// A prose-only review must still count toward the total while leaving the average null
// only if there are NO ratings at all.
func TestReviewProseOnlyLeavesAverageNull(t *testing.T) {
	admin := asAdmin(t)
	moderate := asModerate(t)

	subject, err := admin.client.createPerformer(models.PerformerCreateInput{Name: "Review Prose Only"})
	require.NoError(t, err)

	var out struct {
		ReviewSubmit struct{ Body string }
	}
	moderate.client.MustPost(
		`mutation($input: ReviewSubmitInput!) { reviewSubmit(input: $input) { body } }`,
		&out,
		client.Var("input", map[string]any{
			"entityType": "PERFORMER",
			"entityId":   subject.ID,
			"body":       "I have thoughts, but no number for them.",
		}))

	resp := askReviews(t, moderate, subject.ID)
	require.Len(t, resp.FindPerformer.Reviews, 1)
	assert.Nil(t, resp.FindPerformer.Reviews[0].Rating,
		"the unrated review itself must not acquire a rating")
	sum := resp.FindPerformer.ReviewSummary
	assert.Equal(t, 1, sum.TotalCount, "an unrated review is still a review")
	assert.Equal(t, 0, sum.RatedCount)
	assert.Nil(t, sum.Average,
		"no rated reviews means a null average; reporting 0 would read as 'rated zero'")
}

// Submitting twice must update the author's existing review, not create a second one.
func TestReviewSubmitIsAnUpsert(t *testing.T) {
	admin := asAdmin(t)
	moderate := asModerate(t)

	subject, err := admin.client.createPerformer(models.PerformerCreateInput{Name: "Review Upsert"})
	require.NoError(t, err)

	submit := func(body string, rating int) {
		var out struct {
			ReviewSubmit struct{ Body string }
		}
		moderate.client.MustPost(
			`mutation($input: ReviewSubmitInput!) { reviewSubmit(input: $input) { body } }`,
			&out,
			client.Var("input", map[string]any{
				"entityType": "PERFORMER",
				"entityId":   subject.ID,
				"body":       body,
				"rating":     rating,
			}))
	}
	submit("First opinion.", 3)
	submit("Revised opinion.", 5)

	resp := askReviews(t, moderate, subject.ID)
	assert.Len(t, resp.FindPerformer.Reviews, 1,
		"editing your review is the same act as writing it; two submissions must not make two reviews")
	assert.Equal(t, "Revised opinion.", resp.FindPerformer.Reviews[0].Body)
	assert.Equal(t, 1, resp.FindPerformer.ReviewSummary.RatedCount,
		"the revision replaced the original rating rather than averaging the two")
}

// A non-author must not be able to delete someone else's review, and the error must say
// "not yours" rather than "not found" -- collapsing the two tells an author their review
// vanished when a moderator removed it.
func TestReviewDeleteIsAuthorOnly(t *testing.T) {
	admin := asAdmin(t)
	// Two DIFFERENT users. I first wrote both as asModerate(t), which made the
	// "non-author" the author, so the delete correctly succeeded and the test failed
	// with "an error is expected but got nil" -- the test was wrong, not the guard.
	author := asModerate(t)
	other := asAdmin(t)

	subject, err := admin.client.createPerformer(models.PerformerCreateInput{Name: "Review Delete Guard"})
	require.NoError(t, err)

	var out struct {
		ReviewSubmit struct{ ID string }
	}
	author.client.MustPost(
		`mutation($input: ReviewSubmitInput!) { reviewSubmit(input: $input) { id } }`,
		&out,
		client.Var("input", map[string]any{
			"entityType": "PERFORMER", "entityId": subject.ID, "body": "Mine.", "rating": 4,
		}))
	reviewID := out.ReviewSubmit.ID
	require.NotEmpty(t, reviewID)

	// A non-nil response target: passing nil fails inside the client with a
	// mapstructure error BEFORE the query is sent, which masks the authorization
	// failure I am trying to assert on.
	var del struct {
		ReviewDelete bool
	}
	err2 := other.client.Post(
		`mutation($id: ID!) { reviewDelete(id: $id) }`,
		&del,
		client.Var("id", reviewID))
	require.Error(t, err2, "a non-author must not be able to delete the review")
	assert.Contains(t, err2.Error(), "author",
		"the error must distinguish 'not yours' from 'not found'")

	// And the review must still be there.
	resp := askReviews(t, author, subject.ID)
	assert.Len(t, resp.FindPerformer.Reviews, 1, "the refused delete must not have deleted anything")
}

// A FLAGGED review must be visible to its author as FLAGGED, and must not be listed on
// the entity page. This is the assertion that covers toReviewStatus: mutating its
// restrictive fallback (unknown status -> PUBLISHED) passed the whole suite silently,
// because nothing exercised a status the resolver did not recognise.
func TestReviewFlaggedIsNotListedButKeepsItsStatus(t *testing.T) {
	admin := asAdmin(t)
	author := asModerate(t)

	subject, err := admin.client.createPerformer(models.PerformerCreateInput{Name: "Review Flagged"})
	require.NoError(t, err)

	var out struct {
		ReviewSubmit struct{ ID string }
	}
	author.client.MustPost(
		`mutation($input: ReviewSubmitInput!) { reviewSubmit(input: $input) { id } }`,
		&out,
		client.Var("input", map[string]any{
			"entityType": "PERFORMER", "entityId": subject.ID, "body": "Soon to be flagged.", "rating": 5,
		}))
	reviewID := out.ReviewSubmit.ID
	require.NotEmpty(t, reviewID)

	// A moderator flags it. The filtering lives in the QUERY (status = 'published'), so
	// this is a direct state change rather than a moderation mutation -- there is no
	// GraphQL surface for flagging, which is itself a gap worth naming.
	_, err = q().SetReviewStatus(t.Context(), queries.SetReviewStatusParams{
		ID:     mustUUID(t, reviewID),
		Status: "flagged",
	})
	require.NoError(t, err, "flagging the review")

	// Not listed on the entity page -- its absence must leak nothing about its state.
	resp := askReviews(t, author, subject.ID)
	assert.Empty(t, resp.FindPerformer.Reviews,
		"a flagged review must not appear in the published listing")
	assert.Equal(t, 0, resp.FindPerformer.ReviewSummary.RatedCount,
		"a flagged review must not count toward the average either")

	// But fetched directly by its author, it exists and says so. This is the case that
	// pins toReviewStatus: the row comes back with FLAGGED, and an unrecognised status
	// must never be reported as PUBLISHED.
	var single struct {
		Review struct {
			ID     string
			Status string
		}
	}
	author.client.MustPost(
		`query($id: ID!) { review(id: $id) { id status } }`,
		&single,
		client.Var("id", reviewID))
	assert.Equal(t, "FLAGGED", single.Review.Status,
		"an author must still see their own review and its true state; defaulting an "+
			"unrecognised status to PUBLISHED would show a taken-down review as visible")
}

// An empty body must be refused. A review with no text is noise.
func TestReviewRejectsEmptyBody(t *testing.T) {
	admin := asAdmin(t)
	subject, err := admin.client.createPerformer(models.PerformerCreateInput{Name: "Review Empty Body"})
	require.NoError(t, err)

	err2 := asModerate(t).client.Post(
		`mutation($input: ReviewSubmitInput!) { reviewSubmit(input: $input) { id } }`,
		&struct {
			ReviewSubmit struct{ ID string }
		}{},
		client.Var("input", map[string]any{
			"entityType": "PERFORMER", "entityId": subject.ID, "body": "   ",
		}))
	require.Error(t, err2)
}
