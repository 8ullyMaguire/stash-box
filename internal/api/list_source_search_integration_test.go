//go:build integration

package api_test

import (
	"context"
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/models"
)

// Derived lists over GraphQL, end to end (plan phase 8 slice 8d).
//
// The service is proved by internal/service/list's own suite, including the identity rule with a fake
// runner that can be asked who it was handed. What this file proves is what a CLIENT sees, and the two
// claims that only exist at this layer:
//
//  1. THE LIST IS THE SEARCH'S CURRENT RESULTS, which is the whole feature: a scene created AFTER the
//     list started following the search appears in it, with ZERO list_items rows written. Asserted at
//     the table as well as on the wire, because "the client sees it" and "nothing was stored" are two
//     claims and only the second one is the feature.
//  2. THE CRITERIA ARE NOT PUBLISHED. A published derived list shows its results to everyone and its
//     source search to nobody but its owner -- so the same query, read by two members, must answer
//     different things about `sourceSavedSearch`.
//
// Fixtures are the query-language runner's (a unique tag per test), because the package shares one
// database and the stored text has to be narrow enough that other tests' scenes cannot land in it.

type derivedListOutput struct {
	ID                string
	Name              string
	IsDerived         bool
	ItemCount         int
	SourceSavedSearch *struct {
		ID    string
		Name  string
		Query string
	}
	Items []struct {
		ID         string
		EntityType string
		EntityID   string
		Position   int
	}
}

// derivedFixture makes a tag, a scene carrying it, a member who owns a saved search over that tag's
// name, and a list following it. Returns what the assertions need by name.
type derivedFixture struct {
	admin    *tagOntologyTestRunner
	owner    *testRunner
	tagID    uuid.UUID
	tagName  string
	searchID uuid.UUID
	search   *savedSearchOutput
	listID   uuid.UUID
}

func newDerivedFixture(t *testing.T, publish bool) *derivedFixture {
	t.Helper()

	admin := createTagOntologyTestRunner(t)
	tagID := admin.named("derived")
	tagName := admin.tag(tagID).Name

	owner, _ := listWriter(t)
	search, err := owner.client.createSavedSearch(models.SavedSearchInput{
		Name:  owner.generateTagName() + "-followed",
		Query: tagName,
	})
	require.NoError(t, err)
	searchID, err := uuid.FromString(search.ID)
	require.NoError(t, err)

	created, err := owner.resolver.Mutation().ListCreate(owner.ctx, models.ListCreateInput{
		Name: "derived " + uuid.Must(uuid.NewV4()).String()[:8],
	})
	require.NoError(t, err)

	_, err = owner.resolver.Mutation().ListSetSourceSearch(owner.ctx, models.ListSourceSearchInput{
		ListID:        created.ID,
		SavedSearchID: searchID,
	})
	require.NoError(t, err)

	if publish {
		_, err = owner.resolver.Mutation().ListPublish(owner.ctx, created.ID)
		require.NoError(t, err)
	}

	return &derivedFixture{
		admin: admin, owner: owner, tagID: tagID, tagName: tagName,
		searchID: searchID, search: search, listID: created.ID,
	}
}

// readList asks for a list the way a client does, so the field names, the enum spellings and the
// nullability are all under test -- a value that only exists in Go proves nothing about the wire.
func readList(t *testing.T, r *testRunner, listID uuid.UUID) *derivedListOutput {
	t.Helper()

	q := `
	query OneList($id: ID!) {
		list(id: $id) {
			id
			name
			isDerived
			itemCount
			sourceSavedSearch { id name query }
			items { id entityType entityId position }
		}
	}`

	var resp struct {
		List *derivedListOutput
	}
	r.post(q, &resp, client.Var("id", listID))
	require.NotNil(t, resp.List, "the list resolved to null at the wire")
	return resp.List
}

func derivedEntityIDs(out *derivedListOutput) map[string]bool {
	ids := make(map[string]bool, len(out.Items))
	for i := range out.Items {
		ids[out.Items[i].EntityID] = true
	}
	return ids
}

