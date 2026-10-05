//go:build integration

package api_test

import (
	"fmt"
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
)

// Similar performers over GraphQL (SPEC §7.5, growth item 27).
//
// scripts/verify-similar.sh proves the SCORING against a hand-built fixture whose
// correct order is derivable by hand. This file proves the parts that script cannot:
// that the query runs at all through the service, that the schema and resolver agree
// on names and shapes, and that the exclusions hold on the real code path.
//
// That distinction earned its keep. The query had `ORDER BY score DESC, p.name ASC`
// with `p.name` absent from the GROUP BY — Postgres rejects it outright. sqlc only
// type-checks, never runs, so nothing caught it until the shipped SQL was extracted
// from the generated Go and executed. If this file had gone through the GraphQL
// client without asserting the ranking, the failure would have surfaced as an opaque
// resolver error instead of a clear message.

// similarCast is the query under test. Field names match the selection set exactly,
// because gqlgen decodes by name and panics on a key the struct does not have.
const similarQuery = `
	query($id: ID!, $min: Int, $limit: Int) {
		findPerformer(id: $id) {
			similarPerformers(minShared: $min, limit: $limit) {
				performer { id name }
				scenesShared
				targetScenes
				coPerformers
				score
			}
			similarPerformerCount(minShared: $min)
		}
	}
`

type similarRow struct {
	Performer struct {
		ID   string
		Name string
	}
	ScenesShared int
	TargetScenes int
	CoPerformers int
	Score        float64
}

type similarResponse struct {
	FindPerformer struct {
		SimilarPerformers     []similarRow
		SimilarPerformerCount int
	}
}

// similarScene creates a scene and casts the given performers into it.
//
// Inserted directly rather than through the create mutation because the cast is
// fixture setup, not the behaviour under test: what is being verified is the
// ranking, and going through a mutation that also validates titles and dates would
// make every case in this file a maintenance problem.
func similarScene(t *testing.T, title string, performers ...string) {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	_, err := q().CreateScene(t.Context(), queries.CreateSceneParams{
		ID:    id,
		Title: &title,
	})
	require.NoError(t, err, "creating scene %q", title)
	for _, p := range performers {
		// Performer IDs arrive as strings because that is what the GraphQL client
		// decodes; parsed here once rather than at every call site.
		parsed, err := uuid.FromString(p)
		require.NoError(t, err, "performer id %q must parse", p)
		// `as` is the credited role on scene_performers and is NOT NULL. Left unset
		// rather than invented: the similarity query never reads it, and a fake role
		// in a fixture is one more thing that can drift from what ships.
		_, err = q().CreateScenePerformers(t.Context(),
			[]queries.CreateScenePerformersParams{{SceneID: id, PerformerID: parsed}})
		require.NoError(t, err, "casting %s into %q", p, title)
	}
}

// The full path: create a cast that appears together repeatedly, and read the
// recommendation back through GraphQL.
//
// One test rather than one per step, because what is being proved is that the chain
// is connected. A field that resolves but is never queried behaves identically to one
// that works.
func TestSimilarPerformersResolvesAndRanks(t *testing.T) {
	admin := asAdmin(t)
	read := asRead(t)

	subject, err := admin.client.createPerformer(models.PerformerCreateInput{Name: "Similarity Subject"})
	require.NoError(t, err)
	close, err := admin.client.createPerformer(models.PerformerCreateInput{Name: "Similarity Close"})
	require.NoError(t, err)
	lonely, err := admin.client.createPerformer(models.PerformerCreateInput{Name: "Similarity Lonely"})
	require.NoError(t, err)

	// Five shared scenes for `close`, one for `lonely`. The floor is 2, so `lonely`
	// must be excluded — a single co-appearance is the most common case in the whole
	// dataset, and without a floor the top result is whoever shared a crowded scene.
	for i := 0; i < 5; i++ {
		similarScene(t, fmt.Sprintf("Shared %d", i), subject.ID, close.ID)
	}
	similarScene(t, "Lonely once", subject.ID, lonely.ID)

	var resp similarResponse
	read.client.MustPost(similarQuery, &resp,
		client.Var("id", subject.ID), client.Var("min", 2), client.Var("limit", 10))

	require.Len(t, resp.FindPerformer.SimilarPerformers, 1,
		"only `close` shares two or more scenes; `lonely` shares exactly one")
	got := resp.FindPerformer.SimilarPerformers[0]
	assert.Equal(t, close.Name, got.Performer.Name,
		"the recommendation must be the performer who actually shares scenes")
	assert.Equal(t, 5, got.ScenesShared, "the raw evidence must be reported, not hidden")
	assert.Equal(t, 6, got.TargetScenes,
		"the subject's own scene count, so a client can judge what 5 of 6 means")
	assert.Positive(t, got.Score, "a real recommendation must score above zero")

	assert.Equal(t, 1, resp.FindPerformer.SimilarPerformerCount,
		"the count must agree with the list, or a client paginating it will show holes")
}

