package eval

import (
	"math"
	"testing"
)

// The expected values here are written out by hand from the definition, not captured from
// the implementation. A metric test that asserts what the code currently prints would pass
// just as happily if the formula were wrong.

func TestRecallAtK(t *testing.T) {
	tests := []struct {
		name     string
		ranking  []ID
		relevant []ID
		k        int
		want     float64
	}{
		{"first of two found", []ID{"a", "b", "c"}, []ID{"b", "d"}, 2, 0.5},
		{"both found", []ID{"b", "d", "a"}, []ID{"b", "d"}, 2, 1},
		{"none found", []ID{"a", "b"}, []ID{"q"}, 2, 0},
		{"cutoff excludes the hit", []ID{"a", "b"}, []ID{"b"}, 1, 0},
		{"k beyond the list", []ID{"b"}, []ID{"b", "c"}, 10, 0.5},
		{"empty ranking", nil, []ID{"a"}, 3, 0},
		{"empty labels", []ID{"a"}, nil, 3, 0},
		{"duplicate hits count once", []ID{"a", "a", "b"}, []ID{"a"}, 3, 1},
		{"duplicate labels collapse", []ID{"a"}, []ID{"a", "a"}, 3, 1},
		{"zero k", []ID{"a"}, []ID{"a"}, 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertClose(t, RecallAtK(tc.ranking, tc.relevant, tc.k), tc.want)
		})
	}
}

func TestPrecisionAtK(t *testing.T) {
	tests := []struct {
		name     string
		ranking  []ID
		relevant []ID
		k        int
		want     float64
	}{
		{"half the slots", []ID{"b", "c"}, []ID{"b", "d"}, 2, 0.5},
		{"both slots", []ID{"b", "d"}, []ID{"b", "d"}, 2, 1},
		{"short list is punished", []ID{"b"}, []ID{"b"}, 4, 0.25},
		{"duplicate counts once", []ID{"b", "b"}, []ID{"b"}, 2, 0.5},
		{"empty ranking", nil, []ID{"b"}, 2, 0},
		{"zero k", []ID{"b"}, []ID{"b"}, 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertClose(t, PrecisionAtK(tc.ranking, tc.relevant, tc.k), tc.want)
		})
	}
}

func TestReciprocalRank(t *testing.T) {
	tests := []struct {
		name     string
		ranking  []ID
		relevant []ID
		want     float64
	}{
		{"first", []ID{"b", "a"}, []ID{"b"}, 1},
		{"third", []ID{"x", "y", "b"}, []ID{"b"}, 1.0 / 3.0},
		{"absent", []ID{"x", "y"}, []ID{"b"}, 0},
		{"empty", nil, []ID{"b"}, 0},
		{"first of many labels wins", []ID{"x", "d", "b"}, []ID{"b", "d"}, 0.5},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertClose(t, ReciprocalRank(tc.ranking, tc.relevant), tc.want)
		})
	}
}

func assertClose(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("got %.9f, want %.9f", got, want)
	}
}
