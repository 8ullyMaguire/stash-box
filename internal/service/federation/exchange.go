package federation

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// THE EXCHANGE (phase 12, slice 12f): the outbound half's first caller.
//
// `Client.Broadcast` has existed since the mesh was built and NOTHING in production called it -- every
// caller was a test. A broadcast method with no caller is not a capability, it is a comment with an HTTP
// client in it, and it means the wire had ONE END WIRED: this instance could answer a peer (12b) but had
// no reason and no mechanism to ask one. This file is the mechanism: ask every peer this instance is
// willing to talk to what ONTOLOGY it speaks, and keep one row per peer.
//
// WHY THAT QUESTION. It is the only one this build can both ask and answer: the identification question
// is refused by name (the half is not built), and a question that is asked and always refused would make
// the caller look alive while proving nothing. The ontology question is answerable today, it is the
// question a peer must answer before anything else can be sent to it, and its answer carries the version
// decision (12c) -- so the exchange is the mesh's discovery step, not a probe.

// OntologyExchange is one peer's half of the exchange: what it said, or why it did not say anything.
//
// A row exists for EVERY peer passed in, including the ones that were not asked and the ones that did not
// answer. Dropping a silent peer from the report would make "no peers" and "every peer is down" the same
// list, and an operator reading that list would conclude the mesh is fine.
type OntologyExchange struct {
	// Peer is the registry row the exchange is about.
	Peer Peer

	// Asked is false when the peer was not sent the question at all, and Reason says why. A peer that is
	// disabled, stale or has no weight is not an error -- but it is not silence either.
	Asked bool

	// Answered is true only when a well-formed answer came back.
	Answered bool

	// Answer is what the peer said, empty unless Answered.
	Answer Answer

	// Reason is why the peer was not asked, or did not answer. Never empty when Answered is false.
	Reason string

	// TheirDecision is the crossing the peer sent with its answer: what IT says may be sent to us.
	// OurDecision is the same rule computed HERE from the same two claims.
	//
	// BOTH, because the rule is deterministic and each side must reach the same answer: a peer whose
	// decision differs from ours is either running a different version of the rule or is not running it
	// at all, and that is worth knowing before anything is exchanged on the strength of it.
	TheirDecision []OntologyFieldCrossing
	OurDecision   []OntologyFieldCrossing

	// Disagrees is set when the two decisions are not the same list. It is not an error, and it is not
	// silently ignored: a peer that computes the negotiation differently is the finding.
	Disagrees bool
}

