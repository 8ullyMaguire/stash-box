//go:build integration

package list_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/list"
)

// Derived lists against a real database (plan phase 8 slice 8d).
//
// The pure tests in source_test.go prove the defaults, the presentation key and the run-context
// function. What can ONLY be proved here is the thing the slice turns on: that the run is handed the
// SEARCH'S OWNER and not the VIEWER, and that the refusals refuse without writing. Both need a runner
// that can be asked who it was given, which is why this file exists at this layer -- the api-level
// harness always wires the real scene service, and a real scene service cannot be asked that question.
//
// TestMain brings up the test database.
func TestMain(m *testing.M) {
	testutil.TestWithDatabase(m, nil)
}

func q() *queries.Queries {
	return queries.New(testutil.DB())
}

// runInTxn is the transaction runner the factory builds, spelled out here because the factory's own
// is unexported and this package is the only place that constructs a list service by hand.
func runInTxn(fn func(*queries.Queries) error) error {
	ctx := context.Background()
	tx, err := testutil.DB().Begin(ctx)
	if err != nil {
		return err
	}
	if err := fn(queries.New(testutil.DB()).WithTx(tx)); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}

func newService(runner list.SearchRunner) *list.Service {
	return list.NewService(q(), runInTxn, runner)
}

// fakeRunner stands in for the scene service and RECORDS WHO IT WAS ASKED TO BE, which is the whole
// reason this test exists. It also records the input it was handed, so "the derived read goes through
// the query layer's own path with the search's text" is a claim rather than a comment.
type fakeRunner struct {
	scenes []models.Scene
	total  int

	runs  []models.SceneQueryInput
	users []*auth.AuthUser
}

func (f *fakeRunner) record(ctx context.Context, input models.SceneQueryInput) {
	f.runs = append(f.runs, input)
	f.users = append(f.users, auth.GetCurrentUser(ctx))
}

func (f *fakeRunner) Query(ctx context.Context, input models.SceneQueryInput) ([]models.Scene, error) {
	f.record(ctx, input)
	return f.scenes, nil
}

func (f *fakeRunner) QueryCount(ctx context.Context, input models.SceneQueryInput) (int, error) {
	f.record(ctx, input)
	return f.total, nil
}

// scene mints a scene the way the real query would return one: an id and a creation time.
func scene() models.Scene {
	return models.Scene{ID: uuid.Must(uuid.NewV4()), CreatedAt: time.Now().Add(-time.Hour)}
}

func createUser(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	_, err := q().CreateUser(t.Context(), queries.CreateUserParams{
		ID:           id,
		Name:         "Derived List User " + id.String(),
		PasswordHash: "not-a-real-hash-this-test-never-logs-in",
		Email:        id.String() + "@derived.invalid",
	})
	require.NoError(t, err, "creating the member under test")
	return id
}

func createSearch(t *testing.T, ownerID uuid.UUID, text string) uuid.UUID {
	t.Helper()
	row, err := q().CreateSavedSearch(t.Context(), queries.CreateSavedSearchParams{
		UserID: ownerID,
		Name:   "derived-" + uuid.Must(uuid.NewV4()).String()[:8],
		Query:  text,
	})
	require.NoError(t, err, "creating the saved search under test")
	return row.ID
}

// derivedList creates a PUBLISHED list following a search, so a viewer who is not the owner can read
// it -- which is the case the identity rule is about.
func derivedList(t *testing.T, svc *list.Service, ownerID uuid.UUID, searchID uuid.UUID) uuid.UUID {
	t.Helper()
	created, err := svc.Create(t.Context(), ownerID, "derived-"+uuid.Must(uuid.NewV4()).String()[:8], "")
	require.NoError(t, err)
	_, err = svc.Publish(t.Context(), created.ID, ownerID)
	require.NoError(t, err)
	require.NoError(t, svc.SetSourceSearch(t.Context(), created.ID, ownerID, searchID))
	return created.ID
}

// storedItemCount counts the ROWS a list holds, which is the only way to tell "refused to write" from
// "wrote and hid it" -- a service-level assertion cannot distinguish the two.
func storedItemCount(t *testing.T, listID uuid.UUID) int {
	t.Helper()
	var n int
	require.NoError(t, testutil.DB().QueryRow(t.Context(),
		`SELECT count(*) FROM list_items WHERE list_id = $1`, listID).Scan(&n))
	return n
}

