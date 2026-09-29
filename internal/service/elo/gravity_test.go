package elo

import (
	"math"
	"testing"
)

func TestGravityIsTheProductOfAllFourFactors(t *testing.T) {
	got := Gravity(GravityInput{
		LocalGravity:   1.2,
		PeerSimilarity: 0.8,
		PersonalTaste:  1.5,
		TrustWeight:    1.1,
	})

	want := 1.2 * 0.8 * 1.5 * 1.1
	if math.Abs(got.Score-want) > 1e-9 {
		t.Errorf("Score = %v, want the product %v", got.Score, want)
	}
}

// TestEveryFactorIndependentlyMovesTheScore is the anti-decoration test: a
// factor that stopped mattering would still leave the product looking plausible.
//
// The baseline for each case is the score with that ONE factor at its neutral
// value and the other three at 1.0, then the score with that factor moved away
// from neutral. The neutral value must not itself be 1.0 for every factor: if
// the baseline already had LocalGravity=1.0, a build that DROPPED the local term
// entirely would score the same 1.0 and the test would pass — which is exactly
// what happened the first time this test was written.
func TestEveryFactorIndependentlyMovesTheScore(t *testing.T) {
	neutral := GravityInput{
		LocalGravity:   1.0,
		PeerSimilarity: 1.0,
		PersonalTaste:  1.0,
		TrustWeight:    1.0,
	}

	cases := []struct {
		name string
		// moved sets the factor to a value clearly different from neutral.
		moved GravityInput
		// reference has the SAME factor at neutral, so the only difference is
		// the factor itself.
		reference GravityInput
	}{
		{
			name:      "instance gravity",
			moved:     GravityInput{LocalGravity: 3.0, PeerSimilarity: 1.0, PersonalTaste: 1.0, TrustWeight: 1.0},
			reference: GravityInput{LocalGravity: 1.0, PeerSimilarity: 1.0, PersonalTaste: 1.0, TrustWeight: 1.0},
		},
		{
			name:      "peer similarity",
			moved:     GravityInput{LocalGravity: 1.0, PeerSimilarity: 0.4, PersonalTaste: 1.0, TrustWeight: 1.0},
			reference: GravityInput{LocalGravity: 1.0, PeerSimilarity: 1.0, PersonalTaste: 1.0, TrustWeight: 1.0},
		},
		{
			name:      "personal taste",
			moved:     GravityInput{LocalGravity: 1.0, PeerSimilarity: 1.0, PersonalTaste: 1.6, TrustWeight: 1.0},
			reference: GravityInput{LocalGravity: 1.0, PeerSimilarity: 1.0, PersonalTaste: 1.0, TrustWeight: 1.0},
		},
		{
			name:      "trust weight",
			moved:     GravityInput{LocalGravity: 1.0, PeerSimilarity: 1.0, PersonalTaste: 1.0, TrustWeight: 1.75},
			reference: GravityInput{LocalGravity: 1.0, PeerSimilarity: 1.0, PersonalTaste: 1.0, TrustWeight: 1.0},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			moved := Gravity(tc.moved).Score
			reference := Gravity(tc.reference).Score

			if math.Abs(moved-reference) < 1e-9 {
				t.Errorf("%s: moving the factor from %v to %v left the score at %v, so the factor is ignored",
					tc.name, tc.reference, tc.moved, moved)
			}
		})
	}

	// And the neutral case must score exactly 1.0, which is what makes a
	// dropped term detectable above: dropping LocalGravity from this product
	// also yields 1.0, so this assertion is necessary but not sufficient — the
	// per-factor cases above are what carry the weight.
	if got := Gravity(neutral).Score; math.Abs(got-1.0) > 1e-9 {
		t.Errorf("all-neutral input scored %v, want 1.0", got)
	}
}