// storedEntryRows counts the ROWS a list holds. "The client sees the scene" and "nothing was written"
// are different claims, and only the second one is this feature.
func storedEntryRows(t *testing.T, listID uuid.UUID) int {
	t.Helper()
	var n int
	require.NoError(t, dbtest.DB().QueryRow(context.Background(),
		`SELECT count(*) FROM list_items WHERE list_id = $1`, listID).Scan(&n))
	return n
}

// THE FEATURE: membership is the search's CURRENT results.
func TestADerivedListIsTheSearchsCurrentResults(t *testing.T) {
	f := newDerivedFixture(t, false)

	// A scene that matches, created BEFORE the list existed.
	first := f.admin.sceneTagged(f.tagID)

	out := readList(t, f.owner, f.listID)
	assert.True(t, out.IsDerived, "the client branches on this field")
	assert.Equal(t, 1, out.ItemCount)
	require.Len(t, out.Items, 1)
	assert.Equal(t, first.String(), out.Items[0].EntityID)
	assert.Equal(t, "SCENE", out.Items[0].EntityType, "a saved search is a scene query, so its entries are scenes")

	// THE OWNER SEES WHAT THE LIST FOLLOWS, on the wire.
	require.NotNil(t, out.SourceSavedSearch, "the owner's own list must name the search it follows")
	assert.Equal(t, f.search.Name, out.SourceSavedSearch.Name)
	assert.Equal(t, f.tagName, out.SourceSavedSearch.Query)

	// A NEW MATCH, created after the list was already following the search, and NOTHING was written
	// to the list: this is the whole difference between a derived list and a fork.
	second := f.admin.sceneTagged(f.tagID)

	out = readList(t, f.owner, f.listID)
	assert.Equal(t, 2, out.ItemCount, "itemCount must be the SEARCH's total, not a stored count")
	assert.Equal(t, map[string]bool{first.String(): true, second.String(): true}, derivedEntityIDs(out),
		"the search's current results, with a scene that appeared after the list was made")

	assert.Zero(t, storedEntryRows(t, f.listID),
		"a derived list STORES NOTHING -- the entries are computed on every read")
}

// THE CRITERIA ARE NOT PUBLISHED. The results are; the question that produced them is the owner's.
func TestADerivedListsSourceIsVisibleOnlyToItsOwner(t *testing.T) {
	f := newDerivedFixture(t, true)
	scene := f.admin.sceneTagged(f.tagID)

	ownerView := readList(t, f.owner, f.listID)
	require.NotNil(t, ownerView.SourceSavedSearch)

	strangerView := readList(t, asRead(t), f.listID)
	assert.True(t, strangerView.IsDerived, "a stranger can tell it is a list that tracks a search")
	assert.Nil(t, strangerView.SourceSavedSearch,
		"and cannot tell WHICH search -- the filters are not part of what publishing a list publishes")
	assert.Equal(t, ownerView.ItemCount, strangerView.ItemCount, "but the results are")
	assert.Equal(t, derivedEntityIDs(ownerView), derivedEntityIDs(strangerView))
	assert.True(t, derivedEntityIDs(strangerView)[scene.String()])
}

