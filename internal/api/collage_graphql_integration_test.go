//go:build integration

package api_test

import (
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/queries"
)

// Collages over GraphQL, end to end (SPEC §7.25.1, growth item 12).
//
// collage_integration_test.go proves the persistence path and the sampling rule.
// This file proves the WIRING, which is the entire gap this feature closed: the
// service was complete and tested while no GraphQL type could read any of it.
//
// So every assertion here is about something a service-level test cannot see:
//
//   - that the query and mutation names exist and are spelled the way a client
//     will spell them,
//   - that `stale` is computed against the scene's CURRENT duration, so a client
//     can tell a collage generated before a duration correction from one made
//     after,
//   - that `fraction` is recomputed per request rather than served from a stored
//     value, which is the entire reason the field exists,
//   - and that a scene with no duration reports UNKNOWN rather than 0.0.
//
// Field names match the GraphQL selection sets exactly, because gqlgen decodes by
// name and panics on a key it does not recognise.

// correctSceneDuration changes a scene's duration AFTER a collage was generated.
//
// Through queries.UpdateScene rather than raw SQL, because duration is one column
// of a nine-column UPDATE and the other eight are NOT NULL or carry defaults. There
// is no UpdateSceneDuration query -- an earlier draft called one and it does not
// exist, which the compiler caught immediately.
//
// Returns the updated row so a caller can assert the correction actually landed: a
// silently-ignored UPDATE would make every staleness test pass for the wrong
// reason.
func correctSceneDuration(t *testing.T, sceneID string, seconds int) {
	t.Helper()
	id, err := uuid.FromString(sceneID)
	require.NoError(t, err, "scene id must parse")
	row, err := q().UpdateScene(t.Context(), queries.UpdateSceneParams{
		ID:       id,
		Title:    strPtr("A Duration-Corrected Scene"),
		Duration: &seconds,
	})
	require.NoError(t, err, "correcting the scene duration")
	require.Equal(t, &seconds, row.Duration,
		"the duration correction must actually land, or every staleness "+
			"assertion below passes against an unchanged scene")
}

// seedSnapshots claims n frames evenly across a 100-second scene.
//
// SPEC §8's floor is 12 frames and the service rejects fewer, so every test that
// generates a collage must seed at least that many. Seeding two "because two is
// enough to prove something" is how a test ends up failing on a range constraint
// instead of on the behaviour it was written to check.
func seedSnapshots(t *testing.T, r *testRunner, sceneID string, n int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		addSnapshot(t, r, sceneID, i*5000)
	}
}

// clearSceneDuration REMOVES a scene's duration, which is distinct from setting it
// to zero: NULL means "unknown length", and a scene can reach that state after a
// collage was generated -- which is the only way a stored frame meets a nil
// duration.
func clearSceneDuration(t *testing.T, sceneID string) {
	t.Helper()
	id, err := uuid.FromString(sceneID)
	require.NoError(t, err)
	_, err = q().UpdateScene(t.Context(), queries.UpdateSceneParams{
		ID:    id,
		Title: strPtr("A Duration-Cleared Scene"),
	})
	require.NoError(t, err, "clearing the scene duration")
}

// A scene with a known duration, so fractions are computable and assertable.
func collageScene(t *testing.T, name string, durationSeconds *int) string {
	t.Helper()
	id := createSceneWithDuration(t, name, durationSeconds)
	return id.String()
}

// addSnapshot claims a frame, returning its timestamp as the client would see it.
func addSnapshot(t *testing.T, r *testRunner, sceneID string, timestampMS int) int {
	t.Helper()
	var resp struct {
		AddSnapshot struct {
			Timestamp int
		}
	}
	r.client.MustPost(`
		mutation($s: ID!, $t: Int!) {
			addSnapshot(sceneID: $s, timestamp: $t) { timestamp }
		}
	`, &resp,
		client.Var("s", sceneID),
		client.Var("t", timestampMS))
	return resp.AddSnapshot.Timestamp
}

