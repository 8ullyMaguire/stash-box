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

// Typed comments through the real resolver (plan phase 8 slice 8a).
//
// The pure tests next door prove the vocabulary. These prove the PATH: that the kind and the
// structured field survive the round trip -- the model, the converter, the INSERT and the read --
// because the failure this catches is a comment that is accepted, stored and then read back as a
// plain discussion, which every unit test in the world passes.
//
// What is NOT here is deliberate: confirming a performer_id comment does not yet create an edit.
// That is slice 8b, and the plan's own verification for this phase -- "a confirmed performer_id
// comment creates an edit, and applying that edit links the performer" -- belongs to it. Asserting
// the edit here would be asserting something this slice does not do.

func TestTypedCommentsRoundTripWithTheirKind(t *testing.T) {
	editor := asEdit(t)
	submission := "a submission comment"
	e, err := editor.createTestTagEdit(models.OperationEnumCreate, nil, &models.EditInput{
		Operation: models.OperationEnumCreate,
		Comment:   &submission,
	})
	require.NoError(t, err)

	// A performer the comment will name.
	performer, err := asAdmin(t).createTestPerformer(&models.PerformerCreateInput{Name: "Typed Comment Subject"})
	require.NoError(t, err)
	// The wire carries ids as strings and the model wants a uuid, so the value the API is given is
	// converted once rather than compared in two shapes.
	performerID, err := uuid.FromString(performer.ID)
	require.NoError(t, err)

	performerKind := models.EditCommentKindPerformerID
	_, err = editor.resolver.Mutation().EditComment(editor.ctx, models.EditCommentInput{
		ID:          e.ID,
		Comment:     "this is her, from the tattoo",
		Kind:        &performerKind,
		PerformerID: &performerID,
	})
	require.NoError(t, err)

	comments, err := editor.resolver.Edit().Comments(editor.ctx, e)
	require.NoError(t, err)
	named := findComment(comments, "this is her, from the tattoo")
	require.NotNil(t, named, "the comment must be readable")

	// BY NAME, not by membership in the vocabulary: a mapping that answered "discussion" for
	// everything satisfies "is a kind" and fails this.
	assert.Equal(t, "performer_id", named.Kind)
	require.True(t, named.PerformerID.Valid, "the structured field survived the converter")
	assert.Equal(t, performerID, named.PerformerID.UUID)

	// AND THE GRAPHQL FIELD RESOLVERS, which is the layer a client actually reads and which the
	// column-level assertions above do not touch: the column holds a word, the wire carries an enum,
	// and the mapping between them is hand-written. Replacing it with a constant survives every
	// assertion about the model and fails here -- which is how this assertion came to exist.
	wireKind, err := editor.resolver.EditComment().Kind(editor.ctx, named)
	require.NoError(t, err)
	assert.Equal(t, models.EditCommentKindPerformerID, wireKind, "the enum's spelling, not the column's word")
	assert.NotEqual(t, models.EditCommentKindDiscussion, wireKind,
		"a mapping that answers the default for everything is the failure this asserts against")

	wirePerformer, err := editor.resolver.EditComment().PerformerID(editor.ctx, named)
	require.NoError(t, err)
	require.NotNil(t, wirePerformer)
	assert.Equal(t, performerID, *wirePerformer)

	// AND A COMMENT THAT SAYS NOTHING ABOUT KINDS IS A DISCUSSION, which is the case every existing
	// client is in.
	_, err = editor.resolver.Mutation().EditComment(editor.ctx, models.EditCommentInput{
		ID:      e.ID,
		Comment: "just talking",
	})
	require.NoError(t, err)

	comments, err = editor.resolver.Edit().Comments(editor.ctx, e)
	require.NoError(t, err)
	plain := findComment(comments, "just talking")
	require.NotNil(t, plain)
	assert.Equal(t, "discussion", plain.Kind)
	assert.False(t, plain.PerformerID.Valid, "a discussion names nobody")
}

// THE REFUSALS REACH THE CALLER AS NAMES, not as a constraint violation from the database: the
// service checks the vocabulary before the transaction opens, and the embedded cause travels.
func TestTypedCommentsRefuseAMismatchedField(t *testing.T) {
	editor := asEdit(t)
	e, err := editor.createTestTagEdit(models.OperationEnumCreate, nil, &models.EditInput{
		Operation: models.OperationEnumCreate,
	})
	require.NoError(t, err)
	performer, err := asAdmin(t).createTestPerformer(&models.PerformerCreateInput{Name: "Mismatched Subject"})
	require.NoError(t, err)
	mismatchedID, err := uuid.FromString(performer.ID)
	require.NoError(t, err)

	// The kind that means a performer, with none named.
	kind := models.EditCommentKindPerformerID
	_, err = editor.resolver.Mutation().EditComment(editor.ctx, models.EditCommentInput{
		ID:      e.ID,
		Comment: "her, definitely",
		Kind:    &kind,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, editsvc.ErrPerformerKindNeedsPerformer)

	// And the reverse: a discussion carrying one.
	discussion := models.EditCommentKindDiscussion
	_, err = editor.resolver.Mutation().EditComment(editor.ctx, models.EditCommentInput{
		ID:          e.ID,
		Comment:     "chatting",
		Kind:        &discussion,
		PerformerID: &mismatchedID,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, editsvc.ErrStructuredFieldNotAllowed)

	// Nothing was written by either refusal.
	comments, err := editor.resolver.Edit().Comments(editor.ctx, e)
	require.NoError(t, err)
	for _, c := range comments {
		assert.NotContains(t, []string{"her, definitely", "chatting"}, c.Text,
			"a refused comment must not be stored")
	}
}

// A KIND NOBODY DECLARED IS REFUSED, which is the schema's CHECK reached through the API.
func TestTypedCommentsRefuseAnUnnamedKind(t *testing.T) {
	editor := asEdit(t)
	e, err := editor.createTestTagEdit(models.OperationEnumCreate, nil, &models.EditInput{
		Operation: models.OperationEnumCreate,
	})
	require.NoError(t, err)

	// Built past the enum's own type: the API's type system refuses this word, and the service has
	// to as well, because an enum is not the only way a comment is written.
	unnamed := models.EditCommentKind("vibes")
	_, err = editor.resolver.Mutation().EditComment(editor.ctx, models.EditCommentInput{
		ID:      e.ID,
		Comment: "just vibes",
		Kind:    &unnamed,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, editsvc.ErrUnknownCommentKind)
}
