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

// withSwitches sets the two posture keys for one test and restores them after.
func withSwitches(t *testing.T, images, content bool) {
	t.Helper()
	previousImages, previousContent := config.C.ImagesEnabled, config.C.ContentDelivery
	t.Cleanup(func() {
		config.C.ImagesEnabled, config.C.ContentDelivery = previousImages, previousContent
	})
	config.C.ImagesEnabled, config.C.ContentDelivery = images, content
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
}
