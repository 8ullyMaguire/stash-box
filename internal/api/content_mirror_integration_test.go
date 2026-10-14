//go:build integration

package api_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
)

// The mirror lookup (slice 9d): when a scene's own source is not playable, is there a live URL for the
// SAME content on another scene?
//
// The "same content" part is the fingerprint cluster's answer and the "is handing over appropriate"
// part is content.Mirror's, so the tests below are written in pairs: the cluster half (a shared PHASH
// groups the two scenes) and the rule half (which statuses permit a hand-over and which forbid it).

// linkByPhash gives two scenes the same PHASH fingerprint, which is what the cluster service groups on.
// The hash is shared exactly, so the Hamming distance is zero and the result does not depend on the
// server's distance constant -- these tests are about the rule, not about the arithmetic.
func linkByPhash(t *testing.T, sceneA, sceneB uuid.UUID) {
	t.Helper()

	// A user has to exist for the submission's NOT NULL user_id, and the admin the assertions use
	// anyway is the one this creates.
	asAdmin(t)
	var userID uuid.UUID
	require.NoError(t, dbtest.DB().QueryRow(t.Context(), `SELECT id FROM users LIMIT 1`).Scan(&userID))

	// A decimal is the whole requirement on the value: the column is a varchar and the bktree operator
	// reads it AS a bigint, so a uuid string is refused outright. Nanoseconds apart are also unique
	// per call, which keeps two clusters in one test run from merging.
	hash := time.Now().UnixNano()
	var fingerprintID int
	require.NoError(t, dbtest.DB().QueryRow(t.Context(),
		`INSERT INTO fingerprints (hash, algorithm) VALUES ($1, 'PHASH') RETURNING id`, hash).Scan(&fingerprintID))

	for _, sceneID := range []uuid.UUID{sceneA, sceneB} {
		_, err := dbtest.DB().Exec(t.Context(),
			`INSERT INTO scene_fingerprints (fingerprint_id, scene_id, user_id, duration, vote) VALUES ($1, $2, $3, 0, 1)`,
			fingerprintID, sceneID, userID)
		require.NoError(t, err, "a submitted fingerprint, so the cluster counts it as a submission")
	}
}

func setSourceStatus(t *testing.T, url, status string, canonical bool) {
	t.Helper()
	_, err := dbtest.DB().Exec(t.Context(),
		`UPDATE content_sources SET status = $1, canonical = $2 WHERE url = $3`, status, canonical, url)
	require.NoError(t, err)
}

// mirrorOf asks the API what it would send a reader to, which is the only form of this question that
// matters: the rule is only real if the wire answer changes with it.
func mirrorOf(t *testing.T, sceneID uuid.UUID) *string {
	t.Helper()

	var resp struct {
		FindScene struct {
			Mirror *struct {
				URL string `json:"url"`
			} `json:"mirror"`
		} `json:"findScene"`
	}
	// Through the WIRE, not the resolver method: `mirror` is a force-resolved field, so calling
	// Query().FindScene would return a struct with the field left unpopulated and every assertion
	// below would pass for the wrong reason.
	require.NoError(t, asAdmin(t).client.Post(fmt.Sprintf(`
		query { findScene(id: %q) { mirror { url } } }`, sceneID.String()), &resp))

	if resp.FindScene.Mirror == nil {
		return nil
	}
	return &resp.FindScene.Mirror.URL
}

func TestSceneMirrorFindsTheSameContentElsewhere(t *testing.T) {
	deadURL := "https://mirror-test.invalid/" + uuid.Must(uuid.NewV4()).String()
	liveURL := "https://mirror-test.invalid/" + uuid.Must(uuid.NewV4()).String()

	unplayable, _ := createSceneWithSources(t, "Mirror: unplayable", deadURL)
	other, _ := createSceneWithSources(t, "Mirror: same content elsewhere", liveURL)

	setSourceStatus(t, deadURL, "dead", true)
	setSourceStatus(t, liveURL, "live", true)
	linkByPhash(t, unplayable, other)

	mirror := mirrorOf(t, unplayable)
	require.NotNil(t, mirror, "the other scene is the same content and is live")
	assert.Equal(t, liveURL, *mirror)

	assert.Nil(t, mirrorOf(t, other),
		"and the scene that IS playable has nothing to hand over")
}

// A takedown is a DECISION, not a hosting problem. Routing around one through a fingerprint cluster
// would be circumventing it by accident, so this is the one branch where a mirror that exists is
// deliberately not offered.
func TestSceneMirrorDoesNotRouteAroundATakedown(t *testing.T) {
	takenDownURL := "https://mirror-test.invalid/" + uuid.Must(uuid.NewV4()).String()
	liveURL := "https://mirror-test.invalid/" + uuid.Must(uuid.NewV4()).String()

	takenDown, _ := createSceneWithSources(t, "Mirror: taken down", takenDownURL)
	other, _ := createSceneWithSources(t, "Mirror: still up", liveURL)

	setSourceStatus(t, takenDownURL, "dmca", true)
	setSourceStatus(t, liveURL, "live", true)
	linkByPhash(t, takenDown, other)

	assert.Nil(t, mirrorOf(t, takenDown),
		"a dmca source means the instance has decided not to serve this, and a mirror would undo that "+
			"decision without anyone deciding anything")
}

// A mirror is a claim that the other scene is the SAME content. An unrelated live source must never be
// offered, and this is the test that would catch a lookup that dropped the cluster and simply found
// "some live URL".
func TestSceneMirrorOnlyOffersTheSameContent(t *testing.T) {
	deadURL := "https://mirror-test.invalid/" + uuid.Must(uuid.NewV4()).String()
	unrelatedURL := "https://mirror-test.invalid/" + uuid.Must(uuid.NewV4()).String()

	unplayable, _ := createSceneWithSources(t, "Mirror: nothing else like it", deadURL)
	// Created here deliberately WITHOUT linkByPhash: same instance, no shared fingerprint.
	_, _ = createSceneWithSources(t, "Mirror: unrelated but live", unrelatedURL)

	setSourceStatus(t, deadURL, "dead", true)
	setSourceStatus(t, unrelatedURL, "live", true)

	assert.Nil(t, mirrorOf(t, unplayable),
		"a live URL for a different scene is not a mirror, and offering one would send a reader to "+
			"content they did not ask for")
}

// "Live" strictly: a source nobody has ever checked is not a mirror. Offering one would send a reader
// from one unverified link to another, and reporting it as a mirror would make this instance's claim
// worth nothing -- the field exists to say "somewhere else is KNOWN to be serving this".
func TestSceneMirrorDoesNotOfferAnUncheckedSource(t *testing.T) {
	deadURL := "https://mirror-test.invalid/" + uuid.Must(uuid.NewV4()).String()
	uncheckedURL := "https://mirror-test.invalid/" + uuid.Must(uuid.NewV4()).String()

	unplayable, _ := createSceneWithSources(t, "Mirror: unchecked elsewhere", deadURL)
	other, _ := createSceneWithSources(t, "Mirror: never checked", uncheckedURL)

	setSourceStatus(t, deadURL, "dead", true)
	// The trigger created this row as `unknown` and nothing has swept it, which is the ordinary state
	// of a URL added minutes ago.
	setSourceStatus(t, uncheckedURL, "unknown", true)
	linkByPhash(t, unplayable, other)

	assert.Nil(t, mirrorOf(t, unplayable),
		"an unchecked URL is not a mirror: it is a second link nobody has verified")
}
