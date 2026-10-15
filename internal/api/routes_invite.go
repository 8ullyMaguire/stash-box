package api

import (
	"bytes"
	"html/template"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/stashapp/stash-box/internal/config"
	"github.com/stashapp/stash-box/internal/service"
	"github.com/stashapp/stash-box/pkg/logger"
)

// inviteRoutes serves a key's landing page: GET /invite/<code>.
//
// UNAUTHENTICATED BY DESIGN, and the whole reason is CAREFUL: what a member shares is a link, and a
// link that requires an account to open is not shareable. The page records the visitor as a COUNT and
// nothing else (see RecordScan), and -- the part that has to be got right -- it is the SAME DOCUMENT
// for a live key, an expired key, a spent key and a code that was never a key at all. A page that
// differed between those would be an endpoint a stranger could use to find out which invite codes
// exist, which is a list of every member's invitations.
type inviteRoutes struct {
	fac service.Factory
}

func (rs inviteRoutes) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/{code}", rs.page)
	return r
}

func (rs inviteRoutes) page(w http.ResponseWriter, r *http.Request) {
	code := chi.URLParam(r, "code")

	// THE SCAN IS RECORDED AND THEN FORGOTTEN. It cannot decide the page (the page does not depend on
	// it) and it cannot fail the page: a database error costs the member one uncounted scan, while
	// returning 500 would cost the INVITEE the invitation -- and the invitee is the person who did
	// nothing wrong here.
	if err := rs.fac.Invite().RecordScan(r.Context(), code); err != nil {
		logger.Errorf("could not record an invitation scan: %v", err)
	}

	writeAppHeaders(w)
	// A key in an indexed page is a public key. `Referrer-Policy: same-origin` (in writeAppHeaders)
	// keeps it out of the next site's logs too.
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(renderInvitePage(rs.fac, code))
}

// invitePage is the whole document. It is deliberately plain: it needs no assets, so it renders
// before -- and without -- the application, and it works when the application cannot.
const invitePage = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<title>{{.Title}}</title>
<style>
  :root { color-scheme: light dark; }
  body { font-family: system-ui, sans-serif; line-height: 1.5; margin: 0; padding: 2rem 1rem;
         display: flex; justify-content: center; }
  main { max-width: 34rem; }
  h1 { font-size: 1.4rem; margin: 0 0 0.25rem; }
  p { margin: 0.75rem 0; }
  .key { font-family: ui-monospace, monospace; font-size: 0.85rem; overflow-wrap: anywhere;
         background: rgba(127,127,127,0.15); padding: 0.6rem 0.75rem; border-radius: 0.4rem; }
  ol { margin: 0.75rem 0; padding-left: 1.25rem; }
  footer { margin-top: 2rem; font-size: 0.8rem; opacity: 0.75; }
</style>
</head>
<body>
<main>
  <h1>{{.Title}}</h1>
  <p>{{.Intro}}</p>
  <p>You have been given an invitation. Creating an account here needs this key:</p>
  <p class="key">{{.Code}}</p>
  <ol>
    <li>Open <a href="{{.SiteURL}}">{{.SiteURL}}</a>.</li>
    <li>Choose to create an account.</li>
    <li>Enter this key when it asks for an invitation.</li>
  </ol>
  <p>An invitation lets one person join.{{if .Required}} This instance requires one to register.{{end}}</p>
  <footer>{{.Title}} runs on stash-box. This page records that the link was opened, and nothing about
  who opened it.</footer>
</main>
</body>
</html>
`

// renderInvitePage fills the document in.
//
// THE THREE THINGS THAT MUST NOT VARY WITH THE KEY'S STATE: the status code, the body, and the
// headers. Nothing here consults the key: the code is echoed because the visitor already has it (it is
// in their address bar), the page says what an invitation grants, and whether THIS key would be
// accepted is answered where it is used -- at registration -- so that this page cannot be asked.
func renderInvitePage(fac service.Factory, code string) []byte {
	tmpl := template.Must(template.New("invite").Parse(invitePage))

	data := struct {
		Title    string
		Intro    string
		Code     string
		SiteURL  string
		Required bool
	}{
		Title:    config.GetTitle(),
		Intro:    "This instance runs on stash-box, an open-source catalogue of adult media and its performers.",
		Code:     code,
		SiteURL:  strings.TrimSuffix(config.GetHostURL(), "/") + "/",
		Required: config.GetRequireInvite(),
	}

	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		// A template that cannot render is a bug in the constant above, and it must not become a page
		// that says something different to one visitor than to the next. 500 for everybody.
		logger.Errorf("could not render the invitation page: %v", err)
		return []byte("<!DOCTYPE html><html><body><p>This invitation could not be displayed.</p></body></html>")
	}
	return out.Bytes()
}
