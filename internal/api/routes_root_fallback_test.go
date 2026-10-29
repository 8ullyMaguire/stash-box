package api

// The SPA fallback must not answer for API paths.
//
// WHY THIS TEST EXISTS
// --------------------
// `rootRoutes.Routes()` registers `r.HandleFunc("/*", rr.app)` LAST, so `app` handles every path chi
// did not match — including `/api/...` and `/graphql/...`. It wrote the SPA shell with a 200.
//
// Measured on the live instance before the fix:
//
//	GET /api/__probe_no_such_route__ -> 200 text/html, 1,131 bytes, "<!doctype html>..."
//
// A GraphQL or JSON client reports that as a parse error; a monitor watching status codes reports
// the endpoint healthy. The two tests below pin both halves: the API path becomes a 404 a client can
// read, and a page path still gets the shell.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func rootWithShell() rootRoutes {
	return rootRoutes{index: []byte("<!doctype html><html><body>shell</body></html>")}
}

func TestAnUnroutedAPIPathIsAJSON404(t *testing.T) {
	rr := rootWithShell()

	for _, path := range []string{"/api", "/api/", "/api/__probe_no_such_route__", "/graphql/x", "/graphql"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)

		rr.app(rec, req)

		assert.Equal(t, http.StatusNotFound, rec.Code, "%s must not answer 200", path)
		assert.Contains(t, rec.Header().Get("Content-Type"), "application/json", "%s", path)
		assert.Contains(t, rec.Body.String(), "not_found", "%s", path)
		assert.NotContains(t, rec.Body.String(), "<!doctype html>", "%s answered the SPA shell", path)
	}
}

func TestAPagePathStillServesTheShell(t *testing.T) {
	rr := rootWithShell()

	for _, path := range []string{"/", "/scenes/12345", "/performers/someone", "/apiary"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)

		rr.app(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code, "%s is a page path", path)
		assert.Contains(t, rec.Body.String(), "<!doctype html>", "%s lost the shell", path)
	}
}

// A route that merely starts with the letters "api" is not an API path — the prefix test is on
// path SEGMENTS, or /apiary (a page) would 404.
func TestTheAPIPrefixIsMatchedOnASegmentBoundary(t *testing.T) {
	assert.True(t, isAPIPath("/api"))
	assert.True(t, isAPIPath("/api/v1/anything"))
	assert.True(t, isAPIPath("/graphql"))
	assert.False(t, isAPIPath("/apiary"))
	assert.False(t, isAPIPath("/scenes"))
	assert.False(t, isAPIPath("/"))
}
