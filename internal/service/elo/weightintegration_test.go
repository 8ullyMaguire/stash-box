package elo

import (
	"math"
	"testing"
)

// The weight is on the Outcome now, which means the risk has moved: a field
// that is threaded through the call and then never read would compile, pass
// every existing test, and leave the feature inert. These tests are about the
// field being USED, not about the maths being right in the abstract.

// TestAWeightedVoteMovesTheRatingMoreThanAnUnweightedOne is the load-bearing
// property of SPEC §7.23 D1. If it fails, trust-weighting does nothing.
func TestAWeightedVoteMovesTheRatingMoreThanAnUnweightedOne(t *testing.T) {
	self := Rating{Rating: DefaultRating, Deviation: DefaultDeviation, Volatility: DefaultVolatility}
	opponent := Rating{Rating: DefaultRating, Deviation: DefaultDeviation, Volatility: DefaultVolatility}

	unweighted := self.Update(Outcome{
		Self: self, Opponent: opponent, Score: Win, ElapsedDays: 0, Weight: 1.0,
	})
	weighted := self.Update(Outcome{
		Self: self, Opponent: opponent, Score: Win, ElapsedDays: 0, Weight: 1.75,
	})

	unweightedMove := math.Abs(unweighted.Rating - self.Rating)
	weightedMove := math.Abs(weighted.Rating - self.Rating)

	if weightedMove <= unweightedMove {
		t.Errorf("a vote at the maximum weight moved the rating by %v, and a baseline vote moved it by %v; "+
			"the heavier vote must move it further", weightedMove, unweightedMove)
	}
}

// TestTheDeviationIsIndependentOfTheWeight names a limitation rather than
// hiding it.
//
// The weight is a K-factor: it scales the rating delta and not the variance, so
// two identical votes with different weights move the rating by different
// amounts and leave the SAME deviation. That is a real gap -- a trusted voter's
// result moves a rating without making the system more certain about it.
//
// It is asserted rather than left for someone to discover, because the natural
// assumption after a weight feature is that a heavier weight means more
// evidence, and here it demonstrably does not.
//
// Scaling the variance instead would fix the deviation and break monotonicity
// (see weightOf), so the trade is deliberate: a responsive ranking over a
// conservative uncertainty.
func TestTheDeviationIsAlmostIndependentOfTheWeight(t *testing.T) {
	self := Rating{Rating: DefaultRating, Deviation: DefaultDeviation, Volatility: DefaultVolatility}
	opponent := Rating{Rating: DefaultRating, Deviation: DefaultDeviation, Volatility: DefaultVolatility}

	light := self.Update(Outcome{Self: self, Opponent: opponent, Score: Win, Weight: 0.5})
	heavy := self.Update(Outcome{Self: self, Opponent: opponent, Score: Win, Weight: 1.75})

	// Not EXACTLY independent, and the comment should not pretend otherwise.
	// The weight does not enter v or phi*, but it does enter delta, and delta
	// feeds the volatility solve in Step 5. So a heavier vote moves sigma a
	// little, which moves phiStar a little. The magnitude is the point: 6e-6 on
	// a deviation of 290 is 2e-8 relative, i.e. the weight is a rounding error
	// to the uncertainty and a first-order effect on the rating. Asserting
	// exact equality here is asserting something false about the algorithm.
	relative := math.Abs(light.Deviation-heavy.Deviation) / light.Deviation
	if relative > 1e-6 {
		t.Errorf("deviation moved by %v relative with the weight: %v at 0.5 and %v at 1.75. "+
			"The weight is a K-factor and should barely reach the uncertainty; if it "+
			"now moves it substantially, weightOf's reasoning needs revisiting",
			relative, light.Deviation, heavy.Deviation)
	}

	// The rating must differ by a lot in comparison, or the test above is
	// passing because the weight does nothing at all.
	ratingRelative := math.Abs(light.Rating-heavy.Rating) / math.Abs(light.Rating)
	if ratingRelative < 1e-3 {
		t.Errorf("the two weights changed the rating by only %v relative; the weight is inert",
			ratingRelative)
	}
}

