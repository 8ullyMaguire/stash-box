package federation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/stashapp/stash-box/internal/models"
)

// Slice 12a: the ontology crosses the boundary, and a version a peer cannot speak is REFUSED BY NAME
// rather than silently mis-mapped.
//
// The phase's verification clause names three things, and this file is the half of them that is pure:
// "two instances exchange an ontology version and resolve a local tag against a core tag; ... an
// instance that declares core v1 is refused a v2-only field rather than silently mis-mapping."
//
// WHAT IS HERE AND WHAT IS NOT. The declaration, the canonical digest and the compatibility rule are
// here, with no database and no network: they are decisions, and a decision can be tested without a
// second instance. The TRANSPORT IS NOT HERE, and saying so is part of the slice -- this tree has no
// inbound endpoint at all. `client.go` asks peers at `answerPath` (/federation/identify) and nothing
// in internal/api serves that path, so today this instance asks and cannot be asked. Building the
// serving half is the next slice; it is not a detail this one can pretend to have covered.
//
// WHY A DIGEST RATHER THAN THE DOCUMENT. The question a peer needs answered is "is your ontology the
// same as mine", and the document that answers it is the whole taxonomy. Sending it to every peer
// would make an instance's complete tag graph readable by anyone who can reach the answer endpoint,
// which is a disclosure the mesh's one-question-at-a-time protocol exists to avoid. A SHA-256 of the
// canonical form answers "same or not" and nothing else, and it is enough to decide whether a
// resolution is worth attempting.
//
// WHY THE LOCAL VERSION IS NOT COMPARED. `ontology_versions.version` is a LOCAL counter: it is one
// past the highest the trigger has written, so two instances that share one ontology and edited it in
// different orders reach the same graph at different numbers. Comparing those numbers would say "we
// disagree" about two instances that agree -- precisely the silent mis-mapping this slice is against.

// The core ontology versions. Numbered rather than named, because the number is what travels: a
// peer's declaration has to be decidable by a different implementation, and a string like "stable"
// means whatever the instance that sent it thinks it means.
const (
	// CoreOntologyV1 is the implication graph as migration 111 and 112 store it: a list of
	// {tag, implies} pairs.
	CoreOntologyV1 = 1
	// CoreOntologyV2 is v1 plus category membership on an edge. NOT BUILT -- and named here rather
	// than left to the future because a version this instance knows the SHAPE of is a version it can
	// refuse field by field. The alternative is a v2 instance receiving an edge whose category is
	// dropped on the floor, which is what "silently mis-mapping" looks like in practice: the tag
	// arrives, in the wrong branch, with nothing saying so.
	//
	// The shape is not invented. Migration 112's own COMMENT on `snapshot` states the limit
	// verbatim: "this is the implication graph only -- tag renames and category changes are not in
	// it." Core v1 is that document. v2 is the document that stops being only the implications.
	CoreOntologyV2 = 2
)

// ErrOntologyUnreadable is returned when a document cannot be read as an ontology at all. It is a
// sentinel so a caller can tell "this peer holds a different ontology" from "this peer sent something
// that is not a document".
var ErrOntologyUnreadable = errors.New("the ontology document is unreadable")

// ErrFieldNotInVersion is returned when a document carries a field the version it is being read at does
// not have, or claims a version whose fields it does not carry.
//
// THIS IS THE WHOLE OF CORE V2's VALUE, and the reason the error is a sentinel rather than a string. The
// phase's own clause is "an instance that declares core v1 is refused a v2-only field rather than
// silently mis-mapping", and the alternative -- json.Unmarshal into a struct, which drops what the struct
// does not name -- is precisely the silent version: the tag arrives, in the wrong branch, with nothing
// saying so. A refusal that a caller can TEST for (errors.Is) rather than read for is what lets the
// answering half of the protocol tell "your document is older than the field you sent me" from "your
// document is not readable at all".
var ErrFieldNotInVersion = errors.New("the document carries a field this core version does not have")

