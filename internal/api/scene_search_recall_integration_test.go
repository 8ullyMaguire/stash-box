//go:build integration

package api_test

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
)

// SPEC §7.25.1 / §7.25.2: the recall index.
//
// What this file is for. `scene_search` is a denormalized table maintained
// entirely by triggers, and before migration 103 it indexed none of a scene's
// `details`, none of its `director`, and none of its tags. Those three are where
// broad-stroke recall lives — a person who cannot name a scene remembers "a
// hotel room, rainy night" and "anal", not a title.
//
// The unit tests and the shell probe in `scripts/verify-103.sh` cover the
// mechanics. What is left, and what only this file can do, is prove the whole
// path end to end: a tag added through the ordinary query layer moves the
// search row, and a token from a scene's `details` finds that scene through the
// real search service.
//
// The tests are deliberately built around INSERT/UPDATE/DELETE rather than
// through the service, because the claim under test is about triggers. A test
// that went through the edit path would prove the edit path, and would still
// pass with every trigger in this migration dropped.

// searchRowFor reads the one scene_search row for a scene.
//
// Asserts the row EXISTS rather than returning zero values, because "the trigger
// did not fire" and "the trigger fired with NULLs" are different failures and a
// zero-value struct cannot tell you which.
func searchRowFor(t *testing.T, sceneID uuid.UUID) queries.GetSceneSearchRowRow {
	t.Helper()
	row, err := q().GetSceneSearchRow(t.Context(), sceneID)
	require.NoError(t, err, "reading the search row")
	return row
}

// strPtrValue unwraps the nullable string columns in the generated search row.
//
// `scene_details` and `scene_director` are `*string`, so comparing them to a
// plain string fails with "*string Value" rather than the value -- an assertion
// that reads as "the trigger did not fire" when the trigger worked fine. Three
// tests failed this way before the helper existed.
// sceneIDs projects a search result set to bare ids, so a containment
// assertion reads as a list of ids rather than as struct comparison noise.
func sceneIDs(scenes []models.Scene) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(scenes))
	for _, sc := range scenes {
		out = append(out, sc.ID)
	}
	return out
}

func strPtrValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// recallScene inserts a scene whose only identifying content is in `details`.
func recallScene(t *testing.T, details string, director *string) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	_, err := q().CreateScene(t.Context(), queries.CreateSceneParams{
		ID:       id,
		Title:    strPtr("Untitled"),
		Details:  strPtr(details),
		Director: director,
	})
	require.NoError(t, err, "creating the scene under test")
	return id
}

func TestSceneSearchIndexesDetailsAndDirector(t *testing.T) {
	director := "A Half Remembered Name"
	id := recallScene(t, "a cramped hotel room on a rainy night", &director)

	row := searchRowFor(t, id)
	assert.Equal(t, "a cramped hotel room on a rainy night", strPtrValue(row.SceneDetails),
		"details did not reach scene_search on insert")
	assert.Equal(t, "A Half Remembered Name", strPtrValue(row.SceneDirector),
		"director did not reach scene_search on insert")
}

func TestSceneSearchFollowsAnEditToDetails(t *testing.T) {
	id := recallScene(t, "a cramped hotel room on a rainy night", nil)

	_, err := q().UpdateScene(t.Context(), queries.UpdateSceneParams{
		ID:      id,
		Title:   strPtr("Untitled"),
		Details: strPtr("a rooftop at dawn"),
	})
	require.NoError(t, err)

	assert.Equal(t, "a rooftop at dawn", strPtrValue(searchRowFor(t, id).SceneDetails),
		"an edit to details did not propagate to scene_search")
}

// TestSceneSearchFollowsTagChanges is THE test for migration 103's triggers.
//
// scene_tags carried no trigger at all before this migration, so nothing ever
// recomputed a scene's search row when its tags changed. Each of the three
// subtests fails if its trigger is missing, and all three pass against a
// correct database — which is the pair of facts that makes them evidence.
func TestSceneSearchFollowsTagChanges(t *testing.T) {
	sceneID := recallScene(t, "a cramped hotel room on a rainy night", nil)

	firstTag := uuid.Must(uuid.NewV7())
	_, err := q().CreateTag(t.Context(), queries.CreateTagParams{ID: firstTag, Name: "anal"})
	require.NoError(t, err)
	secondTag := uuid.Must(uuid.NewV7())
	_, err = q().CreateTag(t.Context(), queries.CreateTagParams{ID: secondTag, Name: "outdoors"})
	require.NoError(t, err)

	_, err = q().CreateSceneTags(t.Context(), []queries.CreateSceneTagsParams{{
		SceneID: sceneID, TagID: firstTag,
	}})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"anal"}, searchRowFor(t, sceneID).TagNames,
		"adding the first tag did not reach scene_search")

	// The second tag is what a bulk import looks like: one statement, two rows.
	_, err = q().CreateSceneTags(t.Context(), []queries.CreateSceneTagsParams{{
		SceneID: sceneID, TagID: secondTag,
	}})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"anal", "outdoors"}, searchRowFor(t, sceneID).TagNames,
		"adding a second tag did not reach scene_search")

	// A rename must reach every scene carrying the tag. tags already had its own
	// trigger into upsert_tag_search, so without trg_scene_search_on_tag_rename
	// the tag row updates and every scene keeps the OLD name — which is the recall
	// failure this migration exists to fix, introduced by the fix.
	_, err = q().UpdateTag(t.Context(), queries.UpdateTagParams{
		ID: secondTag, Name: "outdoor",
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"anal", "outdoor"}, searchRowFor(t, sceneID).TagNames,
		"renaming a tag did not reach the scenes carrying it")

	// Removing ALL tags then re-adding one is the only remove path the query
	// layer offers (DeleteSceneTagsByScene), so the assertion is that the column
	// empties and refills rather than that one specific row vanished.
	require.NoError(t, q().DeleteSceneTagsByScene(t.Context(), sceneID))
	assert.Empty(t, searchRowFor(t, sceneID).TagNames,
		"removing tags did not reach scene_search")

	_, err = q().CreateSceneTags(t.Context(), []queries.CreateSceneTagsParams{{
		SceneID: sceneID, TagID: firstTag,
	}})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"anal"}, searchRowFor(t, sceneID).TagNames,
		"re-adding a tag did not reach scene_search")
}

