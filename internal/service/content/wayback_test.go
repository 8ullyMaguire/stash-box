package content

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The submission's contract with the archive: a copy is named, or it is a failure.
//
// The last case is the one worth having the test for -- a 200 with no `Content-Location` is a
// SUCCESS that produced nothing this instance can record, and treating it as one would write a row
// whose archived_url is empty and whose capture time is real.
func TestWaybackNamesTheCopyOrFails(t *testing.T) {
	var location string
	var code int
	var gotForm string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		gotForm = r.Form.Get("url")
		if location != "" {
			w.Header().Set("Content-Location", location)
		}
		w.WriteHeader(code)
	}))
	defer server.Close()

	archiver := Wayback{Client: &http.Client{Transport: rewriteTo(server.URL)}}

	location, code = "/web/20261015120000/https://example.invalid/scene", http.StatusOK
	got, err := archiver.Archive(context.Background(), "https://example.invalid/scene")
	require.NoError(t, err)
	assert.Equal(t, "https://web.archive.org/web/20261015120000/https://example.invalid/scene", got,
		"the path the archive answers with has to be made absolute, or the column holds half a URL")
	assert.Equal(t, "https://example.invalid/scene", gotForm, "the submission carries the URL")

	location, code = "https://web.archive.org/web/9/https://example.invalid/other", http.StatusOK
	got, err = archiver.Archive(context.Background(), "https://example.invalid/other")
	require.NoError(t, err)
	assert.Equal(t, "https://web.archive.org/web/9/https://example.invalid/other", got,
		"an absolute location is taken as it comes")

	location, code = "", http.StatusOK
	_, err = archiver.Archive(context.Background(), "https://example.invalid/silent")
	require.Error(t, err, "a success naming no copy is not an archive")
	assert.Contains(t, err.Error(), "Content-Location")

	location, code = "/web/1/https://example.invalid/refused", http.StatusInternalServerError
	_, err = archiver.Archive(context.Background(), "https://example.invalid/refused")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

// rewriteTo points the client at a local server while the request still looks like the real endpoint,
// so the production URL stays in the code under test.
func rewriteTo(base string) http.RoundTripper {
	return roundTripFunc(func(req *http.Request) (*http.Response, error) {
		rewritten := req.Clone(req.Context())
		rewritten.URL.Scheme = "http"
		rewritten.URL.Host = strings.TrimPrefix(base, "http://")
		return http.DefaultTransport.RoundTrip(rewritten)
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
