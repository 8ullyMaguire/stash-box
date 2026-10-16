package federation

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/models"
)

// fakeOntology is an OntologySource with no database: a version number and the documents it can
// serve. The mutable field is the point -- a test that changes the document between two calls is the
// only way to prove the declaration is read per call rather than cached at startup.
type fakeOntology struct {
	version   int
	documents map[int]string
	err       error
}

func (f *fakeOntology) CurrentVersion(context.Context) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	return f.version, nil
}

func (f *fakeOntology) Version(_ context.Context, version int) (*models.OntologyVersion, error) {
	if f.err != nil {
		return nil, f.err
	}
	doc, ok := f.documents[version]
	if !ok {
		return nil, nil
	}
	return &models.OntologyVersion{Version: version, Snapshot: doc}, nil
}

// The two documents below are the same ontology in two orders, which is the pair the digest rule is
// about. Written out rather than generated so that the test is readable as "same edges, other order".
const (
	docSorted   = `[{"tag":"cowgirl_position","implies":"woman_on_top"},{"tag":"woman_on_top","implies":"vaginal_sex"}]`
	docShuffled = `[
		{"tag":"woman_on_top","implies":"vaginal_sex"},
		{"tag":"cowgirl_position","implies":"woman_on_top"}
	]`
	docOneEdge = `[{"tag":"cowgirl_position","implies":"woman_on_top"}]`
)

func TestCanonicalDigestIgnoresTheOrderOfTheDocument(t *testing.T) {
	// FIXTURE GUARD, and it is the whole test: if the two documents were byte-identical the digest
	// would agree for a reason that has nothing to do with canonicalisation.
	require.NotEqual(t, docSorted, docShuffled, "fixture is wrong: the two documents must differ as text")

	sorted, err := CanonicalDigest(docSorted)
	require.NoError(t, err)
	shuffled, err := CanonicalDigest(docShuffled)
	require.NoError(t, err)

	assert.Equal(t, sorted, shuffled,
		"two instances holding the SAME ontology must agree whatever order their rows came back in")
	assert.Len(t, sorted, 64, "a sha256 in hexadecimal, which is what ValidateDeclaration checks for")
}

func TestCanonicalDigestChangesWhenTheOntologyDoes(t *testing.T) {
	base, err := CanonicalDigest(docOneEdge)
	require.NoError(t, err)
	twoEdges, err := CanonicalDigest(docSorted)
	require.NoError(t, err)
	assert.NotEqual(t, base, twoEdges, "an added implication is a different ontology")

	// The same edge with the relation pointing the other way. A digest that normalised the pair as an
	// unordered set would call these equal, and they are opposites: one says cowgirl_position implies
	// woman_on_top, the other says the reverse.
	reversed, err := CanonicalDigest(`[{"tag":"woman_on_top","implies":"cowgirl_position"}]`)
	require.NoError(t, err)
	assert.NotEqual(t, base, reversed, "the direction of an implication is part of the document")
}

func TestCanonicalDigestTreatsAnUnwrittenDocumentAsEmpty(t *testing.T) {
	empty, err := CanonicalDigest(`[]`)
	require.NoError(t, err)

	// Both of these are "no implications" written by different hands: a column default that produced
	// `null`, and a caller that marshalled a nil slice into nothing at all.
	for _, spelling := range []string{`null`, ``, `   `} {
		got, err := CanonicalDigest(spelling)
		require.NoError(t, err, "spelling %q", spelling)
		assert.Equal(t, empty, got, "spelling %q is the empty ontology", spelling)
	}
}