// The refusals, as a client receives them: by name, and never as a silently-refused write.
func TestDerivedListMutationsRefuseByName(t *testing.T) {
	f := newDerivedFixture(t, false)

	// ADDING an entry to a derived list: the list is the argument, so the reason can be named.
	_, err := f.owner.resolver.Mutation().ListAddItem(f.owner.ctx, models.ListItemInput{
		ListID:     f.listID,
		EntityType: models.ListEntityTypeEnumScene,
		EntityID:   uuid.Must(uuid.NewV4()),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "entries come from a saved search")

	// BECOMING DERIVED WHEN IT HOLDS ENTRIES: refused, and the entries are the caller's to remove.
	//
	// The search this member follows is their OWN, and that is the fixture's load-bearing detail:
	// the ownership of the search is checked BEFORE the list's entries (source.go), so calling with
	// somebody else's search here would prove the wrong refusal for the wrong reason.
	mine, _ := listWriter(t)
	mineSearch, err := mine.client.createSavedSearch(models.SavedSearchInput{
		Name:  mine.generateTagName() + "-followed",
		Query: f.tagName,
	})
	require.NoError(t, err)
	mineSearchID, err := uuid.FromString(mineSearch.ID)
	require.NoError(t, err)

	handmade, err := mine.resolver.Mutation().ListCreate(mine.ctx, models.ListCreateInput{
		Name: "handmade " + uuid.Must(uuid.NewV4()).String()[:8],
	})
	require.NoError(t, err)
	_, err = mine.resolver.Mutation().ListAddItem(mine.ctx, models.ListItemInput{
		ListID:     handmade.ID,
		EntityType: models.ListEntityTypeEnumScene,
		EntityID:   uuid.Must(uuid.NewV4()),
	})
	require.NoError(t, err)

	_, err = mine.resolver.Mutation().ListSetSourceSearch(mine.ctx, models.ListSourceSearchInput{
		ListID:        handmade.ID,
		SavedSearchID: mineSearchID,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has entries of its own")

	// SOMEBODY ELSE'S DRAFT LIST ANSWERS "no such list", and that is the READ PATH'S OWN MESSAGE
	// rather than a permission error: a distinct answer would confirm which guessed ids are real. The
	// assertion is therefore a PAIR -- a stranger's draft and a list that does not exist must be
	// indistinguishable -- because "it errored" is also true of an error returned for the wrong reason.
	other, _ := listWriter(t)
	_, err = other.resolver.Mutation().ListSetSourceSearch(other.ctx, models.ListSourceSearchInput{
		ListID:        handmade.ID,
		SavedSearchID: mineSearchID,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no such list")

	absent := uuid.Must(uuid.NewV4())
	_, err = other.resolver.Mutation().ListSetSourceSearch(other.ctx, models.ListSourceSearchInput{
		ListID:        absent,
		SavedSearchID: mineSearchID,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no such list")

	// SOMEBODY ELSE'S SEARCH on a list that IS theirs: one message, naming no search, the same one an
	// id that does not exist gets -- so the endpoint cannot be asked which searches are real.
	empty, err := other.resolver.Mutation().ListCreate(other.ctx, models.ListCreateInput{
		Name: "empty " + uuid.Must(uuid.NewV4()).String()[:8],
	})
	require.NoError(t, err)
	_, err = other.resolver.Mutation().ListSetSourceSearch(other.ctx, models.ListSourceSearchInput{
		ListID:        empty.ID,
		SavedSearchID: f.searchID,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no saved search with that id")
}

// STOPPING IS THE CALLER'S DECISION AND IT MATERIALISES NOTHING.
func TestClearingADerivedListsSourceLeavesAnOrdinaryEmptyList(t *testing.T) {
	f := newDerivedFixture(t, false)
	scene := f.admin.sceneTagged(f.tagID)

	before := readList(t, f.owner, f.listID)
	require.True(t, before.IsDerived)
	require.Len(t, before.Items, 1)

	cleared, err := f.owner.resolver.Mutation().ListClearSourceSearch(f.owner.ctx, f.listID)
	require.NoError(t, err)
	assert.False(t, cleared.IsDerived, "the payload must describe the list the mutation produced")

	after := readList(t, f.owner, f.listID)
	assert.False(t, after.IsDerived)
	assert.Nil(t, after.SourceSavedSearch)
	assert.Zero(t, after.ItemCount, "an ordinary empty list")
	assert.Empty(t, after.Items)
	assert.Zero(t, storedEntryRows(t, f.listID), "the former entries are NOT copied into it")

	// The fixture proves there WAS something to lose: the search still matches it.
	assert.NotEmpty(t, scene)
}