// TestDroppingAnyOneFactorChangesTheScore is the test that catches a factor
// being REMOVED from the product, which the per-factor test above cannot: moving
// a factor that is absent from the expression changes nothing either, and
// comparing a "moved" case against a "reference" case only proves the factor
// participates in the reference's own arithmetic.
//
// This compares two inputs differing ONLY in one factor and asserts the scores
// differ by exactly the expected ratio. If the factor is missing from the
// product, both scores collapse to the same value and the ratio is 1.0.
func TestDroppingAnyOneFactorChangesTheScore(t *testing.T) {
	cases := []struct {
		name      string
		a, b      GravityInput
		wantRatio float64
	}{
		{
			name:      "local gravity doubled",
			a:         GravityInput{LocalGravity: 1, PeerSimilarity: 1, PersonalTaste: 1, TrustWeight: 1},
			b:         GravityInput{LocalGravity: 2, PeerSimilarity: 1, PersonalTaste: 1, TrustWeight: 1},
			wantRatio: 2,
		},
		{
			name:      "peer similarity halved",
			a:         GravityInput{LocalGravity: 1, PeerSimilarity: 1, PersonalTaste: 1, TrustWeight: 1},
			b:         GravityInput{LocalGravity: 1, PeerSimilarity: 0.5, PersonalTaste: 1, TrustWeight: 1},
			wantRatio: 0.5,
		},
		{
			name:      "personal taste doubled",
			a:         GravityInput{LocalGravity: 1, PeerSimilarity: 1, PersonalTaste: 1, TrustWeight: 1},
			b:         GravityInput{LocalGravity: 1, PeerSimilarity: 1, PersonalTaste: 2, TrustWeight: 1},
			wantRatio: 2,
		},
		{
			// Doubling 1.0 to 2.0 would exceed MaxVoterWeight (1.75), so the
			// clamp makes the observed ratio 1.75. That is the ceiling doing
			// its job, and the test asserts the CLAMPED ratio on purpose: it
			// is also a check that trust is clamped on the way through the
			// product and not merely on the way into VoterWeight.
			name:      "trust weight doubled, clamped at the ceiling",
			a:         GravityInput{LocalGravity: 1, PeerSimilarity: 1, PersonalTaste: 1, TrustWeight: 1},
			b:         GravityInput{LocalGravity: 1, PeerSimilarity: 1, PersonalTaste: 1, TrustWeight: 2},
			wantRatio: MaxVoterWeight,
		},
		{
			// Same, but below the ceiling, where no clamp applies — so this
			// row proves the ratio is 1.75 and not an artefact of the clamp.
			name:      "trust weight 1.5, below the ceiling",
			a:         GravityInput{LocalGravity: 1, PeerSimilarity: 1, PersonalTaste: 1, TrustWeight: 1},
			b:         GravityInput{LocalGravity: 1, PeerSimilarity: 1, PersonalTaste: 1, TrustWeight: 1.5},
			wantRatio: 1.5,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sa := Gravity(tc.a).Score
			sb := Gravity(tc.b).Score

			if sa == 0 {
				t.Fatalf("reference score is zero, cannot compute a ratio")
			}
			if ratio := sb / sa; math.Abs(ratio-tc.wantRatio) > 1e-9 {
				t.Errorf("changing %s gave a ratio of %v, want %v — the factor is missing from the product "+
					"or is applied more than once", tc.name, ratio, tc.wantRatio)
			}
		})
	}
}

// TestTheProductIsThePoint — raising one factor can never cancel another. This
// is the property that distinguishes the product from a weighted sum, and it is
// why an operator cannot amplify a result by turning a policy slider to zero.
func TestOneFactorCannotCancelAnother(t *testing.T) {
	// A huge personal taste must not rescue a zero instance gravity.
	got := Gravity(GravityInput{
		LocalGravity:   0,
		PeerSimilarity: 1,
		PersonalTaste:  1000,
		TrustWeight:    1.75,
	})

	if got.Score != 0 {
		t.Errorf("Score = %v with zero instance gravity; a product must not be cancelled by another factor", got.Score)
	}
}

// TestZeroPersonalTasteFallsBackToInstanceTaste is the day-one-user decision the
// plan flags as the one that matters most. Reading (a) — a zero personal taste
// means no results — would hand every new user an empty feed on their first
// visit.
func TestZeroPersonalTasteFallsBackToInstanceTaste(t *testing.T) {
	got := Gravity(GravityInput{
		LocalGravity:   1.0,
		PeerSimilarity: 1.0,
		PersonalTaste:  0,
		TrustWeight:    1.0,
	})

	if got.Score <= 0 {
		t.Errorf("Score = %v, but a user with no taste history must still get instance-driven results", got.Score)
	}
	if !got.FellBackToInstanceTaste {
		t.Error("FellBackToInstanceTaste = false, but a zero personal taste must be reported as a fallback")
	}
	if got.PersonalTasteUsed != minPersonalTaste {
		t.Errorf("PersonalTasteUsed = %v, want the floor %v", got.PersonalTasteUsed, minPersonalTaste)
	}
}

