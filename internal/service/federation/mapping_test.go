package federation

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The mapping (phase 12, slice 12e). Every case here is a way to get a core name WRONG, and the
// expectation is a refusal rather than a plausible answer -- because a plausible answer crosses the
// boundary as if it were agreed, and a peer files the tag under a meaning it believes it knows.

func TestResolveLocalTag(t *testing.T) {
	mappings := map[string]string{
		"big-breasts": "measurements",
		"hair colour": "hair-color",
		"unknown":     "chest-large", // not in the vocabulary
		"chain":       "big-breasts", // itself a local tag
		"selfish":     "selfish",     // maps to itself, and is not a core tag
		"silly":       "measurements",
		"empty":       "   ",
	}

	cases := []struct {
		name       string
		local      string
		core       string
		refusalHas string
	}{
		{
			name:  "a tag that is already a core name resolves to itself",
			local: "eye-color", core: "eye-color",
		},
		{
			name:  "a declared local tag resolves to the core name it declares",
			local: "big-breasts", core: "measurements",
		},
		{
			name:  "the core name is trimmed, so a declaration with stray spaces still resolves",
			local: "silly", core: "measurements",
		},
		{
			name:  "a local name with a space is not a core name and needs its mapping",
			local: "hair colour", core: "hair-color",
		},
		{
			name:       "an unmapped tag is refused, naming the tag",
			local:      "solely-local",
			refusalHas: `no mapping is declared for "solely-local"`,
		},
		{
			name:       "a mapping pointing outside the vocabulary is refused, naming the name",
			local:      "unknown",
			refusalHas: `maps to "chest-large", which is not a core v1 tag`,
		},
		{
			name:       "a mapping pointing at another local tag is refused as a chain",
			local:      "chain",
			refusalHas: `maps to "big-breasts", which is itself a local tag`,
		},
		{
			name:       "a self-mapping is refused, because a local name cannot cross as itself",
			local:      "selfish",
			refusalHas: `maps to itself, but "selfish" is not a core v1 tag`,
		},
		{
			name:       "a mapping to an empty name is refused",
			local:      "empty",
			refusalHas: "maps to an empty name",
		},
		{
			name:       "an empty tag name is refused rather than resolved",
			local:      "   ",
			refusalHas: "an empty tag name has nothing to resolve",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveLocalTag(tc.local, mappings)
			if tc.refusalHas == "" {
				assert.True(t, got.Resolved(), "expected a core name, got %q", got.Refusal)
				assert.Equal(t, tc.core, got.Core)
				assert.Empty(t, got.Refusal, "a resolved tag must not also carry a refusal")
				return
			}
			// The absence assertion, with its subject counted first: an empty Core with an empty Refusal
			// would satisfy "no core name" while being a table that silently dropped a declaration.
			assert.Empty(t, got.Core)
			require.NotEmpty(t, got.Refusal, "a refusal is a sentence; silence is not one")
			assert.Contains(t, got.Refusal, tc.refusalHas)
		})
	}
}

func TestNoLocalNameEverCrosses(t *testing.T) {
	// The property the whole file exists for: nothing that is merely local is ever reported as a core
	// name. Written as a sweep over the vocabulary AND the declarations, because the interesting case is
	// the one where a mapping points at a name nobody agreed on and the resolver is tempted to pass it
	// through.
	mappings := map[string]string{
		"big-breasts": "measurements",
		"unknown":     "chest-large",
		"chain":       "big-breasts",
	}
	for _, local := range append(append([]string{}, CoreTagVocabularyV1...), "big-breasts", "unknown", "chain", "solely-local") {
		res := ResolveLocalTag(local, mappings)
		if !res.Resolved() {
			continue
		}
		assert.True(t, IsCoreTag(res.Core),
			"%q resolved to %q, which is not in the agreed vocabulary", local, res.Core)
	}
}

func TestCoreTagVocabularySaysWhatItDoesNotHave(t *testing.T) {
	v1 := CoreTagVocabulary(CoreOntologyV1)
	assert.NotEmpty(t, v1)
	assert.Equal(t, CoreTagVocabularyV1, v1, "v1's list is the published one")
	assert.NotSame(t, &CoreTagVocabularyV1[0], &v1[0], "a caller must not be able to edit the vocabulary in place")

	// THIS LINE USED TO ASSERT Nil FOR CORE V2, and that assertion was the not-built state written down:
	// v2 adds category membership to an EDGE, not names to the vocabulary, so "what may a v2 peer resolve
	// against" has always been v1's list. Answering nil told a v2 peer that this instance has no
	// vocabulary at all -- which reads as "nothing crosses", a claim about the mesh, rather than as "not
	// implemented", a claim about this instance.
	assert.Equal(t, CoreTagVocabularyV1, CoreTagVocabulary(CoreOntologyV2),
		"v2 changes the edge, not the names a peer resolves against")

	// An UNIMPLEMENTED version still says so rather than falling back: a fallback would make an instance
	// believe it knows the names of a version it has never read, which is the silent mis-mapping by
	// another door.
	assert.Nil(t, CoreTagVocabulary(99))
}

func TestResolveLocalTagsIsOrderedAndDeduplicated(t *testing.T) {
	mappings := map[string]string{"big-breasts": "measurements", "hair colour": "hair-color"}
	got := ResolveLocalTags([]string{"hair colour", "big-breasts", "hair colour", " sola "}, mappings)

	require.Len(t, got, 3, "a name passed twice is one resolution")
	assert.Equal(t, []string{"big-breasts", "hair colour", "sola"}, []string{got[0].Local, got[1].Local, got[2].Local})
	assert.Equal(t, "measurements", got[0].Core)
	assert.Equal(t, "hair-color", got[1].Core)
	assert.False(t, got[2].Resolved())
	assert.Contains(t, got[2].Refusal, `"sola"`, "the refusal names the trimmed tag, so it matches the table's key")
}