// A performer with no scenes has no similar performers, and that is a fact rather
// than a failure. Returning an error would make a sparse archive look broken.
func TestSimilarPerformersIsEmptyNotAnError(t *testing.T) {
	admin := asAdmin(t)
	read := asRead(t)

	p, err := admin.client.createPerformer(models.PerformerCreateInput{Name: "Similarity Unscened"})
	require.NoError(t, err)

	var resp similarResponse
	read.client.MustPost(similarQuery, &resp,
		client.Var("id", p.ID), client.Var("min", 2), client.Var("limit", 10))

	assert.Empty(t, resp.FindPerformer.SimilarPerformers,
		"a performer with no scenes has nobody to be similar to")
	assert.Equal(t, 0, resp.FindPerformer.SimilarPerformerCount)
}

// The subject must never appear in its own recommendations. A performer trivially
// co-occurs with themselves through every scene, so forgetting the exclusion makes
// them their own top recommendation -- which looks like a result rather than a bug.
func TestSimilarPerformersExcludesTheSubject(t *testing.T) {
	admin := asAdmin(t)
	read := asRead(t)

	subject, err := admin.client.createPerformer(models.PerformerCreateInput{Name: "Similarity Self"})
	require.NoError(t, err)
	for i := 0; i < 3; i++ {
		similarScene(t, fmt.Sprintf("Self scene %d", i), subject.ID)
	}

	var resp similarResponse
	read.client.MustPost(similarQuery, &resp, client.Var("id", subject.ID))

	for _, row := range resp.FindPerformer.SimilarPerformers {
		assert.NotEqual(t, subject.ID, row.Performer.ID,
			"a performer must not be recommended to themselves")
	}
	assert.Empty(t, resp.FindPerformer.SimilarPerformers,
		"appearing alone in every scene means nobody else co-occurs")
}

// A soft-deleted performer co-appearing with the subject must never be recommended:
// the row is gone, so the link would break.
func TestSimilarPerformersExcludesDeletedPerformers(t *testing.T) {
	admin := asAdmin(t)
	read := asRead(t)

	subject, err := admin.client.createPerformer(models.PerformerCreateInput{Name: "Similarity Mourned Subject"})
	require.NoError(t, err)
	gone, err := admin.client.createPerformer(models.PerformerCreateInput{Name: "Similarity Mourned Co-star"})
	require.NoError(t, err)
	for i := 0; i < 3; i++ {
		similarScene(t, fmt.Sprintf("Mourned %d", i), subject.ID, gone.ID)
	}

	// Soft-delete rather than hard-delete: the co-appearance rows survive, which is
	// exactly the state that must be filtered at read time rather than by cascade.
	goneUUID, err := uuid.FromString(gone.ID)
	require.NoError(t, err)
	_, err = q().SoftDeletePerformer(t.Context(), goneUUID)
	require.NoError(t, err, "soft-deleting the co-star")

	var resp similarResponse
	read.client.MustPost(similarQuery, &resp, client.Var("id", subject.ID))

	for _, row := range resp.FindPerformer.SimilarPerformers {
		assert.NotEqual(t, gone.ID, row.Performer.ID,
			"a soft-deleted performer must never be recommended; the link would break")
	}
}