func TestCanonicalDigestRefusesARubbishDocumentByName(t *testing.T) {
	// The positive control comes FIRST: without it, a function that errored on everything would pass
	// every case below and read as a careful parser.
	good, err := CanonicalDigest(docOneEdge)
	require.NoError(t, err, "control: a well-formed document must be readable")
	assert.NotEmpty(t, good)

	for _, tc := range []struct{ name, doc string }{
		{"not json at all", `the ontology, probably`},
		{"an object where the document is a list", `{"tag":"a","implies":"b"}`},
		{"a number", `7`},
		{"an edge with no implied tag", `[{"tag":"cowgirl_position"}]`},
		{"an edge with no tag", `[{"implies":"woman_on_top"}]`},
		{"an edge whose names are blank", `[{"tag":"   ","implies":"woman_on_top"}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := CanonicalDigest(tc.doc)
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrOntologyUnreadable,
				"an unreadable document is a refusal, not the digest of nothing: an empty digest "+
					"would compare equal to another empty one and read as agreement")
		})
	}
}

func TestLocalDeclarationIsReadPerCallNotCached(t *testing.T) {
	src := &fakeOntology{
		version:   3,
		documents: map[int]string{3: docOneEdge, 4: docSorted},
	}

	first, err := LocalDeclaration(context.Background(), src)
	require.NoError(t, err)
	assert.Equal(t, 3, first.LocalVersion)
	assert.Equal(t, 1, first.Implications)
	// A tag with no implications is not in the document, and `namedTags` says so rather than claiming
	// to be a count of the tags table.
	assert.Equal(t, 2, first.NamedTags)
	assert.Equal(t, []int{CoreOntologyV1, CoreOntologyV2}, first.CoreVersions)

	// The ontology moves -- which in production is a tag edit's applier in another transaction.
	src.version = 4

	second, err := LocalDeclaration(context.Background(), src)
	require.NoError(t, err)
	assert.Equal(t, 4, second.LocalVersion)
	assert.Equal(t, 2, second.Implications)
	assert.Equal(t, 3, second.NamedTags)
	assert.NotEqual(t, first.Digest, second.Digest,
		"a declaration cached at startup would report the ontology the process booted with forever")
}

func TestLocalDeclarationRefusesAVersionWithNoDocument(t *testing.T) {
	// Version 0 is seeded as the empty ontology (migration 112), so a version with no row at all is a
	// database that has lost the seed rather than an instance with no implications -- and the two must
	// not produce the same answer.
	_, err := LocalDeclaration(context.Background(), &fakeOntology{version: 0, documents: map[int]string{}})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOntologyUnreadable)

	// The empty ontology IS readable, and digests as the empty document. Same version number, the
	// document present.
	empty, err := LocalDeclaration(context.Background(), &fakeOntology{version: 0, documents: map[int]string{0: `[]`}})
	require.NoError(t, err)
	assert.Equal(t, 0, empty.Implications)
}

func TestLocalDeclarationSurfacesASourceFailure(t *testing.T) {
	boom := errors.New("connection refused")
	_, err := LocalDeclaration(context.Background(), &fakeOntology{err: boom})
	require.Error(t, err)
	assert.ErrorIs(t, err, boom, "the database's own error is wrapped, not replaced")
}

// fieldByName is the read every compatibility test does, and it FAILS the test when the field is
// absent rather than returning a zero value: a decision table that stopped carrying `category` would
// otherwise read as "not allowed, no reason", which is the answer for a field nobody asked about.
func fieldByName(t *testing.T, c models.OntologyCompatibility, name string) models.OntologyFieldDecision {
	t.Helper()
	for _, f := range c.Fields {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("no decision for field %q; the table has %d entries", name, len(c.Fields))
	return models.OntologyFieldDecision{}
}

func TestCompatibilityRefusesAV2FieldToAV1Peer(t *testing.T) {
	c := Compatibility([]int{CoreOntologyV1, CoreOntologyV2}, []int{CoreOntologyV1})

	assert.Equal(t, []int{CoreOntologyV1}, c.Shared)

	for _, name := range []string{"tag", "implies"} {
		f := fieldByName(t, c, name)
		assert.True(t, f.Allowed, "%s is core 1 and both instances speak it", name)
	}

	// THE CLAIM OF THE SLICE: the v2-only field is refused, by name, with a reason that says which
	// side has to move.
	category := fieldByName(t, c, "category")
	assert.False(t, category.Allowed, "a core 1 peer must not be sent a core 2 field")
	assert.Equal(t, CoreOntologyV2, category.Since)
	assert.Contains(t, category.Reason, "core 2")
	assert.Contains(t, category.Reason, "peer declares core 1",
		"the refusal must name the peer as the side that lacks the version")
}

func TestCompatibilityIsSymmetricAboutWhoseVersionIsOld(t *testing.T) {
	// This instance is the one behind. The arm exists because the first version of Compatibility read
	// this instance's versions from a package variable, which made this case unreachable and its
	// message -- the one naming THIS instance as the thing to upgrade -- unread.
	c := Compatibility([]int{CoreOntologyV1}, []int{CoreOntologyV1, CoreOntologyV2})

	assert.Equal(t, []int{CoreOntologyV1}, c.Shared)
	category := fieldByName(t, c, "category")
	assert.False(t, category.Allowed)
	assert.Contains(t, category.Reason, "this instance declares core 1",
		"the message names the instance that has to move, and here it is this one")
}

func TestCompatibilityDecidesEveryField(t *testing.T) {
	c := Compatibility([]int{CoreOntologyV1, CoreOntologyV2}, []int{CoreOntologyV1, CoreOntologyV2})
	require.Len(t, c.Fields, len(ontologyFields),
		"every field of the document must be decided; a field missing from the table is a field "+
			"that would be dropped without a reason")
	for _, f := range ontologyFields {
		assert.True(t, fieldByName(t, c, f.name).Allowed, "%s, with both sides at core 2", f.name)
	}
}

func TestCompatibilityWithNoSharedVersionRefusesEverythingAndSaysSo(t *testing.T) {
	c := Compatibility([]int{CoreOntologyV1}, []int{3})
	assert.Empty(t, c.Shared, "no common version is a fact about the pair, not an error for the broadcast")
	for _, f := range c.Fields {
		assert.False(t, f.Allowed)
	}
	assert.Contains(t, fieldByName(t, c, "tag").Reason, "peer declares core 3",
		"a peer on a version we do not share is named, so an operator knows which end to look at")

	// The OTHER arm of the same message: a peer that declared nothing at all. Two arms, two fixtures
	// -- one fixture cannot distinguish them, and the sentence for the empty set is the one that
	// stops a reader concluding the peer is on some version they cannot see.
	silent := Compatibility([]int{CoreOntologyV1}, nil)
	assert.Empty(t, silent.Shared)
	assert.Contains(t, fieldByName(t, silent, "tag").Reason, "no version",
		"a peer that declared no version must read as that, not as an empty list")
}

func TestSupportsCoreIsAClosedSet(t *testing.T) {
	assert.True(t, SupportsCore(CoreOntologyV1))
	assert.True(t, SupportsCore(CoreOntologyV2))
	assert.False(t, SupportsCore(0), "version 0 is the empty ontology, not a core version")
	assert.False(t, SupportsCore(3), "a version this instance has never heard of is not supported")
}

func TestValidateDeclarationRefusesWhatAPeerCouldSend(t *testing.T) {
	good := models.OntologyDeclaration{
		CoreVersions: []int{CoreOntologyV1},
		LocalVersion: 4,
		Digest:       strings.Repeat("ab", 32),
		NamedTags:    3,
		Implications: 2,
	}
	require.NoError(t, ValidateDeclaration(good), "control: an honest declaration must pass")

	for _, tc := range []struct {
		name string
		make func(models.OntologyDeclaration) models.OntologyDeclaration
		why  string
	}{
		{"no versions at all", func(d models.OntologyDeclaration) models.OntologyDeclaration {
			d.CoreVersions = nil
			return d
		}, "a peer that speaks nothing cannot be asked anything"},
		{"version 0", func(d models.OntologyDeclaration) models.OntologyDeclaration {
			d.CoreVersions = []int{0}
			return d
		}, "version 0 is the empty ontology; a core version is >= 1"},
		{"a truncated digest", func(d models.OntologyDeclaration) models.OntologyDeclaration {
			d.Digest = d.Digest[:16]
			return d
		}, "a short digest is compared as a string and matches nothing"},
		{"a digest that is not hex", func(d models.OntologyDeclaration) models.OntologyDeclaration {
			d.Digest = strings.Repeat("zz", 32)
			return d
		}, "a non-hex digest reads as a different ontology rather than a broken sender"},
		{"a negative count", func(d models.OntologyDeclaration) models.OntologyDeclaration {
			d.Implications = -1
			return d
		}, "counts come from a peer, and a negative one means the sender is confused"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Error(t, ValidateDeclaration(tc.make(good)), tc.why)
		})
	}
}
