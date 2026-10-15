// GET /transparency and GET /transparency/json -- the transparency report (phase 16, slice 16a).
//
// WHAT THIS IS FOR. Phase 14's published policy tells a rights holder what happens to a notice; this page
// tells anybody what DID happen: how many addresses this instance refuses, how many of its catalogue
// records were taken out of service, how many decisions were contested, and how many of those contests
// succeeded. A notice path with no public accounting is a promise; the accounting is the part a reader can
// check, which is also why the numbers are computed by a query over the instance's own tables rather than
// written down here (`docs/goal-check.py`: "Never trust a count written in a document. Recompute it.").
//
// SERVED TWICE, ON PURPOSE. The HTML is for a person and the JSON (same route, `/transparency/json`) is for
// anybody who wants to verify the figures — including this programme's own live walk, which recomputes every
// number with `psql` against the deployed database and requires the JSON to agree. A report nobody can check
// mechanically is a report that can drift from the instance without anybody noticing.
//
// NO `noindex`, for the same reason the policy page has none and the invite page has one: a report nobody can
// find is not a report, and a search index is exactly how a journalist would find it.
package api

import (
	"encoding/json"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/stashapp/stash-box/internal/service"
	"github.com/stashapp/stash-box/internal/service/content"
	"github.com/stashapp/stash-box/internal/service/mediaplane"
)

type transparencyRoutes struct {
	fac service.Factory
}

func (tr transparencyRoutes) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", tr.page)
	r.Get("/json", tr.json)
	return r
}

func (tr transparencyRoutes) json(w http.ResponseWriter, r *http.Request) {
	report, err := tr.fac.Content().TransparencyReport(r.Context())
	if err != nil {
		// The failure is reported AS a failure rather than as a report of zeros: a transparency report that
		// silently published "0 refusals" when its query failed would be the worst possible lie in this
		// programme, and it is the shape a cache or a stale page produces by accident.
		http.Error(w, "the report could not be computed", http.StatusInternalServerError)
		return
	}
	writeAppHeaders(w)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(report)
}

func (tr transparencyRoutes) page(w http.ResponseWriter, r *http.Request) {
	report, err := tr.fac.Content().TransparencyReport(r.Context())
	if err != nil {
		http.Error(w, "the report could not be computed", http.StatusInternalServerError)
		return
	}
	writeAppHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(renderTransparencyPage(report)))
}

// renderTransparencyPage renders the report for a reader.
//
// EVERY NUMBER GOES THROUGH `count`, and every sentence that carries a number is built from the report rather
// than written beside it: a page with "0 addresses refused" hard-coded in a sentence and the real figure in a
// table is a page that will disagree with itself the first time somebody takes a notice seriously.
func renderTransparencyPage(report content.Report) string {
	window := "in the last " + strconv.Itoa(report.WindowDays) + " days"
	windowNote := "This instance deletes its moderation records after " + strconv.Itoa(report.WindowDays) +
		" days (<code>mod_audit_retention_days</code>), so the period figures cover that window and no " +
		"further back. The figures that describe what is in force now have no window: a refusal recorded " +
		"longer ago than that is still refused today."
	if report.RecordsKeptForever {
		window = "over the whole history this instance holds"
		windowNote = "This instance keeps its moderation records indefinitely " +
			"(<code>mod_audit_retention_days</code> is not set above 0, so nothing is deleted), so the " +
			"period figures below cover everything it has ever recorded."
	}

	return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Transparency report</title>
<style>
  :root { color-scheme: light dark; }
  body { margin: 0 auto; max-width: 46rem; padding: 2rem 1.25rem 4rem; line-height: 1.6;
         font-family: system-ui, -apple-system, "Segoe UI", Roboto, sans-serif; }
  h1 { font-size: 1.6rem; margin-bottom: .25rem; }
  h2 { font-size: 1.15rem; margin-top: 2rem; }
  .lead { opacity: .75; margin-top: 0; }
  table { border-collapse: collapse; width: 100%; margin: .5rem 0 1rem; }
  th, td { text-align: left; padding: .35rem .5rem; border-bottom: 1px solid rgba(128,128,128,.35); }
  td.n { text-align: right; font-variant-numeric: tabular-nums; font-weight: 600; }
  code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: .9em; }
  .note { opacity: .8; font-size: .95em; }
  ul { padding-left: 1.25rem; }
