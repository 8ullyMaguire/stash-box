//go:build integration

package api_test

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/models"
)

// The content-item edit type, end to end (aggregator phase 3, slice 3g).
//
// Proposed through the mutation, applied through the vote path, and read back through the
// READ surface -- the three surfaces that have to agree. A create that applies but leaves
// `edit.target` null, or a modify that applies against the wrong row, or a destroy that
// leaves the item listable, are all failures that only an end-to-end pass can see.
//
// THE POINT OF THE EDIT TYPE: content items are created by an edit, and this repository has
// exactly one write path for shared metadata (propose, vote, apply). Before this slice the
// only writer was ingestion, so a curator could not add one at all.

func contentItemEditMutation(t *testing.T, runner *testRunner, operation models.OperationEnum, target *uuid.UUID, details *models.ContentItemEditDetailsInput, comment string) *models.Edit {
	t.Helper()
	edit, err := runner.resolver.Mutation().ContentItemEdit(runner.ctx, models.ContentItemEditInput{
		Edit: &models.EditInput{
			ID:        target,
			Operation: operation,
			Comment:   &comment,
		},
		Details: details,
	})
	require.NoError(t, err)
	require.NotNil(t, edit)
	return edit
}

func TestAContentItemIsCreatedChangedAndRemovedByEdits(t *testing.T) {
	admin := asAdmin(t)

	kind := models.ContentItemKindComic
	title := "An edited comic"
	releaseDate := "2020-03-04"

	detailsText := "still here"
	edit := contentItemEditMutation(t, admin, models.OperationEnumCreate, nil, &models.ContentItemEditDetailsInput{
		Kind:        &kind,
		Title:       &title,
		ReleaseDate: &releaseDate,
		Details:     &detailsText,
	}, "adding a comic")

	assert.Equal(t, models.TargetTypeEnumContentItem.String(), edit.TargetType,
		"the edit must say what kind of thing it is about")
	assert.False(t, edit.Applied, "a proposal must not write the item before it applies")

	// The propose half is a diff stored on the edit, and the diff names the new values.
	details, err := admin.resolver.Edit().Details(admin.ctx, edit)
	require.NoError(t, err)
	proposed, ok := details.(*models.ContentItemEdit)
	require.True(t, ok, "the details of a content-item edit must be the content-item shape")
	require.NotNil(t, proposed.Title)
	assert.Equal(t, title, *proposed.Title)
	require.NotNil(t, proposed.Kind)
	assert.Equal(t, kind, *proposed.Kind)
	require.NotNil(t, proposed.ReleaseDate)
	assert.Equal(t, releaseDate, *proposed.ReleaseDate)

	// Nothing has been written yet: a create edit's item does not exist until the apply, and
	// the edit has no target to resolve.
	target, err := admin.resolver.Edit().Target(admin.ctx, edit)
	require.NoError(t, err)
	assert.Nil(t, target, "an unapplied create has no target: the row does not exist yet")

	applied, err := admin.resolver.Mutation().ApproveEdit(admin.ctx, models.ApproveEditInput{ID: edit.ID})
	require.NoError(t, err)
	assert.True(t, applied.Applied)

	// Now it exists, and the edit resolves to it.
	target, err = admin.resolver.Edit().Target(admin.ctx, applied)
	require.NoError(t, err)
	item, ok := target.(*models.ContentItem)
	require.True(t, ok, "the applied edit's target must be the content item it created")
	assert.Equal(t, title, item.Title)
	assert.Equal(t, kind, item.Kind)
	require.NotNil(t, item.ReleaseDate)
	assert.Equal(t, releaseDate, *item.ReleaseDate)

	itemID := item.ID

	// A MODIFY, through the same path.
	newTitle := "An edited comic, retitled"
	modifyEdit := contentItemEditMutation(t, admin, models.OperationEnumModify, &itemID,
		&models.ContentItemEditDetailsInput{Title: &newTitle}, "retitling")

	modifyDetails, err := admin.resolver.Edit().Details(admin.ctx, modifyEdit)
	require.NoError(t, err)
	modifyShape, ok := modifyDetails.(*models.ContentItemEdit)
	require.True(t, ok)
	require.NotNil(t, modifyShape.Title)
	assert.Equal(t, newTitle, *modifyShape.Title)

	oldDetails, err := admin.resolver.Edit().OldDetails(admin.ctx, modifyEdit)
	require.NoError(t, err)
	oldShape, ok := oldDetails.(*models.ContentItemEdit)
	require.True(t, ok)
	require.NotNil(t, oldShape.Title, "a modify must record what the value WAS")
	assert.Equal(t, title, *oldShape.Title)

	// The edit can be UPDATED before it is voted on -- re-proposed with more of the item
	// named. This also re-links the edit to its target, which is the one path where the
	// same (edit, item) pair reaches the join twice.
	updatedTitle := "An edited comic, retitled twice"
	updated, err := admin.resolver.Mutation().ContentItemEditUpdate(admin.ctx, modifyEdit.ID, models.ContentItemEditInput{
		Edit: &models.EditInput{
			ID:        &itemID,
			Operation: models.OperationEnumModify,
			Comment:   stringPtr("retitling, again"),
		},
		Details: &models.ContentItemEditDetailsInput{Title: &updatedTitle},
	})
	require.NoError(t, err)
	assert.False(t, updated.Applied, "updating the proposal must not apply it")

	_, err = admin.resolver.Mutation().ApproveEdit(admin.ctx, models.ApproveEditInput{ID: updated.ID})
	require.NoError(t, err)

	afterModify, err := admin.resolver.Query().ContentItem(admin.ctx, itemID)
	require.NoError(t, err)
	require.NotNil(t, afterModify)
	assert.Equal(t, updatedTitle, afterModify.Title)
	assert.Equal(t, kind, afterModify.Kind, "a modify that named only the title must not change the kind")
	require.NotNil(t, afterModify.ReleaseDate, "nor drop a date it did not name")
	assert.Equal(t, releaseDate, *afterModify.ReleaseDate)

	// CLEARING a date is spelled as the empty string, because null means "leave it alone"
	// everywhere in this edit system. A client that has no other way to say "no date" would
	// otherwise be unable to remove one.
	clearDate := ""
	clearEdit := contentItemEditMutation(t, admin, models.OperationEnumModify, &itemID,
		&models.ContentItemEditDetailsInput{ReleaseDate: &clearDate}, "removing the release date")
	_, err = admin.resolver.Mutation().ApproveEdit(admin.ctx, models.ApproveEditInput{ID: clearEdit.ID})
	require.NoError(t, err)

	afterClear, err := admin.resolver.Query().ContentItem(admin.ctx, itemID)
	require.NoError(t, err)
	require.NotNil(t, afterClear)
	assert.Nil(t, afterClear.ReleaseDate, "an empty date in an edit must clear the column, not store an empty string")
	assert.NotNil(t, afterClear.Details, "a modify that named only the date must leave the details alone")
	if afterClear.Details != nil {
		assert.Equal(t, "still here", *afterClear.Details)
	}

	// A DESTROY.
	destroyEdit := contentItemEditMutation(t, admin, models.OperationEnumDestroy, &itemID, nil, "retiring it")
	_, err = admin.resolver.Mutation().ApproveEdit(admin.ctx, models.ApproveEditInput{ID: destroyEdit.ID})
	require.NoError(t, err)

	gone, err := admin.resolver.Query().ContentItem(admin.ctx, itemID)
	require.NoError(t, err)
	assert.Nil(t, gone, "a destroyed item must be gone from the read surface")
}