// THE HEADLINE: the search runs as the SEARCH'S OWNER, never as the viewer.
//
// A published derived list read by a stranger must show the owner's results. Running it as the reader
// would show every reader their OWN results while the list claims to be one list -- and the failure is
// invisible in the output, because both answers are "a plausible list of scenes".
func TestADerivedListRunsTheSearchAsItsOwnerAndNotAsTheViewer(t *testing.T) {
	owner := createUser(t)
	viewer := createUser(t)
	searchID := createSearch(t, owner, "anal pov")

	matched := scene()
	runner := &fakeRunner{scenes: []models.Scene{matched}, total: 4312}
	svc := newService(runner)
	listID := derivedList(t, svc, owner, searchID)

	items, err := svc.Items(t.Context(), listID, viewer)
	require.NoError(t, err)
	require.Len(t, items, 1, "the search's results ARE the entries")

	// The entry is the scene the runner returned, presented as a scene.
	assert.Equal(t, matched.ID, items[0].EntityID.UUID)
	require.NotNil(t, items[0].EntityType)
	assert.Equal(t, list.EntityScene, *items[0].EntityType)
	assert.Equal(t, listID, items[0].ListID)

	// WHO THE RUN WAS HANDED. This is the claim the slice exists for.
	require.Len(t, runner.users, 1)
	require.NotNil(t, runner.users[0], "the run must happen as somebody, and it is not nobody")
	assert.Equal(t, owner, runner.users[0].ID, "THE SEARCH RUNS AS ITS OWNER")
	assert.NotEqual(t, viewer, runner.users[0].ID, "and never as the reader of a published list")

	// AND IT IS THE STORED TEXT, through the query layer's own input.
	require.NotNil(t, runner.runs[0].Query)
	assert.Equal(t, "anal pov", *runner.runs[0].Query)
	assert.Equal(t, 1, runner.runs[0].Page)

	// itemCount IS THE SEARCH'S TOTAL, not the length of the page the reader is shown: a client
	// rendering "100 of 4,312" needs both numbers, and deriving the second from the first would cap
	// the displayed total at the page ceiling.
	count, err := svc.CountItems(t.Context(), listID)
	require.NoError(t, err)
	assert.EqualValues(t, 4312, count)
	assert.Greater(t, count, int64(len(items)), "the fixture's total must differ from the page length")

	require.Len(t, runner.users, 2)
	assert.Equal(t, owner, runner.users[1].ID, "the count runs as the owner too")
}

// A DERIVED LIST'S ENTRIES ARE NOT ITS OWN TO EDIT. Each refusal is by name, and none of them writes
// anything: the next read recomputes membership, so a write that appeared to work and then vanished on
// reload is the worst of the possible answers.
func TestADerivedListsEntriesCannotBeEditedAndNothingIsWritten(t *testing.T) {
	owner := createUser(t)
	searchID := createSearch(t, owner, "anal")

	runner := &fakeRunner{scenes: []models.Scene{scene()}, total: 1}
	svc := newService(runner)
	listID := derivedList(t, svc, owner, searchID)

	// ADDING names the reason, because the list is the argument.
	_, err := svc.AddItem(t.Context(), listID, uuid.Must(uuid.NewV4()), owner, list.EntityScene, 10)
	require.Error(t, err, "an inserted entry would be gone on the next read")
	assert.ErrorIs(t, err, list.ErrDerivedList)

	// REMOVING AND MOVING CANNOT NAME IT, and that is a property of the id rather than an oversight:
	// a derived entry has no row, so the id a client holds for one -- minted from (list, entity) --
	// resolves to nothing, and these two paths answer "no such entry". The refusal IS the absence.
	minted := uuid.Must(uuid.NewV4())
	err = svc.RemoveItem(t.Context(), minted, owner)
	require.Error(t, err)
	assert.ErrorIs(t, err, list.ErrNotFound)

	err = svc.ReorderItem(t.Context(), minted, owner, 20)
	require.Error(t, err)
	assert.ErrorIs(t, err, list.ErrNotFound)

	// NOTHING WRITTEN, asserted at the TABLE: three refusals and no rows.
	assert.Equal(t, 0, storedItemCount(t, listID), "a refusal must not leave a row behind")

	// And the list still shows what the search says -- a refusal that emptied the list would be a
	// different bug wearing the same error.
	items, err := svc.Items(t.Context(), listID, owner)
	require.NoError(t, err)
	assert.Len(t, items, 1)
}

