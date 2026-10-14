package similar

import (
	"encoding/binary"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/gofrs/uuid"
)

// The measurement that says where in-memory brute force stops being enough.
//
// Section 8 of the discovery spec says the threshold that justifies an approximate index is
// MEASURED, NOT GUESSED, and recorded where the numbers were taken. This is where they are
// taken. It is a Go benchmark rather than prose because the number has to be reproducible:
// the machine, the dimensions and the candidate count are all in the invocation.
//
//	go test ./internal/service/similar/ -bench BenchmarkNearest -benchtime 200x -run '^$'
//
// A benchmark and not a test, because this asserts nothing about correctness -- it exists to
// produce a number a person reads and writes down. The measurement is the mean: a brute-force
// scan touches every stored vector once and the work per vector is identical, so the
// distribution is tight and the mean is representative. `-benchtime 200x` is deliberate: the
// default duration-based mode would let the 1k case run thousands of iterations and the 100k
// case three, which is not a comparable pair of numbers.
func benchmarkStore(b *testing.B, size, dims int) *InMemory {
	b.Helper()

	store := NewInMemory(dims)
	for i := range size {
		id := uuid.Must(uuid.NewV7())
		vector := make([]float32, dims)
		// Deterministic pseudo-random content: a hash of the index, so two runs on two
		// machines measure the same arithmetic rather than the same seed's luck.
		for j := range vector {
			var buf [8]byte
			binary.LittleEndian.PutUint64(buf[:], uint64(i*dims+j))
			vector[j] = float32(buf[0]%200) / 100.0
		}
		if err := store.Put(id, vector); err != nil {
			b.Fatalf("seeding a %d-vector store: %v", size, err)
		}
	}
	return store
}

func BenchmarkNearest(b *testing.B) {
	for _, dims := range []int{512} {
		for _, size := range []int{1000, 10000, 100000} {
			b.Run(fmt.Sprintf("%d_vectors_%d_dims", size, dims), func(b *testing.B) {
				store := benchmarkStore(b, size, dims)
				query := make([]float32, dims)
				for i := range query {
					query[i] = 0.5
				}

				b.ResetTimer()
				for range b.N {
					if _, err := store.Nearest(query, 10); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// TestNearestLatencyReport prints a percentile report rather than a mean.
//
// Skipped unless SIMILAR_BENCH=1, because it takes seconds and asserts nothing: it exists so
// that the numbers in the spec can be re-taken deliberately, on whatever machine is asking.
// The mean is not enough for a threshold decision -- a p95 four times the mean means the
// candidate set has a tail, and a tail is what a page's worst reader feels.
func TestNearestLatencyReport(t *testing.T) {
	if os.Getenv("SIMILAR_BENCH") == "" {
		t.Skip("set SIMILAR_BENCH=1 to take the in-memory nearest-neighbour latency report")
	}

	const dims = 512
	const trials = 200

	for _, size := range []int{1000, 10000, 100000} {
		store := NewInMemory(dims)
		for i := range size {
			id := uuid.Must(uuid.NewV7())
			vector := make([]float32, dims)
			for j := range vector {
				var buf [8]byte
				binary.LittleEndian.PutUint64(buf[:], uint64(i*dims+j))
				vector[j] = float32(buf[0]%200) / 100.0
			}
			if err := store.Put(id, vector); err != nil {
				t.Fatalf("seeding: %v", err)
			}
		}

		query := make([]float32, dims)
		for i := range query {
			query[i] = 0.5
		}

		samples := make([]time.Duration, 0, trials)
		for range trials {
			start := time.Now()
			if _, err := store.Nearest(query, 10); err != nil {
				t.Fatalf("nearest: %v", err)
			}
			samples = append(samples, time.Since(start))
		}
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })

		var total time.Duration
		for _, s := range samples {
			total += s
		}
		mean := total / time.Duration(len(samples))
		t.Logf("%7d vectors, %d dims: p50 %8s  p95 %8s  max %8s  mean %8s",
			size, dims,
			samples[len(samples)/2].Round(time.Microsecond),
			samples[len(samples)*95/100].Round(time.Microsecond),
			samples[len(samples)-1].Round(time.Microsecond),
			mean.Round(time.Microsecond))
	}
}
