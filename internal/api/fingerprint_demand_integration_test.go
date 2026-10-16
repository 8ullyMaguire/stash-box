//go:build integration

package api_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SPEC §7.24.5 and the plan's gate 6: the fingerprint demand API returns a hash
// and a count, and NO locator.
//
// The board is an aggregate by hash. A fingerprint a member holds is evidence
// that a copy of the file exists somewhere, and the record must not become a
// pointer to it -- so "no scene, no user, no path" is the PROPERTY under test
// here and not a formatting detail. The last test in this file holds it at the
// level of the response's field set.
//
// Hashes are unique per RUN (the suite's database persists between runs), because
// a fixture that reuses a constant is asserting about a name, and the residue of
// the previous run would answer for the current one.

func runHash() int64 {
	return time.Now().UnixNano() & 0x7FFFFFFFFFFFFFFF
}

// demandSubmission records one (user, scene, hash) submission.
func demandSubmission(t *testing.T, r *testRunner, hash int64, algorithm string, scene *sceneOutput, user *models.User) {
	t.Helper()

	fp, err := q().CreateFingerprint(t.Context(), queries.CreateFingerprintParams{
		Hash: hash, Algorithm: algorithm,
	})
	require.NoError(t, err, "creating the fingerprint row")

	// sceneOutput.ID is the ID as the WIRE spells it; the store takes a uuid.
	_, err = q().CreateSceneFingerprints(t.Context(), []queries.CreateSceneFingerprintsParams{{
		FingerprintID: fp.ID, SceneID: uuid.FromStringOrNil(scene.ID), UserID: user.ID, Duration: 60,
	}})
	require.NoError(t, err, "attaching the submission to the scene")
}

func mustScene(t *testing.T, r *testRunner) *sceneOutput {
	t.Helper()
	scene, err := r.createTestScene(nil)
	require.NoError(t, err, "creating a scene to hang the submission on")
	return scene
}

func mustUser(t *testing.T, r *testRunner) *models.User {
	t.Helper()
	user, err := r.createTestUser(nil, nil)
	require.NoError(t, err, "creating a member to attribute the submission to")
	return user
}

// demandEntriesFor filters the board to one hash. The board aggregates the WHOLE
// table and this package's database is shared with every other test, so "how many
// entries came back" is a statement about the suite's residue while "the count for
// THIS hash" is a statement about the rule.
func demandEntriesFor(entries []models.FingerprintDemandEntry, hash int64) []models.FingerprintDemandEntry {
	var out []models.FingerprintDemandEntry
	for _, e := range entries {
		if int64(e.Hash) == hash {
			out = append(out, e)
		}
	}
	return out
}

// The lower bound is DISTINCT SUBMITTERS, not rows. One member who submitted the
// same hash against two scenes is one holder, and counting rows would overstate
// how many copies exist -- the one direction this figure must never take, since
// the endangered-copy notice is built on it.
func TestFingerprintDemandCountsHoldersNotSubmissions(t *testing.T) {
	r := asAdmin(t)
	hash := runHash()
	floor := 2

	var first *models.User
	for i := 0; i < 3; i++ {
		user := mustUser(t, r)
		if i == 0 {
			first = user
		}
		demandSubmission(t, r, hash, "MD5", mustScene(t, r), user)
	}

	// The first member submits the SAME hash against a SECOND scene: a fourth row
	// from a holder who is already counted.
	demandSubmission(t, r, hash, "MD5", mustScene(t, r), first)

	result, err := r.resolver.Query().FingerprintDemand(r.ctx, &models.FingerprintDemandInput{MinHolders: &floor})
	require.NoError(t, err, "calling the demand board")

	entries := demandEntriesFor(result.Entries, hash)
	require.Len(t, entries, 1, "one hash on the board, however many rows name it")
	assert.Equal(t, 3, entries[0].HolderLowerBound,
		"three members hold it and the fourth row is the first member again: rows are not holders")
}

