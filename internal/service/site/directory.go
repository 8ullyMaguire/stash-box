// Package site implements the site directory (SPEC §7.10, phase 3 step 2).
//
// Sites predate the directory: migration 21 gave them name, url and regex, which is
// enough to identify a site but not enough to write a directory entry about one.
// This adds §7.10's fields and the alternatives relation, and the one thing worth
// reading the code for is the traversal in ResolveAlternatives.
package site

import (
	"context"
	"errors"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/stashapp/stash-box/internal/queries"
)

// ErrSelfAlternative is returned when a site is offered as its own alternative.
//
// The database CHECK prevents the row from existing, so this error is a backstop
// for a caller that never reached the database. It exists anyway because the
// alternative is a *cycle*, and the consequence of a cycle is a hung page rather
// than a wrong value -- which is the class of bug that is found by a user
// screenshot rather than by a test, so the cheap check is worth having where the
// intent is visible.
var ErrSelfAlternative = errors.New("a site cannot be an alternative to itself")

// ErrSiteNotFound is returned when a site id does not resolve.
var ErrSiteNotFound = errors.New("site not found")

// Details is a site's directory fields.
//
// The slices are NIL when the field is unknown and EMPTY when it is known to be
// empty, and the distinction is load-bearing: "nobody has recorded this site's
// payment methods" and "this site takes no payment methods" are different facts
// and a directory that renders them identically is lying about its own catalogue.
// The migration's columns are nullable for exactly this reason.
type Details struct {
	SiteID         uuid.UUID
	Pricing        *string
	PaymentMethods []string
	Features       []string
	Pros           []string
	Cons           []string
	EthicalLabels  []string
	UpdatedBy      *uuid.UUID
}

// Entry is one row of the directory search: a site, its details, and its reviews.
type Entry struct {
	SiteID   uuid.UUID
	Name     string
	SiteURL  *string
	Details  Details
	Rating   Rating
	Category *int
}

// Rating is an entity's review summary as the directory displays it.
type Rating struct {
	Average float64
	// Rated is how many reviews carried a rating; Total is how many exist. A
	// directory showing 4.2 over one review is indistinguishable from a
	// consensus, and the count is the only thing that tells them apart.
	Rated int
	Total int
}

// maxDirectoryLimit is the server-side cap on a directory page.
//
// 100 because that is the cap everywhere else in this codebase, so a client that
// asks for more gets the same answer it gets from every other list rather than a
// surprise specific to the directory.
const maxDirectoryLimit int32 = 100

// clampLimit bounds a caller-supplied page size.
//
// EXTRACTED as a pure function, and the reason is a verification finding rather
// than a style preference: this clamp was a mutant SURVIVOR twice. A test that
// runs a real search cannot detect its removal, because a clamped query and an
// unclamped one over nine fixture sites return the SAME ROWS -- the difference is
// a LIMIT the fixtures never approach. Only a test that asserts the number
// itself can see it, and only a pure function can be asserted without a database.
func clampLimit(limit int32) int32 {
	// A limit of zero or less is not "no rows", it is a client that omitted the
	// field, and answering it with an empty page is the bug that makes a
	// directory render as blank on first load. Both ends clamp to the same value
	// for the same reason: the caller is not in a position to know the row count.
	if limit <= 0 || limit > maxDirectoryLimit {
		return maxDirectoryLimit
	}
	return limit
}

// searchText maps a free-text search box to a nullable query parameter.
//
// NIL for an empty string, and that is the whole point. `query = ”` puts an
// ILIKE '%%' predicate on every row, which returns the same rows as no predicate
// while costing a scan on each -- and because the RESULT is identical, no
// output-comparing test can tell the two apart. It was a mutation survivor for
// exactly that reason, and the cost is invisible except in latency, which is the
// hardest kind of regression to notice and the easiest to ship.
func searchText(q string) *string {
	if q == "" {
		return nil
	}
	return &q
}

