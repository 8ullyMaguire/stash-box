//go:build integration

package api_test

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/collage"
)

// The collage persistence path, at the database level.
//
// The unit tests cover the sampling rule, which is pure. What they cannot reach
// is whether a generated collage actually lands: the claim, the optimistic
// concurrency on assignment, the regeneration replacing rather than accumulating,
// and the two distinct failures a scene with no duration produces.

// collageService builds a service over the test database.
//
// Through the Factory rather than by assembling queries and a WithTxnFunc by
// hand, because the Factory accessor is what production uses and a test that
// wired the service differently would not notice if the accessor were broken or
// missing.
func collageService(t *testing.T) *collage.Service {
	t.Helper()
	return dbtest.Factory().Collage()
}

// q is a direct query handle for fixture setup.
func q() *queries.Queries {
	return queries.New(dbtest.DB())
}

// intPtr is for the nullable scene duration.
func intPtr(v int) *int { return &v }

// strPtr is for the nullable scene title.
func strPtr(v string) *string { return &v }

// createSceneWithDuration inserts a scene with a known duration.
//
// Inserted directly rather than through the GraphQL create mutation, because the
// duration is the one field this test has to control exactly and a create input
// that changes shape would make every case in the file a maintenance problem.
func createSceneWithDuration(t *testing.T, name string, durationSeconds *int) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	_, err := q().CreateScene(t.Context(), queries.CreateSceneParams{
		ID:       id,
		Title:    strPtr(name),
		Duration: durationSeconds,
	})
	require.NoError(t, err, "creating the scene under test")
	return id
}

func TestSnapshotIsATimestampAndRejectsDuplicates(t *testing.T) {
	s := collageService(t)
	scene := createSceneWithDuration(t, "Snapshot Timestamp Scene", intPtr(600))

	_, err := s.AddSnapshot(t.Context(), scene, 90_000, nil)
	require.NoError(t, err, "a snapshot is a millisecond offset, not a stored image")

	// The same instant twice would render as one frame and count twice toward the
	// frame budget, so the unique constraint is real and the error says so.
	_, err = s.AddSnapshot(t.Context(), scene, 90_000, nil)
	assert.ErrorIs(t, err, collage.ErrDuplicateSnapshot,
		"two snapshots at the same instant render as one frame and count twice "+
			"against the budget; the refusal must name the problem, not surface "+
			"a raw 23505 naming an index")
}

func TestSnapshotRejectsANegativeTimestamp(t *testing.T) {
	s := collageService(t)
	scene := createSceneWithDuration(t, "Negative Timestamp Scene", intPtr(600))

	_, err := s.AddSnapshot(t.Context(), scene, -1, nil)
	assert.Error(t, err,
		"a negative timestamp is not a position in a video, and the database "+
			"would happily store it")
}

func TestGenerateNeedsADurationAndSaysSo(t *testing.T) {
	s := collageService(t)

	// No duration at all: a distinct error from "not enough snapshots", because
	// the fixes are different -- one needs a duration recorded, the other needs
	// more frames, and a user shown the wrong message learns nothing.
	noDuration := createSceneWithDuration(t, "No Duration Scene", nil)
	_, _, err := s.Generate(t.Context(), noDuration, 16)
	assert.ErrorIs(t, err, collage.ErrNoDuration,
		"a scene with no duration cannot be spaced across, and the caller must "+
			"be told the DURATION is the problem rather than the frame count")

	// A missing scene is a third error again: it is not a state to render, it is
	// a 404.
	_, _, err = s.Generate(t.Context(), uuid.Must(uuid.NewV7()), 16)
	assert.ErrorIs(t, err, collage.ErrSceneNotFound)
}

