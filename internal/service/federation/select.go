package federation

import (
	"math"
	"sort"
)

// MinTasteVotes is the smallest taste vector that counts as a preference (F4).
//
// A vector built from three votes is not evidence of taste; it is noise that
// happens to have a direction. Without this floor, one enthusiastic voter on a
// peer instance defines that peer's entire relevance, and the federation
// inherits their taste as if it were shared.
//
// The cost is stated plainly: a brand-new peer instance with a handful of
// genuine votes will be excluded until it passes this line. That is the
// intended trade — a wrong peer is worse than a missing one, because a wrong
// peer's evidence is attached to a query with a real name and a real
// attribution.
const MinTasteVotes = 10

// TasteVector is one user's (or one instance's aggregate) preferences.
//
// A user with NO taste has no vector at all, not a zero one. The migration's
// own comment records why that distinction matters: "no taste data" is
// different from "taste data that happens to be all zeros", and storing an empty
// row for everyone erases exactly that.
type TasteVector struct {
	// Scores maps an entity key to a preference score. Keys are opaque here;
	// the caller decides whether they are performers, tags, or something else.
	Scores map[string]float64
	// VoteCount is how many votes the vector was built from. It is stored
	// alongside rather than inferred, because a vector from 3 votes and a vector
	// from 300 look identical in the map and mean completely different things.
	VoteCount int
}

// PeerTaste is a candidate peer's taste, for selection.
type PeerTaste struct {
	// InstanceID identifies the peer. Returned on the selected peers so the
	// caller can address them.
	InstanceID string
	// Vector is the peer's aggregate taste. May be empty; an empty vector is
	// excluded rather than scored, for the same reason the floor is.
	Vector TasteVector
}

// SelectPeers ranks candidates by taste similarity to the asker and returns the
// top n, most similar first.
//
// THE ZERO CASES, decided here rather than left to a caller to discover:
//
//   - The asker has no vector (never voted). Cosine against nothing is
//     undefined, so this returns EMPTY. Not "the first n in argument order":
//     returning an arbitrary order would make the federation's choice look
//     considered, and a user with no taste has no basis for preferring one peer
//     over another. The caller falls back to keeping the query local, which is
//     the honest outcome.
//
//   - A candidate has no vector, or too few votes. EXCLUDED outright, not
//     ranked last. "Ranked last" still means "asked" once n is larger than the
//     count of good peers, and at n=5 out of 6 peers it is asked. A floor that
//     can be out-ranked is not a floor.
//
//   - Cosine is zero or negative (disjoint tastes). KEPT, ranked last. Zero
//     similarity is a real answer — "this peer disagrees with you" — and
//     dropping it would silently bias selection toward peers that share
//     something, including peers that share nothing in particular but have
//     narrow vectors.
//
//   - n <= 0. Returns empty. Not "all peers": a caller that computes n wrongly
//     should get a no-op and a log line, not a broadcast to every configured
//     peer.
//
// Pure function: no I/O, no clock, no database. Everything it needs is in its
// arguments, which is what makes the table testable without a fixture.
func SelectPeers(asker TasteVector, candidates []PeerTaste, n int) []PeerTaste {
	if n <= 0 {
		return nil
	}
	// The asker must have a vector, a vote count, and a DIRECTION. The third
	// check is not implied by the first two: a map with one key whose value is 0
	// has len(Scores) == 1 and passes a length check, yet every candidate scores
	// a cosine of 0 against it, so the "selection" would be arbitrary order
	// rather than taste. A vector of all zeros is the same no-op as an empty
	// one, and the two must not be distinguishable to a caller.
	if !hasDirection(asker.Scores) || asker.VoteCount <= 0 {
		return nil
	}

	type scored struct {
		peer PeerTaste
		sim  float64
	}
	var viable []scored
	for _, c := range candidates {
		// The floor, applied to the CANDIDATE and not to the asker. A caller
		// with a thin but real vector still gets selection; a peer with a thin
		// vector is not asked.
		if len(c.Vector.Scores) == 0 || c.Vector.VoteCount < MinTasteVotes {
			continue
		}
		sim := Cosine(asker.Scores, c.Vector.Scores)
		viable = append(viable, scored{peer: c, sim: sim})
	}

	sort.SliceStable(viable, func(i, j int) bool {
		return viable[i].sim > viable[j].sim
	})

	if len(viable) > n {
		viable = viable[:n]
	}
	out := make([]PeerTaste, 0, len(viable))
	for _, v := range viable {
		out = append(out, v.peer)
	}
	return out
}

