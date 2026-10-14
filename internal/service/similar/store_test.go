package similar

import (
	"math"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The vector store, at the level where its claims are actually decidable.
//
// There is no database in this file and there is nothing to mock: the store is arithmetic
// over slices, so the tests are too. What they have to establish is that the numbers are
// right and that the REFUSALS happen -- a store that stores a vector of the wrong width, or
// one with no direction, produces a score for every pair and no error at all, which is the
// kind of bug that looks like a weak model rather than a broken one.

func id(t *testing.T, seed byte) uuid.UUID {
	t.Helper()
	var u uuid.UUID
	for i := range u {
		u[i] = seed
	}
	return u
}

func TestCosineIsTheAngleNotTheDistance(t *testing.T) {
	// Same direction, different magnitude: 1.0, because cosine compares DIRECTION. A scene
	// embedding's length carries no meaning here, and a dot product would let a long vector
	// dominate a page.
	score, err := Cosine([]float32{1, 2, 3}, []float32{2, 4, 6})
	require.NoError(t, err)
	assert.InDelta(t, 1.0, score, 1e-9)

	// Orthogonal: no similarity at all.
	score, err = Cosine([]float32{1, 0}, []float32{0, 1})
	require.NoError(t, err)
	assert.InDelta(t, 0.0, score, 1e-9)

	// Opposite: -1, which is a real answer and not an error. The caller decides what a
	// negative similarity means; the store must not decide by returning zero.
	score, err = Cosine([]float32{1, 0}, []float32{-1, 0})
	require.NoError(t, err)
	assert.InDelta(t, -1.0, score, 1e-9)

	// A known non-trivial angle: 45 degrees is 1/sqrt(2).
	score, err = Cosine([]float32{1, 1}, []float32{1, 0})
	require.NoError(t, err)
	assert.InDelta(t, math.Sqrt2/2, score, 1e-9)
}

func TestCosineRefusesAVectorWithNoDirection(t *testing.T) {
	// Undefined rather than zero: a zero vector has no direction, so every pair involving
	// it would score the same, and that constant would reorder lists.
	_, err := Cosine([]float32{0, 0}, []float32{1, 1})
	assert.ErrorIs(t, err, ErrZeroVector)

	_, err = Cosine([]float32{1, 1}, []float32{0, 0})
	assert.ErrorIs(t, err, ErrZeroVector)

	_, err = Cosine([]float32{1}, []float32{1, 2})
	assert.ErrorIs(t, err, ErrDimension, "two lengths is two models, and comparing them up to the shorter one is inventing a number")
}

func TestInMemoryNearestRanksBySimilarity(t *testing.T) {
	store := NewInMemory(2)
	aligned := id(t, 1)
	orthogonal := id(t, 2)
	opposite := id(t, 3)

	require.NoError(t, store.Put(aligned, []float32{2, 0}))
	require.NoError(t, store.Put(orthogonal, []float32{0, 5}))
	require.NoError(t, store.Put(opposite, []float32{-3, 0}))
	assert.Equal(t, 3, store.Len())

	hits, err := store.Nearest([]float32{1, 0}, 10)
	require.NoError(t, err)
	require.Len(t, hits, 3)
	assert.Equal(t, []uuid.UUID{aligned, orthogonal, opposite}, []uuid.UUID{hits[0].ID, hits[1].ID, hits[2].ID},
		"closest first, and a negative similarity is still ranked rather than dropped")
	assert.InDelta(t, 1.0, hits[0].Score, 1e-9)
	assert.InDelta(t, -1.0, hits[2].Score, 1e-9)

	// The limit is honoured, and it keeps the BEST rows rather than the first ones found.
	hits, err = store.Nearest([]float32{1, 0}, 1)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, aligned, hits[0].ID)

	// limit <= 0 is nothing, not everything.
	hits, err = store.Nearest([]float32{1, 0}, 0)
	require.NoError(t, err)
	assert.Empty(t, hits)
}

