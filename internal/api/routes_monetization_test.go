package api

import (
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/config"
	"github.com/stashapp/stash-box/internal/service/monetization"
)

// The monetization page (phase 15, spec §18), tested as a function of the posture it renders.
//
// No database and no HTTP server here on purpose: the two limits and the entitlement are measured elsewhere,
// and what is left is the thing a rendering test is uniquely able to catch -- a page that disagrees with the
// posture it was handed, or a sentence that is hard-coded rather than derived.

// monetizationForTest sets the switch, the streams and the comp list for one test and restores them: config.C is
// a package global, and a page test that left the switch on would assert the wrong sentence for the next one.
func monetizationForTest(t *testing.T, enabled bool, streams, members []string) {
	t.Helper()
	previousEnabled, previousStreams := config.C.MonetizationEnabled, config.C.MonetizationStreams
	previousMembers := config.C.MonetizationPremiumMembers
	t.Cleanup(func() {
		config.C.MonetizationEnabled = previousEnabled
		config.C.MonetizationStreams = previousStreams
		config.C.MonetizationPremiumMembers = previousMembers
	})

	config.C.MonetizationEnabled = enabled
	config.C.MonetizationStreams = streams
	config.C.MonetizationPremiumMembers = members
}

// serveMonetization renders the page through the same route the instance mounts.
func serveMonetization(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()

	res := httptest.NewRecorder()
	monetizationRoutes{}.
		Routes().
		ServeHTTP(res, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
	return res
}

// THE PAGE STATES THE POSTURE THE INSTANCE IS IN, in both directions, which is the pair that makes a
// hard-coded sentence fail: an instance that charges for nothing says so, and one that charges says so.
func TestTheMonetizationPageStatesWhicheverPostureTheInstanceIsIn(t *testing.T) {
	monetizationForTest(t, false, nil, nil)
	off := serveMonetization(t, "/")
	require.Equal(t, http.StatusOK, off.Code)
	assert.Contains(t, off.Body.String(), "charges for nothing",
		"with the switch off the page must say the instance charges nothing")
	assert.Contains(t, off.Body.String(), "monetization_enabled",
		"and name the key that decides it, so a reader can check the claim against their configuration")

	monetizationForTest(t, true, []string{monetization.StreamPremiumSubscription}, []string{"member-1"})
	on := serveMonetization(t, "/")
	assert.Contains(t, on.Body.String(), "charges</strong> for the streams listed below",
		"with the switch on the page must say the instance charges, not repeat the off sentence")
	assert.NotContains(t, on.Body.String(), "charges for nothing")
}

// EVERY ALWAYS-FREE ITEM AND EVERY SURFACE REACHES THE PAGE. This is the assertion that makes the item list
// load-bearing for a reader: §18.2's words, this instance's surfaces and each item's state all have to appear,
// or the page is describing a list it cannot substantiate.
func TestTheMonetizationPageShowsEveryAlwaysFreeItemAndItsSurfaces(t *testing.T) {
	monetizationForTest(t, false, nil, nil)
	page := serveMonetization(t, "/").Body.String()

	for _, c := range monetization.AlwaysFree() {
		assert.Contains(t, page, c.Name, "the capability %q is missing from the page", c.Name)
		assert.Contains(t, page, c.Words, "§18.2's words for %q are missing", c.Name)
		for _, s := range c.Surfaces {
			// Compared against the ESCAPED reference, because that is what the page legitimately shows: a
			// feed route reads `/feeds/saved-search/&lt;token&gt;` in the document, and a test that insisted on
			// the raw form would be asking the page to emit a `<` it must not.
			assert.Contains(t, page, html.EscapeString(s.Ref),
				"the surface %q that provides %q is missing: the page would promise something nothing shows",
				s.Ref, c.Name)
		}
	}

	// And the enforcement sentence, which is the difference between publishing a list and keeping it.
	assert.Contains(t, page, "refuses to gate",
		"the page must say the list is enforced in code rather than promised on the page")
	assert.NotContains(t, page, "noindex",
		"a posture nobody can find is not a posture")
}

// THE PAGE'S NUMBERS COME FROM THE POSTURE, including both limits: a page that hard-coded 600 beside a changed
// configuration would disagree with the headers the same instance sends.
func TestTheMonetizationPageRendersTheLimitsItWasHanded(t *testing.T) {
	previousPerMinute, previousBurst := config.C.MonetizationPremiumRequestsPerMinute, config.C.MonetizationPremiumBurst
	t.Cleanup(func() {
		config.C.MonetizationPremiumRequestsPerMinute, config.C.MonetizationPremiumBurst = previousPerMinute, previousBurst
	})
	config.C.MonetizationPremiumRequestsPerMinute, config.C.MonetizationPremiumBurst = 4321, 432
	monetizationForTest(t, true, []string{monetization.StreamPremiumSubscription}, nil)

	posture := monetization.PostureOf()
	page := serveMonetization(t, "/").Body.String()

	assert.Contains(t, page, strconv.Itoa(posture.FreeLimit.RequestsPerMinute))
	assert.Contains(t, page, strconv.Itoa(posture.PremiumLimit.RequestsPerMinute))
	assert.Contains(t, page, strconv.Itoa(posture.PremiumLimit.Burst))
	if posture.PremiumLimit.RequestsPerMinute != 4321 {
		t.Fatalf("the fixture did not take effect: the posture reports %d", posture.PremiumLimit.RequestsPerMinute)
	}
}

// The JSON is the same value the page renders, so a reader who checks one against the other cannot find a
// disagreement -- and the switch's own state is in it under the key an operator configures.
func TestTheMonetizationJsonServesTheSamePostureAsThePage(t *testing.T) {
	monetizationForTest(t, true, []string{monetization.StreamPremiumSubscription}, []string{"a", "b"})

	res := serveMonetization(t, "/json")
	require.Equal(t, http.StatusOK, res.Code)
	assert.Contains(t, res.Header().Get("Content-Type"), "application/json")

	var served monetization.Posture
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &served),
		"the endpoint must serve the posture as JSON; the live walker recomputes from this body")

	posture := monetization.PostureOf()
	assert.Equal(t, posture.Enabled, served.Enabled)
	assert.Equal(t, len(posture.AlwaysFree), len(served.AlwaysFree))
	assert.Equal(t, len(posture.Premium), len(served.Premium))
	assert.Equal(t, posture.PremiumMembers, served.PremiumMembers)
	assert.Equal(t, posture.FreeLimit, served.FreeLimit)
	assert.Equal(t, posture.PremiumLimit, served.PremiumLimit)

	// The surface list is carried in the JSON too: a checker needs the references, not only the names.
	require.NotEmpty(t, served.AlwaysFree)
	require.NotEmpty(t, served.AlwaysFree[0].Surfaces)
	assert.NotEmpty(t, served.AlwaysFree[0].Surfaces[0].Ref)

	// And the comps count is published while the ids are not: who pays is their business.
	body := res.Body.String()
	assert.NotContains(t, body, "member-1")
	assert.NotContains(t, body, `"a"`)
}

// The not-built list is built from the two lists rather than written out again, so a feature that becomes
// available stops being described as missing without an edit here.
func TestTheMonetizationPageNamesWhatIsNotBuiltFromTheLists(t *testing.T) {
	monetizationForTest(t, true, []string{monetization.StreamPremiumSubscription}, nil)
	page := serveMonetization(t, "/").Body.String()

	posture := monetization.PostureOf()
	unavailable := 0
	for _, f := range posture.Premium {
		if !f.Available {
			unavailable++
			assert.Contains(t, page, f.Name, "an unavailable feature must be named as such")
		}
	}
	if unavailable == 0 {
		t.Fatal("no premium feature is unavailable on this tree: this test cannot see the not-built list")
	}

	// And the one §18.1 stream with an implementation is NOT described as missing.
	for _, s := range posture.Streams {
		if s.On {
			assert.Contains(t, page, s.Name)
		}
	}
	_ = time.Now
}
