//go:build integration

package api_test

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service/federation"
)

// The ontology's federation half, end to end (phase 12, slice 12a).
//
// What this test can and cannot prove, stated because the phase's clause asks for two instances and
// this is one. It proves the half that decides: the declaration matches the document a peer would get
// (`findOntologyVersion`), a core 1 peer is refused the v2-only field BY NAME, and the declaration
// moves when the ontology does. It cannot prove an exchange happened -- this tree serves no inbound
// endpoint for a peer to ask at (client.go asks at /federation/identify and nothing answers it), so
// the transport has no caller to test yet. That gap is named in the plan and in the track row.

// decided finds one field's decision, failing the test when it is absent: a table that stopped
// carrying `category` would otherwise read as "refused with no reason".
func decided(t *testing.T, c *models.OntologyCompatibility, name string) models.OntologyFieldDecision {
	t.Helper()
	require.NotNil(t, c)
	for _, f := range c.Fields {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("no decision for field %q among %d entries", name, len(c.Fields))

	return models.OntologyFieldDecision{}
}

func TestOntologyDeclarationNamesTheDocumentAPeerWouldGet(t *testing.T) {
	s := createTagOntologyTestRunner(t)

	decl, err := s.resolver.Query().OntologyDeclaration(s.ctx)
	require.NoError(t, err)
	require.NotNil(t, decl)

	assert.Regexp(t, regexp.MustCompile(`^[0-9a-f]{64}$`), decl.Digest,
		"the digest is what two instances compare, so it must be the hexadecimal form ValidateDeclaration accepts")
	assert.Equal(t, []int{federation.CoreOntologyV1, federation.CoreOntologyV2}, decl.CoreVersions)

	// THE ROUND TRIP THAT MAKES THE DIGEST MEANINGFUL: a peer asks for the document at the version
	// this instance just named, hashes it with the same public function, and must get the same number.
	// Without this, `digest` is a number this instance prints and nothing else can reproduce.
	doc, err := s.resolver.Query().FindOntologyVersion(s.ctx, decl.LocalVersion)
	require.NoError(t, err)
	require.NotNil(t, doc, "the declaration named version %d and there is no such document", decl.LocalVersion)

	computed, err := federation.CanonicalDigest(doc.Snapshot)
	require.NoError(t, err)
	assert.Equal(t, decl.Digest, computed,
		"a peer that hashes the document it can fetch must arrive at the digest this instance published")

	// And the counts describe THAT document, not the tags table: a tag with no implications is not in
	// the document at all, which is why the field is `namedTags` and not `tagCount`.
	var edges []struct {
		Tag     string `json:"tag"`
		Implies string `json:"implies"`
	}
	require.NoError(t, json.Unmarshal([]byte(doc.Snapshot), &edges))
	names := map[string]struct{}{}
	for _, e := range edges {
		names[e.Tag] = struct{}{}
		names[e.Implies] = struct{}{}
	}
	assert.Equal(t, len(edges), decl.Implications)
	assert.Equal(t, len(names), decl.NamedTags)
}

func TestOntologyDeclarationMovesWhenTheOntologyDoes(t *testing.T) {
	s := createTagOntologyTestRunner(t)

	head := s.named("head")
	tail := s.named("tail")

	before, err := s.resolver.Query().OntologyDeclaration(s.ctx)
	require.NoError(t, err)
	require.NotNil(t, before)

	// The fixture adds an edge whose two names were created for this test, so `namedTags` must grow by
	// exactly two. Asserting the delta rather than the total: the package shares one database and other
	// tests' ontologies are in the document too.
	s.mustSetImplications(head, []uuid.UUID{tail}, nil)

	after, err := s.resolver.Query().OntologyDeclaration(s.ctx)
	require.NoError(t, err)
	require.NotNil(t, after)

	assert.Greater(t, after.LocalVersion, before.LocalVersion,
		"the version trigger wrote a row, so the declaration must name the newer version")
	assert.Equal(t, before.Implications+1, after.Implications)
	assert.Equal(t, before.NamedTags+2, after.NamedTags,
		"the new edge mentions two tags this instance had never implied anything with")
	assert.NotEqual(t, before.Digest, after.Digest,
		"an instance that changed its ontology must not keep publishing the old digest")
}

func TestOntologyCompatibilityRefusesAV2FieldToAV1Peer(t *testing.T) {
	s := createTagOntologyTestRunner(t)

	// The clause itself: "an instance that declares core v1 is refused a v2-only field rather than
	// silently mis-mapping".
	v1, err := s.resolver.Query().OntologyCompatibility(s.ctx, []int{federation.CoreOntologyV1})
	require.NoError(t, err)

	assert.Equal(t, []int{federation.CoreOntologyV1}, v1.Shared)
	assert.True(t, decided(t, v1, "tag").Allowed)
	assert.True(t, decided(t, v1, "implies").Allowed)

	category := decided(t, v1, "category")
	assert.False(t, category.Allowed, "a core 1 peer must not be sent a core 2 field")
	assert.Contains(t, category.Reason, "core 2")
	assert.Contains(t, category.Reason, "peer declares core 1",
		"the refusal names the side that has to move, which is the difference between a refusal and a shrug")

	// THE CONTROL: a peer that implements BOTH versions gets everything, so the refusal above is about
	// the VERSION and not about a rule that refuses unconditionally. This arm is the reason the field
	// takes a LIST: written with a single coreVersion, no input could reach it.
	both, err := s.resolver.Query().OntologyCompatibility(s.ctx, []int{federation.CoreOntologyV1, federation.CoreOntologyV2})
	require.NoError(t, err)
	assert.Equal(t, []int{federation.CoreOntologyV1, federation.CoreOntologyV2}, both.Shared)
	for _, f := range both.Fields {
		assert.True(t, f.Allowed, "%s, with the peer at both versions", f.Name)
	}

	// The third state, and the one the single-number version of this field could not express: a peer
	// that speaks ONLY core 2 has dropped the older document, so the v1 fields are refused and the
	// v2-only one is allowed. "Newer" does not mean "a superset of the older", and the declaration is a
	// set of versions rather than a level for exactly this reason.
	onlyV2, err := s.resolver.Query().OntologyCompatibility(s.ctx, []int{federation.CoreOntologyV2})
	require.NoError(t, err)
	assert.Equal(t, []int{federation.CoreOntologyV2}, onlyV2.Shared)
	assert.False(t, decided(t, onlyV2, "tag").Allowed)
	assert.Contains(t, decided(t, onlyV2, "tag").Reason, "peer declares core 2",
		"the refusal names the version the peer did declare")
	assert.True(t, decided(t, onlyV2, "category").Allowed)

	// A version neither side implements is a fourth state: no shared versions at all, reported rather
	// than raised, because "these two cannot speak" is a fact about the pair.
	v9, err := s.resolver.Query().OntologyCompatibility(s.ctx, []int{9})
	require.NoError(t, err)
	assert.Empty(t, v9.Shared, "no common version is reported, not raised as a failure of the whole call")
	for _, f := range v9.Fields {
		assert.False(t, f.Allowed)
	}
	assert.Contains(t, decided(t, v9, "tag").Reason, "core 9",
		"the message names the version the peer declared, so an operator can look it up")
}
