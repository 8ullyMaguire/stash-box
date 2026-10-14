//go:build integration

package api_test

import (
	"fmt"
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/config"
	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service"
)

// Scene similarity over GraphQL (feature-discovery SPEC section 8, phase 7 slice 5).
//
// The unit tests in internal/service/similar prove the arithmetic: cosine, the weights, the
// duration falloff. This file proves the parts arithmetic cannot reach -- that the SQL finds
// the evidence it claims to (including BOTH directions of the tag ontology, which no unit
// test can exercise), that the schema, resolver and service agree on names and shapes, that
// the exclusions hold on the real code path, and that a configured vector store actually
// changes the answer through the resolver.
//
// That last one earned its keep in this repo before: a field that resolves but is never
// queried behaves exactly like one that works, and a re-ranker wired to a store built per
// call behaves exactly like no re-ranker at all.

// similarScenesQuery is the query under test. Field names match the selection set exactly,
// because gqlgen decodes by name and panics on a key the struct does not have.
const similarScenesQuery = `
	query($id: ID!, $limit: Int) {
		findScene(id: $id) {
			similarScenes(limit: $limit) {
				scene { id title }
				score
				semanticScore
				reasons { kind label via weight }
			}
		}
	}
`

type similarReasonRow struct {
	Kind   string
	Label  string
	Via    *string
	Weight float64
}

type similarSceneRow struct {
	Scene struct {
		ID    string
		Title string
	}
	Score         float64
	SemanticScore *float64
	Reasons       []similarReasonRow
}

type similarScenesResponse struct {
	FindScene struct {
		SimilarScenes []similarSceneRow
	}
}

// similarTagFixture creates a tag directly. Inserted rather than created through an edit
// because a tag is fixture furniture here, not the behaviour under test.
func similarTagFixture(t *testing.T, name string) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	_, err := q().CreateTag(t.Context(), queries.CreateTagParams{ID: id, Name: name})
	require.NoError(t, err, "creating tag %q", name)
	return id
}

// similarStudioFixture creates a studio directly, for the same reason.
func similarStudioFixture(t *testing.T, name string) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	_, err := q().CreateStudio(t.Context(), queries.CreateStudioParams{ID: id, Name: name})
	require.NoError(t, err, "creating studio %q", name)
	return id
}

// similarSceneFixture inserts a scene with exactly the tags, performers, studio and duration
// a case needs.
//
// Inserted directly rather than through the create mutation, because the fixture has to
// control every input the score reads -- a mutation that also validates titles and dates
// would make each case a maintenance problem, and one that applied implications would hide
// the ontology from the test.
func similarSceneFixture(t *testing.T, title string, opts similarSceneOptions) uuid.UUID {
	t.Helper()

	id := uuid.Must(uuid.NewV7())
	_, err := q().CreateScene(t.Context(), queries.CreateSceneParams{
		ID:       id,
		Title:    strPtr(title),
		StudioID: opts.studio,
		Duration: opts.duration,
	})
	require.NoError(t, err, "creating scene %q", title)

	if len(opts.tags) > 0 {
		rows := make([]queries.CreateSceneTagsParams, 0, len(opts.tags))
		for _, tagID := range opts.tags {
			rows = append(rows, queries.CreateSceneTagsParams{SceneID: id, TagID: tagID})
		}
		_, err = q().CreateSceneTags(t.Context(), rows)
		require.NoError(t, err, "tagging %q", title)
	}

	if len(opts.performers) > 0 {
		rows := make([]queries.CreateScenePerformersParams, 0, len(opts.performers))
		for _, performerID := range opts.performers {
			rows = append(rows, queries.CreateScenePerformersParams{SceneID: id, PerformerID: performerID})
		}
		_, err = q().CreateScenePerformers(t.Context(), rows)
		require.NoError(t, err, "casting %q", title)
	}

	return id
}

type similarSceneOptions struct {
	tags       []uuid.UUID
	performers []uuid.UUID
	studio     uuid.NullUUID
	duration   *int
}

