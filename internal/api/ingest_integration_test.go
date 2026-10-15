//go:build integration

package api_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/99designs/gqlgen/client"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/ingest"
	"github.com/stashapp/stash-box/internal/service/ingest/extract"
	"github.com/stashapp/stash-box/pkg/urlkey"
)

// The ingestion pipeline, end to end: a submitted URL becomes a content record, or a
// recorded reason why not (docs/spec/feature-ingestion.md, plan phase 2).
//
// WHY THIS FILE IS IN internal/api AND NOT BESIDE THE SERVICE. Two reasons, and the first
// one is a hard constraint rather than a preference:
//
//  1. ONE PACKAGE MAY OWN THE TEST DATABASE. dbtest's TestMain DROPS EVERY TABLE at setup
//     and teardown, and `go test ./...` runs packages in parallel -- so a second package
//     with its own TestMain against the same database would drop the schema out from under
//     this one, mid-test, intermittently. The pipeline needs a real database (a constraint
//     has to be able to reject a write, and a claim is SKIP LOCKED), so it lives here.
//  2. It is the only place the whole path can be exercised: GraphQL submission -> queue row
//     -> drain -> content_sources + scenes. A service-level test would show the pipeline
//     writing rows; it could not show that a client can submit anything at all, which is
//     the gap this phase closed.
//
// The extractor is faked for the rule tests -- the fake is in the extract package, next to
// the real one, so it cannot drift -- and REAL for the last two tests, which serve actual
// HTML over an actual socket and let HTTPExtractor read it. Nothing in this file needs the
// public internet.

// ingestSubmission mirrors the GraphQL payload.
//
// The json tags are required, not decoration: gqlgen's client decodes with encoding/json,
// which matches keys case-insensitively against the FIELD NAME, so `normalized_url` lands
// nowhere without the tag and the client rejects the whole response with "has invalid
// keys", failing the test for a reason that has nothing to do with the code under test.
type ingestSubmission struct {
	Status        string  `json:"status"`
	QueueID       *string `json:"queue_id"`
	NormalizedURL string  `json:"normalized_url"`
	Message       *string `json:"message"`
}

// submitContentURL posts the mutation as a client would.
//
// sceneID is passed as an interface so a nil means JSON null rather than an omitted
// variable: the mutation declares `$s: ID`, and MustPost requires every declared variable.
func submitContentURL(t *testing.T, r *testRunner, url string, sceneID *uuid.UUID) ingestSubmission {
	t.Helper()
	var sceneVar interface{}
	if sceneID != nil {
		sceneVar = sceneID.String()
	}

	var resp struct {
		SubmitContentURL ingestSubmission `json:"submitContentUrl"`
	}
	r.client.MustPost(`
		mutation($u: String!, $s: ID) {
			submitContentUrl(url: $u, sceneId: $s) {
				status queue_id normalized_url message
			}
		}
	`, &resp, client.Var("u", url), client.Var("s", sceneVar))
	return resp.SubmitContentURL
}

func ingestServiceWith(extractor extract.Extractor) *ingest.Service {
	return ingest.NewService(queries.New(dbtest.DB()), extractor)
}

// queueState is read through a raw query rather than a generated one, because no generated
// query returns "the queue row for this key" yet -- phase 7's submission-status surface is
// what will need one, and inventing it here to make a test tidy would put a query's shape in
// the test that owns it later.
type queueState struct {
	Status        string
	Outcome       *string
	Reason        *string
	LastError     *string
	ResultSceneID *uuid.UUID
	Confidence    *float32
	Attempts      int
	NextAttemptAt time.Time
}