// The guard behind the minted id, which the write paths cannot reach and which exists for a stored row
// on a derived list -- a state only a future writer could produce. `testutil.DB()` exists for exactly
// this: a claim about a state the service cannot express.
//
// IT IS NOT AN ACADEMIC STATE: the read path IGNORES stored rows on a derived list, so without the
// guard a row would be removable and the removal would vanish on reload -- "it looked like it worked".
func TestADerivedListRefusesEditsToARowThatTheServiceCouldNotHaveWritten(t *testing.T) {
	owner := createUser(t)
	searchID := createSearch(t, owner, "anal")

	runner := &fakeRunner{scenes: []models.Scene{scene()}, total: 1}
	svc := newService(runner)
	listID := derivedList(t, svc, owner, searchID)

	entityID := uuid.Must(uuid.NewV4())
	var rowID uuid.UUID
	require.NoError(t, testutil.DB().QueryRow(t.Context(),
		`INSERT INTO list_items (list_id, entity_type, entity_id, position)
		 VALUES ($1, $2, $3, 10) RETURNING id`, listID, list.EntityScene, entityID).Scan(&rowID),
		"fixture: a stored row on a derived list")
	require.Equal(t, 1, storedItemCount(t, listID))

	err := svc.RemoveItem(t.Context(), rowID, owner)
	require.Error(t, err)
	assert.ErrorIs(t, err, list.ErrDerivedList)
	assert.Equal(t, 1, storedItemCount(t, listID), "the row must survive the refusal")

	err = svc.ReorderItem(t.Context(), rowID, owner, 40)
	require.Error(t, err)
	assert.ErrorIs(t, err, list.ErrDerivedList)
}

// BECOMING DERIVED WOULD DISCARD A LIST'S ENTRIES, so a list that holds any is refused -- and the
// refusal is not "discard them and proceed".
func TestAListThatHoldsEntriesCannotBecomeDerived(t *testing.T) {
	owner := createUser(t)
	searchID := createSearch(t, owner, "anal")

	runner := &fakeRunner{scenes: []models.Scene{scene()}, total: 1}
	svc := newService(runner)

	created, err := svc.Create(t.Context(), owner, "handmade-"+uuid.Must(uuid.NewV4()).String()[:8], "curated by hand")
	require.NoError(t, err)

	item, err := svc.AddItem(t.Context(), created.ID, uuid.Must(uuid.NewV4()), owner, list.EntityPerformer, 10)
	require.NoError(t, err)

	err = svc.SetSourceSearch(t.Context(), created.ID, owner, searchID)
	require.Error(t, err, "becoming derived would discard the entry")
	assert.ErrorIs(t, err, list.ErrListHasItems)

	// NOT DERIVED, asserted by reading the row back rather than by the error alone: a partial write
	// that set the column and then reported the error is exactly the failure this catches.
	sourceID, err := svc.SourceSearchID(t.Context(), created.ID)
	require.NoError(t, err)
	assert.Nil(t, sourceID, "the refusal must not have written the source")
	assert.Equal(t, 1, storedItemCount(t, created.ID))

	// Emptying the list is the caller's decision, and once they have made it the same call succeeds.
	require.NoError(t, svc.RemoveItem(t.Context(), item.ID, owner))
	require.NoError(t, svc.SetSourceSearch(t.Context(), created.ID, owner, searchID))

	sourceID, err = svc.SourceSearchID(t.Context(), created.ID)
	require.NoError(t, err)
	require.NotNil(t, sourceID)
	assert.Equal(t, searchID, *sourceID)

	items, err := svc.Items(t.Context(), created.ID, owner)
	require.NoError(t, err)
	assert.Len(t, items, 1, "and its entries are now the search's, not its own")
}

// A LIST CANNOT PUBLISH SOMEBODY ELSE'S CRITERIA. The search is resolved through the owner-scoped read,
// so a stranger's id and an id that does not exist answer the SAME thing -- one answer for both, for
// the reason the read path gives one answer: reporting the difference is an oracle.
func TestTheSearchAListFollowsMustBeTheCallers(t *testing.T) {
	owner := createUser(t)
	other := createUser(t)
	theirs := createSearch(t, other, "their standing question")

	svc := newService(&fakeRunner{})
	created, err := svc.Create(t.Context(), owner, "mine-"+uuid.Must(uuid.NewV4()).String()[:8], "")
	require.NoError(t, err)

	err = svc.SetSourceSearch(t.Context(), created.ID, owner, theirs)
	require.Error(t, err)
	assert.ErrorIs(t, err, list.ErrNoSuchSearch)

	absent := uuid.Must(uuid.NewV4())
	err = svc.SetSourceSearch(t.Context(), created.ID, owner, absent)
	require.Error(t, err)
	assert.ErrorIs(t, err, list.ErrNoSuchSearch, "an id that does not exist answers the same as somebody else's")

	sourceID, err := svc.SourceSearchID(t.Context(), created.ID)
	require.NoError(t, err)
	assert.Nil(t, sourceID, "neither refusal wrote anything")
}

