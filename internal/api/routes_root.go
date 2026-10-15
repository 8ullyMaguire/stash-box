package api

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/stashapp/stash-box/internal/config"
	"github.com/stashapp/stash-box/internal/service"
)

type rootRoutes struct {
	ui    embed.FS
	index []byte
}

func (rr rootRoutes) Routes(fac service.Factory) chi.Router {
	rr.index = getIndex(rr.ui)

	r := chi.NewRouter()

	// session handlers
	r.Post("/login", handleLogin(fac))
	r.HandleFunc("/logout", handleLogout)

	r.Mount("/images", imageRoutes{
		fac: fac,
	}.Routes())

	r.Mount("/crop-templates", cropTemplateRoutes{
		templates: fac.CropTemplates(),
	}.Routes())

	// A key's landing page (phase 11 slice 11c). Registered BEFORE the SPA catch-all below, which
	// serves the application for every other path and would otherwise swallow the invitation.
	r.Mount("/invite", inviteRoutes{
		fac: fac,
	}.Routes())

	// A saved search as a feed (phase 13 slice 13b). Registered BEFORE the SPA catch-all for the same
	// reason the invitation page is: the application answers every other path, and a feed request that
	// reached it would be handed HTML with a 200 -- a reader would report a parse failure rather than a
	// missing feed.
	r.Mount("/feeds/saved-search", feedRoutes{
		fac: fac,
	}.Routes())

	// Serve static assets
	r.HandleFunc("/assets/*", rr.assets)
	r.HandleFunc("/favicon.ico", rr.assets)
	r.HandleFunc("/manifest.json", rr.assets)

	// Serve the web app
	r.HandleFunc("/*", rr.app)

	return r
}

func (rr rootRoutes) assets(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Cache-Control", "max-age=604800000")
	uiRoot, err := fs.Sub(rr.ui, "build")
	if err != nil {
		panic(error.Error(err))
	}
	http.FileServer(http.FS(uiRoot)).ServeHTTP(w, r)
}

func (rr rootRoutes) app(w http.ResponseWriter, r *http.Request) {
	writeAppHeaders(w)
	_, _ = w.Write(rr.index)
}

// writeAppHeaders sets the security headers used when serving an SPA index.
func writeAppHeaders(w http.ResponseWriter) {
	csp := config.GetCSP()
	if csp != "" {
		w.Header().Add("Content-Security-Policy", csp)
	}
	w.Header().Add("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
	w.Header().Add("X-Content-Type-Options", "nosniff")
	w.Header().Add("Referrer-Policy", "same-origin")
	w.Header().Add("Permissions-Policy", "accelerometer=(), camera=(), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), payment=(), usb=()")
}

// renderIndex applies the title template to an index.html document.
func renderIndex(indexFile []byte) []byte {
	tmpl := template.Must(template.New("index").Parse(string(indexFile)))
	title := template.HTMLEscapeString(config.GetTitle())
	output := new(strings.Builder)
	if err := tmpl.Execute(output, template.HTML(title)); err != nil {
		panic(error.Error(err))
	}
	return []byte(output.String())
}

func getIndex(ui embed.FS) []byte {
	indexFile, err := ui.ReadFile("build/index.html")
	if err != nil {
		panic(error.Error(err))
	}
	return renderIndex(indexFile)
}
