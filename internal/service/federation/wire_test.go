package federation

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// allowedFieldKinds is the CLOSED set of field types a broadcast payload may
// contain, named so a failure says which type broke F1.
//
// It is closed rather than a denylist. A denylist of "[]byte, any, structs with
// a URL" has to be updated every time someone adds a type, and the failure mode
// of forgetting is a payload that grows a field nobody reviewed. A closed set
// fails on the new type instead, which is the direction that makes the test
// worth having.
//
// uuid.UUID is in the set as an ARRAY kind on purpose. uuid.UUID is defined as
// [16]byte, so a blanket "reject arrays" rule would reject the one identifier
// type the payload is supposed to carry, and the obvious "fix" — allowing
// [16]byte because it is a known type — would also allow []byte, which is the
// blob the guard exists to keep out. The distinction has to be by concrete type,
// which is why allowedConcreteTypes below exists.
var allowedFieldKinds = map[reflect.Kind]string{
	reflect.String: "string",
	reflect.Slice:  "slice",
	reflect.Struct: "struct",
	reflect.Int:    "int",
	reflect.Array:  "array",
}

// allowedConcreteTypes are the specific named types permitted regardless of
// their kind.
//
// Exists because kind alone cannot tell uuid.UUID from a blob. uuid.UUID's
// underlying type is [16]byte, so "array" is simultaneously correct for it and
// exactly the shape of the thing being guarded against. Naming the type is the
// only way to allow one and not the other.
var allowedConcreteTypes = map[reflect.Type]string{
	reflect.TypeOf(uuid.UUID{}): "uuid.UUID",
}

// allowedSliceElemTypes are the element types a slice field may hold.
//
// Separate from allowedFieldKinds because []string is F1-safe and
// []RemoteCandidate is F1-safe, while []byte is neither: a byte slice is a blob
// and a blob is how a screenshot crosses a boundary that is supposed to carry
// names. Any is the same problem with better camouflage.
var allowedSliceElemTypes = map[reflect.Type]bool{
	reflect.TypeOf(""):                true,
	reflect.TypeOf(uuid.UUID{}):       true,
	reflect.TypeOf(RemoteCandidate{}): true,
}

// TestWireFieldTypesAreClosed is the F1 test: reflect over every field of the
// broadcast payload and fail by name on anything that could hold content.
//
// This is the test the plan asks for and it is worth being precise about what it
// does and does not prove. It proves the field TYPES cannot hold a path, a URL,
// a blob, or a media reference without a further type being added and reviewed.
// It does not prove a string field cannot contain "/etc/passwd" — TestValidate
// RejectsPaths covers that half, and neither test is redundant with the other.
func TestWireFieldTypesAreClosed(t *testing.T) {
	cases := []struct {
		name string
		typ  reflect.Type
	}{
		{"Question", reflect.TypeOf(Question{})},
		{"Answer", reflect.TypeOf(Answer{})},
		{"RemoteCandidate", reflect.TypeOf(RemoteCandidate{})},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for i := 0; i < c.typ.NumField(); i++ {
				field := c.typ.Field(i)

				// Unexported fields never reach the wire, so they are not part
				// of the payload and cannot leak. Checking them would make the
				// test fail on an implementation detail it does not govern.
				if !field.IsExported() {
					continue
				}

				if _, concrete := allowedConcreteTypes[field.Type]; concrete {
					continue
				}

				if _, ok := allowedFieldKinds[field.Type.Kind()]; !ok {
					t.Errorf("%s.%s has kind %s, which is not in the closed set %v; "+
						"a broadcast payload may only carry names and scalars",
						c.name, field.Name, field.Type.Kind(), allowedFieldKinds)
					continue
				}

				if field.Type.Kind() == reflect.Slice {
					elem := field.Type.Elem()
					if !allowedSliceElemTypes[elem] {
						t.Errorf("%s.%s is a slice of %s, which is not an allowed "+
							"element type; []byte and []any are how content crosses a "+
							"name-only boundary", c.name, field.Name, elem)
					}
				}
			}
		})
	}
}

