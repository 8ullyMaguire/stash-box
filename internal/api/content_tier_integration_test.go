//go:build integration

package api_test

import (
	"testing"

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

	// A viewer who has consented to nothing. Every member starts here.
	unconsented := asRead(t)

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
