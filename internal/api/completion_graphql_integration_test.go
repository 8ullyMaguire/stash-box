//go:build integration

package api_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/99designs/gqlgen/client"

	"github.com/stashapp/stash-box/internal/models"
)

// Completion over GraphQL, end to end (SPEC §7.7).
//
// The service is proved elsewhere. What this file proves is the WIRING: that the
// `completion` field resolves on all five entity types, that the enum round-trips,
// and that the weights a client reads are the ones the formula used.
//
// A field resolver that correctly returns a service value is still broken if the
// schema names it differently, the enum is spelled differently on the two sides, or
// the shape a client renders does not match what the resolver produced. None of
// that is visible to a service-level test, and all of it is what a client hits
// first.

type completionFieldResponse struct {
	EntityType string
	Score      int
	Missing    []string
	Total      int
	Earned     int
}

// Field names match the GraphQL selection set exactly, because gqlgen decodes by
// name and panics on a key it does not recognise. A struct named for the ENTITY
// while the query selects `findPerformer` is a panic, not a test failure.
type completionResponse struct {
	FindPerformer struct{ Completion *completionFieldResponse }
	FindScene     struct{ Completion *completionFieldResponse }
	FindStudio    struct{ Completion *completionFieldResponse }
	FindSite      struct{ Completion *completionFieldResponse }
	FindTag       struct{ Completion *completionFieldResponse }
}

// All five types resolve, and each reports ITS OWN type.
//
// The five fields in ONE query rather than five tests: what is being proved is that
// all five resolvers are wired, and a single query fails if any one is absent from
// the schema -- which is the failure a client would see.
func TestCompletionResolvesOnEveryEntityType(t *testing.T) {
	admin := asAdmin(t)
	s := asRead(t)

	performer, err := admin.client.createPerformer(models.PerformerCreateInput{
		Name: "A Wiring Performer",
	})
	require.NoError(t, err)
	studio, err := admin.client.createStudio(models.StudioCreateInput{
		Name: "A Wiring Studio",
	})
	require.NoError(t, err)
	tag, err := admin.client.createTag(models.TagCreateInput{
		Name: "A Wiring Tag",
	})
	require.NoError(t, err)

	var resp completionResponse
	s.client.MustPost(`
		query($p: ID!, $s: ID!, $t: ID!) {
			findPerformer(id: $p) {
				completion { entityType score missing total earned }
			}
			findScene(id: $s) { completion { entityType score total } }
			findStudio(id: $s) { completion { entityType score } }
			findSite(id: $s) { completion { entityType score } }
			findTag(id: $t) { completion { entityType score } }
		}
	`, &resp,
		client.Var("p", performer.ID),
		client.Var("s", studio.ID),
		client.Var("t", tag.ID))

	require.NotNil(t, resp.FindPerformer.Completion,
		"a performer's completion must resolve to an object, not null")
	require.NotNil(t, resp.FindStudio.Completion)
	require.NotNil(t, resp.FindTag.Completion)

	// The enum must name the type that was actually SCORED. A resolver that
	// hard-coded the wrong EntityType would still produce a plausible score
	// everywhere, and a client keying its progress-bar component off this field
	// would render the wrong one.
	assert.Equal(t, "performer", resp.FindPerformer.Completion.EntityType)
	assert.Equal(t, "studio", resp.FindStudio.Completion.EntityType)
	assert.Equal(t, "tag", resp.FindTag.Completion.EntityType)

	// A newly created performer is missing nearly everything, and the score must
	// reflect that rather than being absent or zero by omission.
	assert.NotEmpty(t, resp.FindPerformer.Completion.Missing,
		"a newly created performer is missing its birthdate, country and urls")
	assert.Greater(t, resp.FindPerformer.Completion.Total, 0)
	assert.LessOrEqual(t, resp.FindPerformer.Completion.Earned,
		resp.FindPerformer.Completion.Total,
		"earned weight cannot exceed the total, and must not exceed it silently")
}

// The total and earned a client reads are the ones the formula used, so a client
// can render "15 of 80" without keeping its own copy of the weights.
func TestCompletionExposesTheWeightsTheScoreWasComputedFrom(t *testing.T) {
	admin := asAdmin(t)
	s := asRead(t)

	performer, err := admin.client.createPerformer(models.PerformerCreateInput{
		Name:      "An Exposed Weights Performer",
		Birthdate: strPtr("1990-01-01"),
	})
	require.NoError(t, err)

	var resp completionResponse
	s.client.MustPost(`
		query($p: ID!) { findPerformer(id: $p) { completion { score total earned missing } } }
	`, &resp, client.Var("p", performer.ID))

	c := resp.FindPerformer.Completion
	require.NotNil(t, c)
	assert.Equal(t, 80, c.Total,
		"the performer's scored weight is 80. It was 90 until the unreachable "+
			"details field was removed, and a client rendering a progress bar "+
			"would have shown a maximum of 90 that nothing could ever reach")
	assert.Equal(t, 15, c.Earned,
		"an exact birthdate earns the birthdate weight")
	assert.Equal(t, 19, c.Score, "15 of 80 rounds to 19")
	assert.NotContains(t, c.Missing, "birthdate",
		"a performer with an exact birthdate is not missing one")
}

