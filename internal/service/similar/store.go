// Package similar answers "what is this scene like", with the evidence for the answer.
//
// THE EXPLANATION COMES FIRST AND THE VECTOR COMES SECOND, which is the shape section 8 of
// the discovery spec asks for and the only shape this instance can honestly serve. The
// structured half -- shared tags through the ontology, a shared performer, a shared studio,
// a similar duration -- is correct at any scale, needs no model, and every one of its facts
// can be checked by whoever reads the recommendation. The vector half needs an embedding
// model that nothing here produces yet, so it ships as an interface with a no-op default and
// an in-memory implementation, wired as a RE-RANKER over the candidates the structured half
// already found, and it never touches the score.
package similar

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"

	"github.com/gofrs/uuid"
)

// Neighbour is one vector-store hit.
type Neighbour struct {
	ID uuid.UUID
	// Score is cosine similarity in [-1, 1], where 1 is the same direction. Not a
	// percentage and not comparable across stores: it is a distance, and the caller
	// re-ranks on it rather than displaying it.
	Score float64
}

// VectorStore holds one embedding per entity and answers nearest neighbours.
//
// An interface rather than a concrete type because THE ONLY IMPLEMENTATION THAT CAN EXIST
// TODAY IS A DEGENERATE ONE. `candidate` sets are already narrowed by the structured
// filters, so brute force in memory is enough for any instance whose scenes fit in RAM,
// while an approximate index is a decision to be made when the measurements say so -- and
// pgvector is NOT available on this instance's Postgres (`pg_available_extensions` has
// bktree, pg_search, pg_trgm and cube; 5 dimensions is not 512). The interface is the seam
// that lets a real index arrive later without the caller learning about it.
type VectorStore interface {
	// Put stores a vector for an entity, replacing any previous one.
	Put(id uuid.UUID, vector []float32) error
	// Scores returns the cosine similarity of each requested id's stored vector to the
	// vector stored for queryID. An id without a stored vector is ABSENT from the map
	// rather than zero -- "nothing is known about this pair" and "these two point in
	// opposite directions" are different answers, and collapsing them would let an
	// unknown vector reorder a list.
	//
	// Batch-shaped rather than a `Get` per id because that is what a real index wants:
	// the pgvector form of this method is one `1 - (embedding <=> ...)` projection over
	// `id = ANY(...)`.
	Scores(queryID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]float64, error)
	// Nearest returns the closest stored vectors to `vector`, closest first, at most
	// `limit` of them. An empty store returns nothing: no content is not an error.
	Nearest(vector []float32, limit int) ([]Neighbour, error)
	// Len is how many vectors are stored. Reported because "the re-ranker changed
	// nothing" and "the re-ranker had nothing to work with" are different answers to a
	// question an operator will ask.
	Len() int
}

// ErrNoStore is returned by Noop.Put: there is nowhere to put it.
//
// A silent success would be worse than an error. The default store is Noop, so a pipeline
// that seeds embeddings and ignores this error would report itself healthy while its
// vectors went nowhere, and the first symptom would be a re-ranker that never reorders
// anything.
var ErrNoStore = errors.New("no vector store is configured: vectors cannot be stored")

// ErrDimension is returned when a vector's length does not match the store's.
//
// Length is the one thing about an embedding that cannot be repaired by the store: two
// vectors of different lengths come from different models, and comparing them up to the
// shorter one would produce a number.
var ErrDimension = errors.New("vector has the wrong number of dimensions")

// ErrZeroVector is returned for a vector with no magnitude.
//
// A zero vector has no direction, so cosine similarity against it is undefined rather than
// zero. Storing one would guarantee a meaningless score for that entity against everything,
// and a query using one would rank the store arbitrarily.
var ErrZeroVector = errors.New("vector has zero magnitude: cosine similarity is undefined")

// Noop is the default store: it keeps nothing and returns no neighbours.
//
// Not an error state. On this instance no embedding model exists yet, so the honest default
// is a re-ranker that provably does nothing, rather than a half-wired one.
type Noop struct{}

// Put always fails with ErrNoStore.
func (Noop) Put(uuid.UUID, []float32) error { return ErrNoStore }