// A full round trip: claim frames, generate a collage, read it back.
//
// One test rather than one per step, because the thing being proved is that the
// chain is connected. A collage that generates but is unreadable, or one whose
// frames come back empty, is exactly the state this feature shipped in.
func TestCollageRoundTripThroughGraphQL(t *testing.T) {
	r := asRead(t)
	// 100 seconds. Timestamps below are milliseconds, so a frame at 50000ms is
	// the midpoint and its fraction must be 0.5.
	sceneID := collageScene(t, "A Collage Scene", intPtr(100))

	// Sparse snapshots, deliberately. Fewer snapshots than requested frames is the
	// normal case -- the sampler snaps each target to the nearest available claim,
	// so this exercises that path rather than an exact-arithmetic path.
	// SPEC §8's floor is 12 frames, and the service rejects anything below it.
	// Sixteen claims spread across the scene, so the sampler has genuine choices
	// and a real midpoint to report a fraction for.
	for i := 1; i <= 16; i++ {
		addSnapshot(t, r, sceneID, i*5000)
	}

	var gen struct {
		GenerateCollage struct {
			FrameCount int
			Stale      bool
			Duration   *int
			// Frames must come back populated: a collage that reports a frame count
			// but hands back nothing is the failure mode a `frameCount`-only test
			// would pass straight over.
			Frames []struct {
				Timestamp int
				Fraction  float64
				Snapshot  struct {
					ID        string
					Timestamp int
				}
			}
		}
	}
	r.client.MustPost(`
		mutation($s: ID!) {
			generateCollage(sceneID: $s, frameCount: 12) {
				frameCount stale duration
				frames { timestamp fraction snapshot { id timestamp } }
			}
		}
	`, &gen, client.Var("s", sceneID))

	g := gen.GenerateCollage
	assert.Equal(t, 12, g.FrameCount, "the requested frame count must be what is reported")
	require.NotNil(t, g.Duration, "a scene with a duration must report it")
	assert.Equal(t, 100000, *g.Duration, "100 seconds is 100000 milliseconds")
	assert.False(t, g.Stale, "a collage generated moments ago cannot be stale")

	require.NotEmpty(t, g.Frames, "a generated collage must return frames")
	for _, f := range g.Frames {
		// The midpoint frame is the one with an exactly checkable fraction.
		if f.Timestamp == 50000 {
			assert.InDelta(t, 0.5, f.Fraction, 0.001,
				"a frame at 50s of a 100s scene is at fraction 0.5")
			assert.NotEmpty(t, f.Snapshot.ID,
				"a frame must resolve back to the claim it came from")
			assert.Equal(t, 50000, f.Snapshot.Timestamp,
				"the resolved snapshot must be the one at that timestamp")
		}
	}
}

// `stale` is the field that makes a duration correction survivable, so it has to
// track the scene's CURRENT duration rather than whatever the collage recorded.
func TestCollageStaleTracksTheCurrentDuration(t *testing.T) {
	r := asRead(t)
	sceneID := collageScene(t, "A Stale Collage Scene", intPtr(100))
	seedSnapshots(t, r, sceneID, 16)

	r.client.MustPost(`
		mutation($s: ID!) {
			generateCollage(sceneID: $s, frameCount: 12) { stale }
		}
	`, &struct {
		GenerateCollage struct{ Stale bool }
	}{}, client.Var("s", sceneID))

	// Correct the scene's duration behind the collage's back. The collage was
	// sampled against 100s; if `stale` is stored rather than computed, it cannot
	// possibly notice this.
	correctSceneDuration(t, sceneID, 200)

	var resp struct {
		SceneCollage struct {
			Stale    bool
			Duration *int
			// SourceDuration is what the sampler believed, and it must STILL say
			// 100s. If the correction rewrote sourceDuration too, a client would
			// have no way to tell what the collage was actually generated against --
			// which is the diagnostic the field exists to provide.
			SourceDuration *int
		}
	}
	r.client.MustPost(`
		query($s: ID!) {
			sceneCollage(sceneID: $s) { stale duration sourceDuration }
		}
	`, &resp, client.Var("s", sceneID))

	assert.True(t, resp.SceneCollage.Stale,
		"a collage generated against 100s must read stale once the scene says 200s")
	require.NotNil(t, resp.SceneCollage.SourceDuration)
	assert.Equal(t, 100000, *resp.SceneCollage.SourceDuration,
		"the duration the sampler used must be preserved, not overwritten")
	require.NotNil(t, resp.SceneCollage.Duration)
	assert.Equal(t, 200000, *resp.SceneCollage.Duration,
		"duration must report what the scene records NOW")
}

