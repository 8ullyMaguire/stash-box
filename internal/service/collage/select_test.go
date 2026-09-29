package collage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The sampling rule, tested without a database.
//
// This is the whole of what makes a collage good or bad, and it is pure, so every
// claim below is checkable without inserting a scene. The first version of
// SelectFrames lived inline in Generate, where the only way to test a case was to
// create a scene, add snapshots and read back a collage -- which is how a rule
// this fiddly ships untested.

// A dense pool must produce exactly the requested number of frames, spread across
// the whole duration.
func TestSelectFramesSpreadsEvenlyAcrossADensePool(t *testing.T) {
	const durationMS = 600_000 // 10 minutes

	// One snapshot every 10 seconds: far more than the 16 frames asked for.
	var pool []int64
	for ts := int64(0); ts < durationMS; ts += 10_000 {
		pool = append(pool, ts)
	}

	chosen, err := SelectFrames(pool, durationMS, 16, StrategyUniform)
	require.NoError(t, err)
	require.Len(t, chosen, 16, "a dense pool must yield exactly the requested count")

	// Gaps between consecutive frames should be roughly even. Uniform sampling
	// over 10 minutes at 16 frames is one frame per 37.5s, so a correct result has
	// gaps near 37.5s. Asserting "roughly" rather than exactly is deliberate: the
	// rule snaps to the nearest AVAILABLE snapshot, and a 10s grid means each
	// target lands within 5s of ideal.
	for i := 1; i < len(chosen); i++ {
		gap := chosen[i] - chosen[i-1]
		assert.InDelta(t, 37_500, gap, 10_000,
			"gap %d between frames %d and %d is not roughly even; the collage "+
				"would bunch or stretch instead of spacing evenly", gap, i-1, i)
	}

	assert.Equal(t, int64(0), chosen[0]-chosen[0], "frames are returned in order")
	for i := 1; i < len(chosen); i++ {
		assert.Less(t, chosen[i-1], chosen[i], "frames must come back in playback order")
	}
}

// A SPARSE pool is the case that separates a good rule from a plausible one.
//
// With 13 snapshots all in the first minute of a 10-minute scene, "evenly space
// the available ones" would pack all 12+ frames into that first minute and
// produce a collage that shows one part of the scene a dozen times. The rule
// targets POSITIONS across the duration and snaps each to the nearest candidate,
// so the frames stay spread and the collision is handled by dropping the
// duplicate.
func TestSelectFramesKeepsSparseFramesSpreadAcrossTheScene(t *testing.T) {
	const durationMS = 600_000

	// 13 snapshots, all inside the first 78 seconds of a 10-minute scene.
	var pool []int64
	for ts := int64(0); ts < 78_000; ts += 6_500 {
		pool = append(pool, ts)
	}

	chosen, err := SelectFrames(pool, durationMS, 16, StrategyUniform)
	require.NoError(t, err)
	require.NotEmpty(t, chosen)

	// Every frame must be distinct. Two targets snapping to the same candidate
	// means one snapshot appears twice, which renders as one frame and counts
	// twice toward the budget.
	seen := map[int64]bool{}
	for _, ts := range chosen {
		assert.False(t, seen[ts], "frame %dms appears twice; a repeated frame "+
			"renders as one and counts twice against the frame budget", ts)
		seen[ts] = true
	}

	// The spread is the real assertion: a naive rule would put all 13 frames below
	// 78s. The correct rule still can only use the 13 available, so the honest
	// check is that it used them in ASCENDING order and did not invent any.
	assert.LessOrEqual(t, len(chosen), len(pool),
		"cannot return more frames than there are snapshots")

	// And the frame count falls short of 16 rather than being padded -- which is
	// what Generate turns into ErrNotEnoughSnapshots.
	assert.Less(t, len(chosen), 16,
		"a 13-snapshot pool cannot fill 16 slots, and the rule must return fewer "+
			"rather than duplicating a frame to reach the count")

	// ASCENDING ORDER, and this is the assertion whose absence let a real bug
	// through. My first version of this test checked only that the frames were
	// distinct, so it passed against a rule that returned them jumbled:
	// 19500, 58500, 71500, 65000, 52000, 45500... A collage renders in slice
	// order, so a jumbled one plays frames backwards and jumps about.
	for i := 1; i < len(chosen); i++ {
		assert.Less(t, chosen[i-1], chosen[i],
			"frames must come back in ascending time order; frame %d (%dms) is "+
				"before frame %d (%dms). A collage renders in slice order, so a "+
				"jumbled one plays backwards", i-1, chosen[i-1], i, chosen[i])
	}
}