// similarSceneTitles is the fixture's answer in order, which is usually what a test asserts.
func similarSceneTitles(rows []similarSceneRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Scene.Title)
	}
	return out
}

// reasonsOfKind collects the labels of one kind, which is how a test asks "what did it say
// this had in common" without depending on the order of the rest.
func reasonsOfKind(row similarSceneRow, kind string) []string {
	out := make([]string, 0, len(row.Reasons))
	for _, r := range row.Reasons {
		if r.Kind == kind {
			out = append(out, r.Label)
		}
	}
	return out
}

func viaOf(row similarSceneRow, kind string) *string {
	for _, r := range row.Reasons {
		if r.Kind == kind {
			return r.Via
		}
	}
	return nil
}

// The full path: a scene recommends its neighbours, names what they have in common, and
// ranks them by the weights the spec states.
//
// One test rather than one per step, because what is being proved is that the chain is
// connected: the SQL finds the evidence, the service scores and explains it, and the
// resolver renders it.
func TestSimilarScenesNamesItsEvidenceAndRanks(t *testing.T) {
	admin := asAdmin(t)
	read := asRead(t)

	subjectPerformer, err := admin.client.createPerformer(models.PerformerCreateInput{Name: "Similarity Scene Star"})
	require.NoError(t, err)
	performerID, err := uuid.FromString(subjectPerformer.ID)
	require.NoError(t, err)

	sharedTag1 := similarTagFixture(t, "Similarity Tag One")
	sharedTag2 := similarTagFixture(t, "Similarity Tag Two")
	unrelatedTag := similarTagFixture(t, "Similarity Tag Unrelated")
	studio := similarStudioFixture(t, "Similarity Studio")

	subject := similarSceneFixture(t, "Similarity Subject Scene", similarSceneOptions{
		tags:       []uuid.UUID{sharedTag1, sharedTag2},
		performers: []uuid.UUID{performerID},
		studio:     uuid.NullUUID{UUID: studio, Valid: true},
	})

	// Two shared tags and nothing else.
	similarSceneFixture(t, "Two shared tags", similarSceneOptions{
		tags: []uuid.UUID{sharedTag1, sharedTag2},
	})
	// One shared tag AND the person. By the documented weights this must outrank the scene
	// above: a shared performer is worth 5 and a second shared tag only 3.
	similarSceneFixture(t, "One tag and the person", similarSceneOptions{
		tags:       []uuid.UUID{sharedTag1},
		performers: []uuid.UUID{performerID},
	})
	// The same person and NO tag in common: the performer axis has to be enough on its own,
	// or a recommendation depends on the two scenes happening to share vocabulary. A mutation
	// that removes the performer axis from the candidate predicate is what found this case.
	similarSceneFixture(t, "Only the person", similarSceneOptions{
		tags:       []uuid.UUID{unrelatedTag},
		performers: []uuid.UUID{performerID},
	})
	// Nothing in common: not a candidate at all, rather than a candidate scoring zero.
	unrelated := similarSceneFixture(t, "Unrelated", similarSceneOptions{
		tags: []uuid.UUID{unrelatedTag},
	})

	var resp similarScenesResponse
	read.client.MustPost(similarScenesQuery, &resp, client.Var("id", subject.String()), client.Var("limit", 10))

	require.Len(t, resp.FindScene.SimilarScenes, 3,
		"the unrelated scene shares no tag, performer or studio and must not appear at all; the subject must not recommend itself")
	assert.Equal(t, []string{"One tag and the person", "Two shared tags", "Only the person"},
		similarSceneTitles(resp.FindScene.SimilarScenes),
		"a shared performer (5) outweighs a second shared tag (3), and a performer alone outranks nothing at all")

	for _, row := range resp.FindScene.SimilarScenes {
		assert.NotEqual(t, subject.String(), row.Scene.ID, "a scene must not be its own recommendation")
		assert.NotEqual(t, unrelated.String(), row.Scene.ID)
		assert.NotEmpty(t, row.Reasons,
			"a recommendation with no stated reason is the thing this feature exists to avoid")
	}

	best := resp.FindScene.SimilarScenes[0]
	assert.Equal(t, []string{"Similarity Tag One"}, reasonsOfKind(best, "SAME_TAG"),
		"the reason must name the tag, not count it")
	assert.Equal(t, []string{"Similarity Scene Star"}, reasonsOfKind(best, "SHARED_PERFORMER"),
		"the reason must name the performer, not count them")
	assert.Empty(t, viaOf(best, "SAME_TAG"),
		"an exact tag match has nothing in between to name")

	// The score is reconstructible from the reasons we were sent: the candidate earns a tag
	// (3) and a performer (5) out of the subject's two tags (6), one performer (5) and
	// studio (2) = (3 + 5) / 13.
	assert.InDelta(t, 8.0/13.0, best.Score, 1e-9,
		"the score must be the weighted evidence over what the subject offers, not a bare number")
	assert.Nil(t, best.SemanticScore,
		"nothing is embedded on this instance, so there is no semantic component to report")

	// And the sum of the reported weights times the same denominator agrees with it.
	total := 0.0
	for _, r := range best.Reasons {
		total += r.Weight
	}
	assert.InDelta(t, 8.0/13.0, total/13.0, 1e-9,
		"a client can reconstruct the score from the reasons it was sent")
}

