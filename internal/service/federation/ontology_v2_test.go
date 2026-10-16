package federation

import (
	"errors"
	"strings"
	"testing"
)

// Core v2 is the document that stops being only the implications: an edge gains a `category`. Nothing in
// this tree WRITES one yet -- the ontology tables hold v1-shaped {tag, implies} rows -- so what is tested
// here is the READER, which is what makes the version number mean anything: a v2 instance sending
// categories at a v1 instance, and a peer claiming v2 while sending v1 edges, have to be told which of the
// two they are doing, in both directions.

// A RECORDED DIGEST. The claim is not "the digest is stable" in the abstract; it is that ADDING a field to
// the canonical form did not change the bytes of a document written before it existed. Without `omitempty`
// on Category every digest this instance has ever computed would change the moment v2 was implemented, and
// two instances that agree would report a disagreement -- the false negative that sends an operator looking
// for a difference that is not there.
func TestTheCanonicalDigestOfAV1DocumentDidNotChangeWhenCategoryWasAdded(t *testing.T) {
	const recorded = "428b05a671d8dc251663952622f556ae8250b3c838e6b43158e9db780396663a"

	got, err := CanonicalDigest(`[{"tag":"a","implies":"b"},{"tag":"b","implies":"c"}]`)
	if err != nil {
		t.Fatalf("CanonicalDigest = %v", err)
	}
	if got != recorded {
		t.Fatalf("the v1 digest is %s, want the recorded %s: an edge with an empty category must marshal "+
			"exactly as it did before the field existed", got, recorded)
	}
}

// The v1 refusal, which is the phase's own clause: a document read at core 1 that carries a v2 field is
// refused BY NAME. json.Unmarshal into a struct drops what the struct does not name, so the alternative is
// the tag arriving in the wrong branch with nothing saying so.
func TestAV1DocumentCarryingACategoryIsRefusedByName(t *testing.T) {
	_, err := parseCanonicalAt(`[{"tag":"a","implies":"b","category":"physical"}]`, CoreOntologyV1)
	if !errors.Is(err, ErrFieldNotInVersion) {
		t.Fatalf("parsing a v2 field at core 1 = %v, want ErrFieldNotInVersion", err)
	}
	for _, want := range []string{"category", "core 2", "core 1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q, so a reader cannot act on it: %q", want, err.Error())
		}
	}
}

// The control, without which "refuse everything" would pass the test above: the same document read at core
// 2 parses, and the categories SURVIVE rather than being read and dropped.
func TestAV2DocumentParsesWithItsCategoriesAtCore2(t *testing.T) {
	doc := `[{"tag":"b","implies":"c","category":"identity"},{"tag":"a","implies":"b","category":"physical"}]`

	edges, err := parseCanonicalAt(doc, CoreOntologyV2)
	if err != nil {
		t.Fatalf("parsing a v2 document at core 2 = %v", err)
	}
	if len(edges) != 2 {
		t.Fatalf("parsed %d edges, want 2", len(edges))
	}
	// Sorted by the pair, so the categories must have travelled WITH their edges rather than in order.
	if edges[0].Tag != "a" || edges[0].Category != "physical" {
		t.Errorf("first edge = %+v, want a/implies b/physical", edges[0])
	}
	if edges[1].Tag != "b" || edges[1].Category != "identity" {
		t.Errorf("second edge = %+v, want b/implies c/identity", edges[1])
	}

	// And the same implications without categories are a DIFFERENT document, which is why the digest is
	// version-dependent: two instances holding different documents should not agree.
	v1Digest, _ := CanonicalDigestAt(doc, CoreOntologyV1)
	if v1Digest != "" {
		t.Fatal("a v2 document was accepted at core 1, so the version means nothing")
	}
	v2Digest, err := CanonicalDigestAt(doc, CoreOntologyV2)
	if err != nil || len(v2Digest) != 64 {
		t.Fatalf("digest at core 2 = (%q, %v)", v2Digest, err)
	}
	plain, _ := CanonicalDigest(`[{"tag":"a","implies":"b"},{"tag":"b","implies":"c"}]`)
	if v2Digest == plain {
		t.Error("a document with categories digests the same as one without: the categories would be " +
			"invisible to the only comparison a peer makes")
	}
}

