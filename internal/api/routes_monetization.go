// GET /monetization and GET /monetization/json -- what this instance charges for (plan phase 15, spec §18).
//
// WHAT THIS IS FOR. §18.2 of the draft lists seven things a paywall may never touch, and the plan's phase 15 is
// that section modelled as operator configuration. A configuration is not a promise to anybody who reads the
// instance rather than the repository, so the same posture that the process logs at startup is published here:
// whether this instance charges for anything, which revenue streams are on, which §18.1 features exist yet,
// which §18.2 items are reachable and where, and the two API limits a caller might be counted against.
//
// SERVED TWICE, ON PURPOSE, AND FROM ONE VALUE. The HTML is for a person and `/monetization/json` is for
// anybody who wants to check it mechanically (the live walker does exactly that). Both render `PostureOf()`, so
// the page cannot disagree with the endpoint -- the rule slice 16a's transparency report established, and the
// reason the page's sentences are built from the struct rather than written beside it.
//
// THE LIST IS NOT MERELY PUBLISHED, IT IS ENFORCED, and the page says so in the place a reader would look for
// the catch: every always-free item names the fields and routes that provide it on THIS instance, and
// `monetization.Gate` refuses to gate any of them by name. A page that described §18.2 without that sentence
// would be the shape this programme keeps refusing -- a claim about a document.
//
// NO `noindex`, for the same reason the policy and transparency pages have none: a posture nobody can find is
// not a posture.
package api

import (
	"encoding/json"
	"html"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/stashapp/stash-box/internal/service/monetization"
)

// monetizationRoutes holds no services: the posture is read from the configuration and from the two lists that
// live in the monetization package, so there is nothing here that could disagree with what the limiter is
// actually running.
type monetizationRoutes struct{}

func (mr monetizationRoutes) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/", mr.page)
	r.Get("/json", mr.json)
	return r
}

func (mr monetizationRoutes) json(w http.ResponseWriter, r *http.Request) {
	writeAppHeaders(w)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(monetization.PostureOf())
}

func (mr monetizationRoutes) page(w http.ResponseWriter, r *http.Request) {
	writeAppHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(renderMonetizationPage(monetization.PostureOf())))
}

// renderMonetizationPage renders the posture for a reader.
//
// EVERY SENTENCE THAT CARRIES A FACT IS BUILT FROM THE POSTURE, including the one about whether this instance
// charges at all: a page with "this instance charges for nothing" hard-coded beside a table of enabled streams
// is a page that will disagree with itself the first time an operator turns the switch on.
func renderMonetizationPage(p monetization.Posture) string {
	lead := "This instance charges for nothing. Its <code>monetization_enabled</code> key is false, so no " +
		"revenue stream is switched on and no feature is withheld."
	if p.Enabled {
		// The key is named in BOTH postures, and that is a correction the live walk caught rather than a
		// flourish: a reader of a charging instance has the same question as a reader of a free one -- which
		// flag decides this -- and the first version answered it only when the answer was "off".
		lead = "This instance <strong>charges</strong> for the streams listed below: its " +
			"<code>monetization_enabled</code> key is true. Every item on the always-free list stays " +
			"reachable: the switch decides what may be sold, never what may be seen."
	}

	return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Monetization</title>
<style>
  :root { color-scheme: light dark; }
  body { margin: 0 auto; max-width: 46rem; padding: 2rem 1.25rem 4rem; line-height: 1.6;
         font-family: system-ui, -apple-system, "Segoe UI", Roboto, sans-serif; }
  h1 { font-size: 1.6rem; margin-bottom: .25rem; }
  h2 { font-size: 1.15rem; margin-top: 2rem; }
  .lead { opacity: .85; margin-top: 0; }
  table { border-collapse: collapse; width: 100%; margin: .5rem 0 1rem; }
  th, td { text-align: left; padding: .35rem .5rem; border-bottom: 1px solid rgba(128,128,128,.35);
           vertical-align: top; }
  td.n { text-align: right; font-variant-numeric: tabular-nums; font-weight: 600; white-space: nowrap; }
  code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: .9em; }
  .note { opacity: .8; font-size: .95em; }
  .on { font-weight: 600; }
  ul { padding-left: 1.25rem; }
  li { margin-bottom: .35rem; }
</style>
</head>
<body>
<h1>Monetization</h1>
<p class="lead">` + lead + `</p>

<h2>Revenue streams</h2>
<table>
<tr><th>Stream</th><th>What it sells</th><th>State</th></tr>
` + streamRows(p) + `</table>

<h2>Always free</h2>
<p class="note">Drawn from the draft specification's &sect;18.2, and enforced in code rather than promised here:
<code>internal/service/monetization</code> holds this list, and its gate refuses to gate any of it &mdash;
asking it to sell one of these returns an error naming the item, so a paywall on a core path fails there
rather than in production. The surfaces below are this instance's, which is why the list is code: what "full
search" means here is these fields and routes.</p>
` + alwaysFreeList(p) + `

<h2>Premium features</h2>
<p class="note">The draft's &sect;18.1 premium subscription, feature by feature, with the state of each on this
instance. A feature that needs work this tree does not have says so, rather than being listed as available.</p>
<table>
<tr><th>Feature</th><th>What a member gets</th><th>State</th></tr>
` + premiumRows(p) + `</table>