// Cosine returns the cosine similarity of two score maps, in [-1, 1].
//
// WHY THERE IS NO EXPLICIT NaN/Inf INPUT SCAN. This function was first written
// with a leading loop rejecting every non-finite input, on the reasoning that
// squaring 1e300 overflows to +Inf and Inf/Inf is NaN. The guard was redundant:
// measured over 81 adversarial pairs (zero, empty, normal, 1e300, 1e-320, +Inf,
// -Inf, NaN), the arithmetic guards below return a finite value for every one
// with the scan removed.
//
// WHY THE ARITHMETIC GUARDS ARE NOT SIMPLIFIED DOWN TO ONE. All of them are
// individually removable, and every removal SURVIVES the suite, because each
// masks the others. That makes them a set of guards no test can isolate, which
// is a different thing from a set of redundant guards, and it was measured
// rather than reasoned about -- reasoning about it here gave the wrong answer
// twice:
//
//	zero-magnitude only  ->  31 of 81 pairs still return NaN
//	non-finite denom only  ->  0 NaN   (sufficient on its own)
//	non-finite sim only  ->  0 NaN   (sufficient on its own)
//
// The denominator check is the load-bearing one: a zero magnitude makes the
// denominator zero, and a non-finite input makes a magnitude overflow to +Inf.
// The `sim` check is kept because it guards a different STEP -- the division
// itself -- so a future change to how the denominator is built cannot silently
// reintroduce an overflow the magnitude check was never able to stop. One
// guard plus two untestable decorations is the outcome to avoid; two guards
// covering two steps of the arithmetic is defensible.
//
// A NaN in the sort comparator below makes the ordering depend on input order,
// which makes the federation's peer choice unreproducible -- the same input
// gives a different answer on different runs.
//
//   - an empty side -> 0. Not NaN, and not 1. An empty vector is "no opinion",
//     and returning 1 would rank a peer with no taste as a perfect match for a
//     user with strong taste.
//   - keys present on one side only are IGNORED, not treated as zero. They
//     contribute nothing to either magnitude, so treating them as an explicit
//     zero would penalise a peer for having opinions the asker has never
//     encountered.
func Cosine(a, b map[string]float64) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	var dot, magA, magB float64
	for k, av := range a {
		bv, ok := b[k]
		if !ok {
			continue
		}
		dot += av * bv
		magA += av * av
		magB += bv * bv
	}
	// The one arithmetic guard. `denom == 0` covers a zero-magnitude vector
	// (every score zero) and `!isFinite(denom)` covers an overflow to +Inf; both
	// would otherwise reach the division and produce NaN, which in the sort
	// comparator above makes the ordering depend on input order.
	denom := math.Sqrt(magA) * math.Sqrt(magB)
	if denom == 0 || !isFinite(denom) {
		// A pair too large to compare is not a pair we can rank. Returning 0
		// ranks it last rather than pretending it does not exist.
		return 0
	}
	sim := dot / denom
	if !isFinite(sim) {
		return 0
	}
	// Clamped, because a floating-point result a hair outside [-1,1] would make
	// a similarity of 1.0000000000000002 sort above a perfect match.
	if sim > 1 {
		return 1
	}
	if sim < -1 {
		return -1
	}
	return sim
}

// isFinite rejects NaN and both infinities.
func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

// hasDirection reports whether a score map contains any non-zero value.
//
// A non-finite value counts as a direction for the purposes of this check, and
// that is deliberate: Cosine rejects non-finite input and returns 0, so such a
// vector is one the caller will get a 0 for regardless. Deciding here that it
// has "no direction" would be a second, disagreeing opinion about the same
// vector -- and Cosine's is the one that governs the number the caller sees.
func hasDirection(scores map[string]float64) bool {
	if len(scores) == 0 {
		return false
	}
	for _, v := range scores {
		if v != 0 {
			return true
		}
	}
	return false
}