func readQueueRow(t *testing.T, normalised string) queueState {
	t.Helper()
	var s queueState
	err := dbtest.DB().QueryRow(t.Context(), `
		SELECT status, outcome, reason, last_error, result_scene_id, confidence, attempts, next_attempt_at
		FROM ingest_queue
		WHERE url_normalized = $1
		ORDER BY created_at DESC
		LIMIT 1`, normalised).
		Scan(&s.Status, &s.Outcome, &s.Reason, &s.LastError, &s.ResultSceneID, &s.Confidence, &s.Attempts, &s.NextAttemptAt)
	require.NoError(t, err, "the queue row for %s must exist", normalised)
	return s
}

func countQueueRows(t *testing.T, normalised string) int {
	t.Helper()
	var n int
	require.NoError(t, dbtest.DB().QueryRow(t.Context(),
		`SELECT count(*) FROM ingest_queue WHERE url_normalized = $1`, normalised).Scan(&n))
	return n
}

func countAggregateScenes(t *testing.T) int {
	t.Helper()
	var n int
	require.NoError(t, dbtest.DB().QueryRow(t.Context(),
		`SELECT count(*) FROM scenes WHERE origin = 'aggregate'`).Scan(&n))
	return n
}

// sceneColumn reads the columns a GraphQL selection cannot show: the origin the pipeline
// wrote, the details it assembled, and the studio_id it deliberately did NOT write.
func sceneColumn(t *testing.T, id uuid.UUID) (origin string, details *string, studioID *uuid.UUID) {
	t.Helper()
	require.NoError(t, dbtest.DB().QueryRow(t.Context(),
		`SELECT origin, details, studio_id FROM scenes WHERE id = $1`, id).
		Scan(&origin, &details, &studioID))
	return origin, details, studioID
}

type sceneView struct {
	ID       string       `json:"id"`
	Title    string       `json:"title"`
	Duration *int         `json:"duration"`
	Date     *string      `json:"date"`
	Sources  []sourceView `json:"sources"`
}

type sourceView struct {
	URL       string `json:"url"`
	Status    string `json:"status"`
	Canonical bool   `json:"canonical"`
}

func readScene(t *testing.T, r *testRunner, id uuid.UUID) sceneView {
	t.Helper()
	var resp struct {
		FindScene sceneView `json:"findScene"`
	}
	r.client.MustPost(`
		query($id: ID!) {
			findScene(id: $id) { id title duration date sources { url status canonical } }
		}
	`, &resp, client.Var("id", id.String()))
	return resp.FindScene
}

// cleanupIngestFixtures removes what these tests create, at the end of each of them.
//
// The database is shared with every other integration test in this package (one TestMain,
// one schema) and most of them leave their scenes behind, so a leftover aggregate scene
// would not break anything -- but the merge test asserts on a DELTA of aggregate scenes, and
// a leaked row would make the next run of it start from a different baseline.
func cleanupIngestFixtures(t *testing.T, urls ...string) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, raw := range urls {
			norm, err := urlkey.Normalize(raw)
			if err != nil {
				norm = raw
			}
			_, _ = dbtest.DB().Exec(ctx, `
				DELETE FROM scenes WHERE id IN (
					SELECT result_scene_id FROM ingest_queue
					WHERE url_normalized = $1 AND result_scene_id IS NOT NULL)`, norm)
			_, _ = dbtest.DB().Exec(ctx, `DELETE FROM content_sources WHERE url_normalized = $1`, norm)
			_, _ = dbtest.DB().Exec(ctx, `DELETE FROM ingest_queue WHERE url_normalized = $1`, norm)
			// The directory entry the pipeline created for this host, guarded by the regex
			// it writes: a site a person curated in the same test database must survive,
			// and only the pipeline's own rows carry the host-as-regex pattern.
			if host := urlkey.SiteHost(raw); host != "" {
				_, _ = dbtest.DB().Exec(ctx, `DELETE FROM sites WHERE name = $1 AND regex = $2`,
					host, regexp.QuoteMeta(host))
			}
		}
	})
}

func occurrences(haystack []string, needle string) int {
	n := 0
	for _, s := range haystack {
		if s == needle {
			n++
		}
	}
	return n
}

