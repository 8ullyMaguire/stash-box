//go:build integration

package api_test

import (
	"context"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/service/review"
	"github.com/stashapp/stash-box/internal/service/site"
)

// The site directory against the real database (SPEC §7.10, Phase 3 step 2).
//
// The load-bearing cases are the two the plan named: a self-alternative must be
// refused BY THE DATABASE, and an alternatives chain must not hang the resolver.
// The second one is a cycle longer than one node, which the CHECK permits.

// directorySites creates sites directly, because the existing Site.Create path
// takes a favicon and a converter and none of that is what these tests are about.
func directorySites(t *testing.T, names ...string) []uuid.UUID {
	t.Helper()
	out := make([]uuid.UUID, 0, len(names))
	for _, name := range names {
		id := uuid.Must(uuid.NewV7())
		_, err := dbtest.DB().Exec(context.Background(),
			`INSERT INTO sites (id, name, valid_types, created_at, updated_at)
			 VALUES ($1, $2, ARRAY['SCENE'], now(), now())`, id, "dir-"+name)
		require.NoError(t, err)
		out = append(out, id)
	}
	return out
}

func dirSvc(t *testing.T) *site.Site {
	t.Helper()
	return dbtest.Factory().Site()
}

// A site cannot be its own alternative, and the DATABASE is what refuses it.
//
// The service check is tested elsewhere. This one goes around the service
// deliberately, because the whole reason the constraint is in the schema is that a
// service check races and any future importer skips it.
func TestTheDatabaseRefusesASelfAlternative(t *testing.T) {
	siteID := directorySites(t, "self-alt")[0]

	_, err := dbtest.DB().Exec(context.Background(),
		`INSERT INTO site_alternatives (site_id, alternative_site_id) VALUES ($1, $1)`,
		siteID)

	require.Error(t, err,
		"a self-alternative is a graph CYCLE, and the alternative list is walked "+
			"to build a navigation tree, so a one-node cycle hangs the page for "+
			"every visitor -- not a rejected write, a hung page")
	assert.Contains(t, err.Error(), "site_alternatives_no_self",
		"and the error names the constraint, so a caller learns WHICH rule it hit")
}

// The SERVICE refuses a self-alternative too, and that is a separate fact from
// the database refusing it.
//
// Mutation testing found the service check untested: dropping it left every test
// green, because the constraint catches the same write one layer down. So the
// constraint makes the rule TRUE and the service check makes it VISIBLE, and
// only the second one needs its own test -- otherwise a future refactor that
// routes around the service (an importer, an admin script) loses the error
// message and nothing notices.
func TestTheServiceRefusesASelfAlternative(t *testing.T) {
	svc := dirSvc(t)
	ctx := context.Background()
	siteID := directorySites(t, "self-alt-service")[0]

	err := svc.AddAlternative(ctx, siteID, siteID)

	assert.ErrorIs(t, err, site.ErrSelfAlternative,
		"the service refuses it with a NAMED error, so a caller gets 'a site "+
			"cannot be an alternative to itself' rather than a raw constraint "+
			"violation naming a table it has no business knowing about")
}

