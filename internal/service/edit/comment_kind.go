package edit

import (
	"fmt"
	"strings"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/models"
)

// Comment kinds (plan phase 8 slice 8a).
//
// A comment used to be free text that a reader had to parse. A kind says what the writer is DOING,
// and for one kind it decides that the comment carries a STRUCTURED FIELD instead of making the
// reader find an id in prose.
//
// THE VOCABULARY IS MIRRORED IN THREE PLACES AND THAT IS DELIBERATE, not an oversight to be
// factored away: the database's CHECK is what refuses a value nobody named, this slice is what
// refuses it with a message a caller can act on, and the GraphQL enum is what a client matches on.
// A single source would mean either the database accepting anything or the API having no vocabulary
// of its own -- and the house rule is that an unnamed value must ERROR.
type CommentKind string

const (
	// CommentKindDiscussion is what a comment is when the writer does not say otherwise: prose.
	CommentKindDiscussion CommentKind = "discussion"
	// CommentKindSourceInfo offers where a fact came from.
	CommentKindSourceInfo CommentKind = "source_info"
	// CommentKindPerformerID is the identification kind, and the only one with a structured field:
	// it names the performer the writer believes belongs here. Its CONFIRMATION routes into an
	// edit (slice 8b) rather than writing the link, because a vote is evidence, never authority.
	CommentKindPerformerID CommentKind = "performer_id"
	// CommentKindTimestamp points at a moment in the scene.
	CommentKindTimestamp CommentKind = "timestamp"
	// CommentKindCorrection proposes that something already recorded is wrong.
	CommentKindCorrection CommentKind = "correction"
	// CommentKindRequest asks for something to be added or changed.
	CommentKindRequest CommentKind = "request"
	// CommentKindReview is the reviewer's own note on the edit.
	CommentKindReview CommentKind = "review"
)

// CommentKinds is the declared vocabulary, in the schema's order.
var CommentKinds = []CommentKind{
	CommentKindDiscussion,
	CommentKindSourceInfo,
	CommentKindPerformerID,
	CommentKindTimestamp,
	CommentKindCorrection,
	CommentKindRequest,
	CommentKindReview,
}

// KindOf normalizes a caller's kind: an empty one is a discussion, the default the schema carries.
//
// An empty string is the ABSENCE of a choice, not an eighth kind, so it resolves to the default
// rather than being refused -- otherwise every existing client that says nothing about kinds would
// start failing.
func KindOf(raw string) CommentKind {
	if raw == "" {
		return CommentKindDiscussion
	}
	return CommentKind(raw)
}

// ValidateKind refuses a kind nobody declared, and then refuses a structured field that does not
// belong to the kind that carries it.
//
// The second rule is the schema's `edit_comments_kind_field` stated in Go, and the two are not
// redundant: the CHECK is the guarantee (no reader can meet the state) and this is the MESSAGE (the
// caller is told which of the two directions they got wrong). A comment that names a performer has
// to BE the kind that means it, and one of that kind has to name somebody -- there is no state in
// which a reader guesses which the writer meant.
func ValidateKind(kind CommentKind, performerID *uuid.UUID) error {
	declared := false
	for _, k := range CommentKinds {
		if k == kind {
			declared = true
			break
		}
	}
	if !declared {
		return fmt.Errorf("%w: %q is not a comment kind", ErrUnknownCommentKind, string(kind))
	}

	if kind == CommentKindPerformerID && performerID == nil {
		return ErrPerformerKindNeedsPerformer
	}
	if kind != CommentKindPerformerID && performerID != nil {
		return fmt.Errorf("%w: only a performer_id comment names a performer", ErrStructuredFieldNotAllowed)
	}
	return nil
}

// performerIDOrNil turns the storage shape into the decision shape. The schema's column is a
// NullUUID and the rule is about PRESENCE, so the conversion happens once, at the edge, rather than
// as a `.Valid` check inside every branch of the rule.
func performerIDOrNil(id uuid.NullUUID) *uuid.UUID {
	if !id.Valid {
		return nil
	}
	out := id.UUID
	return &out
}

// kindString turns the API's nullable enum into the word the vocabulary is written in. The API's
// absence of a choice and the service's default are the same thing, which is why the conversion is
// here and not a nil check at every caller.
func kindString(raw *models.EditCommentKind) string {
	if raw == nil {
		return ""
	}
	return strings.ToLower(string(*raw))
}
