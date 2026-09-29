package elo

import (
	"math"
	"testing"
)

// TestVoterWeightIsClampedToTheCeiling proves the cap is load-bearing: a
// maximum-level, maximum-contribution, vanguard voter — every factor at its
// strongest — must land exactly on the cap and not above it.
//
// The clamp is the safety property of the entire mechanism. If this test can be
// satisfied by a voter that simply scores high, then a sufficiently extreme
// voter escapes the cap and the cap does not exist.
func TestVoterWeightIsClampedToTheCeiling(t *testing.T) {
	got := VoterWeight(9999, true, math.MaxInt64)

	if got != MaxVoterWeight {
		t.Errorf("maximum voter weight = %v, want the cap %v", got, MaxVoterWeight)
	}
	if got > MaxVoterWeight {
		t.Errorf("maximum voter weight %v escaped the cap %v", got, MaxVoterWeight)
	}
}

// TestTheCapIsReachableByARealVoter proves the ceiling is not a limit that only
// absurd inputs can hit. A level-4 vanguard with a long record is a real user,
// so if the cap clamps them it is shaping real rankings; if it does not, the cap
// only guards against synthetic values and the mechanism is effectively
// unbounded for everyone real.
func TestTheCapIsReachableByARealVoter(t *testing.T) {
	realistic := VoterWeight(4, true, 100000)

	if realistic != MaxVoterWeight {
		t.Errorf("a level-4 vanguard with a long record got %v, want the cap %v", realistic, MaxVoterWeight)
	}
}

// TestEveryCombinationOfInputsStaysInBounds sweeps the whole input domain for
// signs of the cap or floor leaking. A clamp implemented as a pair of
// comparisons has an asymmetric failure: it can silently pass NaN, -Inf, and
// out-of-order tables while still clamping ordinary values correctly.
func TestEveryCombinationOfInputsStaysInBounds(t *testing.T) {
	levels := []int{-5, 0, 1, 2, 3, 4, 5, 1000}
	scores := []int64{math.MinInt64, -1, 0, 1, 100, 10000, 1 << 40, math.MaxInt64}

	for _, level := range levels {
		for _, score := range scores {
			for _, vanguard := range []bool{false, true} {
				got := VoterWeight(level, vanguard, score)

				if math.IsNaN(got) {
					t.Errorf("VoterWeight(%d, %v, %d) = NaN", level, vanguard, score)
				}
				if got < MinVoterWeight || got > MaxVoterWeight {
					t.Errorf("VoterWeight(%d, %v, %d) = %v, outside [%v, %v]",
						level, vanguard, score, got, MinVoterWeight, MaxVoterWeight)
				}
			}
		}
	}
}

// TestNaNWeightCollapsesToBaseline proves the NaN case is handled rather than
// passed through. NaN fails every comparison, so a naive `if w < min` /
// `if w > max` pair returns it unchanged and it reaches the database as a
// CHECK violation — a confusing failure far from its cause.
func TestNaNWeightCollapsesToBaseline(t *testing.T) {
	got := clampVoterWeight(math.NaN())

	if got != 1.0 {
		t.Errorf("clampVoterWeight(NaN) = %v, want the neutral baseline 1.0", got)
	}
}

// TestTrustBelowTheFirstBandStillReturnsAUsableWeight proves the level lookup
// does not fall through to zero. Trust level 0 cannot cast a vote at all, so
// this input is unreachable through the API — but a caller that reaches it must
// get a valid multiplier, not 0, because a 0 weight stored on a row is a vote
// that exists in history and counts for nothing.
func TestTrustBelowTheFirstBandStillReturnsAUsableWeight(t *testing.T) {
	got := VoterWeight(0, false, 0)

	if got < MinVoterWeight || got > MaxVoterWeight {
		t.Errorf("VoterWeight(0, false, 0) = %v, want a usable weight in [%v, %v]",
			got, MinVoterWeight, MaxVoterWeight)
	}
	if got <= 0 {
		t.Errorf("VoterWeight(0, false, 0) = %v, must never be zero or negative", got)
	}
}

// TestEachFactorIndependentlyIncreasesTheWeight proves the three factors are
// real inputs rather than decoration. Each row changes exactly one factor from
// a fixed baseline, so a factor that silently stopped mattering fails here.
func TestEachFactorIndependentlyIncreasesTheWeight(t *testing.T) {
	baseline := VoterWeight(2, false, 0)

	cases := []struct {
		name                 string
		level                int
		vanguard             bool
		score                int64
		wantStrictlyGreater  bool
		comparisonIsBaseline bool
	}{
		{name: "higher level", level: 3, vanguard: false, score: 0, wantStrictlyGreater: true, comparisonIsBaseline: true},
		{name: "contribution", level: 2, vanguard: false, score: 5000, wantStrictlyGreater: true, comparisonIsBaseline: true},
		{name: "vanguard", level: 2, vanguard: true, score: 0, wantStrictlyGreater: true, comparisonIsBaseline: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := VoterWeight(tc.level, tc.vanguard, tc.score)

			if tc.wantStrictlyGreater && got <= baseline {
				t.Errorf("%s: got %v, want strictly greater than the %v baseline", tc.name, got, baseline)
			}
		})
	}
}