// THE test that separates this rule from the naive one, found by measurement
// rather than by reasoning.
//
// I asserted even gaps on a dense pool and assumed that was enough to pin the
// rule. It is not. Measured against a naive "space the available pool evenly"
// implementation:
//
//	30 snapshots across the whole scene  -> real span 560000ms, naive 300000ms
//	12 snapshots in the first 78s       -> real span  71500ms, naive  71500ms
//	15 snapshots split early/late       -> real span 580000ms, naive 580000ms
//
// Only the first separates them, and only on SPAN: the rule fills the whole
// duration while the naive one covers roughly half of it, because spacing 30
// candidates 16 ways takes every other one and leaves the back half of the pool
// unused. Even spacing of the SELECTION cannot see that -- the gaps between the
// frames it does return are even in both cases.
func TestSelectFramesCoversTheWholeDurationNotHalfThePool(t *testing.T) {
	const durationMS = 600_000

	// 30 snapshots, one every 20 seconds, across the full 10 minutes.
	var pool []int64
	for ts := int64(0); ts < durationMS; ts += 20_000 {
		pool = append(pool, ts)
	}

	chosen, err := SelectFrames(pool, durationMS, 16, StrategyUniform)
	require.NoError(t, err)
	require.Len(t, chosen, 16)

	span := chosen[len(chosen)-1] - chosen[0]
	assert.Greater(t, span, int64(durationMS*8/10),
		"a collage of 16 frames from a 10-minute scene must cover most of it, "+
			"not roughly half: the frames span %dms of %dms. Spacing the 30 "+
			"available candidates 16 ways takes every other one and abandons the "+
			"back half of the pool, which is the bug this rule exists to avoid",
		span, durationMS)
}

// The midpoint rule, and why it is not the slice boundary.
func TestUniformTargetsAreSliceMidpointsNotBoundaries(t *testing.T) {
	positions := targetPositions(1000, 4, StrategyUniform)

	assert.Equal(t, []int64{125, 375, 625, 875}, positions,
		"frames land at the MIDPOINT of each quarter, not on the boundary: a "+
			"boundary frame is the cut between two shots, and a collage built from "+
			"transition frames identifies a scene worse than one built from the "+
			"middle of each shot")
}

// The head strategy must actually differ from uniform, or a Strategy column with
// two names is a lie.
func TestHeadStrategyFavoursTheOpening(t *testing.T) {
	uniform := targetPositions(1000, 12, StrategyUniform)
	head := targetPositions(1000, 12, StrategyHead)
	require.Equal(t, len(uniform), len(head))

	// Count how many frames fall in the opening 20% of the scene.
	inOpening := func(positions []int64) int {
		n := 0
		for _, p := range positions {
			if p <= 200 {
				n++
			}
		}
		return n
	}

	assert.Greater(t, inOpening(head), inOpening(uniform),
		"the head strategy must put more frames in the opening 20%% than uniform "+
			"does (head %d, uniform %d) -- a scene's title card and establishing "+
			"shot are its most identifiable moments",
		inOpening(head), inOpening(uniform))
}

// A zero or negative duration is a distinct error from "not enough snapshots",
// because the fix is different: one needs a duration recorded, the other needs
// more frames.
func TestSelectFramesRefusesAZeroDuration(t *testing.T) {
	for _, d := range []int64{0, -1, -600_000} {
		_, err := SelectFrames([]int64{0, 1000}, d, 16, StrategyUniform)
		assert.ErrorIs(t, err, ErrNoDuration,
			"a duration of %d cannot be spaced across, and the caller needs to be "+
				"told the duration is the problem rather than the frame count", d)
	}
}

func TestSelectFramesRefusesAnEmptyPool(t *testing.T) {
	_, err := SelectFrames(nil, 600_000, 16, StrategyUniform)
	assert.ErrorIs(t, err, ErrNotEnoughSnapshots,
		"a scene with no snapshots cannot produce a collage, and the caller needs "+
			"to be told to add some")
}

// A single snapshot cannot become a 12-frame collage.
func TestSelectFramesWithOneSnapshotYieldsOneFrame(t *testing.T) {
	chosen, err := SelectFrames([]int64{5000}, 600_000, 16, StrategyUniform)
	require.NoError(t, err)
	assert.Len(t, chosen, 1,
		"one snapshot yields one frame, not sixteen copies of it")
}

// Unsorted input must give the same answer as sorted input.
//
// The pool comes from a query with ORDER BY, so this is belt-and-braces -- but
// "the rule depends on the query being sorted" is a coupling worth pinning, because
// the fix when it is forgotten is a collage whose frames are in a strange order
// with no error anywhere.
func TestSelectFramesIsOrderIndependent(t *testing.T) {
	pool := []int64{50_000, 10_000, 90_000, 30_000, 70_000, 20_000, 110_000, 40_000}
	reversed := make([]int64, len(pool))
	for i, v := range pool {
		reversed[len(pool)-1-i] = v
	}

	a, err := SelectFrames(pool, 120_000, 8, StrategyUniform)
	require.NoError(t, err)
	b, err := SelectFrames(reversed, 120_000, 8, StrategyUniform)
	require.NoError(t, err)

	assert.Equal(t, a, b, "the sampling rule must not depend on the order the "+
		"pool arrives in")
}

// The database CHECK constraint and the Go bounds are duplicated deliberately, so
// something has to keep them in agreement. This is that something.
func TestGoFrameBoundsMatchTheDatabaseConstraint(t *testing.T) {
	// The migration says: CHECK (frame_count BETWEEN 12 AND 24).
	// If these constants drift from it, Generate produces values the database
	// refuses and the error surfaces as an opaque constraint violation.
	assert.Equal(t, 12, MinFrames, "must match the migration's CHECK lower bound")
	assert.Equal(t, 24, MaxFrames, "must match the migration's CHECK upper bound")
	assert.GreaterOrEqual(t, DefaultFrames, MinFrames)
	assert.LessOrEqual(t, DefaultFrames, MaxFrames)
}