// ExchangeOntologies asks every peer what ontology it speaks and returns one row per peer, in the order
// the peers were given.
//
// `ours` is the set of core ontology versions THIS instance implements, and it is what the local side of
// the comparison is computed from -- not the peer's claim read back, which would make every peer agree
// with itself.
func ExchangeOntologies(ctx context.Context, client *Client, peers []Peer, q Question, ours []int, now time.Time) ([]OntologyExchange, error) {
	// THE QUESTION MUST CARRY OUR CLAIM, and this refuses rather than asking anyway.
	//
	// The exchange is a negotiation, and a negotiation needs both sides. A question with no claim would be
	// answered by every peer, and the crossing in that answer would refuse every field with "the peer
	// declares no version" -- a full table of refusals describing a fact about US, filed under the peer's
	// name. The failure would look like the peers being incompatible, which is the one reading that would
	// send an operator to the wrong end of the wire.
	if !q.Ontology.Declared() {
		return nil, fmt.Errorf("an ontology exchange needs this instance's own claim on the question: " +
			"a question with nothing declared is answered by every peer against nothing, and every field " +
			"comes back refused for a reason that is about this instance and not about them")
	}

	rows := make([]OntologyExchange, 0, len(peers))
	askable := make([]Peer, 0, len(peers))
	index := make(map[string]int, len(peers))

	for _, p := range peers {
		index[p.InstanceID] = len(rows)
		row := OntologyExchange{Peer: p}
		switch {
		case p.InstanceID == "":
			// The registry's own key into an answer is the instance id, so a peer without one cannot be
			// matched to a reply -- and matching by URL instead would let a reply claim any row it liked.
			row.Reason = "the registry row has no instance id, so a reply could not be attributed to it"
		case !p.Enabled:
			// ENABLED IS CHECKED HERE, and it has to be: `AskableAt` answers "is this peer fresh enough
			// and trusted enough to ask", not "does the operator want it asked at all". The registry read
			// that filters on it (`ListEnabled`) is the caller's choice, and a caller that reaches for
			// `List` -- as the cron job first did -- would contact a peer the operator had switched OFF.
			// An outbound contact is the side where that mistake is not recoverable.
			row.Reason = fmt.Sprintf("%s is disabled in the registry, so it is not contacted", p.Name)
		case !p.AskableAt(now):
			// The cause, named: a stale peer and a weightless peer are different problems, and a reason
			// that lists both ("enabled=..., weight=...") reads as though the peer were disabled.
			switch {
			case p.LastSeenAt == nil:
				row.Reason = fmt.Sprintf("%s has never been seen, so this instance will not ask it", p.Name)
			default:
				row.Reason = fmt.Sprintf("%s was last seen %s, which is outside the freshness window",
					p.Name, p.LastSeenAt.UTC().Format(time.RFC3339))
			}
		default:
			row.Asked = true
			askable = append(askable, p)
		}
		rows = append(rows, row)
	}

	if len(askable) == 0 {
		return rows, nil
	}

	result, err := client.Broadcast(ctx, askable, q)
	if err != nil {
		// The whole-broadcast error is returned with the rows built so far, which is what a caller needs
		// to log: WHICH peers were skipped, plus why the asking itself failed.
		return rows, err
	}

	for _, failure := range result.Failed {
		i, ok := index[failure.InstanceID]
		if !ok {
			// A failure for a peer this call did not pass in: not someone else's row to fill, and not
			// this call's to invent.
			continue
		}
		rows[i].Reason = failure.Reason
	}

	for _, answer := range result.Answers {
		i, ok := index[answer.PeerInstanceID]
		if !ok {
			continue
		}
		rows[i].Answered = true
		rows[i].Answer = answer
		rows[i].TheirDecision = answer.Crossing

		// The local half of the negotiation, in (this instance, peer) order -- the same order the
		// answering half uses, so that the two SENTENCES name the same side.
		//
		// The DECISION does not depend on the order, and that is worth writing down because the first
		// version of this line assumed it did: the rule requires BOTH instances to implement the version
		// that introduced a field, so a pair that is version-skewed refuses that field whichever way round
		// it is passed, and the only thing the order changes is which side the reason names. Which is why
		// the order has to be right anyway: a comparison between our list and the peer's compares the
		// reasons too, and a sentence calling the peer "this instance" would make two correct
		// implementations look like they disagreed.
		compat := Compatibility(ours, answer.Ontology.CoreVersions)
		rows[i].OurDecision = crossingsFrom(compat)
		rows[i].Disagrees = !sameCrossing(rows[i].Theirs(), rows[i].OurDecision)
	}

	return rows, nil
}

// Theirs is the peer's decision as this instance reads it, and it exists so a caller comparing the two
// sides does not have to remember which field is which.
func (e OntologyExchange) Theirs() []OntologyFieldCrossing { return e.TheirDecision }

// AnsweredCount is how many peers answered. Written as a method rather than left to the caller because
// "how many of the peers I asked actually spoke" is the number an operator wants, and counting it at the
// call site is how it comes out different in two places.
func AnsweredCount(rows []OntologyExchange) int {
	n := 0
	for _, r := range rows {
		if r.Answered {
			n++
		}
	}
	return n
}

// Summary is one line an operator or a log can read.
func Summary(rows []OntologyExchange) string {
	asked, answered := 0, 0
	var silent []string
	for _, r := range rows {
		if r.Asked {
			asked++
		}
		if r.Answered {
			answered++
			continue
		}
		silent = append(silent, fmt.Sprintf("%s (%s)", r.Peer.Name, r.Reason))
	}
	if len(rows) == 0 {
		return "no peers are registered, so nothing was asked"
	}
	if len(silent) == 0 {
		return fmt.Sprintf("asked %d peer(s), %d answered", asked, answered)
	}
	sort.Strings(silent)
	return fmt.Sprintf("asked %d peer(s), %d answered; no answer from: %s", asked, answered, strings.Join(silent, ", "))
}

// sameCrossing compares two decisions field by field, IN ORDER, and deliberately NOT on the Reason.
//
// The reason is PROSE ADDRESSED TO A READER, and both sides word it from their own end: "this instance
// declares core 1 only" is what the peer writes about itself and "the peer declares core 1 only" is what
// this instance writes about the same fact. First version compared the sentences, so every correct peer
// disagreed with a rule it had run perfectly -- a comparison that flags all correct implementations is
// measuring the wording.
//
// What is compared is the DECISION: which field, from which version, allowed or not. A difference in any
// of those is a real one; a difference in the sentence is a difference in whose desk the note is on.
func sameCrossing(a, b []OntologyFieldCrossing) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Since != b[i].Since || a[i].Allowed != b[i].Allowed {
			return false
		}
	}
	return true
}
