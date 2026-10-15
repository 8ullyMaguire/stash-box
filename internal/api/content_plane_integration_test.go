//go:build integration

package api_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/config"
	"github.com/stashapp/stash-box/internal/service/ingest/extract"
)

// fetchSourcesForTest turns the content-plane switch on for one test, and fetchSourcesOffForTest turns
// it off, both restoring the previous value afterwards.
//
// WHY THE TESTS THAT FETCH HAVE TO SAY SO. Since the 2026-10-15 respec the default is OFF, so every
// test in this package that exercises an outbound request to a content source has to turn the switch on
// explicitly -- which is exactly what an operator does, and the reason the gate is a switch and not a
// deletion. The restore is not tidiness either: `config.C` is a package global, and a test that left
// fetching on would let the refusal tests pass for the wrong reason.
func fetchSourcesForTest(t *testing.T) {
	t.Helper()
	setFetchSources(t, true)
}

func fetchSourcesOffForTest(t *testing.T) {
	t.Helper()
	setFetchSources(t, false)
}

func setFetchSources(t *testing.T, enabled bool) {
	t.Helper()
	previous := config.C.ContentFetchSources
	config.C.ContentFetchSources = enabled
	t.Cleanup(func() { config.C.ContentFetchSources = previous })
}

// The gate at the wire, in the two directions that make it mean something.
//
// A REFUSAL TEST ALONE WOULD PASS ON A BROKEN HARNESS, so the control is in the same function: the same
// mutation, the same client, the same URL, with the switch off and then on. Off: a GraphQL error naming
// the key, and ZERO rows queued -- because a row would be work for a drain that will never run, and the
// member would be watching a submission that is never processed. On: the ordinary "queued".
func TestSubmitContentUrlIsRefusedAndQueuesNothingWhileFetchingIsOff(t *testing.T) {
	const raw = "https://respec.invalid/watch/refused"

	fetchSourcesOffForTest(t)
	r := asEdit(t)
	cleanupIngestFixtures(t, raw)

	var out struct {
		SubmitContentURL ingestSubmission `json:"submitContentUrl"`
	}
	err := r.client.Post(`
		mutation { submitContentUrl(url: "https://respec.invalid/watch/refused") { status message } }
	`, &out)

	require.Error(t, err, "a non-fetching instance must refuse this, not resolve it")
	assert.Contains(t, err.Error(), "content_fetch_sources",
		"the refusal has to name the key, because the reader who can act on it is the operator")
	assert.Equal(t, 0, countQueueRows(t, raw),
		"nothing may be queued: a queued row is a promise the drain will not keep")

	// The control. Same call, switch on: queued, with a row.
	fetchSourcesForTest(t)
	submission := submitContentURL(t, r, raw, nil)
	require.Equal(t, "queued", submission.Status,
		"with fetching on the same URL must queue -- otherwise this test is asserting a broken client")
	require.NotNil(t, submission.QueueID)
	assert.Equal(t, 1, countQueueRows(t, raw))
}

// poisonedExtractor fails the test if it is asked to read a page.
type poisonedExtractor struct {
	t     *testing.T
	calls int
}

func (e *poisonedExtractor) Extract(context.Context, string) (extract.Metadata, error) {
	e.calls++
	e.t.Error("the extractor was called: an instance with content_fetch_sources off must make no request")
	return extract.Metadata{}, extract.ErrUnsupportedPage
}

// The other half of the gate, and the half Submit cannot cover: a row that was queued while fetching
// was ON, in a queue that is then drained while it is OFF.
//
// THREE CLAIMS, and each one is a different failure. The extractor was not consulted (no request). The
// row was NOT left 'running' -- it is recorded as a transient failure, so the queue says what happened
// and goes back to 'pending' rather than stranding work or burning the URL. And the recorded error
// names the switch, because the person reading that column is the operator who can change it.
func TestADrainRecordsTheRefusalAndMakesNoRequestWhileFetchingIsOff(t *testing.T) {
	const raw = "https://respec.invalid/watch/drain"

	// Queued the way a member queues it: fetching on.
	fetchSourcesForTest(t)
	r := asEdit(t)
	cleanupIngestFixtures(t, raw)
	require.Equal(t, "queued", submitContentURL(t, r, raw, nil).Status,
		"the fixture is a queue row, and this is how one gets there in the first place")

	// Then the operator turns it off, and the row is still queued.
	fetchSourcesOffForTest(t)

	extractor := &poisonedExtractor{t: t}
	processed, err := ingestServiceWith(extractor).Drain(t.Context(), 10)
	require.NoError(t, err, "recording the refusal is an outcome, not a failure of the drain")
	assert.Equal(t, 1, processed, "the job's outcome was recorded, which is what Drain counts")
	assert.Zero(t, extractor.calls, "no request may be made")

	row := readQueueRow(t, raw)
	assert.Equal(t, "pending", row.Status,
		"a transient failure returns the row to 'pending': the URL is fine, and an operator who turns "+
			"fetching on should have it processed by the retry this schedules")
	assert.NotNil(t, row.Reason)
	assert.Equal(t, "unreachable", *row.Reason, "the failure taxonomy has no arm for 'the instance does not fetch'")
	require.NotNil(t, row.LastError)
	assert.Contains(t, *row.LastError, "content_fetch_sources",
		"the row has to say WHY, or an operator reads 'unreachable' about a host that is up")
	assert.Equal(t, 1, row.Attempts)
	assert.Nil(t, row.ResultSceneID, "no scene was created, so there is nothing to name")
}