// The tag ontology, in BOTH directions, which is the part no unit test can reach: migration
// 111 does not write an implied tag onto `scene_tags`, so a scene tagged `Cowgirl Position`
// carries one row and a reader looking for `Woman On Top` has to walk the DAG.
//
// Getting this wrong is invisible on a single-tag fixture and obvious on a real archive: two
// scenes about the same subject would be called unrelated because nobody typed the general
// tag twice.
func TestSimilarScenesReadsTheTagOntologyBothWays(t *testing.T) {
	read := asRead(t)

	// TWO INDEPENDENT PAIRS, one per direction. Reusing a pair would make each subject
	// neighbour the other direction's fixture as well, and the test would be measuring its
	// own setup rather than the ontology.
	general := similarTagFixture(t, "Woman On Top")
	specific := similarTagFixture(t, "Cowgirl Position")
	require.NoError(t, q().CreateTagImplication(t.Context(), queries.CreateTagImplicationParams{
		TagID:        specific,
		ImpliedTagID: general,
	}), "`Cowgirl Position` implies `Woman On Top`")

	general2 := similarTagFixture(t, "Down Dog")
	specific2 := similarTagFixture(t, "Down Dog Variation")
	require.NoError(t, q().CreateTagImplication(t.Context(), queries.CreateTagImplicationParams{
		TagID:        specific2,
		ImpliedTagID: general2,
	}), "`Down Dog Variation` implies `Down Dog`")

	// Direction 1: the subject claims the general tag and the candidate is a SPECIFIC
	// instance of it. The candidate's tag is narrower than anything the subject said.
	generalSubject := similarSceneFixture(t, "General subject", similarSceneOptions{tags: []uuid.UUID{general}})
	instance := similarSceneFixture(t, "An instance of it", similarSceneOptions{tags: []uuid.UUID{specific}})

	var down similarScenesResponse
	read.client.MustPost(similarScenesQuery, &down, client.Var("id", generalSubject.String()), client.Var("limit", 10))
	require.Len(t, down.FindScene.SimilarScenes, 1,
		"a scene tagged `Cowgirl Position` IS a `Woman On Top` scene; the ontology is what says so")
	assert.Equal(t, instance.String(), down.FindScene.SimilarScenes[0].Scene.ID)
	assert.Equal(t, []string{"Cowgirl Position"}, reasonsOfKind(down.FindScene.SimilarScenes[0], "NARROWER_TAG"))
	require.NotNil(t, viaOf(down.FindScene.SimilarScenes[0], "NARROWER_TAG"),
		"the reason must name the subject's tag, or the implication cannot be checked")
	assert.Equal(t, "Woman On Top", *viaOf(down.FindScene.SimilarScenes[0], "NARROWER_TAG"))

	// Direction 2: the subject claims the SPECIFIC tag and the candidate the general one.
	// A family relative rather than an instance, and named as such.
	specificSubject := similarSceneFixture(t, "Specific subject", similarSceneOptions{tags: []uuid.UUID{specific2}})
	relative := similarSceneFixture(t, "A broader claim", similarSceneOptions{tags: []uuid.UUID{general2}})

	var up similarScenesResponse
	read.client.MustPost(similarScenesQuery, &up, client.Var("id", specificSubject.String()), client.Var("limit", 10))
	require.Len(t, up.FindScene.SimilarScenes, 1,
		"and this direction too, or the feature works only for a subject tagged the general way")
	assert.Equal(t, relative.String(), up.FindScene.SimilarScenes[0].Scene.ID)
	assert.Equal(t, []string{"Down Dog"}, reasonsOfKind(up.FindScene.SimilarScenes[0], "BROADER_TAG"))
	assert.Equal(t, "Down Dog Variation", *viaOf(up.FindScene.SimilarScenes[0], "BROADER_TAG"))

	// The two directions are NOT the same evidence, and the weights say so: the specific
	// candidate is a claim the archive recorded (`Cowgirl Position` implies `Woman On Top`),
	// the general one only shares a family.
	assert.Greater(t, down.FindScene.SimilarScenes[0].Score, up.FindScene.SimilarScenes[0].Score,
		"an instance of a subject's claim must outrank a broader claim against it")
}