// ontologyField is one key of the document, with the core version that introduced it.
type ontologyField struct {
	name  string
	since int
}

// ontologyFields is the CLOSED list of fields an ontology document can carry.
//
// Closed for the reason every closed list in this programme is closed: a decision table that matches
// anything decides nothing, and a field added to the document by a later version has to be added HERE
// to be decided at all. A field that is not in this list is not sent by this instance under any
// version -- `Compatibility` can only allow what it names, and `Question`'s own whitespace-free
// validation is the second gate on the way out.
var ontologyFields = []ontologyField{
	{name: "tag", since: CoreOntologyV1},
	{name: "implies", since: CoreOntologyV1},
	{name: "category", since: CoreOntologyV2},
}

// supportedCore is every core version this instance implements, ascending. The newest entry is what a
// peer reads as "what this instance is"; the others are what it can still speak.
var supportedCore = []int{CoreOntologyV1, CoreOntologyV2}

// canonicalEdge is one implication in the form the digest is computed over: two strings, in a fixed
// order (tag then implies), with a fixed spelling. `omitempty` is deliberately absent -- an edge with
// an empty field is REFUSED below rather than normalised into a shorter object, because two different
// documents that marshal to the same bytes would otherwise digest the same.
type canonicalEdge struct {
	Tag     string `json:"tag"`
	Implies string `json:"implies"`

	// Category is core v2's addition: the category membership of the edge, i.e. the thing migration 112's
	// own comment says v1's document leaves out ("this is the implication graph only -- tag renames and
	// category changes are not in it").
	//
	// `omitempty` IS LOAD-BEARING AND IS NOT TIDINESS. A v1 document has no categories, so an edge that
	// carries an empty one has to marshal to the same bytes it did before this field existed -- otherwise
	// every digest this instance has ever computed would change when a field was ADDED, which would make
	// two instances that agree report a disagreement. There is a test with a recorded SHA-256 for exactly
	// that. The empty case never arises for a legitimate v2 document, because parsing one requires the
	// category to be PRESENT and NON-EMPTY (see parseCanonicalAt): a v2 document whose edges omit it is a
	// peer claiming a version it does not speak, and that is refused rather than normalised.
	Category string `json:"category,omitempty"`
}

// OntologySource is the narrowest thing LocalDeclaration needs: the version in force and the document
// at a version. An interface rather than the concrete tagontology service, so the rule's tests need no
// database -- and so that a second implementation (a peer's document, an imported one) can be read
// through the same path.
type OntologySource interface {
	CurrentVersion(ctx context.Context) (int, error)
	Version(ctx context.Context, version int) (*models.OntologyVersion, error)
}

// LocalDeclaration reads this instance's ontology and describes it.
//
// BUILT PER CALL, never cached. The version moves whenever anybody edits a tag implication, and a
// declaration cached at startup would report the ontology the process booted with forever -- a digest
// that agrees with a peer for as long as the peer caches nothing, which is worse than a wrong answer
// because it is a wrong answer that looks live.
func LocalDeclaration(ctx context.Context, src OntologySource) (models.OntologyDeclaration, error) {
	version, err := src.CurrentVersion(ctx)
	if err != nil {
		return models.OntologyDeclaration{}, fmt.Errorf("reading the ontology version: %w", err)
	}

	doc, err := src.Version(ctx, version)
	if err != nil {
		return models.OntologyDeclaration{}, fmt.Errorf("reading ontology version %d: %w", version, err)
	}
	if doc == nil {
		return models.OntologyDeclaration{}, fmt.Errorf("%w: version %d has no document", ErrOntologyUnreadable, version)
	}

	edges, err := parseCanonical(doc.Snapshot)
	if err != nil {
		return models.OntologyDeclaration{}, err
	}

	return models.OntologyDeclaration{
		CoreVersions: append([]int(nil), supportedCore...),
		LocalVersion: version,
		Digest:       digestOf(edges),
		NamedTags:    namedTagCount(edges),
		Implications: len(edges),
	}, nil
}

