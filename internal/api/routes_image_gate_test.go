package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/config"
	"github.com/stashapp/stash-box/internal/service/mediaplane"
)

// The image posture gate (plan phase 10, slice 10a-ii).
//
// The claim under test is not "the response is 403". It is "nothing served bytes", and the two halves of
// that are measured separately:
//
//   - the REAL router, with its own middleware and its own handlers, refuses while the switches are off
//     (the handlers would panic on a zero factory, so a 403 is also proof they never ran);
//   - the middleware, over a probe handler, lets the request through when EITHER switch is on -- the
//     control, without which the refusal could be a gate that refuses unconditionally.

// withSwitches sets the two posture keys AND the media directory for one test, restoring all three after.
//
// The directory is part of the setup since slice 10b-1, because the gate has TWO conditions: the switch says
// whether this instance may serve image bytes and `media_dir` says where from. A test that turned a switch on
// and expected the request to pass was asserting the old contract, and the mutation battery found it -- the
// harness restores the file and runs the whole package, so a test left behind by a contract change shows up as
// a RESTORE-FAIL rather than as a green run that proves the wrong thing.
func withSwitches(t *testing.T, images, content bool) {
	t.Helper()
	previousImages, previousContent := config.C.ImagesEnabled, config.C.ContentDelivery
	t.Cleanup(func() {
		config.C.ImagesEnabled, config.C.ContentDelivery = previousImages, previousContent
	})
	config.C.ImagesEnabled, config.C.ContentDelivery = images, content

	// A real directory, because "the switches are on" is no longer sufficient to serve: an instance with no
	// directory refuses by naming the key, which is the behaviour TestImageRouterRefusesWhenTheDirIsMissing
	// asserts on purpose.
	withMediaDir(t, t.TempDir())
}

// withMediaDir points the instance at a directory for one test and restores the previous value after.
//
// It also drops the process-wide media store, because that store resolves `media_dir` ONCE and compares the
// filesystem it is on against the value taken at that moment. A test that changes the directory without
// dropping the store would be exercising the previous test's directory -- and the device check that catches
// an unmounted disk is only meaningful against a value taken when the process started, so caching it is the
// point rather than an optimisation.
func withMediaDir(t *testing.T, dir string) {
	t.Helper()
	previous := config.C.MediaDir
	t.Cleanup(func() {
		config.C.MediaDir = previous
		mediaplane.ResetStoreForTest()
	})
	config.C.MediaDir = dir
	mediaplane.ResetStoreForTest()
}

func TestImageRoutesRefuseWhenNeitherSwitchIsOn(t *testing.T) {
	withSwitches(t, false, false)

	// The production router: the same Routes() the server mounts, with a zero-valued factory. A handler
	// that ran would panic on it, which is why every 403 below is also evidence the handler did not.
	srv := httptest.NewServer(imageRoutes{}.Routes())
	t.Cleanup(srv.Close)

	for _, path := range []string{
		"/00000000-0000-4000-8000-000000000000",
		"/site/00000000-0000-4000-8000-000000000000",
		"/not-a-uuid",
	} {
		t.Run(path, func(t *testing.T) {
			resp, err := srv.Client().Get(srv.URL + path)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, http.StatusForbidden, resp.StatusCode)

			var body map[string]string
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
			assert.Equal(t, mediaplane.ErrNoImageBytes.Error(), body["error"],
				"the refusal is the package's own error value, so the key travels with it")
			assert.Contains(t, body["error"], "images_enabled", "an operator needs the key they must change")
			assert.Contains(t, body["error"], "content_delivery", "and the other one that would also do it")
		})
	}
}

func TestImageGateOpensWhenEitherSwitchIsOn(t *testing.T) {
	// The control, and the OR rule: an operator who has accepted content delivery has accepted serving an
	// image, and one who turned images on has accepted images. The probe records that the request reached
	// a handler, which is the half the production handlers cannot show while the gate is open (they need
	// a database).
	probe := func(called *int) http.Handler {
		return requireImageBytes(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*called++
			w.WriteHeader(http.StatusTeapot)
		}))
	}

	for _, tc := range []struct {
		name            string
		images, content bool
	}{
		{"images_enabled alone", true, false},
		{"content_delivery alone", false, true},
		{"both", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withSwitches(t, tc.images, tc.content)

			called := 0
			srv := httptest.NewServer(probe(&called))
			t.Cleanup(srv.Close)

			resp, err := srv.Client().Get(srv.URL + "/images/00000000-0000-4000-8000-000000000000")
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, http.StatusTeapot, resp.StatusCode, "the handler behind the gate ran")
			assert.Equal(t, 1, called)
		})
	}
}

func TestImageBytesFollowTheBroaderPosture(t *testing.T) {
	// The predicate on its own. The error is compared by IDENTITY (assert.Same): two errors with the same
	// text are two refusals a caller cannot tell apart, and the house rule is one error value per refusal.
	withSwitches(t, false, false)
	assert.False(t, mediaplane.ImageBytesEnabled())
	assert.Same(t, mediaplane.ErrNoImageBytes, mediaplane.RequireImageBytes())

	withSwitches(t, true, false)
	assert.True(t, mediaplane.ImageBytesEnabled())
	assert.NoError(t, mediaplane.RequireImageBytes())

	withSwitches(t, false, true)
	assert.True(t, mediaplane.ImageBytesEnabled())
	assert.NoError(t, mediaplane.RequireImageBytes())

	// And the second condition, both ways: the switch is on and the directory is not there.
	withSwitches(t, true, false)
	withMediaDir(t, "")
	assert.Same(t, mediaplane.ErrNoMediaDir, mediaplane.RequireImageBytes(),
		"the switch alone is not enough: an instance with no media_dir has nowhere to serve from")
}

// The production router, with the switch ON and no media directory: 403, and the body names `media_dir`.
//
// This is the arm the shape of the refusal is for. Without it an operator in exactly this state -- images
// enabled, directory missing, nothing writing thumbnails yet -- would see the handlers answer 404 for every
// image, which reads as an instance that has no such image rather than as one that cannot serve any.
func TestImageRouterRefusesWhenTheDirIsMissing(t *testing.T) {
	withSwitches(t, true, false)
	withMediaDir(t, "")

	srv := httptest.NewServer(imageRoutes{}.Routes())
	t.Cleanup(srv.Close)

	resp, err := srv.Client().Get(srv.URL + "/00000000-0000-4000-8000-000000000000")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Contains(t, body["error"], "media_dir", "an operator needs the key they must set")
	assert.Contains(t, body["error"], "not set")
}
