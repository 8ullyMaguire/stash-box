//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/config"
	"github.com/stashapp/stash-box/internal/ratelimit"
	"github.com/stashapp/stash-box/internal/service/monetization"
	"github.com/stashapp/stash-box/internal/service/user"
)

// THE LEAK TEST (plan phase 15, spec §18).
//
// §18.2 lists seven things a paywall may never touch, and the risk the plan names is that one of them leaks:
// a core path silently requires a subscription the day somebody adds a feature. The way to test that is not to
// inspect the switch -- it is to TURN IT ON and require that nothing a member could do before they changed.
// So: every always-free capability's surfaces are called with monetization OFF, then with it ON and the premium
// API tier being SOLD to the very member making the calls, and the two outcomes must be identical.
//
// AND THEN THE CONTROL. An equality between two runs proves nothing unless the thing being switched on can
// change something, so the same test goes on to measure the one §18.1 feature that DOES move: the premium
// member's rate limit. If that measurement ever stops seeing a difference, this test's equality stops meaning
// anything -- which is exactly how a leak test rots into a green light.

// monetizationForTest sets the switch, the streams and the comp list for one test, and restores them.
//
// The restore matters more here than anywhere: config.C is a package global read by the limiter, by the
// entitlement and by every assertion in this file, so a test that left the switch on would let the OFF half of
// this test pass for the wrong reason.
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

// monetizationOnForTest sells the premium API tier to the named members.
func monetizationOnForTest(t *testing.T, members ...string) {
	t.Helper()
	monetizationForTest(t, true, []string{monetization.StreamPremiumSubscription}, members)
}

// probe is one surface the leak test calls, with the role it needs.
type probe struct {
	role  string // "read", "edit" or "admin"
	query string
}

// probesFor maps a GraphQL surface reference to the call that reaches it.
//
// THE MUTATING SURFACES ARE ABSENT ON PURPOSE. `sceneEdit` and `submitSceneDraft` are §18.2's "tagging and
// community participation", and a leak test that submitted an edit on every run would be a leak test that
// writes rows. They are covered by the schema assertion below (the field exists, and the only access rule the
// schema has is the role system), and the limitation is stated rather than papered over.
var probesFor = map[string]probe{
	"searchScenes":     {role: "read", query: `{ searchScenes(term: "monetization-leak-probe") { count } }`},
	"searchPerformers": {role: "read", query: `{ searchPerformers(term: "monetization-leak-probe") { count } }`},
	"queryScenes":      {role: "read", query: `{ queryScenes(input: {}) { count } }`},
	"findScene":        {role: "read", query: `{ findScene(id: "00000000-0000-0000-0000-0000000000ff") { id } }`},
	"findScenes":       {role: "read", query: `{ findScenes(ids: ["00000000-0000-0000-0000-0000000000ff"]) { id } }`},
	"exportScenes":     {role: "read", query: `{ exportScenes(ids: ["00000000-0000-0000-0000-0000000000ff"]) }`},
	"lists":            {role: "read", query: `{ lists { id } }`},
	// The ADMIN surface of §18.2's federation item: a member with no such role is refused in both modes, and
	// the equality is the assertion -- monetization must not be able to change a role's answer.
	"federation_peers": {role: "read", query: `{ federation_peers { id } }`},
}

// probeRunnerFor returns the client a probe's role needs. Named for the probes rather than for roles: the
// suite already has a `runnerFor` with a login's signature, and a redefinition of it is a build failure rather
// than a shadowing warning.
func probeRunnerFor(t *testing.T, role string) *testRunner {
	t.Helper()

	switch role {
	case "edit":
		return asEdit(t)
	case "admin":
		return asAdmin(t)
	default:
		return asRead(t)
	}
}

// runProbes calls every always-free surface and returns one canonical outcome string per surface: the GraphQL
// error if there was one, and the marshalled payload otherwise.
//
// THE OUTCOME IS COMPARED BETWEEN MODES, NEVER AGAINST AN EXPECTATION, and that is deliberate: "search still
// works" is a claim about the fixture, while "search answers the same thing whichever posture the instance is
// in" is a claim about the switch. A paywall that refused the call in one mode and not the other shows up as a
// difference; whether the call itself succeeded is the other tests' business.
func runProbes(t *testing.T) map[string]string {
	t.Helper()

	read := asRead(t)
	outcomes := map[string]string{}

	for _, capability := range monetization.AlwaysFree() {
		for _, surface := range capability.Surfaces {
			if surface.Kind != "graphql" {
				continue
			}
			probe, ok := probesFor[surface.Ref]
			key := capability.Name + "/" + surface.Ref
			if !ok {
				// A surface with no probe is not called, and saying so here is the point: the schema assertion
				// below still requires it to exist, so a surface cannot be listed and forgotten.
				outcomes[key] = "not called by this test (see the schema assertion)"
				continue
			}

			runner := read
			if probe.role != "read" {
				runner = probeRunnerFor(t, probe.role)
			}

			var payload map[string]any
			err := runner.client.Post(probe.query, &payload)
			if err != nil {
				outcomes[key] = "error: " + err.Error()
				continue
			}
			encoded, marshalErr := json.Marshal(payload)
			require.NoError(t, marshalErr)
			outcomes[key] = "ok: " + string(encoded)
		}
	}
	return outcomes
}

