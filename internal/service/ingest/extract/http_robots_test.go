package extract

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/service/hostrules"
)

// The extractor's half of the crawl-hygiene posture (internal/service/hostrules is the other half,
// and owns the rules themselves).
//
// WHAT THIS ASSERTS THAT THE RULES' OWN TESTS CANNOT: that the extractor ASKS. A Rules implementation
// that is perfect and never consulted would pass every test in its own package, and the fetch would
// go out anyway -- so the page handler here counts its requests, and the disallowed case asserts the
// count is zero.

func robotsServer(t *testing.T, robots string) (*httptest.Server, *int32) {
	t.Helper()
	var pageHits int32

	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		if robots == "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(robots))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&pageHits, 1)
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><meta property="og:title" content="A Scene"></head></html>`))
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server, &pageHits
}

// rulesFor builds a Rules with no pacing, so a test that fetches twice does not wait two seconds.
// The robots fetch is the real one, against the httptest server the URL points at.
func rulesFor(t *testing.T) *hostrules.Rules {
	t.Helper()
	return hostrules.New(0)
}

func TestExtractRefusesAPathRobotsDisallowsAndMakesNoRequest(t *testing.T) {
	server, hits := robotsServer(t, "User-agent: *\nDisallow: /private/\n")
	extractor := HTTPExtractor{Rules: rulesFor(t)}

	_, err := extractor.Extract(context.Background(), server.URL+"/private/x")
	require.Error(t, err)
	assert.ErrorIs(t, err, hostrules.ErrDisallowed,
		"the refusal must be the named one: the pipeline records the queue reason from it, and "+
			"'unreachable' would send an operator looking for a network problem")
	assert.Contains(t, err.Error(), "Disallow", "the message carries the rule that refused it")
	assert.Zero(t, atomic.LoadInt32(hits), "the page must not be requested at all")

	// THE CONTROL. Same server, same extractor, a path robots allows: the fetch happens and the
	// metadata comes back. Without this, a test double that refused everything would pass.
	meta, err := extractor.Extract(context.Background(), server.URL+"/watch/1")
	require.NoError(t, err)
	assert.Equal(t, "A Scene", meta.Title, "the allowed path is read and parsed as before")
	assert.Equal(t, int32(1), atomic.LoadInt32(hits))
}

// A host that states no terms is fetched: RFC 9309's allow-all, and the case most real sites are in.
func TestExtractFetchesWhenThereIsNoRobotsTxt(t *testing.T) {
	server, hits := robotsServer(t, "")
	extractor := HTTPExtractor{Rules: rulesFor(t)}

	meta, err := extractor.Extract(context.Background(), server.URL+"/watch/1")
	require.NoError(t, err)
	assert.Equal(t, "A Scene", meta.Title)
	assert.Equal(t, int32(1), atomic.LoadInt32(hits), "the page was fetched exactly once")
}

// And a host whose robots.txt cannot be read is NOT fetched, because the terms are unknown rather
// than absent (RFC 9309: an unavailable robots.txt is treated as complete disallow).
func TestExtractMakesNoRequestWhenRobotsCannotBeRead(t *testing.T) {
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write([]byte(`<html><head><meta property="og:title" content="A Scene"></head></html>`))
	}))
	t.Cleanup(server.Close)

	extractor := HTTPExtractor{Rules: rulesFor(t)}
	_, err := extractor.Extract(context.Background(), server.URL+"/watch/1")
	require.Error(t, err)
	assert.ErrorIs(t, err, hostrules.ErrRobotsUnavailable,
		"an unreadable robots.txt is 'the host did not answer', which is a different error from a rule")
	assert.ErrorIs(t, err, ErrUnreachable,
		"and the extractor reports it in its own taxonomy, so the queue row says 'unreachable' -- "+
			"which is a retry, not a policy decision the operator has to make")
	assert.Zero(t, atomic.LoadInt32(&hits), "an unknown-terms host is not fetched")
}

// The extractor paces itself: two fetches to one host are spaced by the rules' interval. Driven with
// a real clock and a small interval, because a fake one would prove the Rules' arithmetic again
// rather than that the extractor waits.
func TestExtractWaitsBetweenRequestsToOneHost(t *testing.T) {
	server, _ := robotsServer(t, "")
	rules := hostrules.New(150 * time.Millisecond)
	extractor := HTTPExtractor{Rules: rules}

	start := time.Now()
	_, err := extractor.Extract(context.Background(), server.URL+"/watch/1")
	require.NoError(t, err)
	_, err = extractor.Extract(context.Background(), server.URL+"/watch/2")
	require.NoError(t, err)

	if elapsed := time.Since(start); elapsed < 150*time.Millisecond {
		t.Fatalf("two fetches to one host took %s: the extractor is not asking the rules to pace it", elapsed)
	}
}

// The production extractor resolves a nil Rules to the PROCESS-WIDE one, which is the property that
// makes the sweep and the ingest fetch share a pace and a robots cache. Asserted through a real
// refusal rather than by reading a field, because "the field is nil" is exactly what a broken default
// path would also look like.
func TestExtractUsesTheSharedRulesWhenNoneAreInjected(t *testing.T) {
	server, hits := robotsServer(t, "User-agent: *\nDisallow: /\n")
	extractor := NewHTTPExtractor()

	_, err := extractor.Extract(context.Background(), server.URL+"/watch/1")
	require.Error(t, err)
	assert.ErrorIs(t, err, hostrules.ErrDisallowed,
		"an extractor constructed the production way must obey robots.txt: a nil Rules that silently "+
			"meant 'no rules' would make the posture a comment")
	assert.Zero(t, atomic.LoadInt32(hits), "and it must not fetch")
}