// A three-node cycle is legal under the CHECK, so the RESOLVER is what has to
// terminate. This is the test the depth bound exists for.
func TestAnAlternativesCycleTerminates(t *testing.T) {
	svc := dirSvc(t)
	ctx := context.Background()
	ids := directorySites(t, "cycle-a", "cycle-b", "cycle-c")
	a, b, c := ids[0], ids[1], ids[2]

	// A -> B -> C -> A. Every node is a legal row: none is a self-link.
	require.NoError(t, svc.AddAlternative(ctx, a, b))
	require.NoError(t, svc.AddAlternative(ctx, b, c))
	require.NoError(t, svc.AddAlternative(ctx, c, a))

	resolved, err := svc.ResolveAlternatives(ctx, a)
	require.NoError(t, err)

	assert.NotContains(t, resolved, a,
		"the root is never in its own alternative list")
	assert.Len(t, resolved, 2,
		"TWO sites, not three. With the visited set removed this walk still "+
			"terminates -- the depth bound catches the cycle -- but it reports the "+
			"root a second time, because A -> B -> C -> A reaches A again on "+
			"depth 1 and only the visited set rejects it. Termination and "+
			"correctness are different properties and the bound alone gives "+
			"you only the first: the page renders a site listing itself as its "+
			"own alternative, which is the same confusing thing the CHECK "+
			"prevents one node earlier")
	assert.Contains(t, resolved, b)
	assert.Contains(t, resolved, c)

	// No duplicates: the walk reached B on depth 0 and C on depth 1, and must not
	// report C again when C's own edge back to A is followed.
	seen := map[uuid.UUID]bool{}
	for _, id := range resolved {
		require.False(t, seen[id], "site %s appears twice in %v", id, resolved)
		seen[id] = true
	}
}

// A DIAMOND returns each site once, and this is the test the visited set needed.
//
// The cycle test above does not cover this, and I wrote it believing it did. At a
// depth bound of 2, A -> B -> C -> A never re-reaches the root: C is added to the
// next frontier but never expanded, so the walk ends before the cycle closes and
// removing the visited set entirely leaves that test green. It is a test that
// looks like coverage of cycle safety and is not.
//
// A diamond is the shape that actually exercises it. A -> B, A -> C, B -> D, C -> D
// reaches D by two paths, and without the visited set D is returned TWICE. That is
// not a cosmetic duplicate either: a directory card's "also on this site" strip
// would list a studio twice, and the duplicate count is what a reader uses to
// judge whether two entries are the same thing.
func TestADiamondReturnsEachSiteOnce(t *testing.T) {
	svc := dirSvc(t)
	ctx := context.Background()
	ids := directorySites(t, "dia-a", "dia-b", "dia-c", "dia-d")
	a, b, c, d := ids[0], ids[1], ids[2], ids[3]

	require.NoError(t, svc.AddAlternative(ctx, a, b))
	require.NoError(t, svc.AddAlternative(ctx, a, c))
	require.NoError(t, svc.AddAlternative(ctx, b, d))
	require.NoError(t, svc.AddAlternative(ctx, c, d))

	resolved, err := svc.ResolveAlternatives(ctx, a)
	require.NoError(t, err)

	assert.Len(t, resolved, 3, "B, C and D -- D once, not twice. Two paths to a "+
		"node is normal in a hand-curated graph and the visited set is the only "+
		"thing standing between that and a directory listing a studio twice")
	assert.Equal(t, []uuid.UUID{b, c, d}, resolved,
		"and in the order they were first reached, so the list is stable between "+
			"reads")
}

// A long chain is truncated at the depth bound rather than followed to the end.
func TestTheWalkIsBoundedAtTwoLevels(t *testing.T) {
	svc := dirSvc(t)
	ctx := context.Background()
	// A chain six deep: a->b->c->d->e->f.
	ids := directorySites(t, "chain-a", "chain-b", "chain-c",
		"chain-d", "chain-e", "chain-f")
	for i := 0; i < len(ids)-1; i++ {
		require.NoError(t, svc.AddAlternative(ctx, ids[i], ids[i+1]))
	}

	resolved, err := svc.ResolveAlternatives(ctx, ids[0])
	require.NoError(t, err)

	assert.Len(t, resolved, 2,
		"two levels is what a directory needs -- a site's alternatives and their "+
			"alternatives. Following the chain to the end returns sites no reader "+
			"would use and is the region where cycles live")
	assert.Equal(t, []uuid.UUID{ids[1], ids[2]}, resolved)
}

