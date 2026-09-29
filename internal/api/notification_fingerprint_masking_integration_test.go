//go:build integration

package api_test

import (
	"testing"
	"time"

	"github.com/stashapp/stash-box/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Issue #1060: "Fingerprint edit filter masked by favorites".
//
// TriggerSceneEditNotifications has one UNION arm per reason a user should hear
// about a scene edit. It collapsed them with DISTINCT ON (user_id) -- one row
// per user -- and with no ORDER BY, so which arm survived was undefined. A user
// who both favorited the scene's performer AND had submitted a fingerprint for
// the scene kept only one notification, so filtering the notifications page by
// FINGERPRINTED_SCENE_EDIT returned nothing while the same notification showed
// under FAVORITE_PERFORMER_EDIT.
//
// The arms are independent reasons to be told, and the reader filters on the
// stored type, so the fix keys the dedup on (user_id, type).

// notificationCountFor counts a user's notifications of one type. Notification
// has no `type` field in the schema, so the per-type filter is the only way to
// observe which types were stored -- which is exactly the query the reporter
// used, so the test exercises the real path.
func notificationCountFor(t *testing.T, pt *testRunner, nt models.NotificationEnum) int {
	t.Helper()

	res, err := pt.client.queryNotifications(models.QueryNotificationsInput{
		Page:    1,
		PerPage: 500,
		Type:    &nt,
	})
	require.NoError(t, err)
	require.NotNil(t, res)

	return res.Count
}

// A user who qualifies under two arms must keep BOTH notification types.
func TestSceneEditNotificationKeepsFingerprintTypeWhenAlsoFavorited(t *testing.T) {
	pt := createNotificationTestRunner(t)
	// asEdit cannot create entities; fixtures need the admin runner.
	admin := asAdmin(t)

	// The subscriber is the user who both favorites and fingerprints.
	subscriberUser, err := admin.createTestUser(nil, []models.RoleEnum{models.RoleEnumEdit})
	require.NoError(t, err)
	subscriber := createTestRunner(t, subscriberUser, []models.RoleEnum{models.RoleEnumEdit})

	_, err = subscriber.client.updateNotificationSubscriptions([]models.NotificationEnum{
		models.NotificationEnumFingerprintedSceneEdit,
		models.NotificationEnumFavoritePerformerEdit,
	})
	require.NoError(t, err)

	performer, err := admin.createTestPerformer(nil)
	require.NoError(t, err)

	studio, err := admin.createTestStudio(nil)
	require.NoError(t, err)
	studioID := studio.UUID()
	scene, err := admin.createTestScene(&models.SceneCreateInput{
		Title:      pStr(pt.generateSceneName()),
		Date:       "2024-01-01",
		StudioID:   &studioID,
		Performers: []models.PerformerAppearanceInput{{PerformerID: performer.UUID()}},
	})
	require.NoError(t, err)
	sceneID := scene.UUID()

	// Arm under test: the subscriber submitted a fingerprint for this scene.
	fpInput := admin.generateSceneFingerprint(nil)
	submitted, err := subscriber.client.submitFingerprint(models.FingerprintSubmission{
		SceneID: sceneID,
		Fingerprint: &models.FingerprintInput{
			Algorithm: fpInput.Algorithm,
			Hash:      fpInput.Hash,
			Duration:  fpInput.Duration,
		},
	})
	require.NoError(t, err)
	require.True(t, submitted, "fingerprint submission should succeed")

	// The masking condition: the subscriber ALSO favorites the performer, which
	// is a different arm of the same trigger.
	_, err = subscriber.client.favoritePerformer(performer.UUID(), true)
	require.NoError(t, err)

	// Someone else edits the scene -- the subscriber is not the author.
	// createTestSceneEdit posts the SceneEdit mutation, which routes to
	// OnCreateEdit and therefore to TriggerSceneEditNotifications.
	editorUser, err := admin.createTestUser(nil, []models.RoleEnum{models.RoleEnumEdit})
	require.NoError(t, err)
	editor := createTestRunner(t, editorUser, []models.RoleEnum{models.RoleEnumEdit})

	newTitle := admin.generateSceneName()
	_, err = editor.client.submitSceneEdit(models.SceneEditInput{
		Edit: &models.EditInput{
			Operation: models.OperationEnumModify,
			ID:        &sceneID,
		},
		Details: &models.SceneEditDetailsInput{Title: &newTitle},
	})
	require.NoError(t, err)

	// The trigger runs in a goroutine off the handler, so it may not have
	// committed by the time this returns.
	require.Eventually(t, func() bool {
		return notificationCountFor(t, subscriber,
			models.NotificationEnumFingerprintedSceneEdit) > 0
	}, 5*time.Second, 50*time.Millisecond,
		"FINGERPRINTED_SCENE_EDIT notification should arrive (#1060)")

	byFingerprint := notificationCountFor(t, subscriber,
		models.NotificationEnumFingerprintedSceneEdit)

	byFavorite := notificationCountFor(t, subscriber,
		models.NotificationEnumFavoritePerformerEdit)

	// Both arms must be present. Pre-fix, DISTINCT ON (user_id) kept only one
	// row per user, so exactly one of these two was empty.
	assert.Positive(t, byFingerprint,
		"filtering by FINGERPRINTED_SCENE_EDIT must not be empty when the user also favorited the performer (#1060)")
	assert.Positive(t, byFavorite,
		"filtering by FAVORITE_PERFORMER_EDIT must not be empty when the user also submitted a fingerprint (#1060)")
}