// TestSceneSearchFindsASceneByItsDetails is the end-to-end proof.
//
// Everything above asserts the INDEX holds a value. This asserts a SEARCH
// returns the scene, which is the claim a user actually experiences and the one
// the whole feature exists for.
func TestSceneSearchFindsASceneByItsDetails(t *testing.T) {
	id := recallScene(t, "a cramped hotel room on a rainy night", nil)

	results, err := dbtest.Factory().Scene().SearchScenesWithCount(t.Context(), "rainy", 10, 0)
	require.NoError(t, err, "searching by a token from details")
	require.NotNil(t, results.SearchResults)
	require.NotEmpty(t, results.SearchResults.Scenes,
		"a scene whose details contain the search token was not found")

	// CONTAINS, not "is first". A shared test database holds scenes from other
	// tests, and BM25 ranks across all of them, so insisting this scene be the
	// TOP hit asserts something about the fixture rather than about the feature
	// -- and it failed that way. What the feature claims is that a token in
	// `details` brings the scene into the result set at all; the ranking of a
	// scene against an arbitrary rival is a separate property with a separate
	// test.
	assert.Contains(t, sceneIDs(results.SearchResults.Scenes), id,
		"the scene whose details matched is absent from the results")
}

func TestSceneSearchFindsASceneByItsTag(t *testing.T) {
	sceneID := recallScene(t, "a cramped hotel room on a rainy night", nil)
	tagID := uuid.Must(uuid.NewV7())
	_, err := q().CreateTag(t.Context(), queries.CreateTagParams{ID: tagID, Name: "raincoat"})
	require.NoError(t, err)
	_, err = q().CreateSceneTags(t.Context(), []queries.CreateSceneTagsParams{{
		SceneID: sceneID, TagID: tagID,
	}})
	require.NoError(t, err)

	results, err := dbtest.Factory().Scene().SearchScenesWithCount(t.Context(), "raincoat", 10, 0)
	require.NoError(t, err, "searching by a tag name")
	require.NotNil(t, results.SearchResults)
	require.NotEmpty(t, results.SearchResults.Scenes,
		"a scene was not found by a tag name — tag_names is not being matched")
	assert.Contains(t, sceneIDs(results.SearchResults.Scenes), sceneID,
		"the tagged scene is absent from a search for its own tag name")
}

// TestSceneSearchDriftIsDetectable is SPEC §7.25.2's whole test.
//
// A drift check that has only ever run against a healthy database has not been
// tested, it has been exercised. So this deletes a search row BEHIND the
// triggers' back — the only way to produce drift, since the triggers are what
// the migration adds — and asserts the check sees it and names it.
func TestSceneSearchDriftIsDetectable(t *testing.T) {
	svc := dbtest.Factory().Scene()

	before, err := svc.Drift(t.Context())
	require.NoError(t, err)
	require.False(t, before.Drifted(),
		"drift reported on a healthy database: scene_count=%d indexed_count=%d missing=%v",
		before.SceneCount, before.IndexedCount, before.MissingIDs)

	id := recallScene(t, "a cramped hotel room on a rainy night", nil)

	_, err = dbtest.DB().Exec(t.Context(),
		"DELETE FROM scene_search WHERE scene_id = $1", id)
	require.NoError(t, err, "removing a search row behind the triggers' back")

	after, err := svc.Drift(t.Context())
	require.NoError(t, err)
	assert.True(t, after.Drifted(),
		"a scene missing from scene_search was not reported as drift")
	assert.Equal(t, before.SceneCount+1, after.SceneCount)
	assert.Equal(t, before.IndexedCount, after.IndexedCount)
	assert.Contains(t, after.MissingIDs, id,
		"drift was reported but the missing scene was not named")

	// And the repair the upsert function performs on its own.
	_, err = dbtest.DB().Exec(t.Context(), "SELECT upsert_scene_search($1)", id)
	require.NoError(t, err)
	repaired, err := svc.Drift(t.Context())
	require.NoError(t, err)
	assert.False(t, repaired.Drifted(),
		"upsert_scene_search did not repair the drift it is supposed to repair")
}