// The studio term, and the duration term with it, because both are the axes a reader would
// guess first and the ones most easily confused with each other.
func TestSimilarScenesCountsTheStudioAndTheRuntime(t *testing.T) {
	read := asRead(t)

	studio := similarStudioFixture(t, "Similarity Runtime Studio")
	tag := similarTagFixture(t, "Similarity Runtime Tag")

	subject := similarSceneFixture(t, "Runtime subject", similarSceneOptions{
		tags:     []uuid.UUID{tag},
		studio:   uuid.NullUUID{UUID: studio, Valid: true},
		duration: intPtr(1800),
	})
	// Half an hour LONGER: the duration term decays to nothing at exactly the half-width, so
	// this scene is recommended on the studio and the tag alone.
	similarSceneFixture(t, "Half an hour longer", similarSceneOptions{
		tags:     []uuid.UUID{tag},
		studio:   uuid.NullUUID{UUID: studio, Valid: true},
		duration: intPtr(3600),
	})
	// Ten minutes longer: 2.0 * (1 - 600/1800) = 1.333.
	similarSceneFixture(t, "Ten minutes longer", similarSceneOptions{
		tags:     []uuid.UUID{tag},
		studio:   uuid.NullUUID{UUID: studio, Valid: true},
		duration: intPtr(2400),
	})
	// An unknown runtime must not earn the term, and must not be treated as zero either --
	// two unknowns compared would be a perfect match.
	similarSceneFixture(t, "Runtime unknown", similarSceneOptions{
		tags:   []uuid.UUID{tag},
		studio: uuid.NullUUID{UUID: studio, Valid: true},
	})

	var resp similarScenesResponse
	read.client.MustPost(similarScenesQuery, &resp, client.Var("id", subject.String()), client.Var("limit", 10))
	require.Len(t, resp.FindScene.SimilarScenes, 3)

	byTitle := make(map[string]similarSceneRow, len(resp.FindScene.SimilarScenes))
	for _, row := range resp.FindScene.SimilarScenes {
		byTitle[row.Scene.Title] = row
	}

	require.Contains(t, byTitle, "Ten minutes longer")
	assert.Len(t, reasonsOfKind(byTitle["Ten minutes longer"], "SIMILAR_DURATION"), 1,
		"a runtime ten minutes away is a tiebreak worth naming")
	assert.Empty(t, reasonsOfKind(byTitle["Half an hour longer"], "SIMILAR_DURATION"),
		"the term reaches zero at half an hour apart, and then it is not a reason at all")
	assert.Empty(t, reasonsOfKind(byTitle["Runtime unknown"], "SIMILAR_DURATION"),
		"an unrecorded runtime is not evidence of a similar one")

	assert.Len(t, reasonsOfKind(byTitle["Runtime unknown"], "SAME_STUDIO"), 1,
		"the studio is still a fact even when the runtime is not")
	assert.Equal(t, []string{"Similarity Runtime Studio"}, reasonsOfKind(byTitle["Runtime unknown"], "SAME_STUDIO"))

	// Subject offers 1 tag (3) + studio (2) + duration (2) = 7.
	// The nearer scene earns 3 + 2 + 2*(1 - 600/1800) = 6.333...
	assert.InDelta(t, (3.0+2.0+2.0*(1.0-600.0/1800.0))/7.0, byTitle["Ten minutes longer"].Score, 1e-9)
	// The unknown-runtime scene earns 3 + 2 = 5.
	assert.InDelta(t, 5.0/7.0, byTitle["Runtime unknown"].Score, 1e-9,
		"a scene whose runtime is unknown must not be credited with matching one")

	// The far scene and the unknown one score the SAME, and that is the finding rather than a
	// disappointment: half an hour away earns exactly nothing, which is the same as an
	// unrecorded runtime. Neither is penalised and neither is credited, so their order is the
	// id tiebreak and the assertion is on the leading scene and the tie, not on a coin flip.
	assert.Equal(t, "Ten minutes longer", resp.FindScene.SimilarScenes[0].Scene.Title)
	assert.InDelta(t, byTitle["Half an hour longer"].Score, byTitle["Runtime unknown"].Score, 1e-9,
		"a runtime half an hour away and no runtime at all are worth the same: neither earns the term")
	assert.InDelta(t, 5.0/7.0, byTitle["Half an hour longer"].Score, 1e-9)
}

