//go:build integration

package api_test

import (
	"context"
	"testing"
	"time"

	"github.com/99designs/gqlgen/client"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/models"
)

// Saved searches: a stored query, run through the same path a live one takes, and a notification
// job that says so once.
//
// What is proven HERE and nowhere else:
//
//  1. The stored TEXT is what runs. The test compares `results` against `queryScenes` with the
//     same text, so a saved search that grew its own query path -- or dropped a term it could
//     not lower -- shows up as a disagreement instead of as an empty page that looks like an
//     answer.
//  2. The `results` field's paging ARGUMENTS reach the query. This is the gqlgen trap this file
//     exists for: a field bound to the struct field has its arguments parsed and then ignored,
//     so page 2 silently returns page 1. Asserting "page 2 differs from page 1" is what makes
//     that observable; asserting "it returns scenes" does not.
//  3. The job writes ONE notification per RUN, not one per match, and only for what is new since
//     its cursor -- including the first run, which notifies NOTHING on purpose.
//  4. Ownership is enforced by the query, not by a check after the fact: another user's id
//     returns null, their delete returns false, and the row survives.
//
// Fixtures are the query-language runner's (a unique tag per test), because the package shares
// one database: the stored text has to be narrow enough that other tests' scenes cannot land in
// it, and `results` cannot be scoped by an ID criterion the way a live query can.

type savedSearchTestRunner struct {
	sceneQueryLanguageRunner
}

func createSavedSearchTestRunner(t *testing.T) *savedSearchTestRunner {
	t.Helper()
	return &savedSearchTestRunner{sceneQueryLanguageRunner: *createSceneQueryLanguageRunner(t)}
}

// name is unique per call: two saved searches by one user cannot share a name, and tests share a
// database, so an invented constant would collide with a previous test's row.
func (s *savedSearchTestRunner) name(role string) string {
	return s.generateTagName() + "-saved-" + role
}

type savedSearchOutput struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Query        string     `json:"query"`
	Notify       bool       `json:"notify"`
	LastNotified *time.Time `json:"last_notified"`
}

type sceneQueryOutput struct {
	Count  int              `json:"count"`
	Scenes []sceneOutput    `json:"scenes"`
	Diags  []diagOutputType `json:"diagnostics"`
}

type diagOutputType struct {
	Code    string `json:"code"`
	Term    string `json:"term"`
	Offset  int    `json:"offset"`
	Message string `json:"message"`
}

func (c *graphqlClient) createSavedSearch(input models.SavedSearchInput) (*savedSearchOutput, error) {
	q := `
	mutation CreateSavedSearch($input: SavedSearchInput!) {
		createSavedSearch(input: $input) {
			id
			name
			query
			notify
		}
	}`

	var resp struct {
		CreateSavedSearch savedSearchOutput `json:"createSavedSearch"`
	}
	if err := c.Post(q, &resp, client.Var("input", input)); err != nil {
		return nil, err
	}

	return &resp.CreateSavedSearch, nil
}

func (c *graphqlClient) updateSavedSearch(input models.SavedSearchUpdateInput) (*savedSearchOutput, error) {
	q := `
	mutation UpdateSavedSearch($input: SavedSearchUpdateInput!) {
		updateSavedSearch(input: $input) {
			id
			name
			query
			notify
		}
	}`

	var resp struct {
		UpdateSavedSearch savedSearchOutput `json:"updateSavedSearch"`
	}
	if err := c.Post(q, &resp, client.Var("input", input)); err != nil {
		return nil, err
	}

	return &resp.UpdateSavedSearch, nil
}

func (c *graphqlClient) deleteSavedSearch(id uuid.UUID) (bool, error) {
	q := `
	mutation DeleteSavedSearch($id: ID!) {
		deleteSavedSearch(id: $id)
	}`

	var resp struct {
		DeleteSavedSearch bool `json:"deleteSavedSearch"`
	}
	if err := c.Post(q, &resp, client.Var("id", id)); err != nil {
		return false, err
	}

	return resp.DeleteSavedSearch, nil
}

func (c *graphqlClient) savedSearches() ([]savedSearchOutput, error) {
	q := `
	query {
		savedSearches {
			id
			name
			query
			notify
			last_notified
		}
	}`

	var resp struct {
		SavedSearches []savedSearchOutput `json:"savedSearches"`
	}
	if err := c.Post(q, &resp); err != nil {
		return nil, err
	}

	return resp.SavedSearches, nil
}