// maxTraversalDepth bounds the alternatives walk.
//
// THE HAZARD, and the reason this constant exists rather than the recursion
// running until the graph is exhausted: the CHECK on site_alternatives prevents a
// one-node self-cycle and nothing else. A -> B -> C -> A is perfectly legal under
// that CHECK, and an unbounded walk over it never terminates. A site directory
// rendering "related sites" would hang the request, exhaust the connection pool
// one request at a time, and take the instance's public pages down with it -- from
// a data-entry mistake made by one trusted user.
//
// So the walk is bounded, the bound is a CONSTANT rather than a parameter, and a
// caller cannot raise it. A parameter would be a footgun with a plausible-looking
// call site; a constant is a decision someone has to edit deliberately, and the
// comment above it is what they read when they do.
//
// Two levels is what a directory actually needs: a site's alternatives, and the
// alternatives of those. Deeper chains are not useful to a reader and are the
// region where cycles live.
const maxTraversalDepth = 2

// SetDetailsInput is a directory-fields write.
type SetDetailsInput struct {
	SiteID         uuid.UUID
	Pricing        *string
	PaymentMethods []string
	Features       []string
	Pros           []string
	Cons           []string
	EthicalLabels  []string
	// UpdatedBy is the acting user, for attribution. Nullable because an import
	// or a seeding script has no user behind it.
	UpdatedBy *uuid.UUID
}

// SetDetails creates or replaces a site's directory fields.
//
// An upsert, because site_details is 1:1 with sites. The alternative -- read,
// branch on exists, write -- has the same race as every other read-then-write and
// no compensating benefit here, since there is no partial-update semantics worth
// preserving: the input is the whole row.
//
// The nil-versus-empty distinction from Details is preserved all the way through,
// which means the caller has to pass a real empty slice to CLEAR a field. That is
// deliberate friction. "Set payment methods to nothing" and "I am not touching
// payment methods" are different edits, and a request body that cannot tell them
// apart cannot clear a field that was wrong.
func (s *Site) SetDetails(ctx context.Context, in SetDetailsInput) (*Details, error) {
	var row *queries.SiteDetail
	// Inside a transaction, and not because the upsert needs atomicity -- it does
	// not, being a single statement. Because Create and Update on this type both
	// use withTxn, and a directory write that can observe a half-committed site
	// is the kind of inconsistency nobody debugs.
	err := s.withTxn(func(tx *queries.Queries) error {
		created, err := tx.UpsertSiteDetails(ctx, queries.UpsertSiteDetailsParams{
			SiteID:         in.SiteID,
			Pricing:        in.Pricing,
			PaymentMethods: in.PaymentMethods,
			Features:       in.Features,
			Pros:           in.Pros,
			Cons:           in.Cons,
			EthicalLabels:  in.EthicalLabels,
			// uuid.NullUUID rather than a *uuid.UUID, because that is what sqlc
			// generates for a nullable foreign key and the conversion is mechanical.
			// An import has no user behind it, so the NULL case is real and not a
			// placeholder.
			UpdatedBy: toNullUUID(in.UpdatedBy),
		})
		if err != nil {
			return err
		}
		row = &created
		return nil
	})
	if err != nil {
		return nil, err
	}
	return toDetails(row), nil
}

