package federation

import (
	"context"
	"errors"
	"strings"
	"testing"

	// google/uuid, because a Question's id is the WIRE type (wire.go imports it) and not this repo's
	// gofrs/uuid. Getting this wrong is a compile error, which is the good version.
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/models"
)

const thisInstance = "box-under-test"

// answering is the fake source every case below answers from: one document, one version.
func answering() *fakeOntology {
	return &fakeOntology{version: 3, documents: map[int]string{3: docOneEdge}}
}

func ask(q Question) (Answer, error) {
	return AnswerQuestion(context.Background(), answering(), thisInstance, q)
}

func TestAnswerCarriesOurOntologyAndWhoWeAre(t *testing.T) {
	answer, err := ask(Question{TargetType: "", Description: ""})
	require.NoError(t, err)

	assert.Equal(t, thisInstance, answer.PeerInstanceID,
		"an answer that does not name its sender cannot be attributed to a peer row")

	// The claim must describe the SAME document LocalDeclaration describes, because the peer checks it
	// against what it will fetch. A claim built by a second code path is a claim that drifts.
	local, err := LocalDeclaration(context.Background(), answering())
	require.NoError(t, err)
	assert.Equal(t, ClaimOf(local), answer.Ontology)
	assert.Len(t, answer.Ontology.Digest, 64)
	assert.Equal(t, []int{CoreOntologyV1, CoreOntologyV2}, answer.Ontology.CoreVersions)
}

// peerClaim builds a complete, valid claim for a peer that implements the given core versions. The
// digest is fabricated: this test is about the DECISION, and a claim without a digest is refused
// before the decision is reached (which its own case below asserts).
func peerClaim(versions ...int) OntologyClaim {
	return OntologyClaim{
		CoreVersions: versions,
		LocalVersion: 1,
		Digest:       strings.Repeat("cd", 32),
		NamedTags:    1,
		Implications: 1,
	}
}

func crossing(t *testing.T, a Answer, name string) OntologyFieldCrossing {
	t.Helper()
	for _, c := range a.Crossing {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("the answer decided nothing about %q among %d fields", name, len(a.Crossing))
	return OntologyFieldCrossing{}
}

func TestAnswerDecidesWhatMayCrossBackToTheAsker(t *testing.T) {
	// A peer that speaks only core 1. It asks; the answer tells it that the v2-only field will not cross
	// and WHY -- which is the negotiation slice 12c exists for. Before this, the exchange was two
	// instances describing themselves and neither saying what the other could read.
	answer, err := ask(Question{Ontology: peerClaim(CoreOntologyV1)})
	require.NoError(t, err)

	assert.Equal(t, []int{CoreOntologyV1}, answer.SharedCoreVersions)
	assert.True(t, crossing(t, answer, "tag").Allowed)
	assert.True(t, crossing(t, answer, "implies").Allowed)

	category := crossing(t, answer, "category")
	assert.False(t, category.Allowed)
	assert.Equal(t, CoreOntologyV2, category.Since)
	assert.Contains(t, category.Reason, "peer declares core 1",
		"the reason travels with the decision, so the far-end operator can act on it")

	// The control: a peer that implements both is allowed everything. Without it, the refusal above
	// could be a rule that refuses unconditionally.
	both, err := ask(Question{Ontology: peerClaim(CoreOntologyV1, CoreOntologyV2)})
	require.NoError(t, err)
	assert.Equal(t, []int{CoreOntologyV1, CoreOntologyV2}, both.SharedCoreVersions)
	for _, c := range both.Crossing {
		assert.True(t, c.Allowed, "%s, with the peer at both versions", c.Name)
	}

	// And a peer that declared NOTHING gets every field refused with a reason that says so, rather than
	// an empty crossing list -- silence would read as "no fields to discuss".
	silent, err := ask(Question{})
	require.NoError(t, err)
	assert.Empty(t, silent.SharedCoreVersions)
	require.Len(t, silent.Crossing, len(ontologyFields))
	for _, c := range silent.Crossing {
		assert.False(t, c.Allowed)
		assert.Contains(t, c.Reason, "no version")
	}
}

func TestAnswerInventsNoEvidence(t *testing.T) {
	// F2 on the answering path: this instance is a SOURCE of the ontology claim and of nothing else.
	// The identification half needs the local candidate store and F2's evidence rules; until it exists,
	// an empty candidate list would read as "this peer has nothing" -- see the file header.
	answer, err := ask(Question{})
	require.NoError(t, err)
	assert.Empty(t, answer.Candidates)
}

func TestAnswerRefusesWithoutAnIdentity(t *testing.T) {
	_, err := AnswerQuestion(context.Background(), answering(), "", Question{})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoInstanceID)
	assert.Contains(t, err.Error(), "instance_id",
		"the refusal names the config key, so the fix is in the sentence an operator reads")

	// Whitespace is not an identity either.
	_, err = AnswerQuestion(context.Background(), answering(), "   ", Question{})
	assert.ErrorIs(t, err, ErrNoInstanceID)
}

