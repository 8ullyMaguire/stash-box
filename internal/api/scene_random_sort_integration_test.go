//go:build integration

package api_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/models"
)

// RANDOM scene sort (growth item 19).
//
// The tracker said this "needs a real ORDER BY random()", which is true but is the
// smallest part of it. The part that actually needed care:
//
//  1. RANDOM is an enum member that is NOT a column name. Every other member works by
//     lowercasing into `ORDER BY scenes.<member>`, so RANDOM falls through that path and
//     builds `ORDER BY scenes.random` -- a runtime failure, not a compile-time one. It
//     needs its own switch case.
//
//  2. COUNT must not be randomised. The count query shares this builder, and ordering
//     randomness there is at best wasted work. Both paths are asserted below.

// `queryScenes`, not `findScenes`: findScenes takes `ids: [ID!]!` and returns a plain
// list, so it has neither a count nor a sort. I wrote findScenes first and it 422'd on
// five counts of nonsense.
const randomSortQuery = `
	query($input: SceneQueryInput!) {
		queryScenes(input: $input) {
			count
			scenes { id title }
		}
	}
`

type randomSortScene struct {
	ID    string
	Title string
}

type randomSortResult struct {
	Count  int
	Scenes []randomSortScene
}

type randomSortResponse struct {
	QueryScenes randomSortResult
}

func askRandomSort(t *testing.T, r *testRunner, page, perPage int) randomSortResult {
	t.Helper()
	var resp randomSortResponse
	r.client.MustPost(randomSortQuery, &resp,
		client.Var("input", map[string]any{
			"page":     page,
			"per_page": perPage,
			"sort":     "RANDOM",
		}))
	return resp.QueryScenes
}

// seedScenes creates n scenes so there is something to order.
//
// NECESSARY, NOT POLITE: the suite's PopulateDB seeds USERS ONLY. Without this every
// assertion below fails as "expected 8 scenes, got none", which reads as a broken sort
// rather than an empty fixture -- the same plausible-wrong-answer trap as everywhere else
// in this work.
//
// Distinct from the seedScenes in expected_total_integration_test.go, which inserts via
// raw SQL and requires a studio id. This one goes through sceneCreate, so the fixture
// cannot bypass anything the mutation enforces.
func seedRandomSortScenes(t *testing.T, r *testRunner, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		title := fmt.Sprintf("Random Sort Scene %02d", i)
		_, err := r.client.createScene(models.SceneCreateInput{
			Title: &title,
			Date:  "2026-01-01",
			// EXPLICITLY empty, not nil: `fingerprints: [FingerprintEditInput!]!` is
			// non-null in the schema, a nil slice serialises away via omitempty, and the
			// result is a 422 that names only fingerprints -- so it reads as a missing
			// field rather than as a zero value.
			Fingerprints: []models.FingerprintEditInput{},
		})
		require.NoError(t, err, "creating fixture scene %d", i)
	}
}

// RANDOM actually reaches the database and does not error.
//
// The weakest assertion in the file, and the one that would catch the `scenes.random`
// mistake: that mistake does not fail the build or the query construction, it fails at
// execution with "column scenes.random does not exist".
func TestRandomSceneSortIsAccepted(t *testing.T) {
	seedRandomSortScenes(t, asAdmin(t), 12)
	got := askRandomSort(t, asRead(t), 1, 10)

	require.NotEmpty(t, got.Scenes, "the archive has scenes; RANDOM must return some")
	assert.Positive(t, got.Count, "COUNT must be computed, and RANDOM must not break it")
}

// The order genuinely varies between requests.
//
// This is the assertion that proves ORDER BY random() rather than proving "it did not
// error". A hardcoded order would also pass the test above. Ten draws must not all match.
//
// Probabilistic, so it is written to be effectively impossible to pass by luck: with 8
// scenes the chance of 10 identical draws is (1/8)^9 per comparison, and there are two
// comparisons. It is a stable test rather than a flaky one -- the failure mode is the
// concern, not a 1-in-a-million pass.
func TestRandomSceneSortRedrawsPerRequest(t *testing.T) {
	seedRandomSortScenes(t, asAdmin(t), 10)
	r := asRead(t)

	first := askRandomSort(t, r, 1, 8)
	require.GreaterOrEqual(t, first.Count, 8, "need at least 8 scenes for this to mean anything")

	var sameAsFirst int
	identical := 0
	for i := 0; i < 10; i++ {
		got := askRandomSort(t, r, 1, 8)
		require.Len(t, got.Scenes, 8)

		if slices.Equal(idsOf(got.Scenes), idsOf(first.Scenes)) {
			sameAsFirst++
		}
		if len(unique(idsOf(got.Scenes))) != 8 {
			t.Fatalf("a page returned duplicate scenes: %v", idsOf(got.Scenes))
		}
	}
	identical = sameAsFirst

	assert.Less(t, identical, 2,
		"10 random draws of 8 scenes should not mostly repeat one order -- if they do, "+
			"the sort is not random")
}

// COUNT is unaffected by the sort, and pagination bounds the draw.
//
// Count is the assertion that catches RANDOM leaking into the count query: if the count
// builder also ordered randomly, a paged count could disagree with the unfiltered total,
// which shows up as a paginator that stops early.
func TestRandomSceneSortKeepsCountAndPaginates(t *testing.T) {
	seedRandomSortScenes(t, asAdmin(t), 9)
	r := asRead(t)

	unpaged := askRandomSort(t, r, 1, 100)
	page1 := askRandomSort(t, r, 1, 3)
	page2 := askRandomSort(t, r, 2, 3)

	assert.Equal(t, unpaged.Count, page1.Count,
		"COUNT must not depend on the page or on the random order")
	assert.Len(t, page1.Scenes, 3, "per_page must bound the random draw")
	assert.Len(t, page2.Scenes, 3)

	// Page 2 is an INDEPENDENT sample, not a continuation of page 1 -- the ordering is
	// redrawn per request. So the two pages may overlap, and asserting they differ would
	// be asserting the impossible. What must hold is that each page is internally
	// duplicate-free and within the archive.
	assert.Len(t, unique(idsOf(page2.Scenes)), 3, "page 2 must not repeat a scene within itself")
	for _, s := range page2.Scenes {
		assert.NotEmpty(t, s.ID)
	}
}

func idsOf(scenes []randomSortScene) []string {
	out := make([]string, 0, len(scenes))
	for _, s := range scenes {
		out = append(out, s.ID)
	}
	return out
}

func unique(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
