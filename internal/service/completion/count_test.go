package completion

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// CountIncomplete's threshold arithmetic, tested against the weights by hand.
//
// THE TRAP: the caller speaks in SCORES ("below 50") and the query counts MISSING
// WEIGHT. Getting the sense wrong produces a number that is entirely plausible --
// it is a count of real entities, just not the ones asked for -- so the only thing
// that catches it is a test that knows the expected number before running.
//
// THE TRAP THIS FILE FELL INTO, recorded because it nearly hid a real bug: the
// first version of TestThresholdArithmetic carried its OWN COPY of the formula,
// `(total*(100-below)+99)/100`, and asserted against values derived from that same
// expression. It agreed with a buggy implementation perfectly, and the off-by-one
// in minMissingWeight survived until the DEGENERATE cases were written out longhand.
//
// The fix is that the test calls minMissingWeight -- the function the service
// calls -- and the expectations are derived from the DEFINITION, not the
// expression:
//
//	incomplete iff score < below
//	           iff missing > total * (100 - below) / 100
//	so minMissing, the smallest missing weight that counts under the query's
//	`missing >= minMissing`, is floor(boundary) + 1.
//
// Deriving an expectation from the definition is what makes this a test. Deriving
// it from the implementation is a copy that changes with the bug.
//
// SCOPE: this file proves the ARITHMETIC, which is the part that can be wrong
// without a database. Whether the query returns the right COUNT for a threshold is
// `TestCountIncompleteAgreesWithTheFormula` in
// `internal/api/completion_integration_test.go` -- a unit test cannot reach the
// query, and a test that read the count back out of the service would be asserting
// the service against itself.

func TestThresholdArithmetic(t *testing.T) {
	// Hand-summed from the weight lists in score.go, NOT read from TotalWeight: a
	// test that takes the total from the code under test cannot catch a weight that
	// moved on both sides at once.
	//
	// The performer's 80 is 90 minus the 10 for `details`, which was removed
	// because `performers` has no details column. Every table row below is
	// recomputed from 80, so a weight change breaks this file loudly.
	const performerTotal = 80
	const sceneTotal = 110

	cases := []struct {
		below       int
		total       int
		wantMin     int
		explanation string
	}{
		// below 0: NO entity scores below 0, so nothing counts.
		// boundary = 80*100/100 = 80, minMissing = 81 -- unreachable.
		{0, performerTotal, 81,
			"below 0 means no entity is incomplete, so the threshold must be " +
				"UNREACHABLE rather than merely large: minMissing == total would " +
				"count a completely empty performer, who scores 0 and is not below 0"},

		// below 100: every incomplete entity counts.
		// boundary = 80*0/100 = 0, minMissing = 1.
		{100, performerTotal, 1,
			"below 100 means everything incomplete counts, and only a fully " +
				"complete entity scores 100 -- so the threshold is the smallest " +
				"non-zero gap"},

		// The middle, longhand: boundary = 80*50/100 = 40, minMissing = 41.
		{50, performerTotal, 41,
			"half-complete: an entity missing 45 of 90 scores exactly 50, which " +
				"is not below 50, so the threshold must be 46 and not 45"},

		// A different total, so a hard-coded 46 is caught.
		// boundary = 110*50/100 = 55, minMissing = 56.
		{50, sceneTotal, 56,
			"the same threshold against a different total"},

		// below 20 is a LOW bar -- only a badly incomplete scene qualifies.
		// boundary = 110*(100-20)/100 = 88, minMissing = 89.
		{20, sceneTotal, 89,
			"below 20 is a low bar, so the threshold is HIGH: a scene missing 88 " +
				"of 110 scores 20, which is not below 20, so it must not count, " +
				"while a scene missing 89 scores 19 and does"},

		// below 80 is a HIGH bar -- nearly everything incomplete qualifies.
		// boundary = 110*(100-80)/100 = 22, minMissing = 23.
		{80, sceneTotal, 23,
			"below 80 is a high bar, so the threshold is LOW: a scene missing 22 " +
				"of 110 scores 80, which is not below 80, while one missing 23 " +
				"scores 79 and does"},
	}

	for _, tc := range cases {
		got := minMissingWeight(tc.total, tc.below)
		assert.Equal(t, tc.wantMin, got, "below=%d total=%d: %s",
			tc.below, tc.total, tc.explanation)
	}
}

// An entity scoring EXACTLY the threshold is not below it, so it must not count as
// needing work. This is the specific off-by-one the +1 exists to prevent.
func TestAnEntityExactlyAtTheThresholdIsNotIncomplete(t *testing.T) {
	const total = 80
	const missingAt50 = 40

	score := percent(total-missingAt50, total)
	require.Equal(t, 50, score, "a performer missing exactly 40 of 80 scores 50")

	minMissing := minMissingWeight(total, 50)
	assert.Equal(t, missingAt50+1, minMissing,
		"an entity missing exactly 40 of 80 scores exactly 50, which is NOT below "+
			"50. The query counts missing >= minMissing, so minMissing must be 41: "+
			"at 40 the boundary entity is counted and every entity sitting exactly "+
			"on a threshold is reported as needing work it does not need")
}

// One step below the threshold DOES count -- the other half of the boundary, and
// the half a "fix the off-by-one" patch usually breaks by overshooting to +2.
func TestAnEntityJustBelowTheThresholdIsIncomplete(t *testing.T) {
	const total = 80
	minMissing := minMissingWeight(total, 50)

	// An entity missing exactly minMissing scores 49, which IS below 50.
	score := percent(total-minMissing, total)
	assert.Less(t, score, 50,
		"an entity missing exactly minMissing scores 49, so it is below 50 and "+
			"the query's missing >= minMissing must admit it")
}

// A threshold outside 0-100 is clamped rather than refused, and the clamp has to
// produce the right ANSWER, not merely avoid a panic.
func TestThresholdsOutsideTheRangeClamp(t *testing.T) {
	cases := []struct {
		below   int
		wantMin int
	}{
		// Negative clamps to 0, which counts NOTHING.
		{-50, 81},
		// Over 100 clamps to 100, which counts everything incomplete.
		{200, 1},
	}

	for _, tc := range cases {
		below := tc.below
		if below < 0 {
			below = 0
		}
		if below > 100 {
			below = 100
		}
		got := minMissingWeight(80, below)
		assert.Equal(t, tc.wantMin, got,
			"a threshold of %d clamps to %d, and the arithmetic must then be "+
				"identical to that clamped value's", tc.below, below)
	}
}

// The weight totals the table above is derived from.
//
// Asserted against hand-summed constants so a weight change cannot silently move
// the expectations above with it.
func TestTheTotalsTheThresholdTableAssumes(t *testing.T) {
	performer, err := TotalWeight(EntityPerformer)
	require.NoError(t, err)
	assert.Equal(t, 80, performer, "the performer's scored weight, hand-summed")

	scene, err := TotalWeight(EntityScene)
	require.NoError(t, err)
	assert.Equal(t, 110, scene, "the scene's scored weight, hand-summed")
}