func TestAnswerRefusesAnIdentificationQuestionByName(t *testing.T) {
	// Three arms, one fixture each: the refusal must fire for a question that carries any of the three
	// identification fields, and it names which one arrived.
	for _, tc := range []struct {
		name string
		q    Question
		why  string
	}{
		{"a target type", Question{TargetType: "performer"}, "the type names what is being identified"},
		{"a description", Question{Description: "who is this"}, "the free text is the question about a person"},
		{"candidate names", Question{CandidateNames: []string{"A", "B"}}, "names already considered are identification too"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ask(tc.q)
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrIdentifyUnserved, tc.why)
			assert.Contains(t, err.Error(), "identification",
				"the refusal must say which half is missing")
		})
	}
}

func TestAnswerRefusesAClaimThatIsDeclaredButMalformed(t *testing.T) {
	// An EMPTY claim is legal: a peer on an older build sends nothing, which Compatibility renders as
	// "no version". The control comes first, so the refusal below is about the malformation and not
	// about the mere presence of a claim.
	_, err := ask(Question{Ontology: OntologyClaim{}})
	require.NoError(t, err, "control: an empty claim is a legal answer")

	for _, tc := range []struct {
		name  string
		claim OntologyClaim
	}{
		{"a digest that is not hexadecimal", OntologyClaim{CoreVersions: []int{1}, Digest: strings.Repeat("zz", 32)}},
		{"a digest of the wrong length", OntologyClaim{CoreVersions: []int{1}, Digest: "abc"}},
		{"a version set containing zero", OntologyClaim{CoreVersions: []int{0}, Digest: strings.Repeat("ab", 32)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ask(Question{Ontology: tc.claim})
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrClaimRejected)
		})
	}
}

func TestClaimDeclaredIsTrueExactlyWhenSomethingIsThere(t *testing.T) {
	assert.False(t, OntologyClaim{}.Declared())

	// One field at a time, because a claim that declared itself on the wrong field would skip validation
	// for a sender that did send something.
	full := models.OntologyDeclaration{
		CoreVersions: []int{CoreOntologyV1}, LocalVersion: 1,
		Digest: strings.Repeat("ab", 32), NamedTags: 1, Implications: 1,
	}
	assert.True(t, ClaimOf(full).Declared())

	for _, partial := range []OntologyClaim{
		{CoreVersions: []int{1}},
		{LocalVersion: 1},
		{Digest: strings.Repeat("ab", 32)},
		{NamedTags: 1},
		{Implications: 1},
	} {
		assert.True(t, partial.Declared(), "%+v carries something", partial)
	}
}

func TestValidateAllowsAQuestionThatOnlyDeclaresAnOntology(t *testing.T) {
	id := uuid.Must(uuid.NewRandom())

	// The arm slice 12b added: no entity is named, because the question is the ontology itself. Without
	// this, the answering half could not be used for its own purpose -- a sender would have to invent a
	// target type.
	assert.NoError(t, Question{
		QueryID:  id,
		Ontology: OntologyClaim{CoreVersions: []int{CoreOntologyV1}, Digest: strings.Repeat("ab", 32)},
	}.Validate())

	// The other arm is unchanged: a question with neither is a question that asks nothing.
	err := Question{QueryID: id}.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "target type")

	assert.NoError(t, Question{QueryID: id, TargetType: "performer"}.Validate())

	// AND THE F1 CHECKS STILL RUN on an ontology-only question. This is the assertion that says the
	// change above narrowed nothing: a description carrying a path is refused whether or not the
	// question also declares an ontology.
	dirty := Question{
		QueryID:     id,
		Description: "look at /etc/passwd",
		Ontology:    OntologyClaim{CoreVersions: []int{CoreOntologyV1}},
	}
	require.Error(t, dirty.Validate())
	assert.Contains(t, dirty.Validate().Error(), "nothing identifying crosses a node boundary")
}

func TestAnswerSurfacesASourceFailure(t *testing.T) {
	boom := errors.New("connection refused")
	_, err := AnswerQuestion(context.Background(), &fakeOntology{err: boom}, thisInstance, Question{})
	require.Error(t, err)
	assert.ErrorIs(t, err, boom, "a database that cannot be read is not an answer")
}