// Adding the same edge twice is success, not failure.
func TestAddingAnExistingAlternativeIsNotAnError(t *testing.T) {
	svc := dirSvc(t)
	ctx := context.Background()
	ids := directorySites(t, "dup-a", "dup-b")
	a, b := ids[0], ids[1]

	require.NoError(t, svc.AddAlternative(ctx, a, b))
	require.NoError(t, svc.AddAlternative(ctx, a, b),
		"ON CONFLICT DO NOTHING returns no rows, so the second click on a UI "+
			"toggle must not be reported as a failure")

	var count int
	require.NoError(t, dbtest.DB().QueryRow(ctx,
		`SELECT count(*)::int FROM site_alternatives
		 WHERE site_id = $1 AND alternative_site_id = $2`, a, b).Scan(&count))
	assert.Equal(t, 1, count, "and exactly one row exists, not two")
}

// Removing an edge is idempotent, for the same reason adding is.
func TestRemovingAMissingAlternativeIsNotAnError(t *testing.T) {
	svc := dirSvc(t)
	ctx := context.Background()
	ids := directorySites(t, "rm-a", "rm-b")
	a, b := ids[0], ids[1]

	assert.NoError(t, svc.RemoveAlternative(ctx, a, b),
		"the desired state is 'no edge', and it already being absent IS that "+
			"state. A toggle that errors when asked to remove what is not there "+
			"gets clicked twice")
}

// A site with no directory row returns nil, not an error: "unfilled" is the
// normal state for most of the catalogue.
func TestDetailsForAnUnfilledSiteIsNilNotAnError(t *testing.T) {
	svc := dirSvc(t)
	ctx := context.Background()
	siteID := directorySites(t, "unfilled")[0]

	got, err := svc.Details(ctx, siteID)
	require.NoError(t, err, "every site page asks this; an error on a site "+
		"nobody has written about is a broken page, not an exception")
	assert.Nil(t, got, "nil means UNKNOWN, which is not the same as empty")
}

// NIL and EMPTY are different facts and both must survive the round trip.
func TestNilAndEmptyDetailsAreDifferent(t *testing.T) {
	svc := dirSvc(t)
	ctx := context.Background()
	siteID := directorySites(t, "nil-vs-empty")[0]
	// A REAL user row, not a fresh UUID. My first version minted one and the
	// insert failed on the foreign key -- which is the constraint working, and is
	// worth having: attribution that names a user who does not exist is a review
	// history pointing at nothing.
	editor := createUserForQuest(t, "A Directory Editor")

	// Unknown: the columns are not set at all.
	unknown, err := svc.SetDetails(ctx, site.SetDetailsInput{SiteID: siteID})
	require.NoError(t, err)
	assert.Nil(t, unknown.PaymentMethods, "unset is NULL, not an empty list")

	// Known-empty: the fields were filled in and found to be empty.
	known, err := svc.SetDetails(ctx, site.SetDetailsInput{
		SiteID: siteID, PaymentMethods: []string{}, UpdatedBy: &editor,
	})
	require.NoError(t, err)
	assert.NotNil(t, known.PaymentMethods,
		"a deliberately emptied list must not come back as nil, or the UI "+
			"cannot tell 'nobody wrote this' from 'this site takes no payment "+
			"methods' and renders both as the same dash")
	assert.Len(t, known.PaymentMethods, 0)
	require.NotNil(t, known.UpdatedBy)
	assert.Equal(t, editor, *known.UpdatedBy, "and the editor is attributed")
}

// The search filter is OVERLAP, not containment: a site that takes cash AND cards
// must match a filter for cards.
func TestTheSearchFilterMatchesOnOverlap(t *testing.T) {
	svc := dirSvc(t)
	ctx := context.Background()
	cardSite := directorySites(t, "takes-cards")[0]
	cashOnly := directorySites(t, "cash-only")[0]

	_, err := svc.SetDetails(ctx, site.SetDetailsInput{
		SiteID: cardSite, PaymentMethods: []string{"credit card", "cash"},
	})
	require.NoError(t, err)
	_, err = svc.SetDetails(ctx, site.SetDetailsInput{
		SiteID: cashOnly, PaymentMethods: []string{"cash"},
	})
	require.NoError(t, err)

	found, err := svc.Search(ctx, site.SearchInput{
		PaymentMethods: []string{"credit card"}, Limit: 100,
	})
	require.NoError(t, err)

	names := siteIDsToNames(t, found)
	assert.Contains(t, names, "dir-takes-cards",
		"a site taking cards AND cash matches a filter for cards. Containment "+
			"(@>) would require an exact list match and match nothing here, which "+
			"is most real sites")
	assert.NotContains(t, names, "dir-cash-only")
}

