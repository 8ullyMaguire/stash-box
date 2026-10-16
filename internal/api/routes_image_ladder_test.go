package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/service/mediaplane"
)

// THE INVARIANT THIS TEST EXISTS FOR, and the mistake that produced it.
//
// The route accepts size=300, 600 or 1280 (allowedSizes), and the ladder stores VariantWidths. The first
// version of VariantWidths was 320 and 480 -- taken from the storage measurement instead of read off the
// route -- which would have had the producer write files NO REQUEST CAN EVER ASK FOR: the "a thing nothing
// asks for" shape, arrived at by writing the second list from the first one's intent rather than from the
// first list.
//
// The rule is one-directional, and the asymmetry is the decision: every STORED width must be requestable (a
// stored width nobody can ask for is dead weight, and nothing outside can notice), while a requestable width
// the ladder does not store is a fall-through to the instance's own image path. 1280 is one of those.
func TestEveryStoredVariantWidthIsOneTheRouteCanAskFor(t *testing.T) {
	require.NotEmpty(t, mediaplane.VariantWidths, "the ladder stores no widths: this test would pass vacuously")
	for _, width := range mediaplane.VariantWidths {
		assert.True(t, slices.Contains(allowedSizes, width),
			"the ladder stores %d px, which the route refuses (allowedSizes = %v): the producer would write "+
				"files the server never asks for", width, allowedSizes)
	}
}

// A stored variant is served WITHOUT a database, which is the whole point of putting the ladder arm before the
// query: this router is built over a ZERO-VALUED factory, so a handler that reached for the database would
// panic rather than quietly pass. The bytes come from the file the test wrote.
func TestAStoredVariantIsServedWithoutTouchingTheDatabase(t *testing.T) {
	withSwitches(t, true, true)
	dir := t.TempDir()
	withMediaDir(t, dir) // ours, after the helper's own: the request must read the directory this test filled
	id := uuid.Must(uuid.NewV4())
	want := []byte("RIFF....WEBPVP8 ")

	path := filepath.Join(dir, id.String()[:2], id.String()+"_600.webp")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, want, 0o644))

	srv := httptest.NewServer(imageRoutes{}.Routes())
	t.Cleanup(srv.Close)

	resp, err := srv.Client().Get(srv.URL + "/" + id.String() + "?size=600")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode, "a stored variant must be served without a query")
	assert.Equal(t, "image/webp", resp.Header.Get("Content-Type"),
		"the ladder stores one format, so the answer is not negotiated per request")
	assert.Equal(t, "max-age=604800000", resp.Header.Get("Cache-Control"),
		"a stored variant is immutable for the lifetime of the width's definition: without the header every "+
			"client revalidates bytes that cannot change")
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, want, body)
}

// POSTURE BEATS THE LADDER, in that order. An instance with images off must refuse a request for a variant it
// HAS: otherwise "images are off" would be a statement about the configuration rather than about what the
// instance serves, and storing one file would be enough to change it.
func TestPostureBeatsAStoredVariant(t *testing.T) {
	withSwitches(t, false, false)
	dir := t.TempDir()
	withMediaDir(t, dir)
	id := uuid.Must(uuid.NewV4())

	path := filepath.Join(dir, id.String()[:2], id.String()+"_300.webp")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("RIFF....WEBP"), 0o644))

	srv := httptest.NewServer(imageRoutes{}.Routes())
	t.Cleanup(srv.Close)

	resp, err := srv.Client().Get(srv.URL + "/" + id.String() + "?size=300")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	assert.NotContains(t, string(body), "RIFF",
		"the refusal carried the bytes: the gate ran after the ladder instead of before it")
}
