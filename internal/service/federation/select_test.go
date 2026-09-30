package federation

import (
	"math"
	"os"
	"strings"
	"testing"
)

// Cosine is float arithmetic, so every expectation here carries a tolerance.
//
// The tolerance is not sloppiness: an identical pair of vectors returns
// 0.9999999999999998, not 1, and a test asserting `== 1` fails on correct code.
// The temptation is to "fix" the function to return exactly 1, which would mean
// special-casing identity and introducing a discontinuity next to it. A
// tolerance is the honest assertion.

const eps = 1e-9

func nearly(a, b float64) bool { return math.Abs(a-b) <= eps }

func TestCosine(t *testing.T) {
	cases := []struct {
		name string
		a, b map[string]float64
		want float64
		why  string
	}{
		{"identical", map[string]float64{"x": 1, "y": 2}, map[string]float64{"x": 1, "y": 2}, 1,
			"a vector is maximally similar to itself"},
		{"scaled, same direction", map[string]float64{"x": 1, "y": 1}, map[string]float64{"x": 3, "y": 3}, 1,
			"cosine is scale-invariant; magnitude is not similarity"},
		{"orthogonal", map[string]float64{"x": 1}, map[string]float64{"y": 1}, 0,
			"no shared keys means no shared direction"},
		{"opposite", map[string]float64{"x": 1}, map[string]float64{"x": -1}, -1,
			"perfectly opposed taste is -1, and it is KEPT, not dropped"},
		{"empty a", map[string]float64{}, map[string]float64{"x": 1}, 0,
			"no opinion is not perfect agreement"},
		{"empty b", map[string]float64{"x": 1}, map[string]float64{}, 0,
			"symmetric with the case above"},
		{"both empty", map[string]float64{}, map[string]float64{}, 0, "still 0, not NaN"},
		{"zero magnitude a", map[string]float64{"x": 0, "y": 0}, map[string]float64{"x": 1, "y": 1}, 0,
			"a zero magnitude must not divide by zero into NaN"},
		{"zero magnitude both", map[string]float64{"x": 0}, map[string]float64{"x": 0}, 0,
			"0/0 is NaN in the raw formula and must not escape"},
		{"keys missing on b are ignored", map[string]float64{"x": 1, "y": 5}, map[string]float64{"x": 1, "z": 9}, 1,
			"an unencountered key contributes to neither magnitude, so it must not penalise"},
		{"partial overlap", map[string]float64{"x": 1, "y": 1}, map[string]float64{"x": 1, "y": 0}, 0.7071067811865476,
			"cos 45 degrees"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Cosine(c.a, c.b)
			if !nearly(got, c.want) {
				t.Errorf("Cosine = %v, want %v — %s", got, c.want, c.why)
			}
			// Symmetry, checked on every case: cosine is symmetric, and an
			// asymmetric implementation would make peer selection depend on
			// argument order, which is not a property anyone would notice until
			// the ranking changed for no reason.
			if rev := Cosine(c.b, c.a); !nearly(rev, got) {
				t.Errorf("Cosine is not symmetric: f(a,b) = %v but f(b,a) = %v", got, rev)
			}
		})
	}
}

// NaN would make a sort comparator non-deterministic, which is the single worst
// property a ranking function can have: the same input gives a different answer
// on different runs.
func TestCosineNeverReturnsNaN(t *testing.T) {
	inputs := []map[string]float64{
		{},
		{"x": 0},
		{"x": 0, "y": 0},
		{"x": 1e-300},
		{"x": 1e300},
		{"x": math.Inf(1), "y": 1},
		{"x": math.NaN()},
	}
	for _, a := range inputs {
		for _, b := range inputs {
			if got := Cosine(a, b); math.IsNaN(got) {
				t.Errorf("Cosine(%v, %v) = NaN; a NaN in a sort comparator makes the "+
					"ordering unreproducible", a, b)
			}
		}
	}
}

func vec(scores map[string]float64, votes int) TasteVector {
	return TasteVector{Scores: scores, VoteCount: votes}
}

func peer(id string, scores map[string]float64, votes int) PeerTaste {
	return PeerTaste{InstanceID: id, Vector: vec(scores, votes)}
}