// Fractions must be recomputed against the current duration, which is the only
// reason the field is worth sending alongside the timestamp.
func TestCollageFractionFollowsTheCorrectedDuration(t *testing.T) {
	r := asRead(t)
	sceneID := collageScene(t, "A Refractioned Scene", intPtr(100))
	seedSnapshots(t, r, sceneID, 16)

	r.client.MustPost(`
		mutation($s: ID!) { generateCollage(sceneID: $s, frameCount: 12) { frameCount } }
	`, &struct {
		GenerateCollage struct{ FrameCount int }
	}{}, client.Var("s", sceneID))

	correctSceneDuration(t, sceneID, 200)

	var resp struct {
		SceneCollage struct {
			Frames []struct {
				Timestamp int
				Fraction  float64
			}
		}
	}
	r.client.MustPost(`
		query($s: ID!) {
			sceneCollage(sceneID: $s) { frames { timestamp fraction } }
		}
	`, &resp, client.Var("s", sceneID))

	require.NotEmpty(t, resp.SceneCollage.Frames)
	for _, f := range resp.SceneCollage.Frames {
		if f.Timestamp == 30000 {
			assert.InDelta(t, 0.15, f.Fraction, 0.001,
				"30s is 0.15 of a 200s scene; a fraction frozen at generation time "+
					"would still read 0.3 and scrub a client to the wrong frame")
		}
		_ = resp
	}
}

// A scene with no duration must report UNKNOWN position, not the start of the
// scene.
func TestCollageFractionIsUnknownWithoutADuration(t *testing.T) {
	r := asRead(t)
	// No duration at all: the ErrNoDuration state, not a duration of zero.
	sceneID := collageScene(t, "A Durationless Scene", nil)
	// One claim, so the snapshots query has something to return. Claiming a position
	// needs no duration -- a frame claim is "there is something at 1 second", which
	// is a fact about the scene's content rather than its length.
	addSnapshot(t, r, sceneID, 1000)

	var resp struct {
		SceneSnapshots []struct {
			Timestamp int
		}
		SceneCollage *struct {
			Stale bool
		}
	}
	r.client.MustPost(`
		query($s: ID!) {
			sceneSnapshots(sceneID: $s) { timestamp }
			sceneCollage(sceneID: $s) { stale }
		}
	`, &resp, client.Var("s", sceneID))

	// Snapshots are claims about positions, and a scene with no recorded duration
	// can still have them -- so they must read.
	require.Len(t, resp.SceneSnapshots, 1, "the claim itself must be readable")
	assert.Equal(t, 1000, resp.SceneSnapshots[0].Timestamp)

	// NULL, not an empty collage. Generate returns ErrNoDuration for a scene with no
	// duration, so a collage for one cannot exist -- and "no collage yet" is an
	// invitation to contribute while "a collage with no frames" is a bug. Returning
	// an object here would report the latter for what is the former.
	assert.Nil(t, resp.SceneCollage,
		"a scene with no duration can have no collage; it must read as null")

	// And generating one must fail loudly rather than produce a collage with frames
	// at made-up positions.
	var gen struct {
		GenerateCollage struct{ Stale bool }
	}
	err := r.client.Post(`
		mutation($s: ID!) { generateCollage(sceneID: $s, frameCount: 12) { stale } }
	`, &gen, client.Var("s", sceneID))
	require.Error(t, err,
		"generating frames for a scene of unknown length would put them at "+
			"positions derived from nothing")
	assert.Contains(t, err.Error(), "duration",
		"the error must name the missing duration, not fail on a range or an auth check")

	// A duration can also be CLEARED after a collage exists, which is the only way
	// a stored frame reaches the fraction branch with no duration at all. That is
	// the case the nil guard exists for, and it is unreachable from a scene that
	// never had a duration -- a mutation that removes the guard survives every other
	// test in this file, which is exactly what the mutation harness reported.
	durated := collageScene(t, "A Duration-Cleared Scene", intPtr(100))
	seedSnapshots(t, r, durated, 16)
	r.client.MustPost(`
		mutation($s: ID!) { generateCollage(sceneID: $s, frameCount: 12) { frameCount } }
	`, &struct {
		GenerateCollage struct{ FrameCount int }
	}{}, client.Var("s", durated))
	clearSceneDuration(t, durated)

	var cleared struct {
		SceneCollage struct {
			Duration *int
			Frames   []struct {
				Fraction *float64
			}
		}
	}
	r.client.MustPost(`
		query($s: ID!) { sceneCollage(sceneID: $s) { duration frames { fraction } } }
	`, &cleared, client.Var("s", durated))

	assert.Nil(t, cleared.SceneCollage.Duration,
		"a cleared duration must read null, not zero")
	require.NotEmpty(t, cleared.SceneCollage.Frames,
		"the frames still exist; only the scene's length is now unknown")
	for _, fr := range cleared.SceneCollage.Frames {
		assert.Nil(t, fr.Fraction,
			"with no duration the position is UNKNOWN, and 0.0 is a real position "+
				"(the very start of a scene). Reporting 0.0 would scrub a client to "+
				"the opening frame while looking like a valid answer.")
	}
}

