//go:build integration

package api_test

import (
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/queries"
)

// Scene.sources through the real API: the aggregator programme's first observable
// surface (docs/spec/feature-content-aggregator.md, phase 1).
//
// WHY THIS FILE EXISTS WHEN scripts/verify-108.sh ALREADY PASSES. That harness proves
// the constraints, the trigger and the view, and it is the only thing that can. What it
// cannot prove is that any of it is REACHABLE: a table nobody reads, a view nobody queries
// and a field nobody wired all pass it. This is the wiring test, and it goes through the
// GraphQL API, the dataloader and the service -- the path a client actually takes.
//
// Every assertion here names the layer it measures, because the failure modes are
// different: "the field is absent" is a schema or resolver problem, "the list is empty" is
// a view or join problem, "the status is wrong" is a conversion problem.

type sceneSourceFixture struct {
	id            uuid.UUID
	url           string
	siteID        uuid.UUID
	healthMissing bool
}

// sceneSourceRow mirrors the GraphQL selection. Pointer fields are the nullable ones --
// the schema says so, and a non-pointer here would silently accept a null.
//
// The json tags are required, not decoration: gqlgen's client decodes the response with
// encoding/json, which matches keys case-insensitively against the FIELD NAME -- so `url`
// lands on URL and `check_failures` lands nowhere. Without the tags the client refuses the
// whole response ("has invalid keys: check_failures, first_seen, last_ok") and the test
// fails for a reason that has nothing to do with the code under test.
type sceneSourceRow struct {
	ID            *string `json:"id"`
	URL           string  `json:"url"`
	Status        string  `json:"status"`
	Canonical     bool    `json:"canonical"`
	CheckFailures int     `json:"check_failures"`
	Platform      *string `json:"platform"`
	FirstSeen     *string `json:"first_seen"`
	LastOK        *string `json:"last_ok"`
	Site          *struct {
		Name string `json:"name"`
	} `json:"site"`
}

// contentQ is a direct query handle for fixture setup.
//
// Named rather than `q` because collage_integration_test.go already has a `q` in this
// package, and two helpers with one name is a compile error that reads like a broken tree.
func contentQ() *queries.Queries { return queries.New(dbtest.DB()) }

// createSceneWithSources inserts a scene and its URLs directly.
//
// Directly rather than through the create/edit mutation, because what is under test is the
// READ path: going through the edit path would make every case depend on edit application,
// and a failure there would be reported as a failure here.
func createSceneWithSources(t *testing.T, name string, urls ...string) (uuid.UUID, uuid.UUID) {
	t.Helper()

	sceneID := uuid.Must(uuid.NewV7())
	_, err := contentQ().CreateScene(t.Context(), queries.CreateSceneParams{
		ID:    sceneID,
		Title: &name,
	})
	require.NoError(t, err, "the scene under test")

	siteID := uuid.Must(uuid.NewV7())
	_, err = contentQ().CreateSite(t.Context(), queries.CreateSiteParams{
		ID:   siteID,
		Name: "sources test site " + siteID.String(),
		// NOT NULL in the schema, and an empty list is the honest value: this site has no
		// declared valid types. Creating it without this column fails the fixture and every
		// assertion below then reports a missing source instead of a missing site.
		ValidTypes: []string{},
	})
	require.NoError(t, err, "the site under test")

	rows := make([]queries.CreateSceneURLsParams, 0, len(urls))
	for _, u := range urls {
		rows = append(rows, queries.CreateSceneURLsParams{SceneID: sceneID, Url: u, SiteID: siteID})
	}
	if len(rows) > 0 {
		_, err = contentQ().CreateSceneURLs(t.Context(), rows)
		require.NoError(t, err, "the scene's URLs")
	}

	return sceneID, siteID
}

// sourcesOf reads the scene's sources through the API as a moderator.
func sourcesOf(t *testing.T, sceneID uuid.UUID) []sceneSourceRow {
	t.Helper()

	actor := asModify(t)
	var got struct {
		FindScene *struct {
			Sources []sceneSourceRow
		}
	}
	actor.post(`query ($id: ID!) {
		findScene(id: $id) {
			sources { id url status canonical check_failures platform first_seen last_ok site { name } }
		}
	}`, &got, client.Var("id", sceneID.String()))

	require.NotNil(t, got.FindScene,
		"findScene returned null: either the scene is missing or the field is not resolvable")
	return got.FindScene.Sources
}

