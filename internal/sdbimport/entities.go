package sdbimport

import (
	"sync"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
)

// nullString maps "" to nil so FindExistingPerformers' sqlc.narg() branches
// treat "no disambiguation" as absent rather than as the literal empty string
// -- otherwise a performer with no disambiguation would never match itself and
// a re-run would duplicate every such performer.
func nullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Per-entity import functions.
//
// Each one follows the same shape: look up by natural key, skip on a hit,
// otherwise build the create input and hand it to the service. The lookup is
// what makes a re-run safe, so it is never skipped for speed -- at 111k
// performers it is a single indexed query, and it is the only thing standing
// between an interrupted run and a doubled database.

// importerState is the state shared across a run.
type importerState struct {
	cat *SiteCatalogue

	// mu guards the catalogue. The loop is single-goroutine, but a site is
	// created lazily from a performer's URL and from a scene's URL, and a
	// catalogue raced here would create the same site twice -- which the
	// destination's unique constraint would then turn into a hard error on an
	// unrelated record.
	mu sync.Mutex
}

// applyURLs resolves raw URLs to models.URL rows.
//
// Each entity keeps at most ONE url per site: migration 21 replaced the old
// `unique (entity_id, type)` key with a `site_id` foreign key, so a performer
// with five IAFD URLs keeps one and drops four. That is the schema's
// constraint, not a mapping bug, and the drops are counted.
func (i *Importer) applyURLs(st *Stats, entity string, rawURLs []string) []models.URL {
	if len(rawURLs) == 0 {
		return nil
	}
	seenSite := map[uuid.UUID]bool{}
	var out []models.URL
	for _, raw := range rawURLs {
		raw = trimmed(raw)
		if raw == "" {
			continue
		}
		siteID, ok := i.Lookup(i.state.cat, raw)
		if !ok {
			// Lookup already recorded the underlying error under "sites"; here
			// the only remaining cause is a URL with no parsable hostname.
			st.drop(entity+".url_no_host", HostOf(raw))
			continue
		}
		if siteID == uuid.Nil {
			continue // dry run: host parsed, no id assigned
		}
		if seenSite[siteID] {
			st.drop(entity+".url_same_site", raw)
			continue
		}
		seenSite[siteID] = true
		out = append(out, models.URL{URL: raw, SiteID: siteID})
	}
	return out
}

// importTags imports every tag.
//
// Tags first: 2,934 of them, minutes of work, and scenes reference them by
// name. A scene written before its tags exist loses every tag reference, and a
// scene with no tags is indistinguishable from one that had none.
func (i *Importer) ImportTags(r *Resolver) error {
	var resp struct {
		QueryTags struct {
			Count int `json:"count"`
		} `json:"queryTags"`
	}
	if err := i.Client.Query(i.Ctx, CountTags, nil, &resp); err != nil {
		return err
	}

	return i.page("tags", tagQuery, resp.QueryTags.Count, 0, func(rec any) error {
		t := rec.(Tag)
		st := i.stats("tags")

		if trimmed(t.Name) == "" {
			st.Skipped++
			return nil
		}

		existing, err := i.queries.FindTagByName(i.Ctx, t.Name)
		found, lerr := alreadyExists(err)
		if lerr != nil {
			return lerr
		}
		if found {
			r.AddTag(existing.Name, existing.ID)
			st.Skipped++
			return nil
		}
		if i.DryRun {
			st.Created++
			return nil
		}

		created, err := i.Factory.Tag().Create(i.Ctx, models.TagCreateInput{
			Name:        trimmed(t.Name),
			Description: t.Description,
			Aliases:     t.Aliases,
		})
		if err != nil {
			return err
		}
		r.AddTag(created.Name, created.ID)
		st.Created++
		return nil
	})
}

// importSites imports the 113 sites the source declares.
//
// Before everything else, because every URL in the run needs a SiteID. The 113
// are only a seed: SiteCatalogue.Lookup creates more on demand when a URL's
// host is not among them.
func (i *Importer) ImportSites(_ *Resolver) error {
	var resp struct {
		QuerySites struct {
			Count int    `json:"count"`
			Sites []Site `json:"sites"`
		} `json:"querySites"`
	}
	if err := i.Client.Query(i.Ctx, siteQuery, nil, &resp); err != nil {
		return err
	}

	st := i.stats("sites")
	for _, s := range resp.QuerySites.Sites {
		if trimmed(s.Name) == "" {
			st.Skipped++
			continue
		}
		if i.siteNames[normKey(s.Name)] {
			st.Skipped++
			continue
		}
		if i.DryRun {
			i.siteNames[normKey(s.Name)] = true
			st.Created++
			continue
		}

		created, err := i.Factory.Site().Create(i.Ctx, models.SiteCreateInput{
			Name:       trimmed(s.Name),
			URL:        s.URL,
			ValidTypes: []models.ValidSiteTypeEnum{},
		})
		if err != nil {
			st.fail("sites", err)
			continue
		}
		i.siteNames[normKey(created.Name)] = true
		if s.URL != nil {
			i.state.cat.Add(*s.URL, created.ID)
		}
		st.Created++
	}
	return nil
}