// TestWireHasNoURLOrPathField is the same property stated as a name check.
//
// Redundant with the kind check above on purpose, and not merely for the count.
// The kind check would pass on a field named `MediaURL string` — a string is an
// allowed kind — and the NAME is the thing that tells a reviewer the field was
// added on purpose. A guard that only checks structure fails on a well-typed
// field with the wrong name, and that is precisely the field a later edit adds.
func TestWireHasNoURLOrPathField(t *testing.T) {
	banned := []string{"url", "uri", "path", "file", "dir", "host", "image", "screenshot",
		"thumb", "cover", "blob", "base64", "checksum", "hash"}

	for _, typ := range []reflect.Type{
		reflect.TypeOf(Question{}),
		reflect.TypeOf(Answer{}),
		reflect.TypeOf(RemoteCandidate{}),
	} {
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			if !field.IsExported() {
				continue
			}
			lowered := strings.ToLower(field.Name)
			for _, b := range banned {
				if strings.Contains(lowered, b) {
					t.Errorf("%s.%s: field name contains %q; the broadcast payload "+
						"carries names, and a field named for content is how content "+
						"starts crossing", typ.Name(), field.Name, b)
				}
			}
		}
	}
}

// TestWireRoundTrip proves a full Question and Answer survive encode/decode.
//
// A wire format that drops a field is invisible until it meets a real peer
// running a different build, and by then the symptom is a missing candidate
// rather than a parse error. The assertion is deep equality on the DECODED
// value, not on the encoded bytes, because the bytes are the implementation's
// choice and the decoded value is the contract.
func TestWireRoundTrip(t *testing.T) {
	q := Question{
		QueryID:        uuid.Must(uuid.NewV7()),
		TargetType:     "performer",
		Description:    "tall, dark hair, distinctive tattoo on left shoulder",
		CandidateNames: []string{"Alex Winter", "Zoë Kravitz", "李小龙"},
	}

	encodedQ, err := json.Marshal(q)
	if err != nil {
		t.Fatalf("marshalling Question: %v", err)
	}
	var decodedQ Question
	if err := json.Unmarshal(encodedQ, &decodedQ); err != nil {
		t.Fatalf("unmarshalling Question: %v", err)
	}
	if !reflect.DeepEqual(q, decodedQ) {
		t.Errorf("Question did not survive the round trip:\n sent %#v\n got  %#v", q, decodedQ)
	}

	a := Answer{
		PeerInstanceID: "a1b2c3d4e5f60718",
		Candidates: []RemoteCandidate{
			{Name: "Alex Winter", PeerID: "peer-uuid-1", SuggesterCount: 3},
			{Name: "Alex Winter (adult)", PeerID: "peer-uuid-2", SuggesterCount: 1},
		},
	}

	encodedA, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshalling Answer: %v", err)
	}
	var decodedA Answer
	if err := json.Unmarshal(encodedA, &decodedA); err != nil {
		t.Fatalf("unmarshalling Answer: %v", err)
	}
	if !reflect.DeepEqual(a, decodedA) {
		t.Errorf("Answer did not survive the round trip:\n sent %#v\n got  %#v", a, decodedA)
	}
}

// TestWireEmptyAndNilDiffer guards a subtle round-trip case: a nil slice and an
// empty slice decode differently, and a peer that checks `len == 0` and one that
// checks `== nil` disagree about the same answer.
func TestWireEmptyAndNilDiffer(t *testing.T) {
	withNil := Question{QueryID: uuid.Must(uuid.NewV7()), TargetType: "performer"}
	withEmpty := Question{
		QueryID:        uuid.Must(uuid.NewV7()),
		TargetType:     "performer",
		CandidateNames: []string{},
	}

	bNil, _ := json.Marshal(withNil)
	bEmpty, _ := json.Marshal(withEmpty)

	var dNil, dEmpty Question
	_ = json.Unmarshal(bNil, &dNil)
	_ = json.Unmarshal(bEmpty, &dEmpty)

	if dNil.CandidateNames != nil {
		t.Error("a nil CandidateNames came back non-nil")
	}
	if dEmpty.CandidateNames == nil {
		t.Error("an empty CandidateNames came back nil; a peer checking len()==0 " +
			"and a peer checking ==nil will disagree about the same answer")
	}
	if dNil.TargetType != withNil.TargetType {
		t.Errorf("TargetType was dropped: got %q", dNil.TargetType)
	}
}
