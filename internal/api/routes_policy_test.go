package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/config"
)

// The published abuse and takedown policy (phase 14, spec §9).
//
// NO DATABASE AND NO ACCOUNT, which is the whole point of the test rather than a convenience: this page is
// the one surface a rights holder reaches, and they reach it without an account, without a build, and
// before anything in this repository has been configured. A test that needed a fixture would be testing a
// different page than the one they see.
//
// WHAT IS ASSERTED IS THE PROMISE, NOT THE PROSE. The page is hand-written HTML (the file comment in
// routes_policy.go explains why it is not embedded from docs/), so a test that pinned sentences would fail
// on every edit and teach the wrong lesson. What it pins instead is that each commitment an operator cannot
// quietly drop still appears, and that the two states of the contact address are honest about themselves.

func getPolicy(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	policyRoutes{}.Routes().ServeHTTP(rec, req)
	return rec
}

func TestThePolicyIsServedToAnybodyWithNoAccountAtAll(t *testing.T) {
	rec := getPolicy(t)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"))

	body := rec.Body.String()

	// Each of these is a commitment the policy makes, and each one is a thing an operator who wanted to
	// stop making it would have to delete on purpose rather than forget:
	//
	//   1. the 24-hour acknowledgement,
	//   2. the appeal -- and that it is heard by somebody who did not decide,
	//   3. what the instance IS (a catalogue that serves nothing),
	//   4. that the metadata survives the removal of the pointer, with the reason.
	//
	// Counted in one pass and asserted, rather than four bare Contains calls, so that a page which lost
	// three of them fails with a list of what is missing instead of on the first miss.
	commitments := map[string]string{
		"acknowledgement window": "24 hours",
		"the appeal path":        "contest a refusal",
		"who hears the appeal":   "moderator who did not make the",
		"what the instance is":   "does not store the works themselves",
		"the metadata survives":  "The metadata stays",
	}
	missing := []string{}
	for name, phrase := range commitments {
		if !strings.Contains(body, phrase) {
			missing = append(missing, name+" ("+phrase+")")
		}
	}
	assert.Empty(t, missing, "the served policy stopped saying these things")

	// DELIBERATELY INDEXABLE, and it is the one page on this instance for which that is true: the invite
	// page sets X-Robots-Tag noindex (a key in a search index is a public key), while a notice path nobody
	// can find is not a notice path. Asserted as an absence, so the subject is counted first -- the header
	// blocker's own presence is asserted elsewhere, and a page that lost its hardening headers entirely
	// would otherwise pass this.
	assert.NotContains(t, rec.Header().Get("X-Robots-Tag"), "noindex",
		"the policy is the page this instance wants found; only the invite landing page is noindex")
}

func TestThePolicySaysOutLoudWhenNoAddressForNoticesIsConfigured(t *testing.T) {
	previous := config.C.AbuseContact
	t.Cleanup(func() { config.C.AbuseContact = previous })
	config.C.AbuseContact = ""

	body := getPolicy(t).Body.String()

	// Counted first: if the sentence vanished entirely, a bare "no mailto" assertion below would pass
	// vacuously, which is the failure mode this pair exists to prevent.
	require.Contains(t, body, "has not published an address for notices",
		"an instance with no configured contact must say so, not show a blank where an address goes")
	assert.Contains(t, body, "abuse_contact",
		"and it names the key its operator has to set")
	assert.NotContains(t, body, "mailto:")
}

func TestThePolicyNamesTheConfiguredAddressForNotices(t *testing.T) {
	previous := config.C.AbuseContact
	t.Cleanup(func() { config.C.AbuseContact = previous })
	config.C.AbuseContact = "abuse@example.invalid"

	body := getPolicy(t).Body.String()

	assert.Contains(t, body, `mailto:abuse@example.invalid`,
		"the configured address is the notice path, and it is linked rather than printed")
	assert.Contains(t, body, "abuse@example.invalid")
	assert.NotContains(t, body, "has not published an address for notices",
		"an edited copy of the unset sentence would read as a policy that is not in force")
}

// The address comes from an operator's configuration file and is rendered into HTML, so it is escaped. A
// config value is not attacker-controlled in the usual sense, and an instance's policy page is exactly the
// page that must not be able to be turned into somebody else's, which is what an unescaped angle bracket
// buys an operator with a typo.
func TestThePolicyEscapesAnOperatorSuppliedAddress(t *testing.T) {
	previous := config.C.AbuseContact
	t.Cleanup(func() { config.C.AbuseContact = previous })
	config.C.AbuseContact = `abuse@example.invalid"><script>alert(1)</script>`

	body := getPolicy(t).Body.String()

	assert.NotContains(t, body, "<script>alert(1)</script>", "the configured value must not become markup")
	assert.Contains(t, body, "&lt;script&gt;", "it is rendered, escaped, as the text it is")
}