// seedContentSource records a source directly, which is how the edit path
// (scene_urls -> content_sources, phase 1) and an earlier crawl leave rows behind. The
// pipeline has to cope with those, and a submission is the only thing that can produce one
// through the API, so a test that wants "this URL is already claimed, and not by the queue"
// has to write it.
func seedContentSource(t *testing.T, sceneID uuid.UUID, url, normalised string) uuid.UUID {
	t.Helper()
	row, err := q().InsertContentSource(t.Context(), queries.InsertContentSourceParams{
		EntityType:    "scene",
		EntityID:      sceneID,
		Url:           url,
		UrlNormalized: &normalised,
	})
	require.NoError(t, err, "seeding the content_sources row")
	return row.ID
}

func seedFingerprint(t *testing.T, sceneID uuid.UUID, hash int64, algorithm string, duration int) {
	t.Helper()
	var userID uuid.UUID
	require.NoError(t, dbtest.DB().QueryRow(t.Context(), `SELECT id FROM users LIMIT 1`).Scan(&userID),
		"there must be a user to attribute the fingerprint to (CreateSystemUsers runs at setup)")

	row, err := q().CreateFingerprint(t.Context(), queries.CreateFingerprintParams{
		Hash: hash, Algorithm: algorithm,
	})
	require.NoError(t, err, "creating the fingerprint")
	_, err = q().CreateSceneFingerprints(t.Context(), []queries.CreateSceneFingerprintsParams{{
		FingerprintID: row.ID, SceneID: sceneID, UserID: userID, Duration: duration,
	}})
	require.NoError(t, err, "attaching the fingerprint to the scene")
}

// ---------------------------------------------------------------------------------
// The pipeline's rules, with the network faked
// ---------------------------------------------------------------------------------

func TestSubmitContentUrlAndDrainCreatesAnAggregateScene(t *testing.T) {
	fetchSourcesForTest(t)
	r := asEdit(t)
	raw := "https://pipe.invalid/watch/one?utm_source=newsletter&fbclid=abc#t=30"
	cleanupIngestFixtures(t, raw)

	submission := submitContentURL(t, r, raw, nil)
	require.Equal(t, "queued", submission.Status, "a fresh URL is work for the queue")
	require.NotNil(t, submission.QueueID, "and it is enqueued, so it has a row")
	assert.Equal(t, "https://pipe.invalid/watch/one", submission.NormalizedURL,
		"the tracking parameters and the fragment are dropped from the KEY (the fetch still uses the raw URL)")

	fake := extract.NewFake(map[string]extract.Metadata{
		raw: {
			Title:           "A Pipeline Scene",
			Studio:          "Pipe Studio",
			DurationSeconds: 1432,
			Date:            "2023-11-02",
			Source:          "og",
			Confidence:      0.7,
		},
	})
	processed, err := ingestServiceWith(fake).Drain(t.Context(), 10)
	require.NoError(t, err)
	require.GreaterOrEqual(t, processed, 1, "the drain must have claimed this submission")
	assert.Equal(t, 1, occurrences(fake.Calls, raw),
		"the page is fetched exactly once, from the RAW url rather than the normalised key")

	job := readQueueRow(t, submission.NormalizedURL)
	assert.Equal(t, "done", job.Status)
	require.NotNil(t, job.Outcome)
	assert.Equal(t, "created", *job.Outcome)
	require.NotNil(t, job.ResultSceneID, "a created outcome names the record it created")
	require.NotNil(t, job.Confidence)
	assert.InDelta(t, 0.7, *job.Confidence, 0.001)
	assert.Equal(t, 1, job.Attempts)

	scene := readScene(t, r, *job.ResultSceneID)
	assert.Equal(t, "A Pipeline Scene", scene.Title, "the page's title became the record's title")
	require.NotNil(t, scene.Duration)
	assert.Equal(t, 1432, *scene.Duration)
	require.NotNil(t, scene.Date)
	assert.Equal(t, "2023-11-02", *scene.Date)

	require.Len(t, scene.Sources, 1, "the record points at where the content is")
	assert.Equal(t, raw, scene.Sources[0].URL, "the source keeps the URL as submitted, not the dedup key")
	assert.False(t, scene.Sources[0].Canonical,
		"canonicality is a separate decision with its own statement and index; the pipeline must not claim it")

	origin, details, studioID := sceneColumn(t, *job.ResultSceneID)
	assert.Equal(t, "aggregate", origin, "the record is content this instance points at, not a claimed upload")
	assert.Nil(t, studioID, "a studio NAME off a page is evidence; studio_id is a directory a human curates")
	require.NotNil(t, details)
	assert.Contains(t, *details, "Studio: Pipe Studio", "and the name is not lost -- it goes in details")
}