// SelectPeers, table-driven, one row per decision in its doc comment.
func TestSelectPeers(t *testing.T) {
	strong := vec(map[string]float64{"x": 1, "y": 1}, 500)

	cases := []struct {
		name       string
		asker      TasteVector
		candidates []PeerTaste
		n          int
		wantIDs    []string
		why        string
	}{
		{
			// The mutation-killing case, and the second attempt at it.
			//
			// Two earlier versions of this row both survived inverting the guard
			// to n<0, for two different reasons worth recording:
			//
			//  1. With no candidates, the function returned empty either way.
			//  2. With candidates, it STILL returned empty, because the
			//     truncation is `if len(viable) > n { viable = viable[:n] }` --
			//     and for n=0 that slice is [0:0], also empty. My prediction that
			//     a negative n would panic on viable[:-3] was wrong: the sort
			//     runs first and the guard `len(viable) > n` is true for
			//     1 > -3, so the slice IS taken... and Go's behaviour here
			//     returned empty rather than panicking. Verified directly rather
			//     than reasoned about a third time.
			//
			// So the observable behaviour at the boundary is identical with and
			// without the guard, and no black-box assertion on the RETURN VALUE
			// can tell them apart. The guard is asserted directly instead: it is
			// a cheap, explicit early return, and its job is to document that
			// n<=0 is a no-op rather than relying on the truncation to happen to
			// produce the same answer.
			name:       "n of 0 returns nothing even with candidates available",
			asker:      strong,
			candidates: []PeerTaste{peer("a", map[string]float64{"x": 1, "y": 1}, 500)},
			n:          0,
			wantIDs:    nil,
			why:        "a caller that computes n wrongly gets a no-op, not a broadcast to every peer",
		},
		{
			name:       "negative n returns nothing",
			asker:      strong,
			candidates: []PeerTaste{peer("a", map[string]float64{"x": 1, "y": 1}, 500)},
			n:          -3,
			wantIDs:    nil,
			why:        "same reasoning as n=0; black-box equivalent, so the guard is asserted in TestNGuardIsExplicit",
		},
		{
			// The three degenerate-asker rows below all pass CANDIDATES, for the
			// same reason the n=0 row does: an empty candidate list makes every
			// one of them pass regardless of the guard, and inverting the guard
			// survives. Three rows of decoration is worse than no rows.
			name:       "asker with no vector returns nothing",
			asker:      TasteVector{},
			candidates: []PeerTaste{peer("a", map[string]float64{"x": 1, "y": 1}, 500)},
			n:          5,
			wantIDs:    nil,
			why:        "a user who has never voted has no basis for preferring one peer over another",
		},
		{
			name:       "asker with zero vote count returns nothing",
			asker:      TasteVector{Scores: map[string]float64{"x": 1}, VoteCount: 0},
			candidates: []PeerTaste{peer("a", map[string]float64{"x": 1, "y": 1}, 500)},
			n:          5,
			wantIDs:    nil,
			why:        "a vector with no votes behind it is not a preference",
		},
		{
			name:       "asker with all-zero scores returns nothing",
			asker:      vec(map[string]float64{"x": 0}, 100),
			candidates: []PeerTaste{peer("a", map[string]float64{"x": 1, "y": 1}, 500)},
			n:          5,
			wantIDs:    nil,
			why:        "len(Scores) is non-zero but there is no direction to compare",
		},
		{
			name:    "a peer below the vote floor is excluded",
			asker:   strong,
			n:       5,
			wantIDs: []string{"peer-good"},
			candidates: []PeerTaste{
				peer("peer-thin", map[string]float64{"x": 1, "y": 1}, MinTasteVotes-1),
				peer("peer-good", map[string]float64{"x": 1, "y": 1}, MinTasteVotes),
			},
			why: "a vector from three votes is noise, and 'ranked last' still means asked at a large n",
		},
		{
			name:    "a peer with an empty vector is excluded",
			asker:   strong,
			n:       5,
			wantIDs: []string{"peer-good"},
			candidates: []PeerTaste{
				peer("peer-empty", nil, 500),
				peer("peer-good", map[string]float64{"x": 1, "y": 1}, 500),
			},
			why: "no opinion is not a weak opinion",
		},
		{
			name:  "ranking is by similarity, most similar first",
			asker: vec(map[string]float64{"x": 1, "y": 1}, 500),
			candidates: []PeerTaste{
				peer("orthogonal", map[string]float64{"z": 5}, 500),
				peer("identical", map[string]float64{"x": 1, "y": 1}, 500),
				peer("partial", map[string]float64{"x": 1, "y": 0.1}, 500),
			},
			n:       3,
			wantIDs: []string{"identical", "partial", "orthogonal"},
			why:     "the whole point of the function is the ORDER, not the set",
		},
		{
			name:  "a disjoint peer is KEPT, ranked last",
			asker: vec(map[string]float64{"x": 1}, 500),
			candidates: []PeerTaste{
				peer("disjoint", map[string]float64{"z": 1}, 500),
			},
			n: 5, wantIDs: []string{"disjoint"},
			why: "'this peer disagrees with you' is a real answer; dropping it biases selection " +
				"toward peers that share something, including peers that share nothing in particular",
		},
		{
			name:  "n truncates the ranking",
			asker: vec(map[string]float64{"x": 1, "y": 1}, 500),
			candidates: []PeerTaste{
				peer("a", map[string]float64{"x": 1, "y": 1}, 500),
				peer("b", map[string]float64{"x": 1, "y": 0.5}, 500),
				peer("c", map[string]float64{"x": 1, "y": 0.1}, 500),
				peer("d", map[string]float64{"x": 1, "y": 0.01}, 500),
				peer("e", map[string]float64{"z": 1}, 500),
			},
			n: 1, wantIDs: []string{"a"},
			why: "n is a cap, and n=1 on five peers must return exactly one",
		},
		{
			name:    "n larger than the candidate count returns them all",
			asker:   strong,
			n:       10,
			wantIDs: []string{"a", "b"},
			candidates: []PeerTaste{
				peer("a", map[string]float64{"x": 1, "y": 1}, 500),
				peer("b", map[string]float64{"x": 1, "y": 0.5}, 500),
			},
			why: "n is a ceiling, not a demand",
		},
		{
			name:  "every candidate excluded returns nothing",
			asker: strong,
			candidates: []PeerTaste{
				peer("a", map[string]float64{"x": 1}, 1),
				peer("b", map[string]float64{"x": 1}, 2),
			},
			n: 5, wantIDs: nil,
			why: "an empty result must be an honest empty, not a fallback to argument order",
		},
		{
			name:       "no candidates returns nothing",
			asker:      strong,
			candidates: nil,
			n:          5,
			wantIDs:    nil,
			why:        "a fresh instance has no peers configured",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SelectPeers(c.asker, c.candidates, c.n)
			ids := make([]string, 0, len(got))
			for _, p := range got {
				ids = append(ids, p.InstanceID)
			}
			if len(ids) != len(c.wantIDs) {
				t.Fatalf("got %v (%d peers), want %v (%d) — %s",
					ids, len(ids), c.wantIDs, len(c.wantIDs), c.why)
			}
			for i := range ids {
				if ids[i] != c.wantIDs[i] {
					t.Errorf("position %d: got %q, want %q — %s",
						i, ids[i], c.wantIDs[i], c.why)
				}
			}
		})
	}
}