// alwaysFreeOutcomesWithMonetizationOffAndOn is the leak test itself.
func TestEveryAlwaysFreeCapabilityAnswersTheSameWithMonetizationOffAndOn(t *testing.T) {
	require.NotEmpty(t, userDB.read.ID, "the fixture has no member to comp")

	monetizationForTest(t, false, nil, nil)
	off := runProbes(t)

	// The SAME member is now paying for the premium tier, which is the strongest form of the test: not a
	// bystander unaffected by the switch, but the person the switch is about.
	monetizationOnForTest(t, userDB.read.ID.String())
	on := runProbes(t)

	require.Equal(t, len(off), len(on), "the two runs did not visit the same surfaces")
	for key, before := range off {
		assert.Equal(t, before, on[key],
			"%s changed when monetization was turned on: §18.2 says it may not", key)
	}

	// AND §18.2's SEARCH IS REALLY BEING CALLED: a capability whose every probe errored would satisfy the
	// equality above trivially. This is the count assertion that keeps an absence assertion honest.
	assert.Contains(t, off["search/searchScenes"], "count",
		"the search probe did not reach the field: the equality above is about a surface nobody called")
	assert.Contains(t, off["collections/lists"], "ok:",
		"the collections probe did not reach the field: its equality is about a surface nobody called")
}

// THE CONTROL: the switch must be able to change SOMETHING, or the equality above is a statement about a switch
// that does nothing. The one §18.1 feature with an implementation is the premium API tier, and its effect is
// measurable on the wire: the limit the same credential is counted against.
func TestTheMonetizationSwitchChangesTheOnlyThingItMayChange(t *testing.T) {
	require.NotEmpty(t, userDB.read.ID)

	key := apiKeyForMember(t, userDB.read.ID.String())
	other := apiKeyForMember(t, userDB.edit.ID.String())

	// Off: the member's key is at the free limit, and nothing about them is special.
	monetizationForTest(t, false, nil, nil)
	assert.Equal(t, config.GetRateLimit().RequestsPerMinute, limitForRequest(t, key),
		"with monetization off a member's credential must be counted at the free limit")

	// On, and the member comped: their key is at the premium limit -- and an uncomped member's key is not.
	monetizationOnForTest(t, userDB.read.ID.String())
	assert.Equal(t, config.GetMonetizationPremiumRateLimit().RequestsPerMinute, limitForRequest(t, key),
		"a comped member's key must be counted at the premium limit while the stream is on")
	assert.Equal(t, config.GetRateLimit().RequestsPerMinute, limitForRequest(t, other),
		"an uncomped member's key moved with the switch: the premium tier is not a licence to raise everybody")

	// And an anonymous caller, who presents no credential at all: free, in both modes.
	assert.Equal(t, config.GetRateLimit().RequestsPerMinute, limitForRequest(t, ""))

	// THE FEED CREDENTIAL TOO, because §18.2's "API access" item names this instance's feed route as one of its
	// two surfaces: a feed token is not an API key and resolves to no member, so a feed reader cannot be
	// premium in either mode -- measured rather than argued, through the same middleware with the feed's own
	// credential function.
	assert.Equal(t, config.GetRateLimit().RequestsPerMinute, limitForFeedToken(t), "a feed reader moved with the switch")
}

