//go:build integration

package api_test

import (
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Issue #621: "Failed to load edits." after deleting a site used in a pending
// edit.
//
//   1. Create a new site
//   2. Create a pending edit to create or modify a scene by adding a link of
//      that type
//   3. Delete the site
//   4. Go to /edits and enjoy your `Error: Failed to load edits.`
//
// The mechanism. GetMergedURLsForEdit builds its result from two sources:
//
//	current_urls  scene_urls / performer_urls / studio_urls  -- foreign keys
//	              to sites, so a deleted site cascades the row away
//	added_urls    jsonb_array_elements(edit.data->'new_data'->'added_urls')
//	              -- NOT a foreign key, and not cascaded
//
// So the site's own URLs vanish but the edit's added_urls keep a dangling
// site_id. That id reached URL.site, which the schema declares non-null
// (`site: Site!`, misc.graphql:25), so the dataloader's nil became
// "the requested element is null which the schema does not allow" and took
// down the entire /edits page -- one bad row failing every edit on it.

// A deleted site must not leave a URL that breaks the edit query. This
// reproduces the reported steps exactly, and queries the shape the frontend
// uses (URLFragment requires site { id name ... }).
func TestEditWithURLOfDeletedSiteDoesNotBreakQuery(t *testing.T) {
	admin := asAdmin(t)

	// Step 1: the site.
	site, err := admin.createTestSite(nil)
	require.NoError(t, err)
	siteID := site.ID

	scene, err := admin.createTestScene(&models.SceneCreateInput{
		Title: pStr(admin.generateSceneName()),
		Date:  "2024-01-01",
	})
	require.NoError(t, err)
	sceneID := scene.UUID()

	// Step 2: a pending edit adding a link of that site type.
	edit, err := admin.createTestSceneEdit(models.OperationEnumModify,
		&models.SceneEditDetailsInput{
			Urls: []models.URL{{
				URL:    "https://example.org/621-reproducer",
				SiteID: siteID,
			}},
		},
		&models.EditInput{Operation: models.OperationEnumModify, ID: &sceneID})
	require.NoError(t, err)
	require.NotNil(t, edit)

	// Step 3: delete the site. The edit is still pending and still references it.
	_, err = admin.resolver.Mutation().SiteDestroy(admin.ctx, models.SiteDestroyInput{ID: siteID})
	require.NoError(t, err)

	// Step 4: loading the edit must not fail.
	//
	// This is the reported query path. It has to be the real GraphQL query:
	// the failure is gqlgen rejecting a null in a non-null position while
	// marshalling, which a resolver-level assertion cannot observe at all.
	q := `
	query FindEditAfterSiteDeleted($id: ID!) {
		findEdit(id: $id) {
			id
			details {
				... on SceneEdit {
					urls {
						url
						site { id name }
					}
				}
			}
		}
	}`

	var resp struct {
		FindEdit struct {
			ID      string `json:"id"`
			Details struct {
				URLs []struct {
					URL  string `json:"url"`
					Site struct {
						ID   string `json:"id"`
						Name string `json:"name"`
					} `json:"site"`
				} `json:"urls"`
			} `json:"details"`
		} `json:"findEdit"`
	}

	err = admin.client.Post(q, &resp, client.Var("id", edit.ID.String()))
	require.NoError(t, err,
		"querying an edit whose site was deleted must not fail (#621)")

	// The dangling URL must be dropped, not returned with a null site -- and
	// not returned at all, since there is no site to render.
	for _, u := range resp.FindEdit.Details.URLs {
		assert.NotEmpty(t, u.Site.ID,
			"a URL whose site was deleted must not surface a null site (#621)")
		assert.NotEqual(t, "https://example.org/621-reproducer", u.URL,
			"the URL for the deleted site must be filtered out entirely (#621)")
	}
}

// The negative case, and the one that gives the test above its meaning: while
// the site EXISTS, its URL must still be present. Without this, "the query
// succeeded" could be satisfied by dropping every URL unconditionally.
func TestEditWithURLOfLiveSiteStillResolves(t *testing.T) {
	admin := asAdmin(t)

	site, err := admin.createTestSite(nil)
	require.NoError(t, err)
	siteID := site.ID

	scene, err := admin.createTestScene(&models.SceneCreateInput{
		Title: pStr(admin.generateSceneName()),
		Date:  "2024-01-01",
	})
	require.NoError(t, err)
	sceneID := scene.UUID()

	edit, err := admin.createTestSceneEdit(models.OperationEnumModify,
		&models.SceneEditDetailsInput{
			Urls: []models.URL{{
				URL:    "https://example.org/621-live",
				SiteID: siteID,
			}},
		},
		&models.EditInput{Operation: models.OperationEnumModify, ID: &sceneID})
	require.NoError(t, err)

	q := `
	query FindEditWithLiveSiteURL($id: ID!) {
		findEdit(id: $id) {
			details {
				... on SceneEdit {
					urls { url site { id name } }
				}
			}
		}
	}`

	var resp struct {
		FindEdit struct {
			Details struct {
				URLs []struct {
					URL  string `json:"url"`
					Site struct {
						ID   string `json:"id"`
						Name string `json:"name"`
					} `json:"site"`
				} `json:"urls"`
			} `json:"details"`
		} `json:"findEdit"`
	}

	require.NoError(t, admin.client.Post(q, &resp, client.Var("id", edit.ID.String())))

	// The filter must not have eaten a legitimate URL.
	require.Len(t, resp.FindEdit.Details.URLs, 1,
		"a URL whose site still exists must remain in the edit (#621 regression guard)")
	assert.Equal(t, "https://example.org/621-live", resp.FindEdit.Details.URLs[0].URL)
	assert.Equal(t, siteID.String(), resp.FindEdit.Details.URLs[0].Site.ID)
}
