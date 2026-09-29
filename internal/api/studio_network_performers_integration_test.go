//go:build integration

package api_test

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Issue #974: "Network page lists scenes but no performers".
//
// A network/parent studio holds no scenes of its own -- its content lives on
// its sub-studios. The studio page's "All Scenes" tab filters scenes on
// studios.parent_studio_id and shows that content. The "Performers" tab passed
// studio_id straight through to the performer query, which matched it exactly
// against scenes.studio_id, so a network returned nothing.
//
// The fix makes the performer's studio filter cover the studio AND its direct
// children, mirroring the scene query's ParentStudio filter so the two tabs on
// one page cannot disagree.

func (s *testRunner) performersForStudio(t *testing.T, studioID uuid.UUID) []uuid.UUID {
	t.Helper()

	res, err := s.resolver.Studio().Performers(s.ctx, &models.Studio{ID: studioID}, models.PerformerQueryInput{
		Page:    1,
		PerPage: 200,
	})
	require.NoError(t, err)
	require.NotNil(t, res)

	perfs, err := s.resolver.QueryPerformersResultType().Performers(s.ctx, res)
	require.NoError(t, err)

	ids := make([]uuid.UUID, 0, len(perfs))
	for i := range perfs {
		ids = append(ids, perfs[i].ID)
	}
	return ids
}

func TestNetworkStudioListsPerformersFromSubStudios(t *testing.T) {
	pt := createPerformerTestRunner(t)

	network, err := pt.createTestStudio(nil)
	require.NoError(t, err)

	networkID := network.UUID()
	sub, err := pt.createTestStudio(&models.StudioCreateInput{
		Name:     pt.generateStudioName(),
		ParentID: &networkID,
	})
	require.NoError(t, err)

	performer, err := pt.createTestPerformer(nil)
	require.NoError(t, err)

	// The only scene lives on the sub-studio, as it would on a real network.
	subID := sub.UUID()
	_, err = pt.createTestScene(&models.SceneCreateInput{
		Date:       "2024-01-01",
		StudioID:   &subID,
		Performers: []models.PerformerAppearanceInput{{PerformerID: performer.UUID()}},
	})
	require.NoError(t, err)

	ids := pt.performersForStudio(t, networkID)

	assert.Contains(t, ids, performer.UUID(),
		"network page must list performers from its sub-studios (#974)")
}

func TestStudioPerformersStillIncludeStudiosOwnScenes(t *testing.T) {
	pt := createPerformerTestRunner(t)

	studio, err := pt.createTestStudio(nil)
	require.NoError(t, err)

	performer, err := pt.createTestPerformer(nil)
	require.NoError(t, err)

	// A scene on the studio itself -- the pre-existing behaviour must survive
	// the network traversal, or a leaf studio's page goes empty.
	studioID := studio.UUID()
	_, err = pt.createTestScene(&models.SceneCreateInput{
		Date:       "2024-01-01",
		StudioID:   &studioID,
		Performers: []models.PerformerAppearanceInput{{PerformerID: performer.UUID()}},
	})
	require.NoError(t, err)

	ids := pt.performersForStudio(t, studioID)

	assert.Contains(t, ids, performer.UUID(),
		"a studio must still list performers from its own scenes")
}

// A sub-studio page is scoped to that sub-studio, not the whole network. If the
// traversal had gone the wrong way (child pulling in its parent) the sibling's
// performer would leak in.
func TestSubStudioDoesNotListSiblingStudioPerformers(t *testing.T) {
	pt := createPerformerTestRunner(t)

	network, err := pt.createTestStudio(nil)
	require.NoError(t, err)
	networkID := network.UUID()

	subA, err := pt.createTestStudio(&models.StudioCreateInput{
		Name:     pt.generateStudioName(),
		ParentID: &networkID,
	})
	require.NoError(t, err)
	subB, err := pt.createTestStudio(&models.StudioCreateInput{
		Name:     pt.generateStudioName(),
		ParentID: &networkID,
	})
	require.NoError(t, err)

	performerB, err := pt.createTestPerformer(nil)
	require.NoError(t, err)

	// The scene is on sub B only.
	subBID := subB.UUID()
	_, err = pt.createTestScene(&models.SceneCreateInput{
		Date:       "2024-01-01",
		StudioID:   &subBID,
		Performers: []models.PerformerAppearanceInput{{PerformerID: performerB.UUID()}},
	})
	require.NoError(t, err)

	ids := pt.performersForStudio(t, subA.UUID())

	assert.NotContains(t, ids, performerB.UUID(),
		"a sub-studio page must not pull in a sibling sub-studio's performers")
}