func TestContentItemEditsAreRefusedByNameWhenTheyCannotWork(t *testing.T) {
	admin := asAdmin(t)
	ctx := admin.ctx

	kind := models.ContentItemKindText
	title := "A text item"

	// MERGE has no meaning for these kinds: a merge needs two rows that are the same thing,
	// and a kind is a media type rather than an identity.
	sourceID := newUUID(t)
	_, err := admin.resolver.Mutation().ContentItemEdit(ctx, models.ContentItemEditInput{
		Edit: &models.EditInput{
			Operation:      models.OperationEnumMerge,
			MergeSourceIds: []uuid.UUID{sourceID},
		},
		Details: &models.ContentItemEditDetailsInput{Kind: &kind, Title: &title},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be merged",
		"a merge must be refused BY NAME: the moderator reading the failed edit should learn why")

	// A create with no kind.
	_, err = admin.resolver.Mutation().ContentItemEdit(ctx, models.ContentItemEditInput{
		Edit:    &models.EditInput{Operation: models.OperationEnumCreate},
		Details: &models.ContentItemEditDetailsInput{Title: &title},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "needs a kind and a title")

	// A blank title: NOT NULL does not stop an empty string, and an item titled "   " renders
	// as an untitled item.
	blank := "   "
	_, err = admin.resolver.Mutation().ContentItemEdit(ctx, models.ContentItemEditInput{
		Edit:    &models.EditInput{Operation: models.OperationEnumCreate},
		Details: &models.ContentItemEditDetailsInput{Kind: &kind, Title: &blank},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be blank")

	// A date the DATE column could never hold: refused at submit, not at apply.
	badDate := "2020-13-45"
	_, err = admin.resolver.Mutation().ContentItemEdit(ctx, models.ContentItemEditInput{
		Edit: &models.EditInput{Operation: models.OperationEnumCreate},
		Details: &models.ContentItemEditDetailsInput{
			Kind: &kind, Title: &title, ReleaseDate: &badDate,
		},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "YYYY-MM-DD")

	// A modify that changes nothing.
	created := contentItemEditMutation(t, admin, models.OperationEnumCreate, nil,
		&models.ContentItemEditDetailsInput{Kind: &kind, Title: &title}, "creating")
	applied, err := admin.resolver.Mutation().ApproveEdit(ctx, models.ApproveEditInput{ID: created.ID})
	require.NoError(t, err)
	target, err := admin.resolver.Edit().Target(ctx, applied)
	require.NoError(t, err)
	item, ok := target.(*models.ContentItem)
	require.True(t, ok)

	sameTitle := title
	_, err = admin.resolver.Mutation().ContentItemEdit(ctx, models.ContentItemEditInput{
		Edit:    &models.EditInput{ID: &item.ID, Operation: models.OperationEnumModify},
		Details: &models.ContentItemEditDetailsInput{Title: &sameTitle},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no changes",
		"an edit that changes nothing must be refused rather than stored for a voter to reject")
}

// stringPtr is local to this file: the package already has a ptr for uuids.
func stringPtr(s string) *string { return &s }
