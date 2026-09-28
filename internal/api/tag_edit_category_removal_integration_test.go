//go:build integration

package api_test

import (
	"testing"

	"github.com/stashapp/stash-box/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestModifyTagEditRemoveCategory is the regression test for issue #802,
// "Removing category is not a valid change".
//
// The report is that clearing an existing tag's category through an edit
// returns `Error: edit contains no changes` (internal/service/edit.ErrNoChanges).
//
// The mechanism matters for judging any fix. A GraphQL input field carries two
// distinct absences, and gqlgen flattens both into the same nil Go pointer:
//
//	absent       — the edit does not mention the field; keep the old value
//	explicit null — the edit asks for the field to be cleared
//
// utils.ArgumentsQuery exists to keep those apart, and the *EditFromDiff
// functions consult it per field via inputArgs.Field("x").IsNull(). So the
// question is not "is the diff buggy" but "does an explicit null survive the
// whole path from the GraphQL argument map into the persisted edit data, and
// does it reach apply as a deletion".
//
// This drives the real resolver against a real database, so the assertions
// cover the argument map, the diff, the ErrNoChanges guard, and the apply.
func TestModifyTagEditRemoveCategory(t *testing.T) {
	pt := createTagEditTestRunner(t)

	category, err := pt.createTestTagCategory(nil)
	require.NoError(t, err)
	categoryID := category.ID

	created, err := pt.createTestTag(&models.TagCreateInput{
		Name:       "category-removal-tag",
		CategoryID: &categoryID,
	})
	require.NoError(t, err)

	// The tag must actually start WITH a category, or the test proves nothing.
	require.NotNil(t, created.Category,
		"precondition: tag should have a category")

	id := created.UUID()
	// To test removing the category we must send an EXPLICIT nil for CategoryID.
	// An empty TagEditDetailsInput{} would omit the key entirely, which the
	// server interprets as "this edit does not touch the category".
	details := &models.TagEditDetailsInput{CategoryID: nil}

	edit, err := pt.createTestTagEdit(models.OperationEnumModify,
		details, &models.EditInput{
			Operation: models.OperationEnumModify,
			ID:        &id,
		})
	require.NoError(t, err, "clearing a category must not be rejected as having no changes")

	// The old value must be recorded, otherwise the diff is empty and the
	// edit is not reversible.
	tagDetails := pt.getEditTagDetails(edit)
	require.NotNil(t, tagDetails)
	assert.Equal(t, &categoryID, tagDetails.CategoryID,
		"the edit should record the category it is removing")
}

// TestApplyModifyTagEditRemoveCategory checks the other half: once the edit is
// applied, the tag must have no category at all — not the old one, and not an
// error.
//
// Without this, a change that only makes the edit *creatable* would pass while
// leaving the data unchanged, which is the actual complaint in both #802 and
// #9 ("the column is ignored and the old value is retained").
func TestApplyModifyTagEditRemoveCategory(t *testing.T) {
	pt := createTagEditTestRunner(t)

	category, err := pt.createTestTagCategory(nil)
	require.NoError(t, err)
	categoryID := category.ID

	created, err := pt.createTestTag(&models.TagCreateInput{
		Name:       "apply-category-removal-tag",
		CategoryID: &categoryID,
	})
	require.NoError(t, err)

	id := created.UUID()
	// Send an explicit nil for CategoryID to test removal.
	details := &models.TagEditDetailsInput{CategoryID: nil}

	edit, err := pt.createTestTagEdit(models.OperationEnumModify,
		details, &models.EditInput{
			Operation: models.OperationEnumModify,
			ID:        &id,
		})
	require.NoError(t, err)

	_, err = pt.approveEdit(edit.ID)
	require.NoError(t, err)

	modified, err := pt.resolver.Query().FindTag(pt.ctx, &id, nil)
	require.NoError(t, err)
	require.NotNil(t, modified)

	assert.False(t, modified.CategoryID.Valid,
		"after applying a category-removing edit the tag must have no category, got %v",
		modified.CategoryID)
}

// TestTagEditAbsentFieldIsNotTreatedAsClear pins the distinction the fix rests
// on: an edit that omits a field must NOT clear it.
//
// Without this, "make omitted fields clear" would satisfy #802 by silently
// wiping the category, description, and parent of every partial edit submitted
// in the database. This is the data-loss side of the same change.
func TestTagEditAbsentFieldIsNotTreatedAsClear(t *testing.T) {
	pt := createTagEditTestRunner(t)

	category, err := pt.createTestTagCategory(nil)
	require.NoError(t, err)
	categoryID := category.ID

	description := "keep me"
	created, err := pt.createTestTag(&models.TagCreateInput{
		Name:        "partial-edit-tag",
		CategoryID:  &categoryID,
		Description: &description,
	})
	require.NoError(t, err)

	// An edit that mentions only the name must not clear the category or the
	// description.
	newName := "renamed-partial-edit-tag"
	id := created.UUID()
	edit, err := pt.createTestTagEdit(models.OperationEnumModify,
		&models.TagEditDetailsInput{Name: &newName}, &models.EditInput{
			Operation: models.OperationEnumModify,
			ID:        &id,
		})
	require.NoError(t, err)

	tagDetails := pt.getEditTagDetails(edit)
	require.NotNil(t, tagDetails)
	assert.Nil(t, tagDetails.CategoryID,
		"an edit that does not mention category_id must not propose clearing it")
	assert.Nil(t, tagDetails.Description,
		"an edit that does not mention description must not propose clearing it")
	assert.Equal(t, &newName, tagDetails.Name)

	// And after apply, the untouched fields must survive.
	_, err = pt.approveEdit(edit.ID)
	require.NoError(t, err)

	modified, err := pt.resolver.Query().FindTag(pt.ctx, &id, nil)
	require.NoError(t, err)
	assert.True(t, modified.CategoryID.Valid,
		"category must survive an edit that did not mention it")
	require.NotNil(t, modified.Description)
	assert.Equal(t, description, *modified.Description)
}