func TestSubmitContentUrlTwiceIsADuplicateAndQueuesOneRow(t *testing.T) {
	fetchSourcesForTest(t)
	r := asEdit(t)
	raw := "https://dup.invalid/a"
	cleanupIngestFixtures(t, raw)

	first := submitContentURL(t, r, raw, nil)
	require.Equal(t, "queued", first.Status)

	// The same page submitted in a form that differs only by tracking and case: this is the
	// entire reason NormalizeURL exists, and a duplicate the submitter can see explained is
	// the difference between "the aggregator is broken" and "it already has this".
	second := submitContentURL(t, r, "https://DUP.invalid/a/?utm_campaign=x#frag", nil)
	assert.Equal(t, "duplicate", second.Status)
	assert.Nil(t, second.QueueID, "nothing was enqueued, so there is no row to name")
	require.NotNil(t, second.Message)
	assert.Contains(t, *second.Message, "already")

	// The count assertion comes BEFORE any claim about what is absent. An "only one row"
	// assertion over an empty table is vacuously true, and would keep passing if the
	// submission path stopped writing rows at all.
	assert.Equal(t, 1, countQueueRows(t, "https://dup.invalid/a"), "exactly one queue row survives both submissions")
}

func TestSubmitContentUrlRefusesAURLItCannotIngest(t *testing.T) {
	fetchSourcesForTest(t)
	r := asEdit(t)

	for _, raw := range []string{
		"javascript:alert(1)",
		"not-a-url",
		"https:///no-host",
	} {
		t.Run(raw, func(t *testing.T) {
			submission := submitContentURL(t, r, raw, nil)
			assert.Equal(t, "invalid", submission.Status)
			require.NotNil(t, submission.Message, "a refusal the submitter cannot read is not a refusal")
			assert.Empty(t, submission.NormalizedURL,
				"there is no normalised form of a URL that could not be parsed, and inventing one states something we do not know")
			assert.Equal(t, 0, countQueueRows(t, raw), "and nothing was enqueued for it")
		})
	}
}

func TestSubmitContentUrlRequiresEdit(t *testing.T) {
	fetchSourcesForTest(t)
	for _, tc := range []struct {
		name string
		r    *testRunner
	}{
		{"a READ user", asRead(t)},
		{"a user with no roles", asNone(t)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out struct {
				SubmitContentURL ingestSubmission `json:"submitContentUrl"`
			}
			err := tc.r.client.Post(`
				mutation { submitContentUrl(url: "https://role.invalid/a") { status } }
			`, &out)
			require.Error(t, err, "the access rule must refuse this")

			// The assertion that makes the refusal mean something: the mutation was not
			// merely reported as refused, nothing was written.
			assert.Equal(t, 0, countQueueRows(t, "https://role.invalid/a"))
		})
	}
}