// Exclusions, on the real code path: the subject itself, soft-deleted scenes, and a
// soft-deleted performer whose co-appearance rows survive the delete.
func TestSimilarScenesExcludesItselfDeletedScenesAndDeletedPerformers(t *testing.T) {
	admin := asAdmin(t)
	read := asRead(t)

	tag := similarTagFixture(t, "Similarity Exclusion Tag")
	livePerformer, err := admin.client.createPerformer(models.PerformerCreateInput{Name: "Similarity Exclusion Live"})
	require.NoError(t, err)
	livePerformerID, err := uuid.FromString(livePerformer.ID)
	require.NoError(t, err)
	gonePerformer, err := admin.client.createPerformer(models.PerformerCreateInput{Name: "Similarity Exclusion Gone"})
	require.NoError(t, err)
	gonePerformerID, err := uuid.FromString(gonePerformer.ID)
	require.NoError(t, err)

	subject := similarSceneFixture(t, "Exclusion subject", similarSceneOptions{tags: []uuid.UUID{tag}})

	// Soft-deleted AFTER being tagged: the scene_tags rows survive, which is exactly the
	// state a read-time filter has to handle rather than a cascade.
	deleted := similarSceneFixture(t, "Deleted neighbour", similarSceneOptions{tags: []uuid.UUID{tag}})
	_, err = q().SoftDeleteScene(t.Context(), deleted)
	require.NoError(t, err, "soft-deleting the neighbour")

	// A live neighbour sharing a tag, plus a stranger sharing ONLY a soft-deleted performer:
	// it must not be recommended, and its performer must not be named.
	similarSceneFixture(t, "Live neighbour", similarSceneOptions{tags: []uuid.UUID{tag}})
	similarSceneFixture(t, "Shares only a deleted performer", similarSceneOptions{performers: []uuid.UUID{gonePerformerID}})
	// The subject also carries the deleted performer, so the ONLY axis to `other` is one
	// the reader cannot follow.
	_, err = q().CreateScenePerformers(t.Context(), []queries.CreateScenePerformersParams{
		{SceneID: subject, PerformerID: livePerformerID},
		{SceneID: subject, PerformerID: gonePerformerID},
	})
	require.NoError(t, err, "casting the subject")

	_, err = q().SoftDeletePerformer(t.Context(), gonePerformerID)
	require.NoError(t, err, "soft-deleting the co-star")

	var resp similarScenesResponse
	read.client.MustPost(similarScenesQuery, &resp, client.Var("id", subject.String()), client.Var("limit", 10))

	titles := similarSceneTitles(resp.FindScene.SimilarScenes)
	assert.Contains(t, titles, "Live neighbour")
	assert.NotContains(t, titles, "Deleted neighbour", "a link to a deleted scene is worse than one fewer suggestion")
	assert.NotContains(t, titles, "Shares only a deleted performer",
		"a soft-deleted performer is not evidence: the row is gone and the reader cannot follow it")
	for _, row := range resp.FindScene.SimilarScenes {
		assert.NotEqual(t, subject.String(), row.Scene.ID)
		for _, r := range row.Reasons {
			assert.NotEqual(t, "Similarity Exclusion Gone", r.Label,
				"a deleted performer must not be named as a reason")
		}
	}
}