// Details returns a site's directory fields.
//
// No rows is NOT an error: a site nobody has written about is the normal state for
// most of the catalogue, and the caller renders "not filled in yet". Returning an
// error would make every site page special-case the common case.
func (s *Site) Details(ctx context.Context, siteID uuid.UUID) (*Details, error) {
	row, err := s.queries.GetSiteDetails(ctx, siteID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return toDetails(&row), nil
}

// AddAlternative records that `alternativeID` is an alternative to `siteID`.
//
// The self-check is here AND in the schema. Redundant on purpose: the schema is
// what makes the rule TRUE under concurrency and for any writer, and this is what
// makes the intent VISIBLE at the call site. A reader of AddAlternative should not
// have to open the migration to learn why the obvious thing is refused.
//
// An ALREADY-EXISTING edge is not an error. The query is ON CONFLICT DO NOTHING,
// and the two callers that matter are a UI toggle (which can double-submit) and an
// idempotent import -- neither of which should fail because the desired state was
// already reached.
func (s *Site) AddAlternative(ctx context.Context, siteID, alternativeID uuid.UUID) error {
	if siteID == alternativeID {
		return ErrSelfAlternative
	}
	_, err := s.queries.AddSiteAlternative(ctx, queries.AddSiteAlternativeParams{
		SiteID:            siteID,
		AlternativeSiteID: alternativeID,
	})
	// ON CONFLICT DO NOTHING means a duplicate returns NO ROWS, and the generated
	// :one returns pgx.ErrNoRows for that. That is the success case, not a
	// failure, and treating it as an error would make the second click on a
	// toggle fail.
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	return nil
}

// RemoveAlternative deletes an edge.
//
// Idempotent for the same reason AddAlternative is: the desired state is "no edge",
// and it already being absent is that state. A toggle that reports an error when
// asked to remove something that is not there is a toggle that gets clicked twice.
func (s *Site) RemoveAlternative(ctx context.Context, siteID, alternativeID uuid.UUID) error {
	return s.queries.RemoveSiteAlternative(ctx, queries.RemoveSiteAlternativeParams{
		SiteID:            siteID,
		AlternativeSiteID: alternativeID,
	})
}

// Alternatives returns a site's DIRECT alternatives.
//
// Direct only. The transitive walk is a separate function, and the split is the
// point: this is the cheap query a site page's "similar sites" strip uses, and it
// cannot hang because it makes exactly one hop.
func (s *Site) Alternatives(ctx context.Context, siteID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.queries.ListSiteAlternatives(ctx, siteID)
	if err != nil {
		return nil, err
	}
	out := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out, nil
}

// ResolveAlternatives walks the alternatives graph up to maxTraversalDepth and
// returns the sites reachable from `rootID`, EXCLUDING the root and EXCLUDING
// duplicates.
//
// The exclusion of the root is not a nicety -- it is what makes a cycle harmless
// rather than merely bounded. A -> B -> A terminates at depth 2 with B already
// seen, and the visited set is what stops it, not the depth counter. The depth
// bound is the second line of defence for a cycle LONGER than the bound, which the
// visited set alone would also handle; the two are redundant by design, because
// the depth bound survives a bug in the visited set and the visited set survives a
// depth bound that someone raised.
//
// A BFS rather than a DFS, and for one reason that matters: BFS visits each node
// once by construction of the queue, so the frontier cannot grow beyond the number
// of nodes, while a DFS on a graph with high fan-out recurses to a depth bounded
// only by the node count. With the bound in place both terminate, but BFS
// terminates in the number of EDGES in the bounded region rather than the number
// of paths through it, which is the difference between a cheap query and a
// combinatorial one on a densely-linked site graph.
//
// Never returns a cycle, a duplicate, or the root, and the result is stable because
// the frontier is de-duplicated on insert.
func (s *Site) ResolveAlternatives(ctx context.Context, rootID uuid.UUID) ([]uuid.UUID, error) {
	visited := map[uuid.UUID]bool{rootID: true}
	out := []uuid.UUID{}
	frontier := []uuid.UUID{rootID}

	for depth := 0; depth < maxTraversalDepth && len(frontier) > 0; depth++ {
		next := []uuid.UUID{}
		for _, id := range frontier {
			neighbours, err := s.Alternatives(ctx, id)
			if err != nil {
				return nil, err
			}
			for _, n := range neighbours {
				// The visited check is what makes a cycle harmless. A -> B -> A
				// reaches B on depth 0 and A on depth 1, and A is the root, so it
				// is skipped rather than re-expanded -- which is the difference
				// between terminating and looping until the depth bound stops it.
				if visited[n] {
					continue
				}
				visited[n] = true
				out = append(out, n)
				next = append(next, n)
			}
		}
		frontier = next
	}

	return out, nil
}

// SearchInput is a directory search.
//
// Every filter is a slice and an EMPTY slice means "no filter", not "match
// nothing". The distinction is made once, here, so the SQL's three-valued
// COALESCE dance is not something a caller has to know about.
type SearchInput struct {
	Query          string
	EthicalLabels  []string
	PaymentMethods []string
	Features       []string
	Limit          int32
	Offset         int32
}

// Search runs a directory search.
func (s *Site) Search(ctx context.Context, in SearchInput) ([]Entry, error) {
	limit := clampLimit(in.Limit)

	// sqlc.narg is a NULL when unset, and "unset" for a free-text search is
	// meaningfully different from "the empty string": an empty search box should
	// not filter on ILIKE '%%', which matches everything but costs a scan. The
	// helper is what makes that testable without a database.
	query := searchText(in.Query)

	rows, err := s.queries.SearchSiteDirectory(ctx, queries.SearchSiteDirectoryParams{
		EthicalLabels:  in.EthicalLabels,
		PaymentMethods: in.PaymentMethods,
		Features:       in.Features,
		Query:          query,
		Limit:          limit,
		Offset:         in.Offset,
	})
	if err != nil {
		return nil, err
	}

	out := make([]Entry, 0, len(rows))
	for _, r := range rows {
		out = append(out, Entry{
			SiteID:   r.ID,
			Name:     r.Name,
			SiteURL:  r.Url,
			Category: r.CategoryID,
			Details: Details{
				SiteID:         r.ID,
				Pricing:        r.Pricing,
				PaymentMethods: r.PaymentMethods,
				Features:       r.Features,
				Pros:           r.Pros,
				Cons:           r.Cons,
				EthicalLabels:  r.EthicalLabels,
			},
			Rating: Rating{
				Average: r.ReviewAverage,
				Rated:   int(r.ReviewRatedCount),
				Total:   int(r.ReviewTotalCount),
			},
		})
	}
	return out, nil
}

// MissingDetails returns sites with no directory fields at all.
//
// The curation surface, and the reason this method exists rather than being a
// quest generated from the completion engine: "the directory is 40% complete" is a
// number a community can act on, and it is the same shape as the completion
// engine's missing-field queries. The caller supplies the limit for the same
// reason Search does.
func (s *Site) MissingDetails(ctx context.Context, limit, offset int32) ([]uuid.UUID, error) {
	rows, err := s.queries.ListSitesMissingDetails(ctx,
		queries.ListSitesMissingDetailsParams{Limit: clampLimit(limit), Offset: offset})
	if err != nil {
		return nil, err
	}
	out := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out, nil
}

func toDetails(r *queries.SiteDetail) *Details {
	return &Details{
		SiteID:         r.SiteID,
		Pricing:        r.Pricing,
		PaymentMethods: r.PaymentMethods,
		Features:       r.Features,
		Pros:           r.Pros,
		Cons:           r.Cons,
		EthicalLabels:  r.EthicalLabels,
		// A NULL updated_by becomes nil rather than a pointer to the zero UUID,
		// which would read as "written by user 0000" and is worse than absent.
		UpdatedBy: nullUUIDPtr(r.UpdatedBy),
	}
}

// nullUUIDPtr converts sqlc's nullable-UUID to a plain pointer.
//
// The distinction it preserves is that a row written by an IMPORT has no user
// behind it, and collapsing that to the zero UUID would render as "written by
// 00000000-0000-0000-0000-000000000000" -- a specific and entirely fictional user.
func nullUUIDPtr(n uuid.NullUUID) *uuid.UUID {
	if !n.Valid {
		return nil
	}
	id := n.UUID
	return &id
}

// toNullUUID converts a plain pointer to sqlc's nullable-UUID.
//
// Mechanical in one direction and lossy in the other, which is why the lossy
// direction is a named function rather than an inline construction: getting it
// wrong produces a row attributed to the zero UUID, which is a specific and
// entirely fictional user.
func toNullUUID(id *uuid.UUID) uuid.NullUUID {
	if id == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *id, Valid: true}
}