// TestAZeroWeightVoteIsNotSilentlyInert proves the zero case degrades to
// neutral rather than removing the game.
//
// A weight of zero would empty vSum, and UpdateBatch returns the rating
// unchanged on an empty vSum. So "weight zero" would mean "this vote is
// ignored" -- the exact opposite of what a stored vote row means, and a way to
// make a vote in the log silently not count.
func TestAZeroWeightVoteIsNotSilentlyInert(t *testing.T) {
	self := Rating{Rating: DefaultRating, Deviation: DefaultDeviation, Volatility: DefaultVolatility}
	opponent := Rating{Rating: DefaultRating, Deviation: DefaultDeviation, Volatility: DefaultVolatility}

	baseline := self.Update(Outcome{Self: self, Opponent: opponent, Score: Win, Weight: 1.0})

	for _, tc := range []struct {
		name   string
		weight float64
	}{
		{"zero", 0},
		{"negative", -1.0},
		{"NaN", math.NaN()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := self.Update(Outcome{Self: self, Opponent: opponent, Score: Win, Weight: tc.weight})
			if math.Abs(got.Rating-baseline.Rating) > 1e-9 {
				t.Errorf("weight %v gave a rating of %v, a baseline vote gave %v; "+
					"an unusable weight must degrade to the baseline, not to silence",
					tc.weight, got.Rating, baseline.Rating)
			}
		})
	}
}

// TestTheWeightAppliesToBothSidesOfTheMatchup proves the loser's rating moves
// by the same weight as the winner's.
//
// The weight is a property of the VOTE, not of the entity being rated. If it
// only reached the winner, a trusted vote would inflate one rating while barely
// touching the other, and the pair would stop summing to a constant -- a
// rating system that creates rating out of nothing.
func TestTheWeightAppliesToBothSidesOfTheMatchup(t *testing.T) {
	left := Rating{Rating: DefaultRating, Deviation: DefaultDeviation, Volatility: DefaultVolatility}
	right := Rating{Rating: DefaultRating, Deviation: DefaultDeviation, Volatility: DefaultVolatility}

	winnerAfter, loserAfter := left, right
	winnerAfter = winnerAfter.Update(Outcome{Self: left, Opponent: right, Score: Win, Weight: 1.75})
	loserAfter = loserAfter.Update(Outcome{Self: right, Opponent: left, Score: Loss, Weight: 1.75})

	if winnerAfter.Rating <= left.Rating {
		t.Fatalf("the winner's rating fell: %v -> %v", left.Rating, winnerAfter.Rating)
	}
	if loserAfter.Rating >= right.Rating {
		t.Fatalf("the loser's rating rose: %v -> %v", right.Rating, loserAfter.Rating)
	}

	// Symmetry: the same weight, so the magnitude of the two moves should match
	// when the two sides started identical. Glicko-2 is not exactly zero-sum in
	// deviation terms, so this is about the rating, not the pair.
	win := math.Abs(winnerAfter.Rating - left.Rating)
	lose := math.Abs(loserAfter.Rating - right.Rating)
	if math.Abs(win-lose) > 1e-6 {
		t.Errorf("the winner moved %v and the loser moved %v; with one weight on both "+
			"sides and identical starting ratings the moves should match", win, lose)
	}
}

// TestTheMonotonicityInWeightHoldsAcrossTheWholeRange proves no weight in the
// legal range produces a non-monotonic response.
//
// Sampled rather than checked at the endpoints, because a single inversion in
// the middle of the range would make one trust band worth less than a lower
// one -- a bug that two-point comparisons miss.
func TestTheMonotonicityInWeightHoldsAcrossTheWholeRange(t *testing.T) {
	self := Rating{Rating: DefaultRating, Deviation: DefaultDeviation, Volatility: DefaultVolatility}
	opponent := Rating{Rating: DefaultRating, Deviation: DefaultDeviation, Volatility: DefaultVolatility}

	prevMove := -1.0
	for _, w := range []float64{MinVoterWeight, 0.5, 0.75, 1.0, 1.25, 1.5, MaxVoterWeight} {
		got := self.Update(Outcome{Self: self, Opponent: opponent, Score: Win, Weight: w})
		move := math.Abs(got.Rating - self.Rating)

		if move < prevMove {
			t.Errorf("weight %v moved the rating %v, less than a lighter weight's %v; "+
				"the response must be monotonic in weight", w, move, prevMove)
		}
		prevMove = move
	}
}
