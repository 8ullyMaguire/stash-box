package edit

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/models"
)

// The typed-comment vocabulary, without a database (plan phase 8 slice 8a).
//
// The pure half is the vocabulary and the field rule: which words are kinds, and which of them is
// allowed to carry a performer.

func TestEveryDeclaredKindIsAccepted(t *testing.T) {
	for _, kind := range CommentKinds {
		t.Run(string(kind), func(t *testing.T) {
			var performer *uuid.UUID
			if kind == CommentKindPerformerID {
				id := uuid.Must(uuid.NewV7())
				performer = &id
			}
			assert.NoError(t, ValidateKind(kind, performer))
		})
	}
}

// AN UNKNOWN KIND IS REFUSED BY NAME, and the message names the word, so a caller is told which
// value was not a kind rather than that something about their comment was wrong.
func TestAnUnnamedKindIsRefusedByName(t *testing.T) {
	err := ValidateKind(CommentKind("vibes"), nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnknownCommentKind)
	assert.Contains(t, err.Error(), "vibes")
}

// THE STRUCTURED FIELD BELONGS TO ITS KIND, in both directions. This is the schema's
// edit_comments_kind_field CHECK stated in Go, and each direction is its own refusal because each is
// its own mistake.
func TestTheStructuredFieldBelongsToItsKind(t *testing.T) {
	performer := uuid.Must(uuid.NewV7())

	missing := ValidateKind(CommentKindPerformerID, nil)
	require.Error(t, missing)
	assert.ErrorIs(t, missing, ErrPerformerKindNeedsPerformer,
		"a performer_id comment that names nobody knows nothing a reader can use")

	extra := ValidateKind(CommentKindDiscussion, &performer)
	require.Error(t, extra)
	assert.ErrorIs(t, extra, ErrStructuredFieldNotAllowed,
		"a discussion carrying a performer id would be read as a suggestion by every reader that finds it")
}

// AN EMPTY KIND IS THE ABSENCE OF A CHOICE, NOT AN EIGHTH KIND: it resolves to the default, or every
// existing client that says nothing about kinds would start failing.
func TestAnOmittedKindIsADiscussion(t *testing.T) {
	assert.Equal(t, CommentKindDiscussion, KindOf(""))
	assert.NoError(t, ValidateKind(KindOf(""), nil))

	// And a stated kind is not silently normalized into the default.
	assert.Equal(t, CommentKindReview, KindOf("review"))
}

// THE API'S ENUM AND THE SERVICE'S WORD ARE THE SAME SEVEN THINGS. A cast would not have caught a
// missing arm, and a missing arm is an error at runtime rather than a compile failure -- the same
// reason slice 6g's seasonal status is a hand-written mapping.
func TestTheEnumSpellingRoundTrips(t *testing.T) {
	pairs := []struct {
		enum models.EditCommentKind
		word string
	}{
		{models.EditCommentKindDiscussion, "discussion"},
		{models.EditCommentKindSourceInfo, "source_info"},
		{models.EditCommentKindPerformerID, "performer_id"},
		{models.EditCommentKindTimestamp, "timestamp"},
		{models.EditCommentKindCorrection, "correction"},
		{models.EditCommentKindRequest, "request"},
		{models.EditCommentKindReview, "review"},
	}
	assert.Len(t, pairs, len(CommentKinds), "the test and the vocabulary must declare the same number of kinds")
	for _, p := range pairs {
		t.Run(p.word, func(t *testing.T) {
			assert.Equal(t, p.word, kindString(&p.enum))
			assert.Contains(t, CommentKinds, CommentKind(p.word))
		})
	}
	assert.Equal(t, "", kindString(nil), "an omitted kind is the absence of a choice")
}