// STOPPING IS THE CALLER'S DECISION AND IT MATERIALISES NOTHING. A snapshot they did not ask for would
// leave them unable to tell which of their lists track anything.
func TestClearingTheSourceLeavesAnOrdinaryEmptyListAndCopiesNothing(t *testing.T) {
	owner := createUser(t)
	searchID := createSearch(t, owner, "anal")

	runner := &fakeRunner{scenes: []models.Scene{scene(), scene()}, total: 2}
	svc := newService(runner)
	listID := derivedList(t, svc, owner, searchID)

	items, err := svc.Items(t.Context(), listID, owner)
	require.NoError(t, err)
	require.Len(t, items, 2)

	require.NoError(t, svc.ClearSourceSearch(t.Context(), listID, owner))

	sourceID, err := svc.SourceSearchID(t.Context(), listID)
	require.NoError(t, err)
	assert.Nil(t, sourceID)

	// Empty AND ORDINARY: the entries are not copied in, which is asserted at the table because the
	// read path reports the same empty list either way.
	assert.Equal(t, 0, storedItemCount(t, listID), "stopping must NOT materialise the search's results")

	after, err := svc.Items(t.Context(), listID, owner)
	require.NoError(t, err)
	assert.Empty(t, after)

	// The search itself is untouched: following it again shows its results.
	require.NoError(t, svc.SetSourceSearch(t.Context(), listID, owner, searchID))
	again, err := svc.Items(t.Context(), listID, owner)
	require.NoError(t, err)
	assert.Len(t, again, 2)
}

// THE SCHEMA'S DECISION, which is not visible through the service: deleting a search a list follows is
// RESTRICTed, so a source cannot vanish and leave a list that looks like an ordinary empty one.
func TestDeletingASearchAListFollowsIsRefused(t *testing.T) {
	owner := createUser(t)
	searchID := createSearch(t, owner, "anal")

	svc := newService(&fakeRunner{scenes: []models.Scene{scene()}, total: 1})
	listID := derivedList(t, svc, owner, searchID)

	_, err := q().DeleteSavedSearch(t.Context(), queries.DeleteSavedSearchParams{ID: searchID, UserID: owner})
	require.Error(t, err, "a list follows it, so the search may not simply disappear")

	// The owner's way out is to stop following it, and then the delete is theirs to make.
	require.NoError(t, svc.ClearSourceSearch(t.Context(), listID, owner))
	_, err = q().DeleteSavedSearch(t.Context(), queries.DeleteSavedSearchParams{ID: searchID, UserID: owner})
	require.NoError(t, err)
}

// A FORK OF A DERIVED LIST TAKES ITS CURRENT ENTRIES AND TRACKS NOTHING -- written as rows, so the fork
// is independent the moment it exists. That is what keeps 8c and 8d from being the same feature.
func TestForkingADerivedListTakesItsCurrentEntriesAndTracksNothing(t *testing.T) {
	owner := createUser(t)
	searchID := createSearch(t, owner, "anal")

	first, second := scene(), scene()
	runner := &fakeRunner{scenes: []models.Scene{first, second}, total: 2}
	svc := newService(runner)
	listID := derivedList(t, svc, owner, searchID)

	fork, err := svc.Fork(t.Context(), listID, owner, "my copy-"+uuid.Must(uuid.NewV4()).String()[:8], "")
	require.NoError(t, err)

	assert.False(t, fork.SourceSavedSearchID.Valid, "the fork follows nothing")
	require.NotNil(t, fork.ForkedFromID)
	assert.Equal(t, listID, fork.ForkedFromID.UUID)

	// The entries were WRITTEN, which is the only difference that matters between a fork and a list
	// that follows: rows exist, so the fork can be edited.
	assert.Equal(t, 2, storedItemCount(t, fork.ID))

	copied, err := svc.Items(t.Context(), fork.ID, owner)
	require.NoError(t, err)
	require.Len(t, copied, 2)
	assert.Equal(t, first.ID, copied[0].EntityID.UUID, "the entries arrive in the search's order")
	assert.Equal(t, second.ID, copied[1].EntityID.UUID)

	// AND THE TWO ARE INDEPENDENT IN BOTH DIRECTIONS: the search moves on and the fork does not.
	third := scene()
	runner.scenes = []models.Scene{third}

	sourceNow, err := svc.Items(t.Context(), listID, owner)
	require.NoError(t, err)
	require.Len(t, sourceNow, 1)
	assert.Equal(t, third.ID, sourceNow[0].EntityID.UUID, "the derived list follows the search")

	forkNow, err := svc.Items(t.Context(), fork.ID, owner)
	require.NoError(t, err)
	require.Len(t, forkNow, 2, "the fork tracks nothing")
	assert.Equal(t, first.ID, forkNow[0].EntityID.UUID)
}