// A high floor must return nothing rather than everything: the arguments have to
// reach the query, not be silently dropped.
func TestSimilarPerformerMinSharedIsHonoured(t *testing.T) {
	admin := asAdmin(t)
	read := asRead(t)

	subject, err := admin.client.createPerformer(models.PerformerCreateInput{Name: "Similarity Floor Subject"})
	require.NoError(t, err)
	partner, err := admin.client.createPerformer(models.PerformerCreateInput{Name: "Similarity Floor Co-star"})
	require.NoError(t, err)
	for i := 0; i < 3; i++ {
		similarScene(t, fmt.Sprintf("Floor %d", i), subject.ID, partner.ID)
	}

	// Three shared scenes, but the floor is raised above three.
	var strict similarResponse
	read.client.MustPost(similarQuery, &strict,
		client.Var("id", subject.ID), client.Var("min", 10), client.Var("limit", 10))
	assert.Empty(t, strict.FindPerformer.SimilarPerformers,
		"a floor of 10 must exclude a pair that shares 3")

	// And the default floor must include it, proving the difference is the argument.
	var loose similarResponse
	read.client.MustPost(similarQuery, &loose,
		client.Var("id", subject.ID), client.Var("min", 2), client.Var("limit", 10))
	assert.Len(t, loose.FindPerformer.SimilarPerformers, 1)
}

// The limit must cap the list, and an absurd one must not error.
func TestSimilarPerformerLimitIsHonoured(t *testing.T) {
	admin := asAdmin(t)
	read := asRead(t)

	subject, err := admin.client.createPerformer(models.PerformerCreateInput{Name: "Similarity Limit Subject"})
	require.NoError(t, err)
	for i := 0; i < 3; i++ {
		partner, err := admin.client.createPerformer(
			models.PerformerCreateInput{Name: fmt.Sprintf("Similarity Limit Partner %d", i)})
		require.NoError(t, err)
		for j := 0; j < 3; j++ {
			similarScene(t, fmt.Sprintf("Limit %d-%d", i, j), subject.ID, partner.ID)
		}
	}

	var capped similarResponse
	read.client.MustPost(similarQuery, &capped,
		client.Var("id", subject.ID), client.Var("min", 2), client.Var("limit", 2))
	assert.Len(t, capped.FindPerformer.SimilarPerformers, 2,
		"three performers qualify, so a limit of 2 must return 2 and the count 3")
	assert.Equal(t, 3, capped.FindPerformer.SimilarPerformerCount,
		"the count reports what qualifies, not what the limit allowed")

	// An absurd limit must be clamped rather than rejected: the caller wants results.
	var greedy similarResponse
	read.client.MustPost(similarQuery, &greedy,
		client.Var("id", subject.ID), client.Var("min", 2), client.Var("limit", 100000))
	assert.Len(t, greedy.FindPerformer.SimilarPerformers, 3,
		"an over-limit request must be clamped, not refused and not truncated to nothing")
}

// The score must NOT be a rounded 1 for everything: sqlc once inferred `Score int32`
// for this expression, which would have silently flattened every ranking.
func TestSimilarPerformerScoreIsNotTruncated(t *testing.T) {
	admin := asAdmin(t)
	read := asRead(t)

	subject, err := admin.client.createPerformer(models.PerformerCreateInput{Name: "Similarity Score Subject"})
	require.NoError(t, err)
	// Shares 2 of the subject's 3 scenes and has NO co-performer, because the two of
	// them are alone in each scene. So the score is (2/3) * (1 + 0) = 0.667.
	// I first wrote 0.8 here by assuming a co-performer the fixture never creates;
	// the query was right and the expectation was wrong.
	partner, err := admin.client.createPerformer(models.PerformerCreateInput{Name: "Similarity Score Co-star"})
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		similarScene(t, fmt.Sprintf("Score shared %d", i), subject.ID, partner.ID)
	}
	similarScene(t, "Score alone", subject.ID)

	var resp similarResponse
	read.client.MustPost(similarQuery, &resp, client.Var("id", subject.ID), client.Var("min", 2))

	require.Len(t, resp.FindPerformer.SimilarPerformers, 1)
	score := resp.FindPerformer.SimilarPerformers[0].Score
	assert.InDelta(t, 2.0/3.0, score, 0.01,
		"the score is fractional; an integer column would report 0 and flatten the ranking")
}