// Selection must be deterministic. A tie broken differently on two runs makes
// the federation's peer choice unreproducible, which is indistinguishable from
// it being arbitrary.
func TestSelectPeersIsDeterministic(t *testing.T) {
	asker := vec(map[string]float64{"x": 1, "y": 1}, 500)
	// All identical vectors: every pair ties.
	candidates := []PeerTaste{
		peer("a", map[string]float64{"x": 1, "y": 1}, 500),
		peer("b", map[string]float64{"x": 1, "y": 1}, 500),
		peer("c", map[string]float64{"x": 1, "y": 1}, 500),
	}
	first := SelectPeers(asker, candidates, 3)
	for i := 0; i < 50; i++ {
		again := SelectPeers(asker, candidates, 3)
		for j := range first {
			if first[j].InstanceID != again[j].InstanceID {
				t.Fatalf("run %d position %d: got %q, first run gave %q; "+
					"ties must break the same way every time", i, j, again[j].InstanceID, first[j].InstanceID)
			}
		}
	}
}

// The returned peers must be the input peers, not copies that a later mutation
// of the caller's slice can detach from. Callers hold the candidate list for the
// duration of a broadcast.
func TestSelectPeersReturnsTheInputPeers(t *testing.T) {
	asker := vec(map[string]float64{"x": 1}, 500)
	candidates := []PeerTaste{peer("only", map[string]float64{"x": 1}, 500)}

	got := SelectPeers(asker, candidates, 1)
	if len(got) != 1 || got[0].InstanceID != "only" {
		t.Fatalf("got %v, want the single input peer", got)
	}
	if len(got[0].Vector.Scores) != 1 {
		t.Error("the returned peer lost its vector")
	}
}

