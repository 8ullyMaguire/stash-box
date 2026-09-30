package federation

import (
	"time"

	"github.com/google/uuid"
)

// F1: NOTHING IDENTIFYING CROSSES A NODE BOUNDARY. "Content never broadcasts"
// is only a claim until the payload cannot carry content, so the types below
// are deliberately narrow and wire_test.go reflects over every field to prove
// the set of field types is closed.
//
// What a media reference would look like if one fitted: a filesystem path, a
// URL, a base64 blob, a host:port, or an object key. None of those can be
// written into a `string` without a caller first putting a scheme or a separator
// in it — which is why IsSuspiciousValue below exists and why it is checked on
// the way OUT as well as being structurally impossible on the way in. A field
// typed `string` can hold "/etc/passwd". The type system does not stop that; a
// test that reflects over field KINDS stops it from being a design accident,
// and the content check stops it from being a value.

// Question is what this instance asks a peer.
//
// Every field is text or a scalar. There is no field a snapshot, a collage, an
// image URL or a media reference can be put into.
//
// Description is free text and is therefore the field most likely to be abused
// as a side channel, which is exactly why Ask validates it.
type Question struct {
	// QueryID is this instance's id for the query. It is OUR id, not the
	// peer's: a peer must never be able to key its storage off a value we send
	// without also having one of its own.
	QueryID uuid.UUID

	// TargetType names the kind of entity being identified ("performer",
	// "studio", "scene"). A string from a closed set rather than a GQL enum,
	// because the peer is a different implementation and an unknown value must
	// be ignorable rather than a parse error that takes down the batch.
	TargetType string

	// Description is the user's description of what they are looking for.
	Description string

	// CandidateNames are the names we are already considering. These are NAMES,
	// not ids and not URLs: the peer may recognise one and reply with its own
	// id for it, and that id is resolved on this side or not at all.
	CandidateNames []string
}

// RemoteCandidate is one candidate a peer offered, with how many of the peer's
// own users suggested it.
//
// F2: this is EVIDENCE. It is never a vote, never a local row, and never
// something the local vote path can see. The store that receives it writes only
// to identification_foreign_candidates.
type RemoteCandidate struct {
	// Name is the candidate's name as the peer knows it.
	Name string

	// PeerID is the peer's id for it. Opaque here: this instance never looks it
	// up in its own database, because a peer inventing an id that happens to
	// match a local row is precisely the confusion F2 exists to prevent.
	PeerID string

	// SuggesterCount is how many of the peer's users suggested this name.
	//
	// An int and not a vote, and deliberately NOT summed into any local
	// weighting. A peer can report any number it likes; the field exists so an
	// operator can see that one peer disagrees with another, not so the numbers
	// can be added together into a score.
	SuggesterCount int
}

// Answer is a peer's reply: the candidates it already has, with how many of its
// own users suggested each.
//
// F2: it carries the peer's ids and names, and this instance resolves NOTHING
// from them. Nothing in this struct can become a local canonical link without
// passing through the same path a human suggestion would, and the peer has no
// way to take that path.
type Answer struct {
	// PeerInstanceID is who answered. Echoed rather than trusted from the
	// request, so a peer answering on behalf of another is visible as a
	// mismatch rather than silently attributed.
	PeerInstanceID string

	// Candidates are the peer's suggestions.
	Candidates []RemoteCandidate
}

// ProtocolVersion is the wire contract version, sent with every request.
//
// It exists because the two implementations are separate repos on separate
// schedules. A peer running an older build must be recognisable as older, not
// as broken: without it, the only signal a mismatched peer produces is a
// deserialisation error, which reads as "that peer is down" and gets it removed
// from the registry.
const ProtocolVersion = 1

// RequestTimeout is the per-peer budget for one answer.
//
// A federation query fans out to N peers in parallel, so the worst-case latency
// is set by the slowest peer rather than the median. Without a per-peer cap one
// unresponsive peer holds every query open for the full request timeout of
// whoever is waiting, which is the difference between a query that takes a
// second and one that times out.
const RequestTimeout = 5 * time.Second

// MaxDescriptionLen bounds the free-text field.
//
// A cap, not a truncation: a silently truncated description changes the answer
// without telling anyone, and an operator reading two answers to the same
// question has no way to know one was cut.
const MaxDescriptionLen = 2000

// MaxCandidateNames bounds how many names one question may carry.
//
// Also a cap rather than a truncation, for the same reason: a question with 40
// names and a question with 4 are different questions, and the peer cannot tell
// them apart if the list is quietly shortened.
const MaxCandidateNames = 50
