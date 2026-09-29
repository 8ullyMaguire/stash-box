//go:build integration

package api_test

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Issue #943: "Edits are not updated when entities are merged".
//
// A merge soft-deletes the source and writes a redirect, but any edit still
// PENDING against the source was left pointing at a now-deleted entity. It can
// never be applied, and it never shows up in the survivor's edit list, so the
// contributor's work is silently stranded.
//
// These tests pin the contract: after a merge, an edit that was pending against
// a merge source must address the merge target instead. An edit that already
// reached a verdict must NOT move -- rewriting history under voters and the
// author is a different bug, not a fix.

// editTargetID reads the entity an edit currently addresses, via the same
// resolver path the API serves.
func (s *testRunner) editTargetID(t *testing.T, editID uuid.UUID) (uuid.UUID, error) {
	t.Helper()

	edit, err := s.resolver.Query().FindEdit(s.ctx, editID)
	require.NoError(t, err)

	target, err := s.resolver.Edit().Target(s.ctx, edit)
	require.NoError(t, err)
	require.NotNil(t, target, "edit should still resolve a target after the merge")

	switch v := target.(type) {
	case *models.Performer:
		return v.ID, nil
	case *models.Scene:
		return v.ID, nil
	case *models.Studio:
		return v.ID, nil
	case *models.Tag:
		return v.ID, nil
	default:
		t.Fatalf("unexpected edit target type %T", target)
		return uuid.Nil, nil
	}
}

func TestMergeRetargetsPendingTagEdit(t *testing.T) {
	pt := createTagEditTestRunner(t)

	source, err := pt.createTestTag(nil)
	require.NoError(t, err)
	targetTag, err := pt.createTestTag(nil)
	require.NoError(t, err)

	// A pending MODIFY edit addressed at the merge SOURCE.
	sourceID := source.UUID()
	targetID := targetTag.UUID()
	newName := pt.generateTagName()
	details := models.TagEditDetailsInput{Name: &newName}
	editInput := models.EditInput{
		Operation: models.OperationEnumModify,
		ID:        &sourceID,
	}
	pending, err := pt.createTestTagEdit(models.OperationEnumModify, &details, &editInput)
	require.NoError(t, err)

	// Sanity: it addresses the source before the merge.
	before, err := pt.editTargetID(t, pending.ID)
	require.NoError(t, err)
	assert.Equal(t, sourceID, before, "edit should address the source before the merge")

	// Merge source -> target.
	mergeDetails := models.TagEditDetailsInput{Name: &newName}
	mergeInput := models.EditInput{
		Operation:      models.OperationEnumMerge,
		ID:             &targetID,
		MergeSourceIds: []uuid.UUID{sourceID},
	}
	mergeEdit, err := pt.createTestTagEdit(models.OperationEnumMerge, &mergeDetails, &mergeInput)
	require.NoError(t, err)

	_, err = pt.approveEdit(mergeEdit.ID)
	require.NoError(t, err)

	// The stranded edit must now address the survivor.
	after, err := pt.editTargetID(t, pending.ID)
	require.NoError(t, err)
	assert.Equal(t, targetID, after,
		"pending edit should be retargeted to the merge survivor (#943)")
}

func TestMergeRetargetsPendingStudioEdit(t *testing.T) {
	pt := createStudioEditTestRunner(t)

	source, err := pt.createTestStudio(nil)
	require.NoError(t, err)
	targetStudio, err := pt.createTestStudio(nil)
	require.NoError(t, err)

	sourceID := source.UUID()
	targetID := targetStudio.UUID()
	newName := pt.generateStudioName()
	details := models.StudioEditDetailsInput{Name: &newName}
	editInput := models.EditInput{
		Operation: models.OperationEnumModify,
		ID:        &sourceID,
	}
	pending, err := pt.createTestStudioEdit(models.OperationEnumModify, &details, &editInput)
	require.NoError(t, err)

	before, err := pt.editTargetID(t, pending.ID)
	require.NoError(t, err)
	assert.Equal(t, sourceID, before, "edit should address the source before the merge")

	mergeInput := models.EditInput{
		Operation:      models.OperationEnumMerge,
		ID:             &targetID,
		MergeSourceIds: []uuid.UUID{sourceID},
	}
	mergeEdit, err := pt.createTestStudioEdit(models.OperationEnumMerge, &details, &mergeInput)
	require.NoError(t, err)

	_, err = pt.approveEdit(mergeEdit.ID)
	require.NoError(t, err)

	after, err := pt.editTargetID(t, pending.ID)
	require.NoError(t, err)
	assert.Equal(t, targetID, after,
		"pending edit should be retargeted to the merge survivor (#943)")
}

func TestMergeRetargetsPendingPerformerEdit(t *testing.T) {
	pt := createPerformerEditTestRunner(t)

	source, err := pt.createTestPerformer(nil)
	require.NoError(t, err)
	targetPerformer, err := pt.createTestPerformer(nil)
	require.NoError(t, err)

	sourceID := source.UUID()
	targetID := targetPerformer.UUID()

	newName := pt.generatePerformerName()
	details := models.PerformerEditDetailsInput{Name: &newName}
	editInput := models.EditInput{
		Operation: models.OperationEnumModify,
		ID:        &sourceID,
	}
	pending, err := pt.createTestPerformerEdit(models.OperationEnumModify, &details, &editInput, nil)
	require.NoError(t, err)

	before, err := pt.editTargetID(t, pending.ID)
	require.NoError(t, err)
	assert.Equal(t, sourceID, before, "edit should address the source before the merge")

	mergeInput := models.EditInput{
		Operation:      models.OperationEnumMerge,
		ID:             &targetID,
		MergeSourceIds: []uuid.UUID{sourceID},
	}
	mergeEdit, err := pt.createTestPerformerEdit(models.OperationEnumMerge, &details, &mergeInput, nil)
	require.NoError(t, err)

	_, err = pt.approveEdit(mergeEdit.ID)
	require.NoError(t, err)

	after, err := pt.editTargetID(t, pending.ID)
	require.NoError(t, err)
	assert.Equal(t, targetID, after,
		"pending edit should be retargeted to the merge survivor (#943)")
}

func TestMergeRetargetsPendingSceneEdit(t *testing.T) {
	pt := createSceneEditTestRunner(t)

	source, err := pt.createTestScene(nil)
	require.NoError(t, err)
	targetScene, err := pt.createTestScene(nil)
	require.NoError(t, err)

	sourceID := source.UUID()
	targetID := targetScene.UUID()

	newTitle := pt.generateSceneName()
	details := models.SceneEditDetailsInput{Title: &newTitle}
	editInput := models.EditInput{
		Operation: models.OperationEnumModify,
		ID:        &sourceID,
	}
	pending, err := pt.createTestSceneEdit(models.OperationEnumModify, &details, &editInput)
	require.NoError(t, err)

	before, err := pt.editTargetID(t, pending.ID)
	require.NoError(t, err)
	assert.Equal(t, sourceID, before, "edit should address the source before the merge")

	mergeInput := models.EditInput{
		Operation:      models.OperationEnumMerge,
		ID:             &targetID,
		MergeSourceIds: []uuid.UUID{sourceID},
	}
	mergeEdit, err := pt.createTestSceneEdit(models.OperationEnumMerge, &details, &mergeInput)
	require.NoError(t, err)

	_, err = pt.approveEdit(mergeEdit.ID)
	require.NoError(t, err)

	after, err := pt.editTargetID(t, pending.ID)
	require.NoError(t, err)
	assert.Equal(t, targetID, after,
		"pending edit should be retargeted to the merge survivor (#943)")
}