// TestWeakPersonalTasteIsStillPulledDown proves the fallback is a substitution,
// not a clamp. A user with weak-but-present taste (0.05) must rank BELOW the
// substituted value (0.1), or "no taste" and "a little taste" collapse into one
// number and the recommender can no longer tell them apart — the same
// distinction taste_vectors preserves by storing no row at all for a user who
// has never voted.
func TestWeakPersonalTasteIsStillPulledDown(t *testing.T) {
	none := Gravity(GravityInput{LocalGravity: 1, PeerSimilarity: 1, PersonalTaste: 0, TrustWeight: 1})
	weak := Gravity(GravityInput{LocalGravity: 1, PeerSimilarity: 1, PersonalTaste: 0.05, TrustWeight: 1})
	strong := Gravity(GravityInput{LocalGravity: 1, PeerSimilarity: 1, PersonalTaste: 1.5, TrustWeight: 1})

	if !(none.Score > weak.Score) {
		t.Errorf("no-taste score %v must exceed weak-taste %v, or the fallback is a clamp "+
			"and the two cases become indistinguishable", none.Score, weak.Score)
	}
	if !(weak.Score < strong.Score) {
		t.Errorf("weak taste %v must rank below strong taste %v", weak.Score, strong.Score)
	}
	if !none.FellBackToInstanceTaste {
		t.Error("the no-taste case must report the fallback")
	}
	if weak.FellBackToInstanceTaste {
		t.Error("a user with weak-but-present taste is not a fallback case")
	}
}

// TestZeroTrustFallsBackToTheWeightFloor proves an untrusted reader is
// down-weighted, not excluded. Trust gates *content* access elsewhere; it must
// not gate discovery, or a new user would see nothing at all.
func TestZeroTrustFallsBackToTheWeightFloor(t *testing.T) {
	got := Gravity(GravityInput{
		LocalGravity:   1,
		PeerSimilarity: 1,
		PersonalTaste:  1,
		TrustWeight:    0,
	})

	if got.Score <= 0 {
		t.Errorf("Score = %v, but a reader with no trust must still get results, down-weighted", got.Score)
	}
}

// TestEveryInputStaysFiniteAndNonNegative sweeps the whole domain including
// NaN and infinities. A product of four factors has a large surface for
// producing NaN (0 * Inf), and NaN in a sort key silently destroys a ranking
// rather than failing loudly.
func TestEveryInputStaysFiniteAndNonNegative(t *testing.T) {
	values := []float64{math.NaN(), math.Inf(1), math.Inf(-1), -1000, -1, 0, 0.01, 0.5, 1, 1.75, 2, 1000}

	for _, local := range values {
		for _, sim := range values {
			for _, taste := range values {
				for _, trust := range values {
					got := Gravity(GravityInput{local, sim, taste, trust})
					if math.IsNaN(got.Score) {
						t.Fatalf("NaN score from local=%v sim=%v taste=%v trust=%v", local, sim, taste, trust)
					}
					if math.IsInf(got.Score, 0) {
						t.Fatalf("infinite score from local=%v sim=%v taste=%v trust=%v", local, sim, taste, trust)
					}
					if got.Score < 0 {
						t.Fatalf("negative score %v from local=%v sim=%v taste=%v trust=%v", got.Score, local, sim, taste, trust)
					}
				}
			}
		}
	}
}

// TestAnInstanceCanZeroOutACompletelyDissimilarPeer proves zero similarity is
// honoured rather than smoothed. If zero similarity defaulted to 1.0, an
// operator could not exclude a peer at all, and the peering tier would be
// advisory only.
func TestAnInstanceCanZeroOutACompletelyDissimilarPeer(t *testing.T) {
	got := Gravity(GravityInput{
		LocalGravity:   1,
		PeerSimilarity: 0,
		PersonalTaste:  1.5,
		TrustWeight:    1.75,
	})

	if got.Score != 0 {
		t.Errorf("Score = %v, but zero peer similarity must zero the result", got.Score)
	}
}

// TestPersonalTasteIsCapped proves an unbounded personal vector cannot pin a
// result in place and make the operator's configured gravity unreachable.
func TestPersonalTasteIsCapped(t *testing.T) {
	huge := Gravity(GravityInput{LocalGravity: 1, PeerSimilarity: 1, PersonalTaste: 1e9, TrustWeight: 1})
	capped := Gravity(GravityInput{LocalGravity: 1, PeerSimilarity: 1, PersonalTaste: maxPersonalTaste, TrustWeight: 1})

	if math.Abs(huge.Score-capped.Score) > 1e-9 {
		t.Errorf("a huge personal taste scored %v, above the cap's %v", huge.Score, capped.Score)
	}
}
