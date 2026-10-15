package models

import "time"

// ContentSourceAppeal is a contest against a refusal, and its outcome.
//
// The type carries the USER rather than only an id because the two questions a reader asks about an
// appeal -- who contested it, and who decided it -- are both questions about a person, and the operator's
// queue is the surface where that matters. `BlockedByID` is on the appeal rather than read from the block
// because a release deletes the block row while a pending appeal survives it (migration 141): the rule
// that a different moderator decides is a comparison against this field.
type ContentSourceAppeal struct {
	ID ID `json:"id"`

	// BlockID is the block this contested. It goes NIL the moment that block is released, because a
	// release deletes the row (migration 141, decision 1) -- so a reader seeing nil here is reading the
	// honest fact that the block is no longer in force, while Kind and Value still say what it was about.
	BlockID ID `json:"block_id"`

	// Kind and Value are the BLOCK's key: "url" or "host", and the normalized value. Kept on the appeal so
	// a decided appeal still names what it was about after the block row is gone, and so a member can file
	// against the address they were refused without ever seeing an operator's row id.
	Kind  string `json:"kind"`
	Value string `json:"value"`

	// Statement is the appellant's own words. Required, never empty.
	Statement string    `json:"statement"`
	FiledByID ID        `json:"filed_by_id"`
	FiledAt   time.Time `json:"filed_at"`

	// BlockedByID is who recorded the block. Nil only when that user's account has since been deleted;
	// the appeal then cannot be decided by anybody who matches it, which is correct and not a hole --
	// there is no blocked operator left to be the wrong decider.
	BlockedByID ID `json:"blocked_by_id"`

	// The decision. All three are set together or none of them are: the table's two CHECK constraints make
	// "decided with no outcome" and "granted by nobody" unrepresentable, so a half-decided appeal cannot be
	// read from this struct either. Outcome is a string for the same reason Kind is (the vocabulary lives in
	// the CHECK constraint until it needs to be a published enum).
	//
	// POINTERS, AND NOT FOR STYLE. A pending appeal has no decision, and this struct is what the API
	// serialises -- so the zero value of each field IS the answer a client reads. A plain string would make a
	// pending appeal report `"outcome": ""`, which is a decision to any `if (outcome)` test, and a plain time
	// would report a decision date in year 1. The first version of this file used plain values and three
	// resolver methods to reconstruct the nulls; THE LIVE WALK SHOWED THE METHODS WERE NEVER CALLED (gqlgen
	// only generates a resolver for a field whose model type does not already match the schema, and an
	// ungenerated method is dead code Go does not complain about), so the nulls live in the type now, where
	// nothing can skip them.
	DecidedByID  ID         `json:"decided_by_id"`
	Outcome      *string    `json:"outcome"`
	DecisionNote *string    `json:"decision_note"`
	DecidedAt    *time.Time `json:"decided_at"`

	// Released reports whether a granted appeal actually removed a live block, which is a different fact
	// from the outcome: a grant against a block somebody had already released changes nothing, and saying
	// "granted" while reporting a release that did not happen would put a false line in the audit trail.
	Released bool `json:"released"`
}