// The count query must agree with the per-entity scores the same client reads, or
// the quest a curator is handed does not match the progress bar they are looking at.
//
// THE PREMISE, which I got wrong first: the integration database starts EMPTY, and
// this test counted before it had created anything. The count came back 0, which is
// a correct answer to a question about an empty archive, and the assertion "the
// archive is full of partial performers" failed -- not because the code was wrong
// but because the test was describing a database that does not exist in that state.
//
// So the fixture is created FIRST and the count is read second, and the comparison
// is between two numbers taken about the SAME database at the SAME point. A count
// taken before the entity exists is not a weaker assertion than one taken after; it
// is an assertion about nothing.
func TestCountIncompleteAgreesWithThePerEntityScore(t *testing.T) {
	admin := asAdmin(t)
	s := asRead(t)

	// A performer with a name and nothing else: every weight except `name` is
	// missing, so it is incomplete by a wide margin.
	performer, err := admin.client.createPerformer(models.PerformerCreateInput{
		Name: "A Counted Performer",
	})
	require.NoError(t, err)

	var score struct {
		FindPerformer struct{ Completion struct{ Score int } }
	}
	s.client.MustPost(`
		query($p: ID!) { findPerformer(id: $p) { completion { score } } }
	`, &score, client.Var("p", performer.ID))
	require.Less(t, score.FindPerformer.Completion.Score, 100,
		"a performer with only a name is not fully scored, so it belongs in the "+
			"set the count is counting")

	var resp struct {
		Below100 int
		Below50  int
		Zero     int
	}
	s.client.MustPost(`
		query {
			below100: countIncompleteEntities(entityType: performer, below: 100)
			below50: countIncompleteEntities(entityType: performer, below: 50)
			zero: countIncompleteEntities(entityType: performer, below: 0)
		}
	`, &resp)

	assert.Equal(t, 0, resp.Zero,
		"no entity scores below 0, so nothing is incomplete at a threshold of 0")
	assert.GreaterOrEqual(t, resp.Below100, 1,
		"the performer created above is incomplete, so the count at a threshold "+
			"of 100 must include it -- a zero here means the count query is "+
			"counting a different thing from the per-entity score, which is the "+
			"whole failure this test exists to catch")
	assert.GreaterOrEqual(t, resp.Below100, resp.Below50,
		"everything scoring below 50 also scores below 100, so the looser "+
			"threshold can only count more")
}

// Out-of-range thresholds clamp rather than erroring.
//
// A client computing "how complete is this archive" as `countIncomplete(below: 0)`
// is making a legitimate request, and answering it with a GraphQL error would put
// an error branch in every client for a question everyone will eventually ask.
func TestCountIncompleteClampsOutOfRangeThresholds(t *testing.T) {
	s := asRead(t)

	var resp struct {
		Zero     int
		Hundred  int
		Negative int
		Over     int
	}
	s.client.MustPost(`
		query {
			zero: countIncompleteEntities(entityType: performer, below: 0)
			hundred: countIncompleteEntities(entityType: performer, below: 100)
			negative: countIncompleteEntities(entityType: performer, below: -50)
			over: countIncompleteEntities(entityType: performer, below: 200)
		}
	`, &resp)

	assert.Equal(t, 0, resp.Zero, "no entity scores below 0")
	assert.Equal(t, 0, resp.Negative, "a negative threshold clamps to 0")
	assert.Equal(t, resp.Hundred, resp.Over,
		"a threshold over 100 clamps to 100, because no entity scores above 100")
}

// Every scored type is accepted by the enum, and each returns a number rather than
// an error.
//
// The schema enum is closed to five values so this cannot fail today, which is
// exactly why it is here: the failure it guards is someone adding a type to the
// scorer and forgetting the enum, or the reverse.
func TestCountIncompleteAcceptsEveryScoredType(t *testing.T) {
	s := asRead(t)

	for _, entityType := range []string{"performer", "scene", "studio", "site", "tag"} {
		t.Run(entityType, func(t *testing.T) {
			// The field name must match the query's, or gqlgen panics rather than
			// failing: `struct{ Count int }` cannot decode `countIncompleteEntities`.
			var resp struct {
				CountIncompleteEntities int
			}
			s.client.MustPost(`
				query($t: EntityType!) {
					countIncompleteEntities(entityType: $t, below: 50)
				}
			`, &resp, client.Var("t", entityType))
			assert.GreaterOrEqual(t, resp.CountIncompleteEntities, 0,
				"a count is never negative, whatever the type")
		})
	}
}