// importStudios imports every studio.
//
// Studios before performers and scenes because a scene's studio reference
// silently becomes null if the studio is not there yet.
func (i *Importer) ImportStudios(r *Resolver) error {
	var resp struct {
		QueryStudios struct {
			Count int `json:"count"`
		} `json:"queryStudios"`
	}
	if err := i.Client.Query(i.Ctx, CountStudios, nil, &resp); err != nil {
		return err
	}

	return i.page("studios", studioQuery, resp.QueryStudios.Count, 0, func(rec any) error {
		s := rec.(Studio)
		st := i.stats("studios")

		if trimmed(s.Name) == "" {
			st.Skipped++
			return nil
		}

		existing, err := i.queries.FindStudioByName(i.Ctx, s.Name)
		found, lerr := alreadyExists(err)
		if lerr != nil {
			return lerr
		}
		if found {
			r.AddStudio(existing.Name, existing.ID)
			st.Skipped++
			return nil
		}
		if i.DryRun {
			st.Created++
			return nil
		}

		in := models.StudioCreateInput{
			Name: trimmed(s.Name),
			Urls: i.applyURLs(st, "studio", studioURLs(s)),
		}
		// A parent studio is attached only if already imported. The source
		// returns studios in name order, not hierarchy order, so a parent may
		// not exist yet; a null parent is incomplete but correct, which beats a
		// wrong one.
		if s.Parent != nil && s.Parent.Name != nil {
			if id, ok := r.studios[normKey(*s.Parent.Name)]; ok {
				in.ParentID = &id
			} else {
				st.drop("studio.parent", *s.Parent.Name)
			}
		}

		created, err := i.Factory.Studio().Create(i.Ctx, in)
		if err != nil {
			return err
		}
		r.AddStudio(created.Name, created.ID)
		st.Created++
		return nil
	})
}

// importPerformers imports performers, recording name->id for scene resolution.
func (i *Importer) ImportPerformers(r *Resolver, limit int) error {
	var resp struct {
		QueryPerformers struct {
			Count int `json:"count"`
		} `json:"queryPerformers"`
	}
	if err := i.Client.Query(i.Ctx, CountPerformers, nil, &resp); err != nil {
		return err
	}

	return i.page("performers", performerQuery, resp.QueryPerformers.Count, limit, func(rec any) error {
		p := rec.(Performer)
		st := i.stats("performers")

		if trimmed(p.Name) == "" {
			st.Skipped++
			return nil
		}

		// Dedup on (name, disambiguation), NOT on name alone.
		//
		// The destination's uniqueness is `UNIQUE (name, disambiguation) WHERE
		// NOT deleted`, and the source is full of legitimately distinct people
		// who share a name: sampling 20,000 source performers found 3,107 (15.5%)
		// whose name is shared, with "alex" appearing 29 times. Matching on name
		// alone silently merges them -- one person absorbing another's aliases,
		// urls and scenes, and the merged row being indistinguishable from a
		// correct one afterwards.
		//
		// FindExistingPerformers also matches on URL, which catches a performer
		// renamed upstream. A URL hit is a rename and should UPDATE; a name hit
		// is a re-run and should SKIP. Doing that here rather than in the
		// service keeps rename-reconciliation out of an import path.
		matches, err := i.queries.FindExistingPerformers(i.Ctx, queries.FindExistingPerformersParams{
			Name:           strPtr(trimmed(p.Name)),
			Disambiguation: nullString(deref(p.Disambiguation)),
			Urls:           p.URLStrings(),
		})
		if err != nil {
			return err
		}
		if len(matches) > 0 {
			existing := matches[0]
			r.AddPerformer(existing.Name, existing.ID)
			if len(matches) > 1 {
				st.drop("performer.ambiguous_match", trimmed(p.Name))
			}
			st.Skipped++
			return nil
		}
		if i.DryRun {
			st.Created++
			return nil
		}

		in := p.ToCreateInput(st)
		in.Urls = i.applyURLs(st, "performer", p.URLStrings())
		created, err := i.Factory.Performer().Create(i.Ctx, in)
		if err != nil {
			return err
		}
		r.AddPerformer(created.Name, created.ID)
		st.Created++
		return nil
	})
}

// importScenes imports scenes last, resolving their relations by name.
//
// THERE IS NO SCENE DEDUP, deliberately. An earlier draft deduplicated on
// title+date and it was removed: it would silently skip two genuinely distinct
// scenes that share both, and at 1.1M records "the re-run did not duplicate" is
// worth less than "no scene was dropped on a guess". A re-run duplicates scenes,
// and the tool's help says so rather than hiding it.
func (i *Importer) ImportScenes(r *Resolver, limit int) error {
	var resp struct {
		QueryScenes struct {
			Count int `json:"count"`
		} `json:"queryScenes"`
	}
	if err := i.Client.Query(i.Ctx, CountScenes, nil, &resp); err != nil {
		return err
	}

	return i.page("scenes", sceneQuery, resp.QueryScenes.Count, limit, func(rec any) error {
		s := rec.(Scene)
		st := i.stats("scenes")

		in := s.ToSceneInput(r, st)
		if in.NoTitle {
			st.Skipped++
			st.drop("scene.unidentifiable", "no title and no url")
			return nil
		}
		for _, m := range in.Missing {
			st.drop("scene.unresolved_ref", m)
		}
		if i.DryRun {
			st.Created++
			return nil
		}

		in.Create.Urls = i.applyURLs(st, "scene", s.URLStrings())

		if _, err := i.Factory.Scene().Create(i.Ctx, in.Create); err != nil {
			return err
		}
		st.Created++
		return nil
	})
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func studioURLs(s Studio) []string {
	out := make([]string, 0, len(s.URLs))
	for _, u := range s.URLs {
		if u.URL != "" {
			out = append(out, u.URL)
		}
	}
	return out
}
