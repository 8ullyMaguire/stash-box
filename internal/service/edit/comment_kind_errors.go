package edit

import "errors"

// The refusals of the typed-comment vocabulary, named so a caller can match on them rather than on
// a message (plan phase 8 slice 8a).
var (
	// ErrUnknownCommentKind: the kind is not in the declared vocabulary.
	ErrUnknownCommentKind = errors.New("unknown comment kind")
	// ErrPerformerKindNeedsPerformer: a performer_id comment that names no performer.
	ErrPerformerKindNeedsPerformer = errors.New("a performer_id comment must name a performer")
	// ErrStructuredFieldNotAllowed: a structured field on a kind that has none.
	ErrStructuredFieldNotAllowed = errors.New("that comment kind carries no structured field")
)
