// Package federation implements SPEC D2: the identification board federates.
//
// Read docs/spec/feature-04-identification-federation.md first. The decisions
// that shape this package:
//
//	F1 — Federation carries questions and candidates. Never content.
//	F2 — A peer's answer is evidence, never a vote and never a local row.
//	F3 — A peer's answer is scoped to one query and expires.
//	F4 — Taste matching uses vote_count as a floor.
//	F5 — Trust weight multiplies; the peer registry is operator-only.
//	F6 — No discovery protocol in this phase.
//
// The sentence that governs every file here is the identification service's
// own: "a vote is EVIDENCE, not authority." Federation multiplies the
// opportunities to get that wrong, because now the evidence arrives from
// somewhere the local community did not vote on.
package federation

import (
	"errors"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/stashapp/stash-box/internal/queries"
)

// PeerStaleness is how long a peer's last successful contact stays fresh.
//
// Past this window the peer is not asked, because F3 scopes a peer's answer to
// the moment it was given. A three-week-old answer presented as a current
// opinion is worse than no answer at all, because it looks like a second
// source agreeing with the first.
//
// This is a constant and not config because making it an operator dial invites
// setting it to something large, and the failure mode of a large value is
// silent: stale evidence that keeps reasserting itself.
const PeerStaleness = 7 * 24 * time.Hour

// Fresh reports whether a peer is worth asking, at the current time.
//
// See FreshAt for the rule; this is the wall-clock convenience wrapper.
func Fresh(lastSeenAt *time.Time) bool {
	return FreshAt(lastSeenAt, time.Now())
}

// FreshAt is Fresh with an injected clock, which is what makes the boundary
// testable at all.
//
// The reason this exists is not tidiness. With time.Since called internally, a
// test that builds a fixture at `now - PeerStaleness` and asserts the peer is
// still fresh is asserting something the clock has already made false by the
// time the comparison runs — the elapsed time is always a few microseconds past
// the boundary. The resulting failure reads as a code bug when it is a test
// bug, and the tempting response is to "fix" a correct inclusive boundary into
// an exclusive one. That is how an off-by-one gets written off as flaky.
//
// The two negative cases are different facts and are deliberately not collapsed:
//
//   - never contacted (lastSeenAt is nil) — no evidence it is alive;
//   - gone quiet (lastSeenAt older than PeerStaleness) — positive evidence it
//     has stopped answering.
//
// Both mean "do not ask", so both return false. The reason to keep them
// distinguishable is the operator surface: a peer that has never been reached
// is usually a misconfiguration (wrong URL, wrong port, firewall), while a peer
// that went quiet after working is a peer that broke. Those need different
// fixes, and a single "not fresh" list cannot tell them apart.
//
// The boundary is INCLUSIVE at exactly PeerStaleness: a peer seen exactly one
// window ago is still fresh.
func FreshAt(lastSeenAt *time.Time, now time.Time) bool {
	if lastSeenAt == nil {
		return false
	}
	elapsed := now.Sub(*lastSeenAt)
	return elapsed <= PeerStaleness
}

// Askable reports whether a peer should be sent a broadcast at all.
//
// Askable is Fresh AND enabled AND trusted, and the three are separate checks
// because they fail for different reasons and an operator needs to tell them
// apart in a log line: a disabled peer is a decision, a stale peer is a
// condition, and a peer with a non-positive weight is a data error.
func Askable(p queries.FederationPeer) bool {
	return askableFields(p.Enabled, p.TrustWeight, p.LastSeenAt)
}

// askableFields is the row-level rule, taking the three fields it actually
// reads.
//
// Split out from Askable so the rule is testable as a RULE — a table of
// (enabled, weight, seen) rows — instead of every case having to construct a
// whole queries.FederationPeer to exercise three fields. The generated struct
// has eight more fields that are irrelevant here, and a test that has to fill
// them in to say something about freshness is a test nobody extends.
func askableFields(enabled bool, weight float64, seen pgtype.Timestamptz) bool {
	if !enabled {
		return false
	}
	if weight <= 0 || weight > 1 {
		// Out of range rather than clamped. The CHECK constraint prevents this
		// from being written, so reaching it means a peer arrived from somewhere
		// other than that constraint. Refusing to ask is right: broadcasting to
		// a peer whose weight we cannot interpret means we cannot say what its
		// evidence was worth.
		return false
	}
	// Delegated, for the same reason Peer.Askable delegates: one freshness rule,
	// not two.
	return Fresh(pgTime(seen))
}

// pgTime converts a nullable timestamp column to *time.Time.
//
// The two-step (check Valid, then take Time) is what makes a NULL last_seen_at
// come back as a nil pointer rather than as the zero time. Returning
// time.Time{} for a never-contacted peer would make Fresh see an enormous age
// and return false for the RIGHT reason by accident -- and a peer contacted
// exactly at the epoch would then be indistinguishable from one never
// contacted at all.
func pgTime(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := ts.Time
	return &t
}

// Peer is the registry-facing view of a configured peer.
//
// Distinct from queries.FederationPeer so the network layer cannot reach a
// database row directly, and so a peer handed to a transport carries only the
// three fields a transport needs.
type Peer struct {
	ID          uuid.UUID
	Name        string
	BaseURL     string
	InstanceID  string
	TrustWeight float64
	// LastSeenAt is nil for a never-contacted peer. See Fresh.
	LastSeenAt *time.Time
}

// FromRow converts a database row into a Peer.
func FromRow(p queries.FederationPeer) Peer {
	out := Peer{
		ID:          p.ID,
		Name:        p.Name,
		BaseURL:     p.BaseUrl,
		InstanceID:  p.InstanceID,
		TrustWeight: p.TrustWeight,
	}
	if p.LastSeenAt.Valid {
		t := p.LastSeenAt.Time
		out.LastSeenAt = &t
	}
	return out
}

// Askable reports whether this peer should be asked.
//
// Delegates to the package-level Fresh rather than repeating the comparison.
// The two were written separately at first and that is exactly the shape in
// which a rule quietly diverges: one gets the boundary fixed and the other
// does not, and which one is wrong becomes a coin flip.
func (p Peer) Askable() bool {
	if p.TrustWeight <= 0 || p.TrustWeight > 1 {
		return false
	}
	return Fresh(p.LastSeenAt)
}

// AskableAt is Askable with an injected clock. See FreshAt for why the clock
// is a parameter rather than a call to time.Now().
func (p Peer) AskableAt(now time.Time) bool {
	if p.TrustWeight <= 0 || p.TrustWeight > 1 {
		return false
	}
	return FreshAt(p.LastSeenAt, now)
}

// ErrNotFound is returned when a registry lookup matches no peer.
//
// Its own error rather than a wrapped pgx.ErrNoRows, because a caller
// distinguishing "no such peer" from "the database is unreachable" is exactly
// the distinction that decides whether to return 404 or 500, and unwrapping a
// driver error to learn that is a coupling this package should not impose on
// its callers.
var ErrNotFound = errors.New("federation peer not found")

// ErrDisabled is returned when an operation targets a peer the operator has
// switched off.
var ErrDisabled = errors.New("federation peer is disabled")