func (c *graphqlClient) findSavedSearch(id uuid.UUID) (*savedSearchOutput, error) {
	q := `
	query FindSavedSearch($id: ID!) {
		findSavedSearch(id: $id) {
			id
			name
			query
			notify
		}
	}`

	var resp struct {
		FindSavedSearch *savedSearchOutput `json:"findSavedSearch"`
	}
	if err := c.Post(q, &resp, client.Var("id", id)); err != nil {
		return nil, err
	}

	return resp.FindSavedSearch, nil
}

// savedSearchResults asks for the stored query's results. The arguments are nullable in the
// schema and are passed as such, so the same call can prove that the DEFAULTS work (nil) and
// that an explicit page reaches the query (non-nil).
func (c *graphqlClient) savedSearchResults(id uuid.UUID, page *int, perPage *int) (*sceneQueryOutput, error) {
	q := `
	query SavedSearchResults($id: ID!, $page: Int, $per_page: Int) {
		findSavedSearch(id: $id) {
			results(page: $page, per_page: $per_page) {
				count
				diagnostics {
					code
					term
					offset
					message
				}
				scenes {
					id
				}
			}
		}
	}`

	var resp struct {
		FindSavedSearch *struct {
			Results sceneQueryOutput `json:"results"`
		} `json:"findSavedSearch"`
	}
	if err := c.Post(q, &resp, client.Var("id", id), client.Var("page", page), client.Var("per_page", perPage)); err != nil {
		return nil, err
	}

	if resp.FindSavedSearch == nil {
		return nil, nil
	}

	return &resp.FindSavedSearch.Results, nil
}

// savedSearchNotifications asks for this user's saved-search notifications INCLUDING the union
// payload, which is what exercises the `data` resolver for the new type: an unhandled type there
// returns nil for a non-null field, and the whole list fails rather than one row.
func (c *graphqlClient) savedSearchNotifications() (*savedSearchNotificationList, error) {
	notificationType := models.NotificationEnumSavedSearchMatch
	q := `
	query SavedSearchNotifications($type: NotificationEnum) {
		queryNotifications(input: {page: 1, per_page: 50, type: $type}) {
			count
			notifications {
				created
				read
				data {
					... on SavedSearchMatch {
						count
						saved_search {
							id
							name
						}
					}
				}
			}
		}
	}`

	var resp struct {
		QueryNotifications savedSearchNotificationList `json:"queryNotifications"`
	}
	if err := c.Post(q, &resp, client.Var("type", notificationType)); err != nil {
		return nil, err
	}

	return &resp.QueryNotifications, nil
}

type savedSearchNotificationList struct {
	Count         int                             `json:"count"`
	Notifications []savedSearchNotificationOutput `json:"notifications"`
}