// TestANamedTargetBeatsThePipelinesOwnInference is the wiring test for the scene page's "add
// a source" form, and it asserts two things a service test could not: that the instruction
// survives the API, and that the pipeline does not overrule it.
func TestANamedTargetBeatsThePipelinesOwnInference(t *testing.T) {
	fetchSourcesForTest(t)
	r := asEdit(t)
	raw := "https://target.invalid/one"
	cleanupIngestFixtures(t, raw)

	// A scene the reader is looking at, with a title a curator wrote. The page at `raw`
	// states a different title, which is what an overruling pipeline would rename it to.
	target := createSceneWithDuration(t, "The Scene The Reader Was Looking At", intPtr(600))
	t.Cleanup(func() {
		_, _ = dbtest.DB().Exec(context.Background(), `DELETE FROM scenes WHERE id = $1`, target)
	})

	submission := submitContentURL(t, r, raw, &target)
	require.Equal(t, "queued", submission.Status)

	targetID := readQueueTarget(t, submission.NormalizedURL)
	require.NotNil(t, targetID, "the row must carry the instruction, or the pipeline is guessing")
	assert.Equal(t, target, *targetID)

	// The extractor would happily invent a record here, and it must not be asked to: the
	// scene already exists, so there is nothing to create and nothing to read the page for.
	fake := extract.NewFake(map[string]extract.Metadata{
		raw: {Title: "A Title The Pipeline Invented", Source: "og"},
	})
	processed, err := ingestServiceWith(fake).Drain(t.Context(), 10)
	require.NoError(t, err)
	require.GreaterOrEqual(t, processed, 1)

	job := readQueueRow(t, submission.NormalizedURL)
	assert.Equal(t, "done", job.Status)
	require.NotNil(t, job.Outcome)
	assert.Equal(t, "attached", *job.Outcome)
	require.NotNil(t, job.ResultSceneID)
	assert.Equal(t, target, *job.ResultSceneID)
	assert.Empty(t, fake.Calls, "an attach needs nothing from the page, so the page is not requested")
	assert.Nil(t, job.Confidence, "and 'no page was read' is NULL, not 0.0")

	scene := readScene(t, r, target)
	assert.Equal(t, "The Scene The Reader Was Looking At", scene.Title,
		"the attach must not rename the scene the reader was looking at")
	require.Len(t, scene.Sources, 1, "but the source is recorded on it")
	assert.Equal(t, raw, scene.Sources[0].URL)
}

func TestAMatchingFingerprintMergesIntoTheExistingScene(t *testing.T) {
	fetchSourcesForTest(t)
	r := asEdit(t)
	raw := "https://merge.invalid/one"
	cleanupIngestFixtures(t, raw)

	existing := createSceneWithDuration(t, "The Scene We Already Have", intPtr(900))
	t.Cleanup(func() {
		_, _ = dbtest.DB().Exec(context.Background(), `DELETE FROM scenes WHERE id = $1`, existing)
	})
	// A unique hash, so the lookup can only match this scene: a shared value would let
	// another test's fingerprint decide which scene this submission lands on.
	const hash = int64(4242424242424242)
	seedFingerprint(t, existing, hash, "OSSHASH", 900)

	before := countAggregateScenes(t)
	submission := submitContentURL(t, r, raw, nil)
	require.Equal(t, "queued", submission.Status)

	fake := extract.NewFake(map[string]extract.Metadata{
		raw: {
			Title: "The Same Content, A Different URL",
			Fingerprints: []extract.Fingerprint{
				{Algorithm: "OSSHASH", Hash: hash, Duration: 900},
			},
			Source: "og",
		},
	})
	processed, err := ingestServiceWith(fake).Drain(t.Context(), 10)
	require.NoError(t, err)
	require.GreaterOrEqual(t, processed, 1)

	job := readQueueRow(t, submission.NormalizedURL)
	require.NotNil(t, job.Outcome)
	assert.Equal(t, "merged", *job.Outcome)
	require.NotNil(t, job.ResultSceneID)
	assert.Equal(t, existing, *job.ResultSceneID, "the merge lands on the scene that already has the content")

	// The count, not just the outcome string: an implementation that created a scene AND
	// reported "merged" would pass every assertion above.
	assert.Equal(t, before, countAggregateScenes(t), "a merge must not create a second record")

	scene := readScene(t, r, existing)
	assert.Equal(t, "The Scene We Already Have", scene.Title, "a merge does not rewrite the record it merged into")
	require.Len(t, scene.Sources, 1)
	assert.Equal(t, raw, scene.Sources[0].URL, "the second URL is recorded as another source")
}