// An empty list is a fact about the archive, not a failure, and the limit is clamped rather
// than refused: a client asking for more than the ceiling wants results.
func TestSimilarScenesEmptyAndLimitAreNotErrors(t *testing.T) {
	read := asRead(t)

	// A scene with no tags, no performers, no studio and no duration has nothing in common
	// with anything, and no neighbour is an answer rather than an error.
	lonely := similarSceneFixture(t, "Nothing in common", similarSceneOptions{})
	var none similarScenesResponse
	read.client.MustPost(similarScenesQuery, &none, client.Var("id", lonely.String()))
	assert.Empty(t, none.FindScene.SimilarScenes, "no neighbour is an answer, not an error")

	// Three neighbours sharing one tag with the subject, read back through a limit of one
	// and through an absurd one.
	shared := similarTagFixture(t, "Similarity Limit Shared")
	subject := similarSceneFixture(t, "Limit subject", similarSceneOptions{tags: []uuid.UUID{shared}})
	for i := range 3 {
		similarSceneFixture(t, fmt.Sprintf("Limit neighbour %d", i), similarSceneOptions{tags: []uuid.UUID{shared}})
	}

	var capped similarScenesResponse
	read.client.MustPost(similarScenesQuery, &capped, client.Var("id", subject.String()), client.Var("limit", 1))
	assert.Len(t, capped.FindScene.SimilarScenes, 1)

	var greedy similarScenesResponse
	read.client.MustPost(similarScenesQuery, &greedy, client.Var("id", subject.String()), client.Var("limit", 100000))
	assert.Len(t, greedy.FindScene.SimilarScenes, 3,
		"an over-limit request must be clamped, not refused and not truncated to nothing")
}