<h2>API limits</h2>
<table>
<tr><th>Everybody</th><td class="n">` + limitText(p.FreeLimit) + `</td></tr>
<tr><th>` + entitlementsLabel(p) + `</th><td class="n">` + limitText(p.PremiumLimit) + `</td></tr>
</table>
<p class="note">The unit is what a caller presents, which is what this instance counts: an API key, or the
connecting address when a request carries no credential at all. A browser session therefore counts under
&ldquo;everybody&rdquo; whatever it is signed in as, and ` + compsNote(p) + `</p>

<h2>Not built</h2>
<p class="note">Each of these is named rather than implied, because a reader who sees only the reachable list
would reasonably conclude the rest exists.</p>
<ul>` + notBuiltList(p) + `</ul>
</body>
</html>`
}

// streamRows renders §18.1's streams with their state. A stream that is off but buildable is distinguished from
// one this tree cannot sell at all, because those two facts call for different actions from an operator.
func streamRows(p monetization.Posture) string {
	rows := ""
	for _, s := range p.Streams {
		state := "off"
		switch {
		case s.On:
			state = `<span class="on">on</span>`
		case !s.Implemented:
			state = "not built &mdash; " + html.EscapeString(s.Because)
		default:
			state = "available, and off"
		}
		rows += "<tr><td><code>" + html.EscapeString(s.Name) + "</code></td><td>" +
			html.EscapeString(s.Sells) + "</td><td>" + state + "</td></tr>\n"
	}
	return rows
}

// alwaysFreeList renders §18.2's seven items with the surfaces that provide each one here.
func alwaysFreeList(p monetization.Posture) string {
	out := "<ul>"
	for _, c := range p.AlwaysFree {
		out += "<li><strong>" + html.EscapeString(c.Name) + "</strong> &mdash; " +
			html.EscapeString(c.Words) + "<br>"
		for _, s := range c.Surfaces {
			out += "<code>" + html.EscapeString(s.Ref) + "</code> (" + html.EscapeString(s.Kind)
			if s.Note != "" {
				out += " &middot; " + html.EscapeString(s.Note)
			}
			out += ")<br>"
		}
		if !c.Built {
			out += `<em>not built: ` + html.EscapeString(c.Because) + `</em>`
		} else if c.Because != "" {
			out += `<em>` + html.EscapeString(c.Because) + `</em>`
		}
		out += "</li>\n"
	}
	return out + "</ul>"
}

// premiumRows renders §18.1's features. Availability here is `monetization.Gate`'s answer, computed in the
// posture, so this table and the code's own refusals cannot drift apart.
func premiumRows(p monetization.Posture) string {
	rows := ""
	for _, f := range p.Premium {
		state := "available"
		if !f.Available {
			state = html.EscapeString(f.Because)
		}
		rows += "<tr><td><code>" + html.EscapeString(f.Name) + "</code></td><td>" +
			html.EscapeString(f.Sells) + "</td><td>" + state + "</td></tr>\n"
	}
	return rows
}

// limitText states one limit the way the refusal body and the headers state it.
func limitText(l monetization.Limit) string {
	if l.RequestsPerMinute <= 0 || l.Burst <= 0 {
		return "no limit"
	}
	return strconv.Itoa(l.RequestsPerMinute) + " requests/minute, burst " + strconv.Itoa(l.Burst)
}

// entitlementsLabel names the premium row honestly whichever state the instance is in: a table that said
// "premium" beside a limit nobody can be granted would be describing a tier with no members.
func entitlementsLabel(p monetization.Posture) string {
	if p.PremiumMembers == 0 {
		return "Members entitled to the premium tier (none yet)"
	}
	return "Members entitled to the premium tier (" + strconv.Itoa(p.PremiumMembers) + ")"
}

// compsNote states where the grant comes from, and says whose business it is not.
func compsNote(p monetization.Posture) string {
	if p.PremiumMembers == 0 {
		return "no member is entitled to the premium tier at all."
	}
	return "a member is entitled when the operator has comped them (<code>monetization_premium_members</code>), " +
		"so the grant is a configuration change an operator makes deliberately. Who is on that list is not " +
		"published: the count is."
}

// notBuiltList names §18.2's items this instance cannot do and §18.1's features that need work, gathered from
// the two lists rather than written out again.
func notBuiltList(p monetization.Posture) string {
	items := []string{}
	for _, c := range p.AlwaysFree {
		if !c.Built {
			items = append(items, "<code>"+html.EscapeString(c.Name)+"</code>: "+html.EscapeString(c.Because))
		}
	}
	for _, f := range p.Premium {
		if !f.Available {
			items = append(items, "<code>"+html.EscapeString(f.Name)+"</code>: "+html.EscapeString(f.Because))
		}
	}
	if len(items) == 0 {
		return "<li>nothing named by &sect;18.1 or &sect;18.2 is missing from this instance.</li>"
	}
	out := ""
	for _, item := range items {
		out += "<li>" + strings.TrimSpace(item) + "</li>"
	}
	return out
}
