//go:build integration

package api_test

import (
	"reflect"
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/models"
)

// Content tiers on the page path (plan phase 5, migration 120).
//
// THE ABSENCE ASSERTION IS THE SUBJECT: "a tiered scene is absent from search" is
// vacuous unless the same query returns it when the tier is lifted, so every
// absence below is preceded by the scene being COUNTED in that exact query shape.
// This repository has already had an absence assertion pass with the thing on
// screen (`docs/track/WORKLOG.md`, the duplicated nav label), and a deleted-row
// bug and a tier gate produce the same empty page.
//
// The search is by TITLE, which names one scene in a database shared with every
// other test in the package: a page-wide assertion would be an assertion about
// the other tests' fixtures.

func tierProbeSearch(t *testing.T, runner *testRunner, title string) []uuid.UUID {
	t.Helper()
	// Executed through the GraphQL CLIENT, not by calling the resolver: the paginated
	// page is produced by querySceneResolver.Scenes, and queryResolver.QueryScenes returns
	// only the filter -- so a resolver-level call would assert against an empty struct.
	//
	// Page, PerPage, Sort and Direction are spelled out: a Go struct literal does not get
	// the schema's defaults, and `page: 0` is not "unset" but an offset of minus one page.
	result, err := runner.client.queryScenes(models.SceneQueryInput{
		Title:     &title,
		Page:      1,
		PerPage:   25,
		Sort:      models.SceneSortEnumCreatedAt,
		Direction: models.SortDirectionEnumDesc,
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	ids := make([]uuid.UUID, 0, len(result.Scenes))
	for _, scene := range result.Scenes {
		ids = append(ids, scene.UUID())
	}
	return ids
}

func TestATieredSceneIsAbsentFromSearchUntilTheViewerOptsIn(t *testing.T) {
	// The admin creates the fixture and classifies the tag: creating a tag needs the
	// CREATE role, and using the most privileged account for it is deliberate -- step
	// 4 asserts that even THIS account cannot see a tiered scene it has not consented
	// to, so no fixture role can be the thing that makes the absence meaningless.
	moderator := asAdmin(t)

	title := moderator.generateSceneName()
	tagName := moderator.generateSceneName() + " tier probe"
	tag, err := moderator.createTestTag(&models.TagCreateInput{Name: tagName})
	require.NoError(t, err)

	scene, err := moderator.createTestScene(&models.SceneCreateInput{
		Title:  &title,
		TagIds: []uuid.UUID{tag.UUID()},
	})
	require.NoError(t, err)
	sceneID := scene.UUID()

	// A viewer who has consented to nothing.
	//
	// STATED, not assumed: asRead(t) hands out ONE shared user for the whole package, so a consent
	// an earlier test set is still in place when this one runs -- which is how this test passed on
	// its own and failed in the suite. Every test below sets the ceiling it means to test.
	unconsented := asRead(t)
	_, err = unconsented.resolver.Mutation().UserSetVisibleTiers(unconsented.ctx, []int{})
	require.NoError(t, err)

	// STEP 1 -- COUNT THE SUBJECT. Before the tag is classified the scene is
	// visible to this exact viewer through this exact query, so the page below can
	// only be empty because of the tier.
	before := tierProbeSearch(t, unconsented, title)
	require.Len(t, before, 1, "the unclassified scene must be visible to a viewer with no consent: "+
		"without this the absence below would be indistinguishable from a deleted row")
	assert.Equal(t, sceneID, before[0])

	// Classify the tag. A moderator action: a tier is a judgement about what a
	// viewer sees by default, and it can only remove.
	classified, err := moderator.resolver.Mutation().TagSetContentTier(moderator.ctx, models.TagContentTierInput{
		ID:            tag.UUID(),
		Tier:          2,
		RequiresOptIn: false,
	})
	require.NoError(t, err)
	require.Equal(t, 2, classified.ContentTier)
	assert.False(t, classified.RequiresOptIn)

	// STEP 2 -- THE ABSENCE.
	assert.Empty(t, tierProbeSearch(t, unconsented, title),
		"a tier-2 scene must be absent for a viewer who has opted into no tiers")

	// STEP 3 -- THE CONTROL, and the reason step 2 is about the tier: a viewer who
	// has opted in sees it through the same query.
	consented := asRead(t)
	tiers, err := consented.resolver.Mutation().UserSetVisibleTiers(consented.ctx, []int{1, 2})
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2}, tiers, "the array is stored as sent, not collapsed to its maximum")

	stored, err := consented.resolver.Query().VisibleContentTiers(consented.ctx)
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2}, stored)

	after := tierProbeSearch(t, consented, title)
	require.Len(t, after, 1, "opting into tier 2 must make a tier-2 scene visible")
	assert.Equal(t, sceneID, after[0])

	// STEP 4 -- NO ROLE SUBSTITUTES FOR CONSENT. The moderator who classified the
	// tag has consented to nothing, so they cannot see what they classified. This
	// is the asymmetry the whole mechanism rests on: a tier is denylist-shaped, so
	// privilege does not open it -- only the viewer's own opt-in does.
	assert.Empty(t, tierProbeSearch(t, moderator, title),
		"the account that classified the tag must not see the scene above its own ceiling: "+
			"no role may consent on the user's behalf")
}