func TestAURLAlreadyClaimedIsAttachedWithoutAFetch(t *testing.T) {
	fetchSourcesForTest(t)
	r := asEdit(t)
	raw := "https://claimed.invalid/one"
	cleanupIngestFixtures(t, raw)

	// A row that did not come from this queue -- the edit path's mirror, or an earlier
	// crawl. This is the only way to reach the attach arm, because a second submission of a
	// URL this queue has already handled is refused as a duplicate before the pipeline runs.
	owner := createSceneWithDuration(t, "The Scene That Already Has It", intPtr(300))
	norm, err := urlkey.Normalize(raw)
	require.NoError(t, err)
	seedContentSource(t, owner, raw, norm)

	fake := extract.NewFake(map[string]extract.Metadata{})
	submission := submitContentURL(t, r, raw, nil)
	require.Equal(t, "queued", submission.Status, "the queue dedups, and this URL is not in the queue")

	processed, err := ingestServiceWith(fake).Drain(t.Context(), 10)
	require.NoError(t, err)
	require.GreaterOrEqual(t, processed, 1)

	job := readQueueRow(t, submission.NormalizedURL)
	require.NotNil(t, job.Outcome)
	assert.Equal(t, "attached", *job.Outcome)
	require.NotNil(t, job.ResultSceneID)
	assert.Equal(t, owner, *job.ResultSceneID)
	assert.Empty(t, fake.Calls, "a URL we already have is not requested again")
}

func TestAURLTakenDownIsRefusedRatherThanReAttached(t *testing.T) {
	fetchSourcesForTest(t)
	r := asEdit(t)
	raw := "https://dmca.invalid/one"
	cleanupIngestFixtures(t, raw)

	owner := createSceneWithDuration(t, "A Scene With A Taken-Down Source", intPtr(300))
	t.Cleanup(func() {
		_, _ = dbtest.DB().Exec(context.Background(), `DELETE FROM scenes WHERE id = $1`, owner)
	})
	norm, err := urlkey.Normalize(raw)
	require.NoError(t, err)
	sourceID := seedContentSource(t, owner, raw, norm)
	_, err = dbtest.DB().Exec(t.Context(), `UPDATE content_sources SET status = 'dmca' WHERE id = $1`, sourceID)
	require.NoError(t, err)

	fake := extract.NewFake(map[string]extract.Metadata{
		raw: {Title: "A Title Nobody Should See", Source: "og"},
	})
	submission := submitContentURL(t, r, raw, nil)
	require.Equal(t, "queued", submission.Status)

	processed, err := ingestServiceWith(fake).Drain(t.Context(), 10)
	require.NoError(t, err)
	require.GreaterOrEqual(t, processed, 1)

	job := readQueueRow(t, submission.NormalizedURL)
	// `rejected`, not `failed`, and the distinction is the point: nothing broke, the
	// pipeline declined to ingest a URL a human had already taken down. Reporting it as a
	// failure is how "how many of the last hundred submissions were refused rather than
	// broken" becomes a string comparison over the reason instead of a filter on a column.
	assert.Equal(t, "rejected", job.Status)
	require.NotNil(t, job.Reason)
	assert.Contains(t, *job.Reason, "taken down")
	assert.Nil(t, job.Outcome, "a rejected row ran nothing to completion, so it has no outcome")
	assert.Empty(t, fake.Calls, "a URL a human has taken down is not requested again")

	// The takedown state is a human's, and the ingest path must not move it in either
	// direction: re-attaching would undo it, and rewriting the status would be this
	// pipeline claiming an authority it does not have.
	var status string
	require.NoError(t, dbtest.DB().QueryRow(t.Context(),
		`SELECT status FROM content_sources WHERE id = $1`, sourceID).Scan(&status))
	assert.Equal(t, "dmca", status)
}

