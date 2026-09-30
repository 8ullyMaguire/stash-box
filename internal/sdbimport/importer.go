package sdbimport

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service"
)

// Importer drives one run.
//
// Every write goes through a service's Create method, never raw SQL. The
// reasons are in the package doc of the original draft and they have not
// changed: no fingerprints, no alias rows and no relation rows is an instance
// that is full of rows and behaves as if it were empty.
//
// IDEMPOTENCE IS THE LOAD-BEARING PROPERTY. Every entity is looked up by its
// natural key before creation, and a hit is a skip rather than an insert. That
// is what makes a run resumable: 1.1M scenes is many hours, and the run will be
// interrupted, and a second run must not double the database.
type Importer struct {
	Client  *Client
	Factory *service.Factory

	Stats  map[string]*Stats
	DryRun bool
	Delay  time.Duration
	Ctx    context.Context

	// state carries the site catalogue and its lock; see sites.go.
	state *importerState

	// siteNames dedups the 113 seed sites. There is no FindSitesByName query
	// in the generated set, and 113 rows are not worth adding production schema
	// for, so the names already seen this run are tracked in memory.
	siteNames map[string]bool

	// queries is used for the read-side dedup lookups. Reading through the
	// generated queries and writing through the service is deliberate: the
	// service exposes no "does this exist" method, and adding one to the
	// production API for an importer's convenience is the wrong direction.
	queries *queries.Queries
}

// NewImporter returns an Importer with its shared state initialised.
//
// The state MUST come from here rather than being left nil: a nil catalogue
// would panic on the first URL, which at 111k performers is several minutes in
// and after a lot of committed work.
func NewImporter(client *Client, factory *service.Factory, q *queries.Queries) *Importer {
	return &Importer{
		Client:    client,
		Factory:   factory,
		queries:   q,
		Stats:     map[string]*Stats{},
		state:     &importerState{cat: NewSiteCatalogue()},
		siteNames: map[string]bool{},
	}
}

func (i *Importer) stats(entity string) *Stats {
	if i.Stats == nil {
		i.Stats = map[string]*Stats{}
	}
	if i.Stats[entity] == nil {
		i.Stats[entity] = newStats()
	}
	return i.Stats[entity]
}

// NewResolver returns the name->id map that scenes are resolved through.
func (i *Importer) NewResolver() *Resolver { return NewResolver() }

// Bind attaches the query handle used for dedup lookups.
//
// Called by main after the factory exists, because the factory does not export
// its pool and an importer reaching into it would be evidence of a missing
// service method -- except that an importer legitimately needs a read handle,
// so this is the one place the pool is exposed to non-service code.
func (i *Importer) Bind(q *queries.Queries) { i.queries = q }

// page runs one paged query, invoking fn per record.
//
// Errors from an individual record do not abort the page. A page is 5,000
// records and one malformed record must not cost the other 4,999; the failure
// is counted and the run continues. What WOULD be wrong is swallowing every
// error, which is why the first error per entity is retained and reported.
func (i *Importer) page(entity, query string, total int, limit int, fn func(rec any) error) error {
	st := i.stats(entity)
	pages := TotalPages(total)
	// A limit is a cap on RECORDS, not on pages. Collapsing it to a single page
	// would silently import 5,000 records for any limit above one page, which
	// reads as "I asked for 7,000 and got exactly 5,000" with no error.
	if limit > 0 {
		if capped := (limit + pagedFields - 1) / pagedFields; capped < pages {
			pages = capped
		}
	}

	processed := 0
	for p := 1; p <= pages; p++ {
		if err := i.Ctx.Err(); err != nil {
			return err
		}

		var resp struct {
			Performers struct {
				Count      int         `json:"count"`
				Performers []Performer `json:"performers"`
			} `json:"queryPerformers"`
			Scenes struct {
				Count  int     `json:"count"`
				Scenes []Scene `json:"scenes"`
			} `json:"queryScenes"`
			Tags struct {
				Count int   `json:"count"`
				Tags  []Tag `json:"tags"`
			} `json:"queryTags"`
			Studios struct {
				Count   int      `json:"count"`
				Studios []Studio `json:"studios"`
			} `json:"queryStudios"`
			Sites struct {
				Count int    `json:"count"`
				Sites []Site `json:"sites"`
			} `json:"querySites"`
		}

		if err := i.Client.Query(i.Ctx, query, map[string]any{"page": p}, &resp); err != nil {
			return fmt.Errorf("page %d: %w", p, err)
		}

		// Dispatch on which list came back, so one page function serves every
		// entity type. The list is chosen by what is non-empty, which is safe
		// because no query populates two of them.
		var recs []any
		switch {
		case len(resp.Performers.Performers) > 0:
			for k := range resp.Performers.Performers {
				recs = append(recs, resp.Performers.Performers[k])
			}
		case len(resp.Scenes.Scenes) > 0:
			for k := range resp.Scenes.Scenes {
				recs = append(recs, resp.Scenes.Scenes[k])
			}
		case len(resp.Tags.Tags) > 0:
			for k := range resp.Tags.Tags {
				recs = append(recs, resp.Tags.Tags[k])
			}
		case len(resp.Studios.Studios) > 0:
			for k := range resp.Studios.Studios {
				recs = append(recs, resp.Studios.Studios[k])
			}
		case len(resp.Sites.Sites) > 0:
			for k := range resp.Sites.Sites {
				recs = append(recs, resp.Sites.Sites[k])
			}
		default:
			// An empty page before the last one means the source's count is
			// stale, which happens as records are added during a long run.
			// Stopping is correct: continuing would loop over empty pages
			// forever.
			return nil
		}

		if limit > 0 {
			remaining := limit - processed
			if remaining <= 0 {
				return nil
			}
			if len(recs) > remaining {
				recs = recs[:remaining]
			}
			processed += len(recs)
		}

		for _, rec := range recs {
			if err := i.Ctx.Err(); err != nil {
				return err
			}
			if err := fn(rec); err != nil {
				st.fail(entity, err)
			}
		}

		fmt.Printf("  %s page %d/%d: created %d skipped %d failed %d\n",
			entity, p, pages, st.Created, st.Skipped, st.Failed)

		if i.Delay > 0 {
			select {
			case <-time.After(i.Delay):
			case <-i.Ctx.Done():
				return i.Ctx.Err()
			}
		}
	}
	return nil
}

// alreadyExists reports the id of an entity with this name, or nil.
//
// pgx.ErrNoRows is the normal answer for "not there yet" and is not an error
// worth surfacing. Any OTHER error is returned, because swallowing it would
// turn a database outage into 111,705 duplicate inserts.
func alreadyExists(err error) (found bool, err2 error) {
	if err == nil {
		return true, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return false, err
}

func trimmed(s string) string { return strings.TrimSpace(s) }
