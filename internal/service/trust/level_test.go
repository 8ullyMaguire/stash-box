package trust

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The level curve, in isolation: no database, no service.
//
// SPEC §6 defines six levels. The curve itself is a product decision, and a
// product decision that cannot be tested is a product decision that will be
// changed by accident. These tests pin the shape of the curve, not the specific
// numbers -- but they do pin the one property that is a correctness question
// rather than a taste question: an account with no contributions must not be
// granted anything.

// The guard the plan calls out by name.
//
// A brand-new user has zero of everything. If a threshold bug makes level 0
// require one point, every new account becomes a Contributor with auto-approving
// edits, and nothing else in the system would catch it: there is no user, no
// edit and no event for the new account to be wrong about.
func TestZeroContributionIsLevelPublic(t *testing.T) {
	level := LevelForTotals(Totals{})

	assert.Equal(t, LevelPublic, level,
		"a user with no contributions must be level 0 (Public); anything higher "+
			"grants auto-approving edits to every new account")
}

// Negative totals must clamp rather than produce a level below Public.
//
// Defensive: the rollup counters are only ever incremented, so a negative total
// should be unreachable. But "should be" is not "cannot be" -- a manual database
// edit or a future refund-style negative event could produce one, and a level
// below Public would fall through every `level >= X` check in the codebase,
// including the content-viewing gate.
func TestNegativeTotalsClampToLevelPublic(t *testing.T) {
	level := LevelForTotals(Totals{ApprovedEdits: -100, RejectedEdits: -50})

	assert.Equal(t, LevelPublic, level,
		"a negative total must clamp to level 0, never fall below it: a level "+
			"under 0 would fail every `level >= X` gate in the codebase, "+
			"including content viewing")
}

// LevelForPoints is the only implementation of the curve, so it is the only
// place an off-by-one can live.
func TestLevelForPointsWalksTheCurve(t *testing.T) {
	for _, tc := range []struct {
		points int
		want   LevelEnum
	}{
		{points: 0, want: LevelPublic},
		{points: 9, want: LevelPublic},
		{points: 10, want: LevelRegistered},
		{points: 49, want: LevelRegistered},
		{points: 50, want: LevelContributor},
		{points: 199, want: LevelContributor},
		{points: 200, want: LevelCurator},
		{points: 749, want: LevelCurator},
		{points: 750, want: LevelArchivist},
		{points: 2499, want: LevelArchivist},
		{points: 2500, want: LevelSteward},
	} {
		assert.Equal(t, tc.want, LevelForPoints(tc.points),
			"%d points must be level %d", tc.points, tc.want)
	}
}

// A very large total must not produce a level outside the enum.
//
// The walk breaks at the first unmet threshold, so a user with a million points
// ends at the highest one. Without that, a bug that appended a threshold
// unconditionally would return a level no switch statement handles, and the
// failure would surface as a user silently having no permissions.
func TestAbsurdTotalStaysAtTheHighestLevel(t *testing.T) {
	level := LevelForPoints(1_000_000_000)

	assert.Equal(t, LevelSteward, level,
		"an absurd total must cap at the highest defined level, not exceed the "+
			"enum: an out-of-range level silently grants nothing and looks like "+
			"a permissions bug")
}

// The curve must be strictly increasing.
//
// A flat step (two levels needing the same points) means one of them is
// unreachable, and which one depends on iteration order. That is invisible in
// normal use and shows up months later as "nobody can ever become a Curator".
func TestThresholdsAreStrictlyIncreasing(t *testing.T) {
	require.NotEmpty(t, thresholds, "the curve must not be empty")

	for i := 1; i < len(thresholds); i++ {
		assert.Greater(t, thresholds[i].points, thresholds[i-1].points,
			"level %d must need strictly more than level %d, or one of them is "+
				"unreachable", thresholds[i].level, thresholds[i-1].level)
	}
}

// The curve must start at zero points.
//
// If the first threshold were above zero there would be no way to express
// "no contributions", and LevelForPoints would have to special-case it.
func TestCurveStartsAtZeroPoints(t *testing.T) {
	require.NotEmpty(t, thresholds)
	assert.Equal(t, 0, thresholds[0].points,
		"the first threshold must be 0 points so a zero contribution maps to a "+
			"level without a special case")
}

// LevelArchivist is the content boundary (SPEC §6), and it must be reachable.
func TestContentViewingLevelIsReachable(t *testing.T) {
	level := LevelForPoints(thresholds[4].points)

	assert.Equal(t, LevelContentViewing, level,
		"LevelContentViewing must be a level the curve can actually produce, "+
			"or content viewing is gated behind an unreachable state")
}

// Points must be a weighted sum, and every kind must contribute.
//
// A kind present in AllKinds but absent from Points would silently earn nothing
// -- and the rollup would still count it, so the totals and the score would
// disagree with no error anywhere.
func TestEveryKindHasPoints(t *testing.T) {
	for _, kind := range AllKinds {
		points, ok := PointsPerKind[kind]
		require.True(t, ok, "kind %q is in AllKinds but has no points weight", kind)
		assert.Positive(t, points,
			"kind %q must be worth a positive amount, or contributing it is pointless", kind)
	}

	assert.Len(t, PointsPerKind, len(AllKinds),
		"PointsPerKind and AllKinds must stay the same size: a weight with no "+
			"kind, or a kind with no weight, is a silent omission")
}

// A rejected edit must cost far less than an approved one.
//
// This is the weighting decision with a safety consequence: if rejections were
// worth as much as approvals, a user could reach Curator by mass-submitting
// edits that get rejected, which is an attack on the trust system rather than
// an accident.
func TestRejectedEditsAreWorthFarLessThanApproved(t *testing.T) {
	assert.Less(t, PointsPerKind[KindEditRejected], PointsPerKind[KindEditApproved]/4,
		"a rejected edit must be worth much less than an approved one, or a user "+
			"can farm trust by submitting edits that get rejected")
}

// The curve must get steep above the content boundary.
//
// Level 4 is the media-access line, so it must not be reachable by volume. A
// gentle slope here means a spammer who mass-submits acceptable edits unlocks
// content access.
func TestCurveIsSteepAboveTheContentBoundary(t *testing.T) {
	var contentIdx, topIdx int
	for i, t := range thresholds {
		if t.level == LevelContentViewing {
			contentIdx = i
		}
		if t.level == LevelSteward {
			topIdx = i
		}
	}
	require.NotZero(t, contentIdx, "LevelContentViewing must appear in the curve")
	require.Greater(t, topIdx, contentIdx, "LevelSteward must be above LevelContentViewing")

	contentStep := thresholds[contentIdx].points - thresholds[contentIdx-1].points
	topStep := thresholds[topIdx].points - thresholds[contentIdx].points

	assert.Greater(t, topStep, contentStep,
		"climbing from Archivist to Steward must cost more than reaching "+
			"Archivist, or the top of the trust system is cheaper than the "+
			"media-access line")
}