type savedSearchNotificationOutput struct {
	// A string, not a time.Time: the client decodes into these structs verbatim and `Time`
	// arrives as an RFC3339 string. Nothing here asserts on the instant, so parsing it would
	// only add a way for the decode to fail.
	Created string `json:"created"`
	Read    bool   `json:"read"`
	Data    *struct {
		Count       int `json:"count"`
		SavedSearch *struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"saved_search"`
	} `json:"data"`
}

// mustCreate saves a search and fails the test if it could not be.
func (s *savedSearchTestRunner) mustCreate(input models.SavedSearchInput) *savedSearchOutput {
	s.t.Helper()

	savedSearch, err := s.client.createSavedSearch(input)
	require.NoError(s.t, err)
	require.NotNil(s.t, savedSearch)

	return savedSearch
}

func (s *savedSearchTestRunner) mustID(savedSearch *savedSearchOutput) uuid.UUID {
	s.t.Helper()

	id, err := uuid.FromString(savedSearch.ID)
	require.NoError(s.t, err)

	return id
}

func (s *savedSearchTestRunner) sceneIDs(out *sceneQueryOutput) []string {
	ids := make([]string, 0, len(out.Scenes))
	for _, scene := range out.Scenes {
		ids = append(ids, scene.ID)
	}

	return ids
}

// A saved search keeps exactly what it was given, and an update changes exactly what it names.
func TestSavedSearchRoundTrip(t *testing.T) {
	s := createSavedSearchTestRunner(t)

	tag := s.named("roundtrip")
	query := s.tag(tag).Name

	notify := false
	created := s.mustCreate(models.SavedSearchInput{Name: s.name("roundtrip"), Query: query, Notify: &notify})
	assert.Equal(t, query, created.Query, "the stored text is what was typed")
	assert.False(t, created.Notify)

	id := s.mustID(created)

	// The list is this user's, and it contains the row that was just made. Counted first: an
	// absence assertion about a list that was empty for another reason proves nothing.
	listed, err := s.client.savedSearches()
	require.NoError(t, err)
	require.NotEmpty(t, listed, "the list must be non-empty before a membership claim is made")

	found := false
	for _, item := range listed {
		if item.ID == created.ID {
			found = true
		}
	}
	assert.True(t, found, "the saved search that was just created must be in the list")

	byID, err := s.client.findSavedSearch(id)
	require.NoError(t, err)
	require.NotNil(t, byID)
	assert.Equal(t, created.Name, byID.Name)

	renamed := s.name("renamed")
	notify = true
	updated, err := s.client.updateSavedSearch(models.SavedSearchUpdateInput{
		ID:     id,
		Name:   renamed,
		Query:  query,
		Notify: &notify,
	})
	require.NoError(t, err)
	assert.Equal(t, renamed, updated.Name)
	assert.True(t, updated.Notify, "subscribing is part of the update, not a separate mutation")

	deleted, err := s.client.deleteSavedSearch(id)
	require.NoError(t, err)
	assert.True(t, deleted)

	gone, err := s.client.findSavedSearch(id)
	require.NoError(t, err)
	assert.Nil(t, gone)
}

// Another user's saved search is not a row with a flag on it: it is null, and deleting it is
// false, and the owner still has it afterwards.
func TestAnotherUsersSavedSearchIsNotReachable(t *testing.T) {
	s := createSavedSearchTestRunner(t)

	tag := s.named("owned")
	owner := s.mustCreate(models.SavedSearchInput{Name: s.name("owner"), Query: s.tag(tag).Name})
	ownerID := s.mustID(owner)

	otherUser, err := s.createTestUser(nil, []models.RoleEnum{models.RoleEnumRead})
	require.NoError(t, err)
	other := createTestRunner(t, otherUser, []models.RoleEnum{models.RoleEnumRead})

	// The runner that OWNS a search whose name it knows, and a second user who guesses the id.
	seenByOther, err := other.client.findSavedSearch(ownerID)
	require.NoError(t, err)
	assert.Nil(t, seenByOther, "another user's id must be indistinguishable from an id that does not exist")

	deletedByOther, err := other.client.deleteSavedSearch(ownerID)
	require.NoError(t, err)
	assert.False(t, deletedByOther, "a delete that removed nothing must not report success")

	stillThere, err := s.client.findSavedSearch(ownerID)
	require.NoError(t, err)
	require.NotNil(t, stillThere, "the owner's search must survive another user's delete")
	assert.Equal(t, owner.Name, stillThere.Name)

	othersList, err := other.client.savedSearches()
	require.NoError(t, err)
	assert.Empty(t, othersList, "a new user has no saved searches of their own")
}

// The stored text runs through the same path a live query takes, so the two agree.
func TestSavedSearchResultsAgreeWithTheQueryTheyStore(t *testing.T) {
	s := createSavedSearchTestRunner(t)

	tag := s.named("stored")
	first := s.sceneTagged(tag)
	second := s.sceneTagged(tag)
	s.visible(first, second)

	query := s.tag(tag).Name
	saved := s.mustCreate(models.SavedSearchInput{Name: s.name("stored"), Query: query})

	// `queryScenes` with the same text, scoped by id so the shared database cannot dilute the
	// comparison: the point is the two paths agreeing, not the size of the table.
	viaLiveQuery := s.searchInput(models.SceneQueryInput{Query: &query}, []uuid.UUID{first, second})
	require.Len(t, viaLiveQuery, 2, "the fixture must be findable by the live query first")

	results, err := s.client.savedSearchResults(s.mustID(saved), nil, nil)
	require.NoError(t, err)
	require.NotNil(t, results, "`results` must answer with the defaults, not with nothing")

	assert.Equal(t, 2, results.Count)
	assert.Empty(t, results.Diags, "a query that means what it says has nothing to report")

	for _, scene := range results.Scenes {
		id, err := uuid.FromString(scene.ID)
		require.NoError(t, err)
		assert.True(t, viaLiveQuery[id], "a scene the saved search returned must be one the live query returned too")
	}
}

// The paging arguments reach the query. A field bound to the struct would return page 1 twice,
// which is why this asserts that two pages DIFFER rather than that a page is non-empty.
func TestSavedSearchResultsHonourTheirPagingArguments(t *testing.T) {
	s := createSavedSearchTestRunner(t)

	tag := s.named("paged")
	ids := []uuid.UUID{s.sceneTagged(tag), s.sceneTagged(tag), s.sceneTagged(tag)}
	s.visible(ids...)

	saved := s.mustCreate(models.SavedSearchInput{Name: s.name("paged"), Query: s.tag(tag).Name})
	savedID := s.mustID(saved)

	one := 1
	firstPage, err := s.client.savedSearchResults(savedID, &one, &one)
	require.NoError(t, err)
	require.Len(t, firstPage.Scenes, 1)

	two := 2
	secondPage, err := s.client.savedSearchResults(savedID, &two, &one)
	require.NoError(t, err)
	require.Len(t, secondPage.Scenes, 1)

	assert.Equal(t, 3, firstPage.Count, "the count is the whole result, not the page")
	assert.NotEqual(t, firstPage.Scenes[0].ID, secondPage.Scenes[0].ID,
		"page 2 must be the second result: identical pages mean the arguments were parsed and ignored")
}

// A term the filter cannot apply makes the answer empty and reports itself, rather than being
// dropped -- dropping it would return MORE than was asked for and present that as the answer.
func TestASavedSearchReportsATermItCannotApply(t *testing.T) {
	s := createSavedSearchTestRunner(t)

	tag := s.named("applicable")
	scene := s.sceneTagged(tag)
	s.visible(scene)

	// A tag name nobody has. It parses (bare words are tags), so the search saves; what it
	// cannot do is match.
	unknown := s.generateTagName() + "-no-such-tag"
	saved := s.mustCreate(models.SavedSearchInput{Name: s.name("unapplicable"), Query: unknown})

	results, err := s.client.savedSearchResults(s.mustID(saved), nil, nil)
	require.NoError(t, err)

	assert.Empty(t, results.Scenes, "an unapplicable term must not be ignored into a broader answer")
	assert.Equal(t, 0, results.Count)
	require.Len(t, results.Diags, 1, "exactly one diagnostic: the term that could not be applied")
	assert.Equal(t, unknown, results.Diags[0].Term)
	assert.NotEmpty(t, results.Diags[0].Message, "a diagnostic the reader cannot act on is not a diagnostic")
}

// The job notifies ONCE per run, with the newest match as the target, and only about what is
// newer than the last time it looked.
func TestSavedSearchNotificationsOnePerRun(t *testing.T) {
	s := createSavedSearchTestRunner(t)

	tag := s.named("notified")
	// A scene the search matches BEFORE it is subscribed. It must never be announced: the first
	// run sets the cursor and says nothing, or a search saved after a year of watching would
	// announce the year.
	old := s.sceneTagged(tag)
	s.visible(old)

	notify := true
	saved := s.mustCreate(models.SavedSearchInput{Name: s.name("notified"), Query: s.tag(tag).Name, Notify: &notify})
	savedID := s.mustID(saved)

	fac := *dbtest.Factory()

	// First run: a cursor, no notification.
	written, err := fac.SavedSearch().NotifyMatches(context.Background(), fac.Scene())
	require.NoError(t, err)

	first, err := s.client.savedSearchNotifications()
	require.NoError(t, err)
	assert.Zero(t, first.Count, "the first run has nothing to compare against and must announce nothing")

	// Two arrivals after the cursor.
	newer := s.sceneTagged(tag)
	s.sceneTagged(tag)
	s.visible(newer, old)

	written, err = fac.SavedSearch().NotifyMatches(context.Background(), fac.Scene())
	require.NoError(t, err)
	assert.Positive(t, written, "a run that found matches must say so")

	second, err := s.client.savedSearchNotifications()
	require.NoError(t, err)
	require.Equal(t, 1, second.Count, "ONE notification per run, not one per match")

	// The payload renders: the union member resolves, the count is the number of matches, and the
	// search it names is the one that matched.
	payload := second.Notifications[0].Data
	require.NotNil(t, payload, "the notification's data must resolve for the saved-search type")
	assert.Equal(t, 2, payload.Count)
	require.NotNil(t, payload.SavedSearch)
	assert.Equal(t, saved.Name, payload.SavedSearch.Name)

	// A second run over the same state writes nothing: the cursor moved past what it just saw.
	written, err = fac.SavedSearch().NotifyMatches(context.Background(), fac.Scene())
	require.NoError(t, err)
	assert.Zero(t, written, "the same matches must not be announced twice")

	third, err := s.client.savedSearchNotifications()
	require.NoError(t, err)
	assert.Equal(t, 1, third.Count)

	// Deleting the search takes its notifications with it, in the same transaction.
	deleted, err := s.client.deleteSavedSearch(savedID)
	require.NoError(t, err)
	require.True(t, deleted)

	afterDelete, err := s.client.savedSearchNotifications()
	require.NoError(t, err)
	assert.Zero(t, afterDelete.Count, "a notification whose search is gone has nothing to render")
}
