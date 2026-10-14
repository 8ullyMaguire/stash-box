package ingest

import (
	"github.com/gofrs/uuid"
)

// Decision is what the pipeline decided to do with a submitted URL (spec §1, §3).
type Decision string

const (
	// DecisionReject means the pipeline refused the URL and will not fetch it:
	// unsupported scheme, a denied host, or a source already recorded as taken down.
	DecisionReject Decision = "rejected"
	// DecisionAttach means an entity already claims this URL. Nothing is created; the
	// submission is a duplicate and is reported as handled, not as an error (spec §5).
	DecisionAttach Decision = "attached"
	// DecisionMerge means the URL is new but its content is not: the extracted
	// fingerprint matches an existing entity, so a source is added to that entity rather
	// than a second record being created for the same content.
	DecisionMerge Decision = "merged"
	// DecisionCreate means the URL is new and so is its content: a new record is written
	// with origin = 'aggregate'.
	DecisionCreate Decision = "created"
)

// Known is everything the database already knows about a submitted URL, gathered before
// the decision is made.
//
// It is a value, not a set of calls, so that the decision is a pure function: the rules
// below are the interesting part of the pipeline and they should be readable in one place
// and testable without a database. The caller's job is to assemble this accurately, and
// that is what the integration test checks.
type Known struct {
	// EntityIDs are the entities that already claim this exact normalised URL. More than
	// one is possible and not an error: two scenes may legitimately point at the same
	// tube page (a compilation and its parts), and the pipeline attaches the source to the
	// one it was asked about rather than guessing at a winner.
	EntityIDs []uuid.UUID
	// EntityID is the entity the submission was made against -- a scene page's "add a
	// source" form knows its target. Nil for a bare URL submission, where the pipeline has
	// no target and the URL's own match decides.
	EntityID *uuid.UUID
	// FingerprintMatch is an entity whose fingerprint matches what the extractor found.
	// Nil when nothing matched, or when no fingerprint was extracted at all.
	FingerprintMatch *uuid.UUID
	// Denied is set when the host is on the deny list or the source is already recorded
	// as taken down (DMCA). A denial is final: no fetch, no retry.
	Denied bool
	// DenyReason is the human-readable reason, recorded on the queue row.
	DenyReason string
}

// Decide applies the pipeline's rules to what is known, in the order that matters.
//
// THE ORDER IS THE RULE, and it is not the obvious one:
//
//  1. A DENIAL beats everything. A host we have been told to stop fetching from must not
//     be fetched because a page happens to be popular, and a URL already recorded as dead
//     by a takedown must not be re-attached because its fingerprint matches a scene.
//  2. An EXISTING CLAIM beats a fingerprint match. If some entity already records this
//     URL, the answer is that URL's, and looking up a fingerprint could only move the
//     source to a different entity -- a second decision about a question already decided.
//  3. A fingerprint match merges. The content is here twice under two URLs.
//  4. Otherwise, create.
//
// The reversed order is a plausible-looking bug with a real cost: with attach and merge
// swapped, a URL already on scene A whose fingerprint matches scene B would silently move
// the source to B, and dedup's whole output -- one scene per piece of content -- would
// depend on which of the two lookups happened to return first.
func Decide(k Known) Decision {
	if k.Denied {
		return DecisionReject
	}
	if len(k.EntityIDs) > 0 {
		return DecisionAttach
	}
	if k.FingerprintMatch != nil {
		return DecisionMerge
	}
	return DecisionCreate
}

// Target reports which entity a non-create decision applies to, so the caller does not have
// to re-derive the same precedence and get it wrong in a second place.
//
// The split exists because Decide answers WHAT happened and Target answers WHERE it landed,
// and only the second question differs between "the URL's entity" and "the fingerprint's
// entity".
func (k Known) Target() *uuid.UUID {
	if k.Denied {
		return nil
	}
	if k.EntityID != nil {
		// The submission named a target (a scene's "add source" form). Attach there even
		// if the URL is already claimed elsewhere: the submitter is telling us which
		// record the source belongs to, and that is information the URL match does not
		// have.
		return k.EntityID
	}
	if len(k.EntityIDs) > 0 {
		first := k.EntityIDs[0]
		return &first
	}
	return k.FingerprintMatch
}

// BackoffSeconds is how long to wait before retrying a transient failure, as a function of
// how many attempts have already been made.
//
// Doubling with a ceiling, and the ceiling is the point: an unbounded exponential makes a
// retry of a site that came back after a day into a wait of nine years, which is the same
// outcome as giving up while looking like patience. The attempt count's own ceiling (how
// many tries before a row is finished for good) is the sweep's decision, not this
// function's -- and it is phase 9's, deliberately, because a retry policy that lives in
// two places is one that disagrees with itself.
func BackoffSeconds(attempts int) int {
	const (
		base = 60
		max  = 6 * 60 * 60 // six hours
	)
	if attempts < 1 {
		attempts = 1
	}
	seconds := base
	for i := 1; i < attempts && seconds < max; i++ {
		seconds *= 2
	}
	if seconds > max {
		return max
	}
	return seconds
}
