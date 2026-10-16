package federation

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/stashapp/stash-box/internal/models"
)

// Slice 12b: the ANSWERING half.
//
// Until this file, this tree asked and could not be asked. `client.go` POSTs a Question to a peer's
// AnswerPath and nothing served that path, so "two instances exchange an ontology version" was a
// sentence about a protocol rather than about this instance. AnswerQuestion is the decision the
// endpoint makes, kept separate from the HTTP so it needs no server to test.
//
// WHAT THIS HALF ANSWERS, AND WHAT IT REFUSES. It answers the ontology claim -- what this instance
// speaks, as a digest and a version set -- and it REFUSES an identification question by name. That
// refusal is the design, not a gap: identification is the mesh's expensive half (it needs the local
// candidate store, a suggester weighting and F2's evidence rules), and answering it with an empty
// candidate list would be a peer's honest "I have nothing" -- indistinguishable from "I have not
// built this", which is the confusion R074's protocol version exists to prevent. A named refusal can
// be fixed by an operator; an empty answer cannot.

var (
	// ErrNoInstanceID is returned when this instance has no id to answer with.
	//
	// It is a refusal rather than a fallback to the hostname because the peer registry is keyed by
	// instance id: an answer that attributes itself to a guess puts one instance's answers under
	// another's row, and the damage is silent. The message names the config key, so the fix is in the
	// sentence.
	ErrNoInstanceID = errors.New("this instance has no instance_id, so it cannot answer a peer")

	// ErrIdentifyUnserved is returned for an identification question, which this instance does not
	// answer yet.
	ErrIdentifyUnserved = errors.New("the answering half of the identification protocol is not built")

	// ErrClaimRejected is returned when a peer's own claim is declared but malformed.
	ErrClaimRejected = errors.New("the asking peer's ontology claim is not usable")
)

// AnswerQuestion builds the answer to a peer's question.
//
// `selfInstanceID` is this instance's own name (config's instance_id) and is passed in rather than read
// from a package here, so the refusal for a missing one is testable without touching global state.
func AnswerQuestion(
	ctx context.Context, src OntologySource, selfInstanceID string, q Question,
) (Answer, error) {
	// WHO AM I. Checked before anything else, because every answer carries it and a peer cannot
	// attribute an anonymous answer.
	if strings.TrimSpace(selfInstanceID) == "" {
		return Answer{}, ErrNoInstanceID
	}

	// WHAT WAS ASKED. A question carrying identification content is refused by name: see the file
	// header for why an empty candidate list would be worse than a refusal.
	if q.TargetType != "" || q.Description != "" || len(q.CandidateNames) > 0 {
		return Answer{}, fmt.Errorf("%w (target type %q)", ErrIdentifyUnserved, q.TargetType)
	}

	// THE ASKER'S CLAIM, if it declared one. Validated here rather than trusted, because a peer is a
	// different implementation: a claim that is half-written (a digest that is not a digest, a version
	// set containing 0) compares equal to nothing and would read as "the peer speaks a different
	// ontology" rather than "the peer is confused". An EMPTY claim is legal and means exactly that --
	// "no version declared" -- which Compatibility already renders.
	if q.Ontology.Declared() {
		decl := declarationFrom(q.Ontology)
		if err := ValidateDeclaration(decl); err != nil {
			return Answer{}, fmt.Errorf("%w: %v", ErrClaimRejected, err)
		}
	}

	// OUR CLAIM, read per call: an instance that changed its ontology a minute ago must not answer with
	// the ontology it booted with.
	local, err := LocalDeclaration(ctx, src)
	if err != nil {
		return Answer{}, err
	}

	return Answer{
		PeerInstanceID: selfInstanceID,
		Ontology:       claimFrom(local),
		// Candidates stays nil, and that is the whole of F2 on this path: this instance invents no
		// evidence. The field will be filled by the identification slice, from the local store, under
		// the same rules a local suggestion takes.
		Candidates: nil,
	}, nil
}

// declarationFrom reads a wire claim as the model the validator takes. Empty fields are preserved:
// validation is what decides whether they are acceptable, and a conversion that defaulted them would
// hide the very input the check exists for.
func declarationFrom(c OntologyClaim) models.OntologyDeclaration {
	return models.OntologyDeclaration{
		CoreVersions: c.CoreVersions,
		LocalVersion: c.LocalVersion,
		Digest:       c.Digest,
		NamedTags:    c.NamedTags,
		Implications: c.Implications,
	}
}

// claimFrom is the other direction, for the answer we send.
func claimFrom(d models.OntologyDeclaration) OntologyClaim {
	return OntologyClaim{
		CoreVersions: d.CoreVersions,
		LocalVersion: d.LocalVersion,
		Digest:       d.Digest,
		NamedTags:    d.NamedTags,
		Implications: d.Implications,
	}
}

// ClaimOf is the exported reading of a local declaration, for the client that puts its own claim on a
// question it asks. Exported so the outbound and inbound halves describe themselves with one function
// rather than two that can drift.
func ClaimOf(d models.OntologyDeclaration) OntologyClaim { return claimFrom(d) }
