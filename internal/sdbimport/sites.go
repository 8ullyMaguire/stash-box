package sdbimport

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/models"
)

// SiteCatalogue assigns a SiteID to each URL, creating sites on demand.
//
// WHY THIS EXISTS. This fork's `*_urls` tables have a NOT NULL foreign key to
// `sites` (migration 21 renamed the `type` column to `site_id` and added the
// constraint), so a URL cannot be written without naming the site it belongs
// to. The source does not say which site a URL belongs to: it returns a bare
// URL string.
//
// Measured against the 113 sites the source itself declares, hostname matching
// resolves only 68.6% of performer URLs and 35.7% of scene URLs -- the misses
// are the 681 hostnames of studios whose site the source does not declare.
// Dropping the misses would lose a third of all scene URLs, and the URL is the
// field that makes a scene findable and scrapable afterwards.
//
// So a site is CREATED for any hostname that is not already known. The cost is
// a few hundred additive rows with no behaviour attached; the benefit is that
// URLs survive. An unknown site is a correct statement ("we saw this host")
// where a missing URL is a silent data loss.
type SiteCatalogue struct {
	byHost map[string]uuid.UUID
}

// NewSiteCatalogue returns an empty catalogue.
func NewSiteCatalogue() *SiteCatalogue {
	return &SiteCatalogue{byHost: map[string]uuid.UUID{}}
}

// HostOf extracts a comparable hostname from a URL.
//
// Normalised by dropping the scheme, a leading "www.", and any path, query or
// fragment. Comparing full URLs instead would match almost nothing: the source
// stores "https://brazzers.com/models/x" while the site is declared as
// "https://www.brazzers.com".
func HostOf(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "//") {
		s = "//" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}

// HostCandidates returns the hostname spellings worth trying, most specific
// first.
//
// "models.ferronetwork.com" and "ferronetwork.com" are the same site and which
// one the source declared is arbitrary. Trying the progressively broader forms
// gets the match rate up without a public-suffix list.
func HostCandidates(raw string) []string {
	h := HostOf(raw)
	if h == "" {
		return nil
	}
	out := []string{h}
	if i := strings.Index(h, "."); i > 0 {
		rest := h[i+1:]
		out = append(out, rest)
		if j := strings.Index(rest, "."); j > 0 {
			out = append(out, rest[j+1:])
		}
	}
	return out
}

// Add registers a site under every hostname form of its URL.
func (c *SiteCatalogue) Add(rawURL string, id uuid.UUID) {
	for _, h := range HostCandidates(rawURL) {
		if _, exists := c.byHost[h]; !exists {
			c.byHost[h] = id
		}
	}
}

// Known reports whether a URL's host is already mapped, without creating.
func (c *SiteCatalogue) Known(raw string) (uuid.UUID, bool) {
	for _, h := range HostCandidates(raw) {
		if id, ok := c.byHost[h]; ok {
			return id, true
		}
	}
	return uuid.Nil, false
}

// Lookup returns the site id for a URL, creating a site when no host matches.
//
// The two failure modes are DISTINGUISHED, because conflating them made a real
// bug invisible: a site-create failure (typically a name that already exists,
// since the catalogue is per-run and a previous run already created it) was
// being reported as "url_unparseable", which reads as malformed source data
// when the URL is perfectly fine and the site is already there. The report said
// linktr.ee URLs were unparseable when the linktr.ee site had in fact been
// created and the URL was dropped for a different reason.
//
// Returns false only when the URL genuinely cannot be parsed.
func (i *Importer) Lookup(cat *SiteCatalogue, raw string) (uuid.UUID, bool) {
	if id, ok := cat.Known(raw); ok {
		return id, true
	}
	h := HostOf(raw)
	if h == "" {
		return uuid.Nil, false
	}
	if i.DryRun {
		// A dry run must not create rows, but the URL is still resolvable as
		// far as the read-only pass can tell.
		return uuid.Nil, true
	}

	created, err := i.Factory.Site().Create(i.Ctx, models.SiteCreateInput{
		Name:       h,
		URL:        strPtr("https://" + h),
		ValidTypes: []models.ValidSiteTypeEnum{},
	})
	if err != nil {
		// Almost always "this site already exists": the catalogue only knows
		// about sites created THIS run, so a re-run collides on every one of
		// them. The site is not missing, only the id. Look it up so the URL
		// still imports, and record the collision rather than dropping the URL.
		if id, ok := i.findSiteByName(h); ok {
			cat.Add("https://"+h, id)
			return id, true
		}
		i.stats("sites").fail("site_autocreate", fmt.Errorf("creating site for %q: %w", h, err))
		return uuid.Nil, false
	}
	cat.Add("https://"+h, created.ID)
	return created.ID, true
}

func strPtr(s string) *string { return &s }

// findSiteByName resolves an existing site row by name.
//
// Needed because the in-memory catalogue cannot survive between runs, and a
// resumed import must not drop every URL belonging to a site it already
// created. The destination enforces a unique index on site name, so a
// name lookup is sufficient and needs no new query.
func (i *Importer) findSiteByName(name string) (uuid.UUID, bool) {
	rows, err := i.queries.GetSitesByName(i.Ctx, name)
	if err != nil || len(rows) == 0 {
		return uuid.Nil, false
	}
	return rows[0].ID, true
}
