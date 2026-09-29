//go:build integration

package api_test

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Issue #337: "Scene edits from favorited networks don't appear in the Edits
// view".
//
// The favorite filter matched a scene against a favorited studio by exact id
// (studio_favorites.studio_id = scenes.studio_id). A network's content lives on
// its sub-studios, so favoriting the network surfaced none of it and the only
// way to see those edits was to favorite every sub-studio individually.
//
// Same one-level traversal as #974 and the scene query's ParentStudio filter, so
// all three now agree on what a network contains.

func (s *testRunner) favoriteEditIDs(t *testing.T, isFavorite bool) []uuid.UUID {
	t.Helper()

	res, err := s.resolver.Query().QueryEdits(s.ctx, models.EditQueryInput{
		IsFavorite: &isFavorite,
		Page:       1,
		PerPage:    500,
	})
	require.NoError(t, err)
	require.NotNil(t, res)

	edits, err := s.resolver.QueryEditsResultType().Edits(s.ctx, res)
	require.NoError(t, err)

	ids := make([]uuid.UUID, 0, len(edits))
	for i := range edits {
		ids = append(ids, edits[i].ID)
	}
	return ids
}

// Favorite a network, create a scene on a sub-studio, submit an edit to that
// scene, and check the edit surfaces under the "favorites" filter.
func TestFavoriteNetworkIncludesSubStudioSceneEdits(t *testing.T) {
	pt := createSceneEditTestRunner(t)

	network, err := pt.createTestStudio(nil)
	require.NoError(t, err)
	networkID := network.UUID()

	sub, err := pt.createTestStudio(&models.StudioCreateInput{
		Name:     pt.generateStudioName(),
		ParentID: &networkID,
	})
	require.NoError(t, err)

	// Favorite only the network, never the sub-studio.
	_, err = pt.client.favoriteStudio(networkID, true)
	require.NoError(t, err)

	subID := sub.UUID()
	scene, err := pt.createTestScene(&models.SceneCreateInput{
		Title:    pStr(pt.generateSceneName()),
		Date:     "2024-01-01",
		StudioID: &subID,
	})
	require.NoError(t, err)

	// An edit against the sub-studio's scene, submitted by someone else.
	sceneID := scene.UUID()
	otherUser, err := pt.createTestUser(nil, []models.RoleEnum{models.RoleEnumEdit})
	require.NoError(t, err)
	editor := createTestRunner(pt.t, otherUser, []models.RoleEnum{models.RoleEnumEdit})

	newTitle := pt.generateSceneName()
	edit, err := editor.createTestSceneEdit(models.OperationEnumModify,
		&models.SceneEditDetailsInput{Title: &newTitle},
		&models.EditInput{Operation: models.OperationEnumModify, ID: &sceneID})
	require.NoError(t, err)

	ids := pt.favoriteEditIDs(t, true)

	assert.Contains(t, ids, edit.ID,
		"favoriting a network must surface its sub-studios' scene edits (#337)")
}

// A favorited network must not surface another, unrelated studio's scene
// edits. If the traversal were written wrong this is what breaks.
func TestFavoriteNetworkDoesNotIncludeUnrelatedStudioEdits(t *testing.T) {
	pt := createSceneEditTestRunner(t)

	network, err := pt.createTestStudio(nil)
	require.NoError(t, err)
	networkID := network.UUID()

	sub, err := pt.createTestStudio(&models.StudioCreateInput{
		Name:     pt.generateStudioName(),
		ParentID: &networkID,
	})
	require.NoError(t, err)

	// An entirely separate studio with no relationship to the network.
	other, err := pt.createTestStudio(nil)
	require.NoError(t, err)

	_, err = pt.client.favoriteStudio(networkID, true)
	require.NoError(t, err)

	otherID := other.UUID()
	otherScene, err := pt.createTestScene(&models.SceneCreateInput{
		Title:    pStr(pt.generateSceneName()),
		Date:     "2024-01-01",
		StudioID: &otherID,
	})
	require.NoError(t, err)

	otherUser, err := pt.createTestUser(nil, []models.RoleEnum{models.RoleEnumEdit})
	require.NoError(t, err)
	editor := createTestRunner(pt.t, otherUser, []models.RoleEnum{models.RoleEnumEdit})

	otherSceneID := otherScene.UUID()
	newTitle := pt.generateSceneName()
	otherEdit, err := editor.createTestSceneEdit(models.OperationEnumModify,
		&models.SceneEditDetailsInput{Title: &newTitle},
		&models.EditInput{Operation: models.OperationEnumModify, ID: &otherSceneID})
	require.NoError(t, err)

	// The sub-studio must also not be reachable unless favorited, so sanity
	// check the traversal really is scoped: the network's own sub-studio is
	// the only thing that should be in scope.
	subID := sub.UUID()
	subScene, err := pt.createTestScene(&models.SceneCreateInput{
		Title:    pStr(pt.generateSceneName()),
		Date:     "2024-01-01",
		StudioID: &subID,
	})
	require.NoError(t, err)

	subSceneID := subScene.UUID()
	subEdit, err := editor.createTestSceneEdit(models.OperationEnumModify,
		&models.SceneEditDetailsInput{Title: &newTitle},
		&models.EditInput{Operation: models.OperationEnumModify, ID: &subSceneID})
	require.NoError(t, err)

	ids := pt.favoriteEditIDs(t, true)

	assert.Contains(t, ids, subEdit.ID, "the network's own sub-studio should be in scope")
	assert.NotContains(t, ids, otherEdit.ID,
		"an unrelated studio's scene edit must not appear under a network favorite")
}