// ---------------------------------------------------------------------------------
// Failure outcomes: what the queue row says, and whether the next tick tries again
// ---------------------------------------------------------------------------------

func TestAFetchFailureIsRecordedWithTheRightRetryPolicy(t *testing.T) {
	fetchSourcesForTest(t)
	tests := []struct {
		name      string
		extract   error
		wantStat  string
		wantReas  string
		retryable bool
	}{
		{
			name:     "a page that is gone is permanent",
			extract:  fmt.Errorf("%w: the server answered 404", extract.ErrGone),
			wantStat: "failed",
			wantReas: "gone",
		},
		{
			name:     "a login wall is permanent, and is not called gone",
			extract:  fmt.Errorf("%w: the server answered 403", extract.ErrUnsupportedPage),
			wantStat: "failed",
			wantReas: "unsupported_page",
		},
		{
			name:      "an unreachable host is tried again later",
			extract:   fmt.Errorf("%w: dial tcp: connection refused", extract.ErrUnreachable),
			wantStat:  "pending",
			wantReas:  "unreachable",
			retryable: true,
		},
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := asEdit(t)
			raw := fmt.Sprintf("https://fail%d.invalid/one", i)
			cleanupIngestFixtures(t, raw)

			submission := submitContentURL(t, r, raw, nil)
			require.Equal(t, "queued", submission.Status)

			fake := extract.NewFake(map[string]extract.Metadata{})
			fake.ByURLError[raw] = tc.extract
			processed, err := ingestServiceWith(fake).Drain(t.Context(), 10)
			require.NoError(t, err)
			require.GreaterOrEqual(t, processed, 1)

			job := readQueueRow(t, submission.NormalizedURL)
			assert.Equal(t, tc.wantStat, job.Status)
			require.NotNil(t, job.Reason)
			assert.Equal(t, tc.wantReas, *job.Reason)
			require.NotNil(t, job.LastError, "the reason a person reads is the error, not just the category")
			assert.Contains(t, *job.LastError, tc.extract.Error()[:20])

			if tc.retryable {
				assert.True(t, job.NextAttemptAt.After(time.Now()),
					"a transient failure schedules the retry in the future, or the next tick spins on it")
			} else {
				assert.Nil(t, job.Outcome, "a failed row has no outcome: nothing was created, attached or merged")
				assert.Nil(t, job.ResultSceneID)
			}
		})
	}
}

// ---------------------------------------------------------------------------------
// The real extractor, over a real socket
// ---------------------------------------------------------------------------------