// CanonicalDigest is the digest of a document read as text, for a caller that holds the document
// rather than a database. It is the same function LocalDeclaration uses, so a peer can compute its
// own digest and the two are comparable.
func CanonicalDigest(snapshot string) (string, error) {
	return CanonicalDigestAt(snapshot, CoreOntologyV1)
}

// CanonicalDigestAt is the same digest over a document read at a chosen version.
//
// VERSION-DEPENDENT, and that is correct rather than a side effect: a v2 document carrying categories IS
// a different document from one carrying the same implications without them, and two instances that hold
// different documents should not agree. What must NOT happen is the digest changing for a document that
// did not change, which is why the field is `omitempty` and why there is a recorded digest in the tests.
func CanonicalDigestAt(snapshot string, version int) (string, error) {
	edges, err := parseCanonicalAt(snapshot, version)
	if err != nil {
		return "", err
	}
	return digestOf(edges), nil
}

// parseCanonical reads a document and returns it in canonical order.
//
// CANONICAL, and that is the whole point of the function: the trigger writes the snapshot ORDER BY
// tag, implies (migration 112), so a document read straight out of any one database is already
// sorted. The digest must not depend on that luck. A document that arrived through an export, a
// hand-written seed, or a different implementation's marshaller has the same edges in another order,
// and two instances holding the SAME ontology would then disagree -- the false negative that would
// send an operator looking for a difference that is not there.
func parseCanonical(snapshot string) ([]canonicalEdge, error) {
	return parseCanonicalAt(snapshot, CoreOntologyV1)
}

// rawEdge is an edge as it arrived, with the category as a POINTER so that "absent" and "empty" are
// different facts. That distinction is the whole of the version rule below: absent means the document was
// written before core v2, empty means the writer had the field and left it blank, and a struct with a
// plain string cannot tell them apart.
type rawEdge struct {
	Tag      string  `json:"tag"`
	Implies  string  `json:"implies"`
	Category *string `json:"category"`
}