// The snapshots query is a scrub bar, so order is part of its contract.
func TestSceneSnapshotsAreReturnedInTimestampOrder(t *testing.T) {
	r := asRead(t)
	sceneID := collageScene(t, "A Scrub Bar Scene", intPtr(100))

	// Claimed out of order on purpose.
	for _, ms := range []int{70000, 10000, 40000} {
		addSnapshot(t, r, sceneID, ms)
	}

	var resp struct {
		SceneSnapshots []struct {
			Timestamp int
		}
	}
	r.client.MustPost(`
		query($s: ID!) { sceneSnapshots(sceneID: $s) { timestamp } }
	`, &resp, client.Var("s", sceneID))

	require.Len(t, resp.SceneSnapshots, 3)
	got := []int{
		resp.SceneSnapshots[0].Timestamp,
		resp.SceneSnapshots[1].Timestamp,
		resp.SceneSnapshots[2].Timestamp,
	}
	assert.Equal(t, []int{10000, 40000, 70000}, got,
		"a snapshot list is a scrub bar; unsorted timestamps make every client "+
			"re-sort the same data")
}

// A negative timestamp is not a position in a scene, and must be rejected before
// it reaches storage.
func TestAddSnapshotRejectsANegativeTimestamp(t *testing.T) {
	r := asRead(t)
	sceneID := collageScene(t, "A Negative Scene", intPtr(100))

	var resp struct {
		AddSnapshot struct {
			Timestamp int
		}
	}
	err := r.client.Post(`
		mutation($s: ID!, $t: Int!) { addSnapshot(sceneID: $s, timestamp: $t) { timestamp } }
	`, &resp, client.Var("s", sceneID), client.Var("t", -1))
	require.Error(t, err,
		"a negative position is meaningless and must not be stored")
}

// The under-snapshotted query names scenes MISSING snapshots, which is a curation
// quest rather than a discovery surface. It has to be reachable or §8's quests
// have no input.
func TestUnderSnapshottedIsReachable(t *testing.T) {
	r := asRead(t)
	// One snapshot, below the default floor.
	collageScene(t, "An Under-Snapshotted Scene", intPtr(100))
	addSnapshot(t, r, collageScene(t, "A Second Under-Snapshotted Scene", intPtr(100)), 1000)

	var resp struct {
		UnderSnapshottedScenes []struct {
			SnapshotCount int
			Minimum       int
			Scene         struct {
				ID string
			}
		}
	}
	r.client.MustPost(`
		query {
			underSnapshottedScenes { snapshotCount minimum scene { id } }
		}
	`, &resp)

	require.NotEmpty(t, resp.UnderSnapshottedScenes,
		"§8's curation quests read this query; if it returns nothing they have no input")
	for _, u := range resp.UnderSnapshottedScenes {
		assert.Less(t, u.SnapshotCount, u.Minimum,
			"a scene in this list has fewer snapshots than the minimum it needs")
		assert.NotEmpty(t, u.Scene.ID, "each entry must name the scene needing snapshots")
	}
}