// TestTheRealExtractorIngestsARealPage is the only test in this file where nothing is
// faked except the internet: a real HTTP server, a real response, the production extractor,
// a real database. It is what makes the fake defensible -- a rule pipeline tested only
// against a table of Metadata could be passing while the extractor it ships never parses
// anything at all.
func TestTheRealExtractorIngestsARealPage(t *testing.T) {
	fetchSourcesForTest(t)
	r := asEdit(t)

	page := `<!doctype html>
<html><head>
	<title>BigSite - free videos</title>
	<meta property="og:title" content="A Real Page Title &amp; More">
	<meta property="og:video:duration" content="1432">
	<meta property="og:video:release_date" content="2023-11-02T10:30:00Z">
	<meta property="og:image" content="https://img.invalid/a.jpg">
</head><body><h1>A Real Page Title &amp; More</h1></body></html>`

	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		// The extractor asks robots.txt first (internal/service/hostrules) and this host states no
		// terms. It is answered BEFORE the counter and the page, because the claim below is about the
		// PAGE: a request for the host's terms is not a fetch of the page, and counting it would make
		// every robots-obeying extractor look like it fetched twice.
		case req.URL.Path == "/robots.txt":
			w.WriteHeader(http.StatusNotFound)
		case strings.HasPrefix(req.URL.Path, "/login"):
			w.WriteHeader(http.StatusForbidden)
		case strings.HasPrefix(req.URL.Path, "/gone"):
			w.WriteHeader(http.StatusNotFound)
		default:
			hits++
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprint(w, page)
		}
	}))
	defer server.Close()

	raw := server.URL + "/watch/1?utm_source=test"
	cleanupIngestFixtures(t, raw)
	submission := submitContentURL(t, r, raw, nil)
	require.Equal(t, "queued", submission.Status)

	processed, err := ingestServiceWith(extract.NewHTTPExtractor()).Drain(t.Context(), 10)
	require.NoError(t, err)
	require.GreaterOrEqual(t, processed, 1)

	assert.Equal(t, 1, hits, "the page was requested exactly once")

	job := readQueueRow(t, submission.NormalizedURL)
	require.NotNil(t, job.Outcome)
	require.Equal(t, "created", *job.Outcome)
	require.NotNil(t, job.ResultSceneID)

	scene := readScene(t, r, *job.ResultSceneID)
	// The entity is unescaped: a title that reads "A Real Page Title &amp; More" on a public
	// record is a parse bug a reader sees.
	assert.Equal(t, "A Real Page Title & More", scene.Title,
		"the og:title wins over the <title> element, and the entity is decoded")
	require.NotNil(t, scene.Duration)
	assert.Equal(t, 1432, *scene.Duration)
	require.NotNil(t, scene.Date)
	assert.Equal(t, "2023-11-02", *scene.Date, "a full timestamp is reduced to the date")
}

// TestTheRealExtractorReportsA404AsGoneAndAForbiddenAsUnsupported: the same mapping as the
// unit test, but through the queue and into a status a person can read, which is where the
// consequence lives.
func TestTheRealExtractorReportsA404AsGoneAndAForbiddenAsUnsupported(t *testing.T) {
	fetchSourcesForTest(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// HasPrefix, not equality: the subtests below append an index to the path so that
		// each run gets its own queue key, and an exact match here made every arm answer
		// 404 -- which read as "the mapping is wrong" for a test whose server was wrong.
		if strings.HasPrefix(req.URL.Path, "/login") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	tests := []struct {
		path     string
		wantReas string
	}{
		{"/gone", "gone"},
		{"/login", "unsupported_page"},
	}

	for i, tc := range tests {
		t.Run(tc.wantReas, func(t *testing.T) {
			r := asEdit(t)
			raw := fmt.Sprintf("%s%s/%d", server.URL, tc.path, i)
			cleanupIngestFixtures(t, raw)

			submission := submitContentURL(t, r, raw, nil)
			require.Equal(t, "queued", submission.Status)

			processed, err := ingestServiceWith(extract.NewHTTPExtractor()).Drain(t.Context(), 10)
			require.NoError(t, err)
			require.GreaterOrEqual(t, processed, 1)

			job := readQueueRow(t, submission.NormalizedURL)
			assert.Equal(t, "failed", job.Status)
			require.NotNil(t, job.Reason)
			assert.Equal(t, tc.wantReas, *job.Reason)
		})
	}
}

func readQueueTarget(t *testing.T, normalised string) *uuid.UUID {
	t.Helper()
	var id *uuid.UUID
	require.NoError(t, dbtest.DB().QueryRow(t.Context(), `
		SELECT target_entity_id FROM ingest_queue
		WHERE url_normalized = $1
		ORDER BY created_at DESC
		LIMIT 1`, normalised).Scan(&id))
	return id
}
