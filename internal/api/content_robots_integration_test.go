//go:build integration

package api_test

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/service/ingest/extract"
	"github.com/stashapp/stash-box/pkg/urlkey"
)

// Crawl hygiene, at the far end: a submitted URL whose host disallows it, drained by the REAL
// extractor, ends as a queue row that says why and a page that was never requested.
//
// THIS IS THE INTEGRATION HALF of internal/service/ingest/extract's own tests, and it exists because
// the two halves are different claims: the extractor's test proves it consults the rules, and this one
// proves the REASON reaches the operator. "unreachable" and "robots_disallowed" are both non-fatal and
// both retried, and only one of them sends somebody to look at a firewall.
func TestADrainRecordsARobotsRefusalWithoutFetchingThePage(t *testing.T) {
	fetchSourcesForTest(t)

	var pageHits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			_, _ = w.Write([]byte("User-agent: *\nDisallow: /watch/\n"))
			return
		}
		atomic.AddInt32(&pageHits, 1)
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><meta property="og:title" content="A Scene"></head></html>`))
	}))
	t.Cleanup(server.Close)

	raw := server.URL + "/watch/" + uuid.Must(uuid.NewV4()).String()
	normalized, err := urlkey.Normalize(raw)
	require.NoError(t, err)
	cleanupIngestFixtures(t, normalized)

	r := asEdit(t)
	require.Equal(t, "queued", submitContentURL(t, r, raw, nil).Status)

	processed, err := ingestServiceWith(extract.NewHTTPExtractor()).Drain(t.Context(), 10)
	require.NoError(t, err)
	require.Equal(t, 1, processed, "the job's outcome was recorded, which is what Drain counts")

	assert.Zero(t, atomic.LoadInt32(&pageHits), "the page must never be requested")

	row := readQueueRow(t, normalized)
	require.NotNil(t, row.Reason)
	assert.Equal(t, "robots_disallowed", *row.Reason,
		"the queue says the host's robots.txt refused this, and not that the host was broken")
	assert.Equal(t, "pending", row.Status,
		"and it is transient: robots.txt is a file the host can change, so the backoff is the right "+
			"way to ask again rather than burning the URL as a permanent failure")
	require.NotNil(t, row.LastError)
	assert.Contains(t, *row.LastError, "Disallow")
	assert.Nil(t, row.ResultSceneID, "no record was created")

	// THE CONTROL. The same server, a path its robots.txt allows: the drain fetches it and the
	// pipeline does its ordinary work. Without this the test would pass on an extractor that refused
	// everything, which is a different bug and a much louder one.
	allowed := server.URL + "/public/" + uuid.Must(uuid.NewV4()).String()
	allowedNormalized, err := urlkey.Normalize(allowed)
	require.NoError(t, err)
	cleanupIngestFixtures(t, allowedNormalized)
	require.Equal(t, "queued", submitContentURL(t, r, allowed, nil).Status)

	processed, err = ingestServiceWith(extract.NewHTTPExtractor()).Drain(t.Context(), 10)
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	assert.Equal(t, int32(1), atomic.LoadInt32(&pageHits), "an allowed path IS fetched")
}
