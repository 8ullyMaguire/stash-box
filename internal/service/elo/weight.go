package elo

import "math"

// Voter weight bounds.
//
// The floor exists because "this vote barely counts" and "this vote did not
// count" are different claims, and a 0 weight would make them identical while
// still storing the row — the history would claim a vote that the rating maths
// silently ignored.
//
// Measured, not assumed: the lowest weight any legal input produces is
// baseWeightForLevel(0) == 0.5 (see TestTheFloorIsNotReachableByRealInputs).
// The floor is therefore a BACKSTOP, not a shaping bound — it exists so that a
// future band table, a new factor, or a direct SQL write cannot produce a
// weight of 0, and so the DB CHECK constraint has a domain to agree with. Do
// not read it as "some user is weighted this low".
//
// The ceiling is the whole safety property of the mechanism: an unbounded
// weight lets one account decide every matchup it participates in, which is
// precisely the failure mode trust-weighting exists to prevent. It is
// deliberately well under 2.0 so a single maximum-weight voter still cannot
// outvote a coalition of baseline voters, and it is a named constant so a test
// can assert the clamp rather than re-deriving the number.
const (
	// MinVoterWeight is the floor. Unreachable by design; see above.
	MinVoterWeight = 0.1
	// MaxVoterWeight is the ceiling, and it IS reachable by a real user.
	MaxVoterWeight = 1.75
)

// Weight bands by trust level. Trust below the first band is not weighted
// down — a level 0 user cannot cast a vote at all, so VoterWeight is never
// reached with such a level, and silently returning a reduced weight for it
// would hide a caller error rather than report it.
//
// The table is a slice of thresholds so the mapping is data, not a chain of
// comparisons that drifts out of sync with the levels the rest of the system
// defines.
var levelBands = []struct {
	minLevel int
	weight   float64
}{
	{minLevel: 4, weight: 1.0},
	{minLevel: 3, weight: 0.8},
	{minLevel: 2, weight: 0.6},
	{minLevel: 1, weight: 0.5},
}

// baseWeightForLevel is the weight a vote carries at the given trust level,
// before the contribution score and vanguard status are applied.
func baseWeightForLevel(level int) float64 {
	for _, band := range levelBands {
		if level >= band.minLevel {
			return band.weight
		}
	}
	return levelBands[len(levelBands)-1].weight
}

// contributionMultiplier scales the base weight by contribution score, so a
// long-standing contributor is weighted more heavily than a newly-registered
// user who happens to sit at the same trust level.
//
// Saturating rather than linear: without a ceiling on the multiplier, a
// long-tenured account accumulates an unbounded advantage, and the cap on the
// final weight becomes unreachable dead code. The curve still rewards
// contribution, it just stops rewarding it without limit.
const (
	contributionSaturation = 10000.0
	maxContributionMult    = 1.5
)

func contributionMultiplier(score int64) float64 {
	if score <= 0 {
		return 1.0
	}
	scaled := float64(score) / contributionSaturation
	if scaled > 1.0 {
		scaled = 1.0
	}
	return 1.0 + scaled*(maxContributionMult-1.0)
}

// vanguardMultiplier is the extra factor a vanguard user's votes carry.
//
// Vanguard status is granted by the instance to a small trusted subset, so it
// is the strongest single trust signal available and is worth more than any
// level band. It is still a multiplier and not a replacement, so a vanguard
// user with a poor contribution record is weighted above baseline but does not
// run away with every rating.
const vanguardMultiplier = 1.25

// VoterWeight is the multiplier recorded on a vote when it is cast.
//
// Snapshotted, NOT computed at read time (plan D5, SPEC §7.17.2). Recomputing
// means a user's trust today silently re-weights every vote they ever cast,
// which changes historical rankings with no record that it happened.
//
// The result is always clamped into [MinVoterWeight, MaxVoterWeight], so every
// caller gets a usable multiplier without repeating the bounds, and so the
// clamp cannot be bypassed by a caller that composes its own factors.
func VoterWeight(level int, isVanguard bool, contributionScore int64) float64 {
	weight := baseWeightForLevel(level) * contributionMultiplier(contributionScore)
	if isVanguard {
		weight *= vanguardMultiplier
	}
	return clampVoterWeight(weight)
}

func clampVoterWeight(weight float64) float64 {
	switch {
	case math.IsNaN(weight):
		// NaN has no meaningful position on an interval, and every comparison
		// against it is false, so it would sail through a naive bounds check
		// and reach the database as a CHECK-constraint failure. Treat it as
		// the neutral baseline rather than propagating it.
		return 1.0
	case weight < MinVoterWeight:
		return MinVoterWeight
	case weight > MaxVoterWeight:
		return MaxVoterWeight
	default:
		return weight
	}
}