func TestInMemoryTiesAreOrderedByID(t *testing.T) {
	// Two identical vectors is the case a map iteration decides: the same read twice would
	// return a different order, and a reshuffling recommendation list is unreproducible.
	store := NewInMemory(2)
	first := id(t, 1)
	second := id(t, 2)
	require.NoError(t, store.Put(second, []float32{1, 1}))
	require.NoError(t, store.Put(first, []float32{1, 1}))

	for range 5 {
		hits, err := store.Nearest([]float32{1, 1}, 10)
		require.NoError(t, err)
		require.Len(t, hits, 2)
		assert.Equal(t, []uuid.UUID{first, second}, []uuid.UUID{hits[0].ID, hits[1].ID},
			"equal scores must come back in a stable order")
	}
}

func TestInMemoryRefusesWhatItCannotCompare(t *testing.T) {
	store := NewInMemory(3)

	assert.ErrorIs(t, store.Put(id(t, 1), []float32{1, 2}), ErrDimension,
		"a vector of the wrong width is a different model, not a padded one")
	assert.ErrorIs(t, store.Put(id(t, 1), []float32{0, 0, 0}), ErrZeroVector)
	assert.Equal(t, 0, store.Len(), "a refused vector must not be stored")

	require.NoError(t, store.Put(id(t, 1), []float32{1, 0, 0}))
	_, err := store.Nearest([]float32{1, 0}, 5)
	assert.ErrorIs(t, err, ErrDimension, "a query of the wrong width must fail rather than compare nothing")
	_, err = store.Nearest([]float32{0, 0, 0}, 5)
	assert.ErrorIs(t, err, ErrZeroVector)
}

func TestInMemoryPutReplacesAndCopies(t *testing.T) {
	store := NewInMemory(2)
	key := id(t, 7)
	original := []float32{1, 0}
	require.NoError(t, store.Put(key, original))
	assert.Equal(t, 1, store.Len())

	// Stored by value: mutating the caller's slice afterwards must not change the store.
	original[0] = -1
	hits, err := store.Nearest([]float32{1, 0}, 1)
	require.NoError(t, err)
	assert.InDelta(t, 1.0, hits[0].Score, 1e-9,
		"a stored vector that changes with the caller's slice makes every score depend on timing")

	// Replaced, not accumulated.
	require.NoError(t, store.Put(key, []float32{0, 1}))
	assert.Equal(t, 1, store.Len())
	hits, err = store.Nearest([]float32{1, 0}, 1)
	require.NoError(t, err)
	assert.InDelta(t, 0.0, hits[0].Score, 1e-9)
}

// The query side of the store: what the re-ranker actually calls.
func TestInMemoryScoresTheRequestedIdsOnly(t *testing.T) {
	store := NewInMemory(2)
	subject := id(t, 1)
	near := id(t, 2)
	far := id(t, 3)
	stranger := id(t, 4)

	require.NoError(t, store.Put(subject, []float32{1, 0}))
	require.NoError(t, store.Put(near, []float32{1, 0.1}))
	require.NoError(t, store.Put(far, []float32{0, 1}))

	scores, err := store.Scores(subject, []uuid.UUID{near, far, stranger})
	require.NoError(t, err)
	require.Len(t, scores, 2, "an id with no vector is ABSENT, not zero")
	assert.Greater(t, scores[near], scores[far])
	_, present := scores[stranger]
	assert.False(t, present, "unknown and unlike are different answers")
}

func TestInMemoryScoresWithoutASubjectVectorIsNotAnError(t *testing.T) {
	store := NewInMemory(2)
	other := id(t, 2)
	require.NoError(t, store.Put(other, []float32{1, 0}))

	// The ordinary state of an instance with no embedding model: the subject has no vector,
	// so nothing is known and the caller leaves the order alone.
	scores, err := store.Scores(id(t, 1), []uuid.UUID{other})
	require.NoError(t, err)
	assert.Empty(t, scores)
}

// The default store must be inert AND must say so when something tries to fill it.
func TestNoopStoresNothingAndSaysSo(t *testing.T) {
	var store Noop

	assert.ErrorIs(t, store.Put(id(t, 1), []float32{1, 0}), ErrNoStore,
		"a silent success would let a seeding pipeline report itself healthy while storing nothing")
	assert.Equal(t, 0, store.Len())

	hits, err := store.Nearest([]float32{1, 0}, 10)
	require.NoError(t, err, "an empty store is not an error: there is simply nothing to rank")
	assert.Empty(t, hits)

	scores, err := store.Scores(id(t, 1), []uuid.UUID{id(t, 2)})
	require.NoError(t, err)
	assert.Empty(t, scores)
}