// parseCanonicalAt reads a document as an ontology AT A VERSION, and refuses what that version does not
// have.
//
// CORE V2 IS A READER'S PROBLEM BEFORE IT IS A WRITER'S. Nothing in this tree writes a category yet --
// the ontology tables hold v1-shaped {tag, implies} rows, and the writer is named as outstanding rather
// than assumed. What v2 needs first is the ability to READ a document that has one, which is what makes
// the version number mean anything: a v2 instance sending categories to a v1 instance has to produce a
// refusal naming the FIELD, in both directions, because the two failures look identical from outside --
// a document that arrives with part of itself dropped on the floor.
//
// The rules, each with the failure it prevents:
//
//   - at core 1, an edge carrying `category` is REFUSED, naming the field and the version that introduced
//     it. The alternative is json.Unmarshal quietly dropping it, which is the "silently mis-mapping" the
//     phase's clause names: the tag arrives, in the wrong branch, with nothing saying so;
//   - at core 2, an edge that does NOT carry a non-empty category is refused as well. A peer that declares
//     v2 and sends v1 edges is claiming a version it does not speak, and accepting it would make v2
//     undetectable in exactly the case the version exists for;
//   - the same {tag, implies} appearing twice is refused, because two edges that differ only in their
//     category are two answers to one question -- which category this implication belongs to -- and
//     picking one of them would be a decision made by sort order.
func parseCanonicalAt(snapshot string, version int) ([]canonicalEdge, error) {
	// `null` and `[]` are different bytes and the same ontology: an instance whose graph is empty
	// must not digest differently from one whose graph was never written. Both become the empty list.
	if strings.TrimSpace(snapshot) == "" || strings.TrimSpace(snapshot) == "null" {
		return []canonicalEdge{}, nil
	}

	var raw []rawEdge
	if err := json.Unmarshal([]byte(snapshot), &raw); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOntologyUnreadable, err)
	}

	edges := make([]canonicalEdge, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for i, r := range raw {
		// An edge missing one of its two names is refused, not kept as an empty string. It would digest
		// as `{"tag":"a","implies":""}` -- a value no honest document produces -- and two peers would
		// then agree on a digest that describes an edge nobody can resolve.
		if strings.TrimSpace(r.Tag) == "" || strings.TrimSpace(r.Implies) == "" {
			return nil, fmt.Errorf("%w: edge %d has no tag or no implied tag", ErrOntologyUnreadable, i)
		}

		category := ""
		switch {
		case r.Category != nil && version < CoreOntologyV2:
			return nil, fmt.Errorf("%w: edge %d (%s implies %s) carries `category`, which was introduced "+
				"in core %d; this document is being read at core %d", ErrFieldNotInVersion, i, r.Tag,
				r.Implies, CoreOntologyV2, version)
		case r.Category != nil:
			category = strings.TrimSpace(*r.Category)
			if category == "" {
				return nil, fmt.Errorf("%w: edge %d (%s implies %s) declares core %d and carries an empty "+
					"`category`", ErrFieldNotInVersion, i, r.Tag, r.Implies, version)
			}
		case version >= CoreOntologyV2:
			return nil, fmt.Errorf("%w: edge %d (%s implies %s) carries no `category`, but the document is "+
				"being read at core %d, where every edge has one", ErrFieldNotInVersion, i, r.Tag,
				r.Implies, version)
		}

		pair := r.Tag + "\x00" + r.Implies
		if _, dup := seen[pair]; dup {
			return nil, fmt.Errorf("%w: two edges for %s implies %s, which may only belong to one category",
				ErrOntologyUnreadable, r.Tag, r.Implies)
		}
		seen[pair] = struct{}{}

		edges = append(edges, canonicalEdge{Tag: r.Tag, Implies: r.Implies, Category: category})
	}

	sort.Slice(edges, func(i, j int) bool {
		if edges[i].Tag != edges[j].Tag {
			return edges[i].Tag < edges[j].Tag
		}
		return edges[i].Implies < edges[j].Implies
	})

	return edges, nil
}