// A report is not demand. `vote = -1` says the hash is wrong for that scene, which
// is a statement about the fingerprint rather than a request for the content, so
// the row is excluded rather than subtracted. Three holders minus one report is
// two -- still on the board, which is what keeps this from being a threshold test
// in disguise.
func TestFingerprintDemandExcludesReports(t *testing.T) {
	r := asAdmin(t)
	hash := runHash()
	floor := 2

	reporter := mustUser(t, r)
	reportScene := mustScene(t, r)
	demandSubmission(t, r, hash, "MD5", reportScene, reporter)
	demandSubmission(t, r, hash, "MD5", mustScene(t, r), mustUser(t, r))
	demandSubmission(t, r, hash, "MD5", mustScene(t, r), mustUser(t, r))

	_, err := dbtest.DB().Exec(t.Context(),
		`UPDATE scene_fingerprints sf SET vote = -1
		 FROM fingerprints f
		 WHERE f.id = sf.fingerprint_id AND f.hash = $1 AND sf.user_id = $2`,
		hash, reporter.ID)
	require.NoError(t, err, "marking one member's submission as a report")

	result, err := r.resolver.Query().FingerprintDemand(r.ctx, &models.FingerprintDemandInput{MinHolders: &floor})
	require.NoError(t, err)

	entries := demandEntriesFor(result.Entries, hash)
	require.Len(t, entries, 1, "two members still hold it, so it is still demand")
	assert.Equal(t, 2, entries[0].HolderLowerBound, "the reported submission is not a holder")
}

// "Exposed thresholded" means a caller cannot ask for the unthresholded board: the
// floor is enforced with the data, and a request below it is RAISED rather than
// honoured. The echoed floor is asserted because a board that silently answers a
// weaker question is exactly the failure this guards.
func TestFingerprintDemandCannotBeAskedForBelowTheFloor(t *testing.T) {
	r := asAdmin(t)

	for _, asked := range []int{0, 1} {
		result, err := r.resolver.Query().FingerprintDemand(r.ctx, &models.FingerprintDemandInput{
			MinHolders: &asked,
		})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, result.MinHolders, 2,
			"asking for %d must be raised to the floor, not honoured", asked)
		for _, e := range result.Entries {
			assert.GreaterOrEqual(t, e.HolderLowerBound, result.MinHolders,
				"an entry below the floor the response claims to have applied")
		}
	}
}

// The gate itself: marshal the response and check the FIELD SET. Asserting on the
// field set rather than on the values is what makes this a firewall instead of a
// specimen -- adding a scene id, a user id or a path later fails here even though
// every value would still be right.
func TestFingerprintDemandAPIResponseCarriesNoLocator(t *testing.T) {
	r := asAdmin(t)
	hash := runHash()
	floor := 2

	// Two holders, so the response is non-empty and the positive control below is
	// about a response that actually carries an entry.
	demandSubmission(t, r, hash, "MD5", mustScene(t, r), mustUser(t, r))
	demandSubmission(t, r, hash, "MD5", mustScene(t, r), mustUser(t, r))

	result, err := r.resolver.Query().FingerprintDemand(r.ctx, &models.FingerprintDemandInput{MinHolders: &floor})
	require.NoError(t, err)
	require.NotEmpty(t, result.Entries, "the fixture must put something on the board or nothing below is asserted")

	raw, err := json.Marshal(result)
	require.NoError(t, err, "marshalling the response the API would serve")

	var decoded map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &decoded))
	assert.ElementsMatch(t, []string{"entries", "min_holders"}, keysOf(decoded),
		"the response's top-level field set is part of the rule")

	// Positive control: the aggregate is really in there. "No locator" is only
	// meaningful in a response that carries the thing it is about.
	assert.Contains(t, string(raw), "holder_lower_bound",
		"the response must carry the count, or this test asserts an empty object")

	entryRaw, err := json.Marshal(result.Entries[0])
	require.NoError(t, err)
	var entry map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(entryRaw, &entry))
	assert.ElementsMatch(t, []string{"algorithm", "hash", "holder_lower_bound"}, keysOf(entry),
		"an entry is a hash and a count and nothing else")

	// The same rule at the TYPE level, which is the half that stays true when the
	// board is empty -- the state in which a value-based assertion checks nothing.
	for _, typ := range []reflect.Type{
		reflect.TypeOf(models.FingerprintDemand{}),
		reflect.TypeOf(models.FingerprintDemandEntry{}),
	} {
		for i := 0; i < typ.NumField(); i++ {
			name := strings.ToLower(typ.Field(i).Name)
			for _, forbidden := range []string{"scene", "user", "path", "submitter", "location", "url"} {
				assert.NotContains(t, name, forbidden,
					"%s.%s must not carry a locator: a pointer to a copy is not a statement about demand",
					typ.Name(), typ.Field(i).Name)
			}
		}
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