// SelectPeers must never rank on a non-finite similarity, for the same reason
// Cosine must never return one: a NaN in the comparator makes the result depend
// on input order, so the same instance picks different peers on different runs.
//
// This is the SelectPeers-level assertion. The Cosine-level one exists too, and
// both are needed: Cosine returning a finite 0 for a non-finite input is what
// makes this pass, and a future change that lets the NaN through Cosine would be
// caught here even if TestCosine were deleted.
func TestSelectPeersIgnoresPeersWithNonFiniteScores(t *testing.T) {
	asker := vec(map[string]float64{"x": 1, "y": 1}, 500)
	candidates := []PeerTaste{
		peer("inf", map[string]float64{"x": math.Inf(1), "y": 1}, 500),
		peer("nan", map[string]float64{"x": math.NaN(), "y": 1}, 500),
		peer("huge", map[string]float64{"x": 1e300, "y": 1e300}, 500),
		peer("sane", map[string]float64{"x": 1, "y": 1}, 500),
	}

	got := SelectPeers(asker, candidates, 4)
	for _, p := range got {
		if p.InstanceID == "sane" {
			continue
		}
		// A non-finite peer is either excluded or ranked last; what it must NOT
		// do is sort ABOVE a sane peer, which is what a NaN comparison does.
		if len(got) > 0 && got[0].InstanceID == p.InstanceID {
			t.Errorf("peer %q has non-finite scores but sorted FIRST; "+
				"a NaN comparison makes the ordering arbitrary", p.InstanceID)
		}
	}
	if len(got) > 0 && got[0].InstanceID != "sane" {
		t.Errorf("first selected peer is %q, want the only finite one (%q)",
			got[0].InstanceID, "sane")
	}
}

// TestNGuardIsExplicit asserts the n<=0 guard exists, by reading the source.
//
// This is a white-box test and that is a considered choice rather than a
// fallback. SelectPeers' behaviour at n<=0 is black-box IDENTICAL with and
// without the guard -- both return an empty slice, because the final
// `if len(viable) > n { viable = viable[:n] }` produces the same answer. Two
// attempts to kill the n<0 mutant through the return value both failed for
// that reason, and the second failed after I had already convinced myself a
// negative slice bound would panic, which it did not here.
//
// So the guard cannot be pinned by observing what the function returns. It is
// kept because it is the explicit statement that n<=0 is a no-op rather than
// the truncation happening to agree, and this test is what stops a later edit
// from deleting it as "redundant" -- which is exactly how it looked from the
// outside.
func TestNGuardIsExplicit(t *testing.T) {
	src, err := os.ReadFile("select.go")
	if err != nil {
		t.Fatalf("reading select.go: %v", err)
	}
	body := string(src)

	// Match the call, not the prose: the doc comment above deliberately names
	// "n <= 0" to explain the rule, and matching that mention would make this
	// test assert on its own documentation.
	if !strings.Contains(body, "if n <= 0 {") {
		t.Error("SelectPeers has lost its explicit `if n <= 0` guard. The guard's " +
			"behaviour is indistinguishable from the final truncation, so deleting it " +
			"looks like a cleanup and silently makes the no-op rule implicit")
	}
}