// The other direction: a peer that DECLARES core 2 and sends v1 edges is claiming a version it does not
// speak. Accepting it would make v2 undetectable in exactly the case the version exists for.
func TestAV2DocumentWithoutCategoriesIsRefused(t *testing.T) {
	_, err := parseCanonicalAt(`[{"tag":"a","implies":"b"}]`, CoreOntologyV2)
	if !errors.Is(err, ErrFieldNotInVersion) {
		t.Fatalf("a v2 document with no categories = %v, want ErrFieldNotInVersion", err)
	}
	if !strings.Contains(err.Error(), "category") {
		t.Errorf("the refusal does not name the field: %q", err.Error())
	}
}

// An EMPTY category is a third fact, and the pointer in rawEdge is what tells it from an absent one: the
// writer had the field and left it blank. Both are refused, for different reasons, and both refusals name
// the edge so an operator can find it in their document.
func TestAnEmptyCategoryIsRefusedAsItsOwnCase(t *testing.T) {
	_, err := parseCanonicalAt(`[{"tag":"a","implies":"b","category":"  "}]`, CoreOntologyV2)
	if !errors.Is(err, ErrFieldNotInVersion) {
		t.Fatalf("an empty category = %v, want ErrFieldNotInVersion", err)
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("the refusal does not say the category is empty, which is a different fix from absent: %q",
			err.Error())
	}
}

// Two edges that differ only in their category are two answers to one question -- which category this
// implication belongs to -- and picking one of them would be a decision made by sort order.
func TestTwoCategoriesForOneImplicationIsRefused(t *testing.T) {
	doc := `[{"tag":"a","implies":"b","category":"physical"},{"tag":"a","implies":"b","category":"identity"}]`
	_, err := parseCanonicalAt(doc, CoreOntologyV2)
	if !errors.Is(err, ErrOntologyUnreadable) {
		t.Fatalf("two categories for one implication = %v, want ErrOntologyUnreadable", err)
	}
	if !strings.Contains(err.Error(), "one category") {
		t.Errorf("the refusal does not say why: %q", err.Error())
	}
}

// The vocabulary: v2 changes the EDGE, not the names, so a v2 peer resolves against the same list. Returning
// nil for v2 -- which this did -- reads as "nothing crosses", a claim about the mesh, rather than as "not
// implemented", a claim about this instance. Counted, because a list that came back empty would pass a
// loop-over-it assertion.
func TestCoreV2SpeaksTheSameVocabularyAsV1(t *testing.T) {
	v1, v2 := CoreTagVocabulary(CoreOntologyV1), CoreTagVocabulary(CoreOntologyV2)
	if len(v1) == 0 {
		t.Fatal("core v1 reports no vocabulary: the comparison below would prove nothing")
	}
	if len(v2) != len(v1) {
		t.Fatalf("core v2 lists %d names, core v1 lists %d", len(v2), len(v1))
	}
	for i := range v1 {
		if v1[i] != v2[i] {
			t.Fatalf("name %d: v1 has %q, v2 has %q", i, v1[i], v2[i])
		}
	}
	// A copy, not the backing array: a caller sorting what it was handed must not reorder this package's
	// own list for everything that reads it afterwards.
	v2[0] = "mutated"
	if CoreTagVocabulary(CoreOntologyV2)[0] == "mutated" {
		t.Error("CoreTagVocabulary hands out its own backing array")
	}
	if CoreTagVocabulary(99) != nil {
		t.Error("an unimplemented version reports a vocabulary")
	}
}