// A generated collage must land whole: the row, the frame count, and the claim on
// every frame -- all of it or none of it.
func TestGenerateProducesAWholeCollage(t *testing.T) {
	s := collageService(t)
	scene := createSceneWithDuration(t, "Whole Collage Scene", intPtr(600))

	// 30 snapshots across 10 minutes.
	for ts := int64(0); ts < 600_000; ts += 20_000 {
		_, err := s.AddSnapshot(t.Context(), scene, ts, nil)
		require.NoError(t, err)
	}

	coll, frames, err := s.Generate(t.Context(), scene, 16)
	require.NoError(t, err)
	require.NotNil(t, coll)
	assert.Equal(t, 16, coll.FrameCount)
	assert.Len(t, frames, 16, "the row and the returned frames must agree")

	// Every frame is a distinct snapshot, and they come back in order.
	seen := map[uuid.UUID]bool{}
	for i, f := range frames {
		assert.False(t, seen[f.SnapshotID], "frame %d repeats a snapshot", i)
		seen[f.SnapshotID] = true
		if i > 0 {
			assert.Less(t, frames[i-1].TimestampMS, f.TimestampMS,
				"frames must be in playback order; a collage renders in slice "+
					"order and a jumbled one plays backwards")
		}
		assert.GreaterOrEqual(t, f.Fraction, 0.0)
		assert.LessOrEqual(t, f.Fraction, 1.0,
			"the fraction is what a client with its own duration scrubs by, so it "+
				"must be a fraction")
	}

	// Reading it back must give the same frames.
	reread, err := s.ListFrames(t.Context(), scene)
	require.NoError(t, err)
	assert.Len(t, reread, 16, "reading a collage back must return what was stored")
	assert.Equal(t, frames[0].TimestampMS, reread[0].TimestampMS)
}

// Regeneration REPLACES, and the previous frames return to the pool rather than
// being deleted. Both halves matter and they fail in opposite directions.
func TestRegenerateReplacesRatherThanAccumulates(t *testing.T) {
	s := collageService(t)
	scene := createSceneWithDuration(t, "Regenerate Scene", intPtr(600))

	for ts := int64(0); ts < 600_000; ts += 20_000 {
		_, err := s.AddSnapshot(t.Context(), scene, ts, nil)
		require.NoError(t, err)
	}

	first, _, err := s.Generate(t.Context(), scene, 12)
	require.NoError(t, err)
	second, frames, err := s.Generate(t.Context(), scene, 16)
	require.NoError(t, err)
	require.NoError(t, err)

	assert.NotEqual(t, first.ID, second.ID, "a regeneration is a new collage row")
	assert.Equal(t, 16, second.FrameCount, "the new frame count is the one asked for")

	// One live collage per scene: a second generation must not leave the first
	// lying around for every reader to have to choose between.
	reread, err := s.ListFrames(t.Context(), scene)
	require.NoError(t, err)
	assert.Len(t, reread, 16,
		"exactly one collage may exist per scene, or every reader has to pick "+
			"one and most will not")

	// The snapshots themselves are untouched -- a re-roll is not a data loss.
	all, err := s.ListSnapshots(t.Context(), scene)
	require.NoError(t, err)
	assert.Len(t, all, 30, "regenerating must not delete curated snapshots: the "+
		"collage is a selection over them, and deleting a user's frames because "+
		"someone re-rolled would be data loss dressed as a cascade")
	assert.Len(t, frames, 16)
}

// A sparse scene cannot be made into a compliant collage, and the service says so
// rather than padding.
func TestGenerateRefusesToPadASparseCollage(t *testing.T) {
	s := collageService(t)
	scene := createSceneWithDuration(t, "Sparse Scene", intPtr(600))

	for _, ts := range []int64{0, 10_000, 20_000} {
		_, err := s.AddSnapshot(t.Context(), scene, ts, nil)
		require.NoError(t, err)
	}

	_, _, err := s.Generate(t.Context(), scene, 16)
	assert.ErrorIs(t, err, collage.ErrNotEnoughSnapshots,
		"3 snapshots cannot fill 16 slots, and returning a short collage "+
			"presented as a 16-frame one would be a lie about the scene's "+
			"identifiability")
}