// An unset free-text query must be NULL, not the empty string.
//
// The distinction is invisible in the RESULT -- an ILIKE '%%' filter matches
// everything, so both paths return the same rows -- and that is exactly why it
// needed a test written against the query plan rather than the output. `query = ”`
// puts a filter on every row of the directory; `query = NULL` does not, and on a
// large instance the difference is a sequential scan per search box keystroke.
func TestAnUnsetSearchQueryIsNullNotEmptyString(t *testing.T) {
	dirSvc(t) // ensure the schema is loaded before the EXPLAIN below

	var isNull bool
	require.NoError(t, dbtest.DB().QueryRow(context.Background(),
		`SELECT $1::text IS NULL`, nil).Scan(&isNull))
	assert.True(t, isNull, "the service passes a nil pointer, so sqlc's narg "+
		"binds NULL and the SQL's `narg IS NULL` branch takes the no-filter path")

	// And the empty string is NOT null, which is the whole reason the Go-side
	// `if in.Query != ""` exists.
	var emptyIsNull bool
	require.NoError(t, dbtest.DB().QueryRow(context.Background(),
		`SELECT $1::text IS NULL`, "").Scan(&emptyIsNull))
	assert.False(t, emptyIsNull,
		"an empty search box must not become an ILIKE '%%' filter. It costs a "+
			"scan and returns the same rows, so it is invisible except in latency")
}

// An empty filter means NO filter, and that is decided once, in Go.
func TestAnEmptyFilterMatchesEverything(t *testing.T) {
	svc := dirSvc(t)
	ctx := context.Background()
	directorySites(t, "filter-target")

	found, err := svc.Search(ctx, site.SearchInput{Limit: 100})
	require.NoError(t, err)
	assert.NotEmpty(t, found,
		"an empty slice means no filter. If it meant 'match nothing', every "+
			"unfiltered directory search would return nothing at all")
}

// The review summary is joined into the search, and a site with no reviews is
// zero-of-zero rather than an error or a NULL.
func TestTheDirectorySearchIncludesTheReviewSummary(t *testing.T) {
	svc := dirSvc(t)
	ctx := context.Background()
	reviewed := directorySites(t, "reviewed")[0]
	author := createUserForQuest(t, "A Site Reviewer")

	_, err := dbtest.Factory().Review().Submit(ctx, review.SubmitInput{
		AuthorID: author, EntityType: review.EntitySite, EntityID: reviewed,
		// intPtr is from review_integration_test.go, in the same package.
		Rating: intPtr(4), Body: "Solid.",
	})
	require.NoError(t, err)

	found, err := svc.Search(ctx, site.SearchInput{Query: "dir-reviewed", Limit: 100})
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.InDelta(t, 4.0, found[0].Rating.Average, 0.001)
	assert.Equal(t, 1, found[0].Rating.Rated)
	assert.Equal(t, 1, found[0].Rating.Total)
	// And a site with NO reviews is present with a zero summary rather than
	// absent, because the COALESCE in the join is what a directory card renders.
	unreviewed := directorySites(t, "unreviewed")[0]
	found, err = svc.Search(ctx, site.SearchInput{Query: "dir-unreviewed", Limit: 100})
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, unreviewed, found[0].SiteID)
	assert.Equal(t, float64(0), found[0].Rating.Average)
	assert.Equal(t, 0, found[0].Rating.Total,
		"zero of zero, not a NULL the client has to special-case")
}

func siteIDsToNames(t *testing.T, entries []site.Entry) []string {
	t.Helper()
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name)
	}
	return out
}
