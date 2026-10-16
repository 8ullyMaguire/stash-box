package models

// The federation ontology declaration (phase 12, slice 12a).
//
// These are HAND-WRITTEN rather than generated, like the rest of this repo's models, and they are
// deliberately scalars: an ontology declaration is a claim ABOUT a document, and the document itself
// never travels in one. See docs/plan/feature-content-aggregator.md, slice 12a.

// OntologyDeclaration is what an instance says about the ontology it speaks.
//
// It carries no tag names and no edges. A peer that could ask for the whole graph could learn this
// instance's taxonomy in one call, and the mesh's question protocol is one question at a time for
// exactly that reason; `digest` is what makes two instances comparable WITHOUT either of them
// handing the other its document.
type OntologyDeclaration struct {
	// CoreVersions are the versions of the shared core ontology this instance implements, ascending.
	// A set rather than a single number because an instance that implements core 2 can usually still
	// speak to one that stops at core 1, and the answer to that is a decision (see Compatibility)
	// rather than a version number.
	CoreVersions []int `json:"coreVersions"`
	// LocalVersion is the version of THIS instance's ontology document -- the row the trigger wrote
	// (migration 112). It is local: two instances at local version 7 hold two different ontologies,
	// which is why it is never compared across the boundary and why digest exists.
	LocalVersion int `json:"localVersion"`
	// Digest is the SHA-256, in hexadecimal, of the CANONICAL form of the implication document.
	// Two instances holding the same implicational ontology agree on it whatever order their rows
	// came back in; an instance that changed one edge does not. It is a digest and not the document
	// so that comparison is possible without disclosure.
	Digest string `json:"digest"`
	// NamedTags is how many distinct tag names the document mentions. It is NOT the number of tags
	// in the database: a tag with no implications is not in the document at all (the document is the
	// implication graph, migration 112's own COMMENT says so), and calling this `tagCount` would make
	// a reader compare it against `select count(*) from tags` and conclude the ontology was truncated.
	NamedTags int `json:"namedTags"`
	// Implications is how many edges the document holds.
	Implications int `json:"implications"`
}

// OntologyFieldDecision is one field of the ontology document, and whether it may cross to the peer
// under discussion.
type OntologyFieldDecision struct {
	// Name is the field's key in the document ("tag", "implies", "category").
	Name string `json:"name"`
	// Since is the core ontology version that introduced the field.
	Since int `json:"since"`
	// Allowed is whether BOTH sides implement Since. A field that only one side implements is not
	// sent, and it is not silently dropped either: Reason says why.
	Allowed bool `json:"allowed"`
	// Reason is the sentence an operator reads: which version the field needs, and which side lacks
	// it. "not supported" would leave the reader to find out which end to upgrade.
	Reason string `json:"reason"`
}

// OntologyCompatibility is what may be exchanged between this instance and a peer that declares the
// given core versions.
type OntologyCompatibility struct {
	// Shared is every core version both instances implement, ascending. Empty means the two cannot
	// speak at all, which is a refusal rather than a partial answer.
	Shared []int `json:"shared"`
	// Fields is every field of the ontology document, decided. The closed list rather than only the
	// refused ones: a caller that gets one refusal back cannot tell whether the other fields were
	// allowed or simply not asked about.
	Fields []OntologyFieldDecision `json:"fields"`
}
