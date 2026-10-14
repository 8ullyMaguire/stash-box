package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/config"
	"github.com/stashapp/stash-box/internal/service/similar"
)

// The vector store NAME -> implementation mapping.
//
// Worth its own test because it is the only place a configuration string becomes behaviour,
// and because every failure mode here is silent: a typo that quietly yields the no-op store
// looks exactly like an instance with no embeddings, which is the state this feature spends
// most of its time in. The test asserts the DEFAULT by name, the one real implementation, and
// that an unknown value degrades to the no-op rather than refusing to serve scenes.
func TestNewVectorStoreMapsTheConfiguredName(t *testing.T) {
	original := config.C.SimilarVectorStore
	originalDims := config.C.SimilarVectorDimensions
	t.Cleanup(func() {
		config.C.SimilarVectorStore = original
		config.C.SimilarVectorDimensions = originalDims
	})

	config.C.SimilarVectorDimensions = 8

	cases := []struct {
		name       string
		configured string
		wantNoop   bool
		wantInMem  bool
	}{
		{"the default name", "noop", true, false},
		{"nothing configured at all", "", true, false},
		{"the in-memory store", "inmemory", false, true},
		{"case is not a trap", "InMemory", false, true},
		{"an unknown name degrades rather than failing to serve", "pgvector", true, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config.C.SimilarVectorStore = tc.configured
			store := newVectorStore()
			require.NotNil(t, store)

			if tc.wantNoop {
				assert.IsType(t, similar.Noop{}, store)
			}
			if tc.wantInMem {
				inMem, ok := store.(*similar.InMemory)
				require.True(t, ok, "inmemory must be the in-memory store")
				assert.Equal(t, 0, inMem.Len(), "a new store is empty: nothing produces vectors yet")
			}
		})
	}
}