// Scores always returns an empty map: nothing is stored, so nothing is known.
func (Noop) Scores(uuid.UUID, []uuid.UUID) (map[uuid.UUID]float64, error) { return nil, nil }

// Nearest always returns nothing.
func (Noop) Nearest([]float32, int) ([]Neighbour, error) { return nil, nil }

// Len is always zero.
func (Noop) Len() int { return 0 }

// InMemory is a brute-force cosine store. It is safe for concurrent use.
//
// Brute force is the right default rather than a placeholder: the candidate set arriving
// here has already been narrowed by the structured filters, so the cost is a linear scan of
// a page-sized set. See store_bench_test.go for the measurement that says where that stops
// being true -- the number is in the spec, not guessed here.
type InMemory struct {
	mu      sync.RWMutex
	vectors map[uuid.UUID][]float32
	dims    int
}

// NewInMemory returns a store whose vectors must all have `dims` dimensions.
func NewInMemory(dims int) *InMemory {
	return &InMemory{
		vectors: make(map[uuid.UUID][]float32),
		dims:    dims,
	}
}

// Put stores a vector, replacing any previous one for the id.
func (s *InMemory) Put(id uuid.UUID, vector []float32) error {
	if len(vector) != s.dims {
		return fmt.Errorf("%w: expected %d, got %d", ErrDimension, s.dims, len(vector))
	}
	if magnitude(vector) == 0 {
		return ErrZeroVector
	}
	// Copied, not referenced: the caller's slice is its own, and a stored vector that
	// changes under the store would make every score depend on when it was read.
	stored := make([]float32, len(vector))
	copy(stored, vector)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.vectors[id] = stored
	return nil
}

// Nearest returns the closest stored vectors, closest first.
//
// TIES ARE BROKEN BY ID, because the alternative is an order that depends on Go's map
// iteration: two identical vectors would come back in a different order on every call, and
// a recommendation list that reshuffles between two identical reads is a bug report nobody
// can reproduce.
func (s *InMemory) Nearest(vector []float32, limit int) ([]Neighbour, error) {
	if len(vector) != s.dims {
		return nil, fmt.Errorf("%w: expected %d, got %d", ErrDimension, s.dims, len(vector))
	}
	if magnitude(vector) == 0 {
		return nil, ErrZeroVector
	}
	if limit <= 0 {
		return nil, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Neighbour, 0, len(s.vectors))
	for id, stored := range s.vectors {
		score, err := Cosine(vector, stored)
		if err != nil {
			// Only reachable if a zero vector were stored, which Put refuses. Skipped
			// rather than fatal: one bad row must not empty the whole result.
			continue
		}
		out = append(out, Neighbour{ID: id, Score: score})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ID.String() < out[j].ID.String()
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Scores returns each requested id's cosine similarity to the queryId's vector.
//
// A missing query vector is NOT an error: it means the subject has no embedding yet, which
// is the ordinary state of an instance with no embedding model, and the caller's job there
// is to leave the order alone. Absent candidates are likewise simply absent from the map.
func (s *InMemory) Scores(queryID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]float64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query, ok := s.vectors[queryID]
	if !ok {
		return nil, nil
	}

	out := make(map[uuid.UUID]float64, len(ids))
	for _, id := range ids {
		stored, ok := s.vectors[id]
		if !ok {
			continue
		}
		score, err := Cosine(query, stored)
		if err != nil {
			// Unreachable while Put refuses zero vectors; skipped rather than fatal so
			// one unusable row cannot empty a whole page.
			continue
		}
		out[id] = score
	}
	return out, nil
}

// Len is how many vectors are stored.
func (s *InMemory) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.vectors)
}

// Cosine is the cosine similarity of two vectors.
//
// Out of range rather than undefined for a zero vector: see ErrZeroVector.
func Cosine(a, b []float32) (float64, error) {
	if len(a) != len(b) {
		return 0, fmt.Errorf("%w: %d and %d", ErrDimension, len(a), len(b))
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0, ErrZeroVector
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB)), nil
}

func magnitude(v []float32) float64 {
	var sum float64
	for _, f := range v {
		sum += float64(f) * float64(f)
	}
	return sum
}