func TestATierAboveTheCeilingStaysAbsent(t *testing.T) {
	moderator := asAdmin(t)

	title := moderator.generateSceneName()
	tagName := moderator.generateSceneName() + " tier 3 probe"
	tag, err := moderator.createTestTag(&models.TagCreateInput{Name: tagName})
	require.NoError(t, err)

	_, err = moderator.createTestScene(&models.SceneCreateInput{
		Title:  &title,
		TagIds: []uuid.UUID{tag.UUID()},
	})
	require.NoError(t, err)

	// A viewer who opted into tiers 1 and 2 but not 3. Consent is per tier, so a
	// ceiling of 2 must not be a key to everything -- which is what the ceiling
	// would become if the gate compared "consented at all" instead of the value.
	twoTier := asRead(t)
	_, err = twoTier.resolver.Mutation().UserSetVisibleTiers(twoTier.ctx, []int{1, 2})
	require.NoError(t, err)

	_, err = moderator.resolver.Mutation().TagSetContentTier(moderator.ctx, models.TagContentTierInput{
		ID:            tag.UUID(),
		Tier:          3,
		RequiresOptIn: true,
	})
	require.NoError(t, err)

	assert.Empty(t, tierProbeSearch(t, twoTier, title),
		"a tier-3 scene must stay absent for a viewer whose ceiling is 2")

	// And the same viewer with tier 3 added sees it, which is what makes the
	// assertion above about the ceiling rather than about a broken query.
	_, err = twoTier.resolver.Mutation().UserSetVisibleTiers(twoTier.ctx, []int{1, 2, 3})
	require.NoError(t, err)

	visible := tierProbeSearch(t, twoTier, title)
	require.Len(t, visible, 1, "opting into tier 3 must make a tier-3 scene visible")
}

// --- the similarity path: a RECOMMENDATION, which is where a tier matters most ---

type similarSceneOutput struct {
	Scene   sceneOutput `json:"scene"`
	Score   float64     `json:"score"`
	Reasons []struct {
		Kind string `json:"kind"`
	} `json:"reasons"`
}

func (c *graphqlClient) similarScenes(sceneID uuid.UUID, limit int) ([]similarSceneOutput, error) {
	q := `
	query SimilarScenes($id: ID!, $limit: Int) {
		findScene(id: $id) {
			id
			similarScenes(limit: $limit) {
				` + makeFragment(reflect.TypeFor[similarSceneOutput]()) + `
			}
		}
	}`

	var resp struct {
		FindScene struct {
			ID            string               `json:"id"`
			SimilarScenes []similarSceneOutput `json:"similarScenes"`
		} `json:"findScene"`
	}
	if err := c.Post(q, &resp, client.Var("id", sceneID), client.Var("limit", limit)); err != nil {
		return nil, err
	}
	return resp.FindScene.SimilarScenes, nil
}

func similarSceneIDs(scenes []similarSceneOutput) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(scenes))
	for _, s := range scenes {
		ids = append(ids, s.Scene.UUID())
	}
	return ids
}

// A similarity list is a RECOMMENDATION: the viewer did not ask for this scene and did not know
// it existed. Offering what they opted out of is the failure the tier exists to prevent, and it
// is worse here than in a search result they typed.
func TestSimilarScenesExcludesWhatTheViewerHasNotOptedInto(t *testing.T) {
	admin := asAdmin(t)

	titleA := admin.generateSceneName()
	titleB := admin.generateSceneName()
	tagName := admin.generateSceneName() + " similarity"
	tag, err := admin.createTestTag(&models.TagCreateInput{Name: tagName})
	require.NoError(t, err)

	// Two scenes sharing one tag: that shared fact is what makes B a candidate for A at all.
	subject, err := admin.createTestScene(&models.SceneCreateInput{
		Title: &titleA, TagIds: []uuid.UUID{tag.UUID()},
	})
	require.NoError(t, err)
	candidate, err := admin.createTestScene(&models.SceneCreateInput{
		Title: &titleB, TagIds: []uuid.UUID{tag.UUID()},
	})
	require.NoError(t, err)

	// The same shared user: consent to NOTHING first, explicitly, or this test inherits whatever
	// the test before it opted into.
	viewer := asRead(t)
	_, err = viewer.resolver.Mutation().UserSetVisibleTiers(viewer.ctx, []int{})
	require.NoError(t, err)

	// COUNT THE SUBJECT FIRST: before the tag is classified, the candidate is offered. Without
	// this, an empty list below would be indistinguishable from a similarity query that never
	// worked -- and an empty candidate pool is exactly what the tier predicate produces.
	before, err := viewer.client.similarScenes(subject.UUID(), 10)
	require.NoError(t, err)
	require.NotEmpty(t, before, "the subject must have a candidate to lose")
	assert.Contains(t, similarSceneIDs(before), candidate.UUID())

	_, err = admin.resolver.Mutation().TagSetContentTier(admin.ctx, models.TagContentTierInput{
		ID: tag.UUID(), Tier: 2, RequiresOptIn: false,
	})
	require.NoError(t, err)

	unconsented, err := viewer.client.similarScenes(subject.UUID(), 10)
	require.NoError(t, err)
	assert.Empty(t, similarSceneIDs(unconsented), "similar-to must not offer a scene above the viewer's ceiling")

	// THE CONTROL: the same query with consent returns the candidate, so the emptiness above is
	// the tier and not the query.
	_, err = viewer.resolver.Mutation().UserSetVisibleTiers(viewer.ctx, []int{1, 2})
	require.NoError(t, err)

	after, err := viewer.client.similarScenes(subject.UUID(), 10)
	require.NoError(t, err)
	require.NotEmpty(t, after, "opting into tier 2 must restore the candidate")
	assert.Contains(t, similarSceneIDs(after), candidate.UUID())
}