func TestSceneSourcesAreReachableFromASceneQuery(t *testing.T) {
	const url = "https://sources-test.invalid/one"
	sceneID, siteID := createSceneWithSources(t, "Sources reachable", url)

	sources := sourcesOf(t, sceneID)

	// Counted before anything is asserted about their contents: an absence assertion over
	// a list that came back empty because the FIELD is missing is vacuously true, which is
	// the failure this whole file is here to avoid.
	require.Len(t, sources, 1, "one URL was inserted, so one source must come back; zero "+
		"means the field, the view or the join is not wired, and it is none of those if the "+
		"URL is below")

	source := sources[0]
	assert.Equal(t, url, source.URL)
	// Nothing has ever checked this URL -- the trigger creates the row, migration 108 sets
	// no status of its own, and the default is 'unknown' rather than 'live' precisely so a
	// client cannot read an unverified claim as a verified one.
	// The field is an ENUM now (`ContentSourceStatus`), and the database's column is lowercase while
	// the enum is not: this assertion is the one that would catch a mapping that passed the column's
	// string through instead of reading it, because the wire value would come back as "" rather than
	// failing.
	assert.Equal(t, "UNKNOWN", source.Status,
		"a source nothing has checked must report UNKNOWN, not LIVE")
	assert.False(t, source.Canonical, "no canonical source has been elected for this scene")
	assert.Equal(t, 0, source.CheckFailures)

	require.NotNil(t, source.Site, "the site exists and is loaded through the dataloader")
	require.NotEmpty(t, source.Site.Name, "a site with no name is as useless as no site")
	assert.Contains(t, source.Site.Name, siteID.String(),
		"the site returned must be the one this URL belongs to, not any site")

	require.NotNil(t, source.ID,
		"the health row was created by the scene_urls trigger, so the source has an id")
	require.NotNil(t, source.FirstSeen,
		"first_seen is set at insert time; null here means the row->model conversion lost it")
}

func TestSceneSourceReportsTheHealthStateTheRowCarries(t *testing.T) {
	sceneID, _ := createSceneWithSources(t, "Sources health", "https://sources-test.invalid/dead")

	// Write a state by hand, as the phase 9 health sweep will. Nothing else in this test
	// changes it, so every value asserted below has exactly one possible source: the row.
	var sourceID uuid.UUID
	require.NoError(t, dbtest.DB().QueryRow(t.Context(),
		`UPDATE content_sources SET status = 'dead', check_failures = 9,
		     last_checked = now(), last_ok = now() - interval '2 days', canonical = true
		 WHERE url = $1 RETURNING id`,
		"https://sources-test.invalid/dead").Scan(&sourceID))

	sources := sourcesOf(t, sceneID)
	require.Len(t, sources, 1)

	source := sources[0]
	assert.Equal(t, "DEAD", source.Status,
		"the status came from the row; if this says UNKNOWN the field is not reading health, and a "+
			"lowercase 'dead' here means the enum mapping passed the column straight through")
	assert.Equal(t, 9, source.CheckFailures)
	assert.True(t, source.Canonical, "canonical is the row's, not a constant")
	require.NotNil(t, source.ID)
	assert.Equal(t, sourceID.String(), *source.ID, "the id is the content_sources row's id")
	require.NotNil(t, source.LastOK,
		"last_ok is set, and it is the field that says WHEN the source was good; null here "+
			"means the timestamp conversion dropped a valid value")
}

func TestSceneSourceWithNoHealthRowStillAppears(t *testing.T) {
	const url = "https://sources-test.invalid/no-health"
	sceneID, _ := createSceneWithSources(t, "Sources orphan", url)

	// The state the view's LEFT JOIN exists for: the scene claims the URL, no health row
	// exists. Migration 108 makes this reachable deliberately (a URL added between two
	// sweeps, or a scene_urls row whose mirror has not fired), and verify-108.sh asserts it
	// at the SQL level. Here it has to survive the API: dropping the row and then reporting
	// NOTHING would leave a scene listing one URL under `urls` and zero under `sources`,
	// which a client cannot explain.
	_, err := dbtest.DB().Exec(t.Context(),
		`DELETE FROM content_sources WHERE url = $1`, url)
	require.NoError(t, err)

	sources := sourcesOf(t, sceneID)
	require.Len(t, sources, 1, "the URL is still claimed by the scene, so it must still appear")

	assert.Equal(t, url, sources[0].URL)
	assert.Equal(t, "UNKNOWN", sources[0].Status,
		"with no health row the only honest status is UNKNOWN, and it must survive the enum mapping "+
			"rather than erroring the read")
	assert.Nil(t, sources[0].ID,
		"there is no source row to identify, and a zero uuid would tell a client the opposite")
	assert.Nil(t, sources[0].FirstSeen)
	assert.Equal(t, 0, sources[0].CheckFailures)
}

func TestSceneWithNoURLsReturnsAnEmptySourceListNotAnAbsentOne(t *testing.T) {
	sceneID, _ := createSceneWithSources(t, "Sources empty")

	// The subject is COUNTED first. `sources` is `[ContentSource!]!`, so "empty" and
	// "missing" are different claims and only one of them is legal; asserting emptiness
	// without proving the scene resolved would pass on a scene that does not exist.
	actor := asModify(t)
	var got struct {
		FindScene *struct {
			ID      string
			Sources []sceneSourceRow
		}
	}
	actor.post(`query ($id: ID!) { findScene(id: $id) { id sources { url } } }`,
		&got, client.Var("id", sceneID.String()))

	require.NotNil(t, got.FindScene, "the scene under test must resolve, or nothing below is meaningful")
	assert.Equal(t, sceneID.String(), got.FindScene.ID)
	assert.NotNil(t, got.FindScene.Sources,
		"a scene with no URLs has an EMPTY source list, not a null one: the schema declares "+
			"the field non-null, and a null here is a resolver returning nil for an empty slice")
	assert.Empty(t, got.FindScene.Sources)
}