// digestOf hashes a canonical edge list. The list is marshalled by THIS function rather than by the
// caller, so the bytes hashed are the ones this file defines -- a digest computed over a caller's
// own marshalling would change with a struct tag and quietly invalidate every stored comparison.
func digestOf(edges []canonicalEdge) string {
	// json.Marshal of a struct cannot fail, and of a []canonicalEdge cannot fail either; the error is
	// dropped rather than returned because a "cannot happen" error path here would be an untested
	// branch a reader has to check. If it ever did fail, the panic names this line.
	body, err := json.Marshal(edges)
	if err != nil {
		panic(fmt.Sprintf("marshalling canonical edges: %v", err))
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// namedTagCount is how many distinct tag names the document mentions, on either side of an edge.
func namedTagCount(edges []canonicalEdge) int {
	seen := make(map[string]struct{}, len(edges)*2)
	for _, e := range edges {
		seen[e.Tag] = struct{}{}
		seen[e.Implies] = struct{}{}
	}
	return len(seen)
}

// SupportsCore reports whether this instance implements a core version.
func SupportsCore(version int) bool {
	for _, v := range supportedCore {
		if v == version {
			return true
		}
	}
	return false
}

// Compatibility decides what may cross between an instance declaring `ours` and a peer declaring
// `theirs`.
//
// BOTH SETS ARE PARAMETERS, not "ours" plus a constant. The first version of this function read this
// instance's versions out of the package variable and took only the peer's, which made the
// "this instance is the one that is behind" arm unreachable -- a branch that cannot be reached in a
// test is a branch nobody has read, and the message it writes ("this instance declares core 1 only")
// is exactly the one an operator needs when the instance, not the peer, is the thing to upgrade.
//
// The rule is one line -- a field may cross when BOTH sides implement the version that introduced it
// -- and the reason each refusal carries is the part worth writing: an operator reading "category is
// not supported" has to find out which end to change, while "introduced in core 2; the peer declares
// core 1 only" is actionable.
//
// The empty intersection is NOT a special case that returns an error. It returns a compatibility with
// no shared versions and every field refused, because "we cannot speak" is a fact about the pair that
// the caller may want to report per peer rather than fail the whole broadcast with -- the same
// judgement client.go's Broadcast already makes about unreachable peers.
func Compatibility(ours, theirs []int) models.OntologyCompatibility {
	shared := make([]int, 0, len(ours))
	for _, v := range ours {
		if supportsVersion(theirs, v) {
			shared = append(shared, v)
		}
	}
	sort.Ints(shared)

	fields := make([]models.OntologyFieldDecision, 0, len(ontologyFields))
	for _, f := range ontologyFields {
		mine, peer := supportsVersion(ours, f.since), supportsVersion(theirs, f.since)
		fields = append(fields, models.OntologyFieldDecision{
			Name:    f.name,
			Since:   f.since,
			Allowed: mine && peer,
			Reason:  fieldReason(f, ours, theirs, mine, peer),
		})
	}

	return models.OntologyCompatibility{Shared: shared, Fields: fields}
}

// fieldReason is the sentence a person reads, and it names the side that has to move.
func fieldReason(f ontologyField, ours, theirs []int, mine, peer bool) string {
	switch {
	case mine && peer:
		return fmt.Sprintf("carried by core %d, which both instances implement", f.since)
	case mine && !peer:
		return fmt.Sprintf("introduced in core %d; the peer declares core %s only",
			f.since, versionList(theirs))
	case !mine && peer:
		return fmt.Sprintf("introduced in core %d; this instance declares core %s only",
			f.since, versionList(ours))
	default:
		return fmt.Sprintf("introduced in core %d, which neither instance implements", f.since)
	}
}

// versionList spells a version set for a message. Ascending and comma-separated, and the empty set
// reads as a word rather than as nothing: a refusal caused by a peer that declared NO versions should
// say that, not "the peer declares core only".
func versionList(versions []int) string {
	if len(versions) == 0 {
		return "no version"
	}
	sorted := append([]int(nil), versions...)
	sort.Ints(sorted)
	parts := make([]string, 0, len(sorted))
	for _, v := range sorted {
		parts = append(parts, fmt.Sprintf("%d", v))
	}
	return strings.Join(parts, ", ")
}

// ValidateDeclaration checks a declaration that arrived from a peer.
//
// A peer is a different implementation, so every field of its answer is input rather than fact. The
// checks are the ones whose absence would be SILENT: a version set containing 0 would make the
// compatibility walk agree with itself about a version that does not exist, and a digest that is not
// hexadecimal would be compared as a string and never match anything -- reading as "a different
// ontology" when the truth is "a broken sender".
func ValidateDeclaration(d models.OntologyDeclaration) error {
	if len(d.CoreVersions) == 0 {
		return errors.New("the declaration names no core ontology version")
	}
	for _, v := range d.CoreVersions {
		if v < 1 {
			return fmt.Errorf("core ontology version %d does not exist", v)
		}
	}
	if len(d.Digest) != 64 {
		return fmt.Errorf("the digest is %d characters, not a sha256", len(d.Digest))
	}
	if _, err := hex.DecodeString(d.Digest); err != nil {
		return fmt.Errorf("the digest is not hexadecimal: %v", err)
	}
	if d.LocalVersion < 0 {
		return fmt.Errorf("local version %d is negative", d.LocalVersion)
	}
	if d.NamedTags < 0 || d.Implications < 0 {
		return errors.New("the declaration counts a negative number of tags or implications")
	}
	return nil
}

func supportsVersion(versions []int, v int) bool {
	for _, c := range versions {
		if c == v {
			return true
		}
	}
	return false
}