// limitForRequest runs one request through the WIRED middleware chain and reads the limit it was counted
// against.
//
// It builds the chain the server builds (the same constructor, the same credential function, the same
// entitlement), because the rate limiter is not reachable through the GraphQL test harness -- that harness
// calls the gqlgen handler directly with a context, so no middleware runs. What this measures is therefore the
// limiter's decision as the wire would see it, which is the same number the response header carries.
func limitForRequest(t *testing.T, apiKey string) int {
	t.Helper()

	free, premium := monetization.Limits()
	limiter := ratelimit.NewWithTiers(free, premium, monetization.CredentialIsPremium)
	require.NotNil(t, limiter, "the free limit is disabled in this environment: the measurement is impossible")

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := ratelimit.Middleware(limiter, ratelimit.Credential)(next)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/graphql", nil)
	if apiKey != "" {
		req.Header.Set("ApiKey", apiKey)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "the probe request was refused: %s", rec.Body.String())
	limit := rec.Header().Get("X-RateLimit-Limit")
	require.NotEmpty(t, limit, "the chain did not state the limit it counted against")

	parsed, err := strconv.Atoi(limit)
	require.NoError(t, err)
	return parsed
}

// limitForFeedToken measures the limit a feed reader is counted against, through the credential function the
// feed route is mounted with. The feed's own handler is not exercised here on purpose: what §18.2 requires is
// that a feed reader's access does not depend on a tier, and that is the limiter's answer, not the feed
// writer's.
func limitForFeedToken(t *testing.T) int {
	t.Helper()

	free, premium := monetization.Limits()
	limiter := ratelimit.NewWithTiers(free, premium, monetization.CredentialIsPremium)

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := ratelimit.Middleware(limiter, ratelimit.FeedToken("/feeds/saved-search/"))(next)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/feeds/saved-search/a-feed-token-that-is-not-an-api-key", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	parsed, err := strconv.Atoi(rec.Header().Get("X-RateLimit-Limit"))
	require.NoError(t, err)
	return parsed
}

// apiKeyForMember mints the credential this instance issues for a member: the same claims
// internal/service/user builds, signed with the instance's own key, so the entitlement parses the real thing
// rather than a format invented for the test.
func apiKeyForMember(t *testing.T, member string) string {
	t.Helper()

	claims := &user.APIKeyClaims{
		UserID: member,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:  user.APIKeySubject,
			IssuedAt: jwt.NewNumericDate(time.Now()),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(config.GetJWTSignKey())
	require.NoError(t, err)
	return signed
}

// THE SURFACES EXIST AND THE SCHEMA HAS NO PAYWALL IN IT, asserted against the SERVED schema rather than the
// files: a field the service does not expose is not a surface, and a directive list is the one place a
// monetization rule would have to appear to be enforced by the schema.
//
// This is also what covers the mutating surfaces the leak test does not call: `sceneEdit` has to exist, and the
// schema's only access rules must still be `hasRole` and `isUserOwner`.
func TestTheServedSchemaExposesEveryAlwaysFreeSurfaceAndHasNoMonetizationDirective(t *testing.T) {
	monetizationOnForTest(t, userDB.read.ID.String())
	r := asRead(t)

	// The alias names the OBJECT __schema returns, so its own `directives` field is nested one level down --
	// the shape that cost this test its first run.
	var schema struct {
		Query struct {
			Fields []struct{ Name string } `json:"fields"`
		} `json:"query"`
		Mutation struct {
			Fields []struct{ Name string } `json:"fields"`
		} `json:"mutation"`
		Schema struct {
			Directives []struct{ Name string } `json:"directives"`
		} `json:"directives"`
	}
	err := r.client.Post(`
		query {
			query: __type(name: "Query") { fields { name } }
			mutation: __type(name: "Mutation") { fields { name } }
			directives: __schema { directives { name } }
		}
	`, &schema)
	require.NoError(t, err, "introspection must be available to the test harness")

	fields := map[string]bool{}
	for _, f := range schema.Query.Fields {
		fields[f.Name] = true
	}
	for _, f := range schema.Mutation.Fields {
		fields[f.Name] = true
	}
	require.NotEmpty(t, fields, "introspection returned no fields: every assertion below would be vacuous")

	for _, capability := range monetization.AlwaysFree() {
		for _, surface := range capability.Surfaces {
			if surface.Kind != "graphql" {
				continue
			}
			assert.True(t, fields[surface.Ref],
				"%s names the surface %q, which the served schema does not expose",
				capability.Name, surface.Ref)
		}
	}

	names := make([]string, 0, len(schema.Schema.Directives))
	for _, d := range schema.Schema.Directives {
		names = append(names, d.Name)
	}
	assert.Contains(t, names, "hasRole",
		"the schema's access rule is missing: this assertion would not notice a replacement")
	assert.Contains(t, names, "isUserOwner")
	for _, name := range names {
		lower := strings.ToLower(name)
		assert.NotContains(t, lower, "monetiz")
		assert.NotContains(t, lower, "premium")
		assert.NotContains(t, lower, "subscri")
		assert.NotContains(t, lower, "entitle")
	}
}