// A configured vector store must actually change the answer, through the resolver.
//
// This is the test the slice's honesty rests on. The vector half cannot be the score:
// nothing on this instance produces embeddings, and a component nobody can check must not
// invent a number. So it REORDERS -- and "reorders" is a claim about behaviour rather than
// about code, which means it has to be observed. The same fixture is read twice, once with
// the default no-op store and once with an in-memory store holding vectors, and the two
// answers must differ in exactly the way the design says and in no other way.
func TestSimilarScenesAreReorderedByTheConfiguredVectorStore(t *testing.T) {
	// The fixture is arranged so the two orders are OPPOSITE and neither is a coincidence:
	// `beta` shares the studio as well as the tag, so the evidence puts it first
	// (5/5 = 1.0 against alpha's 3/5 = 0.6), while the vectors point `alpha` closer to the
	// subject (cosine 0.707 against 0.196). A tie would have made this test pass on either
	// answer; an inversion can only pass one way.
	shared := similarTagFixture(t, "Similarity Vector Tag")
	studio := similarStudioFixture(t, "Similarity Vector Studio")

	subject := similarSceneFixture(t, "Vector subject", similarSceneOptions{
		tags:   []uuid.UUID{shared},
		studio: uuid.NullUUID{UUID: studio, Valid: true},
	})
	alpha := similarSceneFixture(t, "Vector alpha", similarSceneOptions{tags: []uuid.UUID{shared}})
	beta := similarSceneFixture(t, "Vector beta", similarSceneOptions{
		tags:   []uuid.UUID{shared},
		studio: uuid.NullUUID{UUID: studio, Valid: true},
	})

	// The evidence-only answer, from the default store.
	read := asRead(t)
	var plain similarScenesResponse
	read.client.MustPost(similarScenesQuery, &plain, client.Var("id", subject.String()), client.Var("limit", 10))
	require.Len(t, plain.FindScene.SimilarScenes, 2)
	assert.Equal(t, []string{"Vector beta", "Vector alpha"}, similarSceneTitles(plain.FindScene.SimilarScenes),
		"the evidence puts the scene that also shares the studio first")
	for _, row := range plain.FindScene.SimilarScenes {
		assert.Nil(t, row.SemanticScore, "the default store holds nothing, so there is nothing to report")
	}

	// A factory whose configuration names the in-memory store, built here because a factory
	// reads its configuration once: mutating a singleton other tests already hold would
	// prove nothing about the configuration path.
	originalStore := config.C.SimilarVectorStore
	originalDims := config.C.SimilarVectorDimensions
	t.Cleanup(func() {
		config.C.SimilarVectorStore = originalStore
		config.C.SimilarVectorDimensions = originalDims
	})
	config.C.SimilarVectorStore = "inmemory"
	config.C.SimilarVectorDimensions = 3

	factory := service.NewFactory(dbtest.DB(), nil)
	store := factory.Similar().VectorStore()
	require.NoError(t, store.Put(subject, []float32{1, 0, 0}))
	require.NoError(t, store.Put(alpha, []float32{1, 1, 0}))  // cosine 1/sqrt(2) = 0.7071
	require.NoError(t, store.Put(beta, []float32{0.2, 1, 0})) // cosine 0.2/sqrt(1.04) = 0.1961

	vectorRunner := createTestRunner(t, userDB.read, userDB.readRoles, factory)
	var reordered similarScenesResponse
	vectorRunner.client.MustPost(similarScenesQuery, &reordered,
		client.Var("id", subject.String()), client.Var("limit", 10))

	require.Len(t, reordered.FindScene.SimilarScenes, 2,
		"the store reorders; it does not add or drop candidates")
	assert.Equal(t, []string{"Vector alpha", "Vector beta"}, similarSceneTitles(reordered.FindScene.SimilarScenes),
		"the closer vector must be promoted above the candidate with more structured evidence, or the store is not wired")

	require.NotNil(t, reordered.FindScene.SimilarScenes[0].SemanticScore)
	require.NotNil(t, reordered.FindScene.SimilarScenes[1].SemanticScore)
	assert.InDelta(t, 0.7071, *reordered.FindScene.SimilarScenes[0].SemanticScore, 1e-4,
		"the number it ranked on must be the one a reader can check")
	assert.InDelta(t, 0.1961, *reordered.FindScene.SimilarScenes[1].SemanticScore, 1e-4)

	// THE SCORE IS UNTOUCHED, which is the contract the schema states: the vector half ranks
	// and does not score. A client sorting by `score` gets the evidence-only order on both
	// instances, and the reason list is identical too -- a re-ranker that edited the evidence
	// would be inventing it.
	assert.InDelta(t, 0.6, reordered.FindScene.SimilarScenes[0].Score, 1e-9,
		"alpha earned 3 of the subject's 5 and the store must not change that")
	assert.InDelta(t, 1.0, reordered.FindScene.SimilarScenes[1].Score, 1e-9)
	assert.Len(t, reordered.FindScene.SimilarScenes[0].Reasons, 1)
	assert.Equal(t, "SAME_TAG", reordered.FindScene.SimilarScenes[0].Reasons[0].Kind)
}
