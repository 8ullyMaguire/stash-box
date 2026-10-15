// GET /policy -- the published abuse and takedown policy (phase 14, spec §9).
//
// WHY THIS IS A ROUTE AND NOT ONLY A FILE. Phase 14's policy text is what phase 10 waits for: the moment
// this instance stores a byte of somebody's work it is a host, and a host without a published notice path
// is a host whose operator improvises a response to the first notice they receive. A document in a
// repository answers that for developers; a page answers it for the person holding the notice, who should
// not need an account, a build, or a checkout to find out what an instance will do with it.
//
// THE SERVED TEXT IS WRITTEN HERE AND NOT EMBEDDED FROM docs/policy/. The two are different documents for
// different readers -- this one is what a complainant is told, the file is what a maintainer is told -- and
// an embed would force one of them to be worse at its job. What keeps them from drifting is not an import
// but a TEST: `routes_policy_integration_test.go` asserts that the served page still names the three
// commitments an operator cannot quietly drop (the address to send a notice to, the appeal path, and the
// 24-hour acknowledgement), so a page that stopped describing the process fails the build.
//
// X-Robots-Tag IS DELIBERATELY ABSENT, and it is the one place on this instance that is true. The invite
// landing page sets noindex (a key in a search index is a public key), but a notice path nobody can find is
// not a notice path: this is the page an instance WANTS indexed.
package api

import (
	"html"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/stashapp/stash-box/internal/config"
)

type policyRoutes struct{}

func (pr policyRoutes) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", pr.page)
	return r
}

func (pr policyRoutes) page(w http.ResponseWriter, r *http.Request) {
	writeAppHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Deliberately NOT noindex: see the file comment.
	_, _ = w.Write([]byte(renderPolicyPage()))
}

// renderPolicyPage builds the published policy.
//
// The contact address is CONFIGURED, and an instance that has not configured one gets a sentence saying so
// rather than a blank where an address should be. That is not a nicety: a policy that claims to answer
// notices while naming nobody to send one to is a promise nobody can collect on, and phase 14's ordering
// exists precisely so that this state is visible BEFORE the instance starts storing content. The value is
// escaped because it comes from an operator's config file and is rendered into HTML.
func renderPolicyPage() string {
	contact := strings.TrimSpace(config.GetAbuseContact())

	contactLine := `<p class="unset">This instance has not published an address for notices yet. Until its
operator sets <code>abuse_contact</code>, this policy is not complete and this instance should not be
storing content it does not own.</p>`
	if contact != "" {
		contactLine = `<p>Send notices to <a href="mailto:` + html.EscapeString(contact) + `">` +
			html.EscapeString(contact) + `</a>.</p>`
	}

	return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Abuse and takedown policy</title>
<style>
  :root { color-scheme: light dark; }
  body { margin: 0 auto; max-width: 46rem; padding: 2rem 1.25rem 4rem; line-height: 1.6;
         font-family: system-ui, -apple-system, "Segoe UI", Roboto, sans-serif; }
  h1 { font-size: 1.6rem; margin-bottom: .25rem; }
  h2 { font-size: 1.15rem; margin-top: 2rem; }
  .lead { opacity: .75; margin-top: 0; }
  .unset { border-left: 3px solid currentColor; padding-left: .75rem; font-style: italic; }
  code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: .9em; }
  ol, ul { padding-left: 1.25rem; }
  li { margin: .35rem 0; }
</style>
</head>
<body>
<h1>Abuse and takedown policy</h1>
<p class="lead">What this instance does with a notice, what it will not do, and how to disagree with a decision.</p>

<h2>What this instance is</h2>
<p>This instance is a <strong>catalogue</strong>: it records addresses, fingerprints and descriptions of
works so that people can find and identify them. It does not store the works themselves, and it does not
serve them. Removing a record from this instance removes an <em>index entry</em>, not a copy of anything.</p>

<h2>Sending a notice</h2>
` + contactLine + `
<p>A notice needs the address the record is about, and enough to identify what is wrong with it: a rights
holder or their agent, the work, and the notice's own reference. Vague notices are answered with a request
for the missing part rather than with a removal, because a removal that cannot be explained is a removal
that cannot be defended.</p>

<h2>What happens next</h2>
<ol>
  <li><strong>Acknowledgement within 24 hours.</strong> A verified request to remove material is actioned
      the same day it is read, and the answer says which addresses were taken out.</li>
  <li><strong>The address stops being used.</strong> The refused URL, or a whole host where that is what the
      notice is about, is recorded as refused. Every record of it stops being served, and the pipeline
      refuses it if somebody submits it again -- including for an address this instance had never seen,
      which is the case a notice is actually about.</li>
  <li><strong>The metadata stays.</strong> The fingerprint and the description remain, marked as refused.
      That is deliberate and it is not a loophole: the fingerprints are what let the same work be recognised
      elsewhere, and deleting them is what would let the material come back under a new address unnoticed.
      What is removed is the pointer, and a pointer is what a notice is about.</li>
  <li><strong>Every decision is recorded</strong>: who decided, when, and on what. Those records are the
      basis of any transparency report this instance publishes, and they survive the removal of the thing
      they describe.</li>
</ol>

<h2>Disagreeing with a decision</h2>
<p>Any member may contest a refusal, in writing, and the appeal is heard by a moderator who did not make the
decision -- not by the operator who recorded the block, and not by the person who filed the appeal. That is
enforced by identity rather than by trust, and the audit trail names both. An appeal that is granted releases
the refusal through the same code path an operator's own release uses; an appeal that is denied says why, and
the refusal stays.</p>
<p>An appeal is filed against the address, not against a case number: the person who was refused knows what
they submitted.</p>

<h2>If you run another instance</h2>
<p>This instance is a peer, not a platform, and it does not administer anybody else's copy of anything. If you
received a notice about an address you obtained from here, the expectation is not that you forward it
unquestioningly: apply your own law and your own judgement, and tell us if you refuse. If we receive a notice
about something you reference, we remove our record and we do not touch yours -- we hold an address, not a
copy, and we cannot reach what we do not have.</p>

<h2>What this instance does not do</h2>
<ul>
  <li><strong>It does not match hashes against an external blocklist.</strong> Nothing here accepts uploads,
      so there is no ingest to match against; the refusal happens at the source layer, where a refused source
      is simply never fetched.</li>
  <li><strong>It does not verify ages or consent.</strong> Nothing here publishes performer content. That is
      a consequence of being a catalogue, not a claim to have solved the problem -- and it stops being true
      the moment this instance starts storing content it does not own.</li>
  <li><strong>It does not keep a copy of a work it does not own.</strong> This instance serves no file it did
      not receive as a URL: there is no hosting, no proxy and no re-encoding here.</li>
</ul>

<h2>When this policy changes</h2>
<p>Storing content is a capability an operator turns on deliberately, and turning it on makes this instance a
host -- which changes what it owes a rights holder and, in most jurisdictions, what paperwork it needs. This
text is written for the catalogue it currently is. An operator who enables content delivery must revise this
page before serving the first byte, not afterwards.</p>
</body>
</html>
`
}