</style>
</head>
<body>
<h1>Transparency report</h1>
<p class="lead">What this instance refused, what was contested, and how it answered — computed from its own
records at ` + html.EscapeString(report.GeneratedAt.UTC().Format(time.RFC3339)) + `.</p>

<h2>In force now</h2>
<table>
<tr><th>Refused addresses and hosts</th><td class="n">` + count(report.InForce.Blocks) + `</td></tr>
<tr><th>Catalogue records taken out of service</th><td class="n">` + count(report.InForce.RecordsOutOfService) + `</td></tr>
<tr><th>Appeals filed</th><td class="n">` + count(report.InForce.AppealsFiled) + `</td></tr>
<tr><th>Appeals pending</th><td class="n">` + count(report.InForce.AppealsPending) + `</td></tr>
<tr><th>Appeals decided</th><td class="n">` + count(report.InForce.AppealsDecided) + `</td></tr>
<tr><th>&nbsp;&nbsp;granted (the refusal was released)</th><td class="n">` + count(report.InForce.AppealsGranted) + `</td></tr>
<tr><th>&nbsp;&nbsp;denied (the refusal stands)</th><td class="n">` + count(report.InForce.AppealsDenied) + `</td></tr>
</table>
<p class="note">A refusal and a record are two different things: one refusal can take many addresses out of
service, and a refusal against an address this instance never recorded takes none.</p>

<h2>` + html.EscapeString(strings.ToUpper(window[:1])+window[1:]) + `</h2>
<table>
<tr><th>Refusals recorded</th><td class="n">` + count(report.Activity.Refusals) + `</td></tr>
<tr><th>Refusals released</th><td class="n">` + count(report.Activity.Releases) + `</td></tr>
<tr><th>Addresses or hosts newly refused</th><td class="n">` + count(report.Activity.BlocksRecorded) + `</td></tr>
<tr><th>Appeals filed</th><td class="n">` + count(report.Activity.AppealsFiled) + `</td></tr>
<tr><th>Appeals decided</th><td class="n">` + count(report.Activity.AppealsDecided) + `</td></tr>
</table>
<p class="note">` + windowNote + `</p>

<h2>What this instance serves</h2>
<p>` + html.EscapeString(mediaplane.PostureLine()) + `</p>
<p class="note">That sentence is read from the same source the process logs at startup, so the report and the
log cannot disagree about the posture.</p>

<h2>How to read these figures</h2>
<ul>
  <li><strong>A release is not a new refusal.</strong> Both appear in the audit trail, and they are counted
      apart: the released figure counts decisions that took a refusal out of service, which is the number a
      person checks against the notice they sent.</li>
  <li><strong>Denials are not failures.</strong> Every denial was heard by a moderator who did not make the
      original decision, which is the commitment the
      <a href="/policy">abuse and takedown policy</a> makes; the word this report uses for the alternative is
      "granted".</li>
  <li><strong>These numbers are recomputable.</strong> The same figures are served as JSON at
      <a href="/transparency/json"><code>/transparency/json</code></a>, and every one of them is a count over
      this instance's own tables — there is no figure here that a document supplied.</li>
</ul>
</body>
</html>
`
}

// count formats a figure with thousands separators.
//
// Small, and worth its own function: a report of a six-figure number written `1234567` is a number a reader
// has to count digits to read, and this page exists to be read rather than to be technically correct.
func count(value int64) string {
	digits := strconv.FormatInt(value, 10)
	negative := strings.HasPrefix(digits, "-")
	if negative {
		digits = digits[1:]
	}
	var grouped []string
	for len(digits) > 3 {
		grouped = append([]string{digits[len(digits)-3:]}, grouped...)
		digits = digits[:len(digits)-3]
	}
	grouped = append([]string{digits}, grouped...)
	out := strings.Join(grouped, ",")
	if negative {
		return "-" + out
	}
	return out
}