// TestVanguardOutranksALevelBand proves vanguard status is the strongest single
// signal, which is the reason for it existing. A vanguard user at the same level
// and contribution as a non-vanguard must outweigh them, or the status carries
// no weight in the ranking at all.
func TestVanguardOutranksALevelBand(t *testing.T) {
	plain := VoterWeight(3, false, 1000)
	vanguard := VoterWeight(3, true, 1000)

	if vanguard <= plain {
		t.Errorf("vanguard weight %v does not exceed the same user's non-vanguard weight %v", vanguard, plain)
	}
}

// TestContributionMultiplierSaturates proves the contribution curve stops
// rewarding without limit. Without saturation, the final cap becomes unreachable
// dead code for any long-tenured account, and the ceiling stops being a real
// bound.
func TestContributionMultiplierSaturates(t *testing.T) {
	atSaturation := contributionMultiplier(int64(contributionSaturation))
	beyondSaturation := contributionMultiplier(math.MaxInt64)

	if atSaturation != maxContributionMult {
		t.Errorf("contributionMultiplier(saturation) = %v, want %v", atSaturation, maxContributionMult)
	}
	if beyondSaturation != maxContributionMult {
		t.Errorf("contributionMultiplier(MaxInt64) = %v, want it to stay at %v", beyondSaturation, maxContributionMult)
	}
}

// TestTheFloorIsNotReachableByRealInputs measures the domain instead of
// asserting a guess. This is the test that explains why deleting the lower
// clamp does not fail anything: no legal input produces a weight near the
// floor, so the clamp is a backstop against future change rather than a bound
// that shapes today's rankings.
//
// It is written as a sweep with a logged minimum rather than an equality
// assertion so that a future change to the band table shows up as a changed
// number a reviewer has to look at, instead of failing mysteriously.
func TestTheFloorIsNotReachableByRealInputs(t *testing.T) {
	lowest := math.MaxFloat64
	var lowestLevel int
	var lowestVanguard bool
	var lowestScore int64

	for _, level := range []int{0, 1, 2, 3, 4, 5, 99} {
		for score := int64(-1000); score < 200000; score += 97 {
			for _, vanguard := range []bool{false, true} {
				got := VoterWeight(level, vanguard, score)
				if got < lowest {
					lowest, lowestLevel, lowestVanguard, lowestScore = got, level, vanguard, score
				}
			}
		}
	}

	t.Logf("lowest reachable weight = %v at level=%d vanguard=%v score=%d (floor=%v)",
		lowest, lowestLevel, lowestVanguard, lowestScore, MinVoterWeight)

	if lowest <= MinVoterWeight {
		t.Errorf("lowest reachable weight %v sits at or below the floor %v, so the floor is shaping real input — "+
			"if that is intended, the comment on MinVoterWeight is wrong", lowest, MinVoterWeight)
	}
	if lowest <= 0 {
		t.Errorf("lowest reachable weight is %v, which must never be zero or negative", lowest)
	}
}

// TestClampVoterWeightEnforcesTheFloorDirectly covers the backstop itself. The
// clamp is not reachable through VoterWeight, so this is the only place its
// lower half can be observed — and the reason the lower clamp exists at all is
// that this function is the boundary every factor passes through.
func TestClampVoterWeightEnforcesTheFloorDirectly(t *testing.T) {
	cases := []struct {
		name string
		in   float64
		want float64
	}{
		{name: "zero", in: 0, want: MinVoterWeight},
		{name: "negative", in: -3.5, want: MinVoterWeight},
		{name: "just below the floor", in: MinVoterWeight - 0.001, want: MinVoterWeight},
		{name: "exactly the floor is left alone", in: MinVoterWeight, want: MinVoterWeight},
		{name: "inside the range is untouched", in: 1.2, want: 1.2},
		{name: "exactly the ceiling is left alone", in: MaxVoterWeight, want: MaxVoterWeight},
		{name: "just above the ceiling", in: MaxVoterWeight + 0.001, want: MaxVoterWeight},
		{name: "infinity clamps to the ceiling", in: math.Inf(1), want: MaxVoterWeight},
		{name: "negative infinity clamps to the floor", in: math.Inf(-1), want: MinVoterWeight},
		{name: "NaN collapses to baseline", in: math.NaN(), want: 1.0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := clampVoterWeight(tc.in); got != tc.want {
				t.Errorf("clampVoterWeight(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestANegativeContributionScoreIsNeutral proves a corrupt or nonsensical score
// cannot reduce a voter's weight. A negative score is not a signal — it is bad
// data — and letting it discount a vote would turn a data-integrity bug into a
// silent ranking manipulation.
func TestANegativeContributionScoreIsNeutral(t *testing.T) {
	zero := VoterWeight(3, false, 0)
	negative := VoterWeight(3, false, -5000)

	if negative != zero {
		t.Errorf("a negative contribution score changed the weight: %v vs %v at zero", negative, zero)
	}
}
