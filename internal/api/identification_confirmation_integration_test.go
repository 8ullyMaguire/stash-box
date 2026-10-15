//go:build integration

package api_test

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/models"
	editsvc "github.com/stashapp/stash-box/internal/service/edit"
)

// Confirming an identification (plan phase 8 slice 8b).
//
// THE PHASE'S OWN VERIFICATION, IN ONE TEST: "a confirmed performer_id comment creates an edit, and
// applying that edit links the performer -- the test asserts the EDIT exists, because the failure mode
// is a comment that silently writes the link."
//
// So the assertions come in this order, and the order is the point: the edit exists, and the scene
// does NOT have the performer. Then, and only then, an admin approves the edit and the performer is
// linked. A version of this feature that wrote the link would pass the last assertion and fail the
// earlier one, which is exactly what the plan asks for.

func TestConfirmingAnIdentificationCreatesAnEditAndNotALink(t *testing.T) {
	admin := asAdmin(t)
	sceneTitle := "Scene Awaiting Identification"

	// THE SCENE ALREADY HAS SOMEBODY ON IT, and that is the fixture half of the assertion the slice
	// could not make before. With an empty scene, a confirmation that REPLACED the appearance list
	// instead of adding to it produces exactly the same observable result as one that adds -- so the
	// adding-vs-replacing rule had nothing to test it, and a probe that dropped the existing
	// appearances SURVIVED. Recorded as a surviving probe, now paid for.
	existing, err := admin.createTestPerformer(&models.PerformerCreateInput{Name: "Already In The Scene"})
	require.NoError(t, err)
	existingID, err := uuid.FromString(existing.ID)
	require.NoError(t, err)
	scene, err := admin.createTestScene(&models.SceneCreateInput{
		Title:      &sceneTitle,
		Performers: []models.PerformerAppearanceInput{{PerformerID: existingID}},
	})
	require.NoError(t, err)

	performer, err := admin.createTestPerformer(&models.PerformerCreateInput{Name: "The Identified Performer"})
	require.NoError(t, err)
	performerID, err := uuid.FromString(performer.ID)
	require.NoError(t, err)
	sceneID, err := uuid.FromString(scene.ID)
	require.NoError(t, err)

	// An editable proposal on the scene, which is what a comment can attach to: comments belong to
	// edits, and the edit is what carries the target.
	editor := asEdit(t)
	submitted, err := editor.createTestSceneEdit(models.OperationEnumModify, nil, &models.EditInput{
		ID:        &sceneID,
		Operation: models.OperationEnumModify,
	})
	require.NoError(t, err)

	// THE SUGGESTION.
	kind := models.EditCommentKindPerformerID
	_, err = editor.resolver.Mutation().EditComment(editor.ctx, models.EditCommentInput{
		ID:          submitted.ID,
		Comment:     "this is her, from the scene credits",
		Kind:        &kind,
		PerformerID: &performerID,
	})
	require.NoError(t, err)

	comments, err := editor.resolver.Edit().Comments(editor.ctx, submitted)
	require.NoError(t, err)
	suggestion := findComment(comments, "this is her, from the scene credits")
	require.NotNil(t, suggestion, "fixture: the suggestion must exist")
	require.Nil(t, suggestion.ConfirmedAt, "fixture: an unconfirmed suggestion has no confirmation")

	// THE CONFIRMATION, by a moderator.
	moderator := asModerate(t)
	reason := "verified against the scene credits"
	proposal, err := moderator.resolver.Mutation().ConfirmPerformerComment(moderator.ctx, models.ConfirmPerformerCommentInput{
		CommentID: suggestion.ID,
		Reason:    &reason,
	})
	require.NoError(t, err, "confirming an identification must produce a proposal")
	require.NotNil(t, proposal)
	assert.NotEqual(t, submitted.ID, proposal.ID, "the confirmation is a NEW edit, not the comment's own")

	// 1. THE EDIT EXISTS, and that assertion is carried by step 5 rather than by reading the edit's
	// details here: approving THIS edit is what puts her on the scene, and the scene starts empty, so
	// a proposal that is not the one under test cannot satisfy step 5.

	// 2. AND THE SCENE DOES NOT HAVE THE PERFORMER. This is the assertion the whole slice is for:
	// the confirmation proposed a change and did not make one.
	current, err := admin.resolver.Scene().Performers(admin.ctx, mustFindScene(t, admin, sceneID))
	require.NoError(t, err)
	require.Len(t, current, 1, "confirming a comment must not link the performer")
	assert.Equal(t, existingID, current[0].Performer.ID, "and must leave the scene exactly as it was")
	assert.NotEqual(t, performerID, current[0].Performer.ID, "the identified performer is NOT on the scene yet")

	// 3. The comment RECORDS ITS OUTCOME -- which edit it became -- or a moderator confirms the same
	// suggestion twice and nobody can tell.
	comments, err = editor.resolver.Edit().Comments(editor.ctx, submitted)
	require.NoError(t, err)
	recorded := findComment(comments, "this is her, from the scene credits")
	require.NotNil(t, recorded)
	require.NotNil(t, recorded.ConfirmedAt, "the suggestion records that it was confirmed")
	require.True(t, recorded.ConfirmedEditID.Valid)
	assert.Equal(t, proposal.ID, recorded.ConfirmedEditID.UUID, "the comment points at the edit it became")

	// 4. AND A SECOND CONFIRMATION IS REFUSED, which is the rule that keeps one suggestion one edit.
	_, err = moderator.resolver.Mutation().ConfirmPerformerComment(moderator.ctx, models.ConfirmPerformerCommentInput{
		CommentID: suggestion.ID,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, editsvc.ErrCommentAlreadyConfirmed)

	// 5. APPLYING THE EDIT IS WHAT LINKS HER -- the second half of the phase's verification.
	_, err = admin.approveEdit(proposal.ID)
	require.NoError(t, err)

	// THE READER IS A DIFFERENT RUNNER, and that is a finding rather than tidiness: the GraphQL
	// resolvers go through a DATALOADER, which caches per REQUEST -- per context, here. The first
	// version read the scene's performers twice in one context, and the second read answered with the
	// first read's empty list, so the apply looked like it had done nothing. A fresh session is the
	// only honest way to ask the question again.
	fresh := asAdmin(t)
	after, err := fresh.resolver.Scene().Performers(fresh.ctx, mustFindScene(t, fresh, sceneID))
	require.NoError(t, err)
	require.Len(t, after, 2, "the approved edit adds the performer WITHOUT removing the one already there")
	ids := []uuid.UUID{after[0].Performer.ID, after[1].Performer.ID}
	assert.Contains(t, ids, performerID, "the identified performer is linked")
	assert.Contains(t, ids, existingID, "and the performer who was already there is still there")
}

// A DISCUSSION CANNOT BE CONFIRMED: there is no suggestion in it, and the refusal comes before any
// edit is created.
func TestOnlyAnIdentificationCanBeConfirmed(t *testing.T) {
	editor := asEdit(t)
	submitted, err := editor.createTestTagEdit(models.OperationEnumCreate, nil, &models.EditInput{
		Operation: models.OperationEnumCreate,
	})
	require.NoError(t, err)

	_, err = editor.resolver.Mutation().EditComment(editor.ctx, models.EditCommentInput{
		ID:      submitted.ID,
		Comment: "just talking",
	})
	require.NoError(t, err)

	comments, err := editor.resolver.Edit().Comments(editor.ctx, submitted)
	require.NoError(t, err)
	discussion := findComment(comments, "just talking")
	require.NotNil(t, discussion)

	_, err = asModerate(t).resolver.Mutation().ConfirmPerformerComment(asModerate(t).ctx, models.ConfirmPerformerCommentInput{
		CommentID: discussion.ID,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, editsvc.ErrNotAnIdentification)
}

func mustFindScene(t *testing.T, r *testRunner, id uuid.UUID) *models.Scene {
	t.Helper()
	scene, err := r.resolver.Query().FindScene(r.ctx, id)
	require.NoError(t, err)
	return scene
}