// The frame-count bound is enforced before anything is written, so a bad request
// cannot leave a half-built collage behind.
func TestGenerateRejectsAFrameCountOutsideTheContract(t *testing.T) {
	s := collageService(t)
	scene := createSceneWithDuration(t, "Bad Frame Count Scene", intPtr(600))
	for ts := int64(0); ts < 600_000; ts += 20_000 {
		_, err := s.AddSnapshot(t.Context(), scene, ts, nil)
		require.NoError(t, err)
	}

	for _, n := range []int{1, 11, 25, 100} {
		_, _, err := s.Generate(t.Context(), scene, n)
		assert.Error(t, err, "a frame count of %d is outside SPEC §8's 12-24", n)
	}

	coll, err := s.Get(t.Context(), scene)
	require.NoError(t, err)
	assert.Nil(t, coll, "a refused generate must leave no collage behind")
}

// §7's curation quests and §3's preservation work both need this list, so it is
// tested as a query rather than left for a UI to discover.
func TestListUnderSnapshottedFindsThinScenes(t *testing.T) {
	s := collageService(t)

	thin := createSceneWithDuration(t, "Thin Scene", intPtr(600))
	_, err := s.AddSnapshot(t.Context(), thin, 1000, nil)
	require.NoError(t, err)

	wellCovered := createSceneWithDuration(t, "Well Covered Scene", intPtr(600))
	for ts := int64(0); ts < 600_000; ts += 20_000 {
		_, err := s.AddSnapshot(t.Context(), wellCovered, ts, nil)
		require.NoError(t, err)
	}

	under, err := s.ListUnderSnapshotted(t.Context(), 12, 100)
	require.NoError(t, err)

	found := map[uuid.UUID]int64{}
	for _, u := range under {
		found[u.SceneID] = u.SnapshotCount
	}

	assert.Contains(t, found, thin, "a scene with one snapshot is the definition "+
		"of under-snapshotted, and it is the input to SPEC §7's curation quests")
	assert.Equal(t, int64(1), found[thin])
	assert.NotContains(t, found, wellCovered,
		"a scene with 30 snapshots is not a curation target and listing it would "+
			"fill the quest queue with scenes that need nothing")
}

// A mutation that removes the optimistic-concurrency check on frame assignment
// SURVIVES this file, and that is worth saying out loud rather than letting a
// green suite imply otherwise.
//
// The check is real and load-bearing: AssignSnapshotsToCollage is a CLAIM (WHERE
// collage_id IS NULL), so two concurrent Generate calls for the same scene each
// read the same pool, each sample the same frames, and the second's claim comes
// up short. Without the count comparison the second commits a collage whose
// frame_count says 16 while fewer frames are assigned -- a broken strip that
// renders as though it were fine.
//
// Producing that race needs two transactions interleaving between the SELECT of
// the pool and the UPDATE that claims it. A sequential test cannot do it, and a
// test that injects a fault at the database level would be testing the mock. So
// the honest position is: the guard is verified by reading the query (it is one
// comparison against a returned row count) and NOT by a test, and a mutation
// removing it is a real gap in coverage rather than defence in depth.
//
// The cheapest thing that WOULD cover it: a test that calls Generate twice
// concurrently from two goroutines and asserts exactly one succeeds. Not written
// here because a flaky concurrency test is worse than an acknowledged gap -- it
// fails intermittently, and a test people learn to re-run is not evidence.
func TestConcurrentGenerateIsNotSimulatedHere(t *testing.T) {
	// A placeholder that documents the gap in executable form, so the next person
	// sees it in the test list rather than only in this comment.
	t.Log("acknowledged gap: AssignSnapshotsToCollage's row-count guard is " +
		"uncovered; see the comment above for why and for what would cover it")
}
