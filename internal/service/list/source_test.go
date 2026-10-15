package list

import (
	"context"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service/query"
)

// The derived list's decisions that need no database (plan phase 8 slice 8d).
//
// These follow the same rule as list_test.go: everything here is something the SCHEMA cannot express,
// so writing it into a database test would be testing the same thing twice in a way that only ever
// agrees. Three of them are choices made in Go that no constraint covers -- the defaults an input
// built by hand has to spell out, the key a row-less entry is presented with, and the identity a run
// is handed -- and the fourth is the entity type, which the schema's enum permits to be five things.

// A derived entry has NO ROW, so its id is minted rather than read, and the client-visible
// consequences are all in this function.
func TestDerivedItemIDIsStableScopedAndNotAStoredID(t *testing.T) {
	listA := uuid.Must(uuid.NewV4())
	listB := uuid.Must(uuid.NewV4())
	scene := uuid.Must(uuid.NewV4())
	other := uuid.Must(uuid.NewV4())

	// STABLE. A list rendered with fresh uuids on every read is a list whose rows are all new to the
	// framework on each refresh, which loses the reader's selection and defeats any client-side cache.
	assert.Equal(t, derivedItemID(listA, scene), derivedItemID(listA, scene),
		"the same (list, entity) pair must produce the same key on every read")

	// SCOPED TO THE LIST. Two lists showing the same scene are two different rows to a client, and a
	// key minted from the entity alone would collide across lists in any list keyed by item id.
	assert.NotEqual(t, derivedItemID(listA, scene), derivedItemID(listB, scene),
		"two lists presenting the same scene must not present the same key")

	// SCOPED TO THE ENTITY.
	assert.NotEqual(t, derivedItemID(listA, scene), derivedItemID(listA, other))

	// AND DISTINCT FROM A STORED ITEM'S ID IN PRACTICE, which is the reason for v5 over a bare hash:
	// stored item ids are v4, and the version occupies a different nibble of the same field.
	id := derivedItemID(listA, scene)
	assert.EqualValues(t, 5, id.Version(), "a derived key is v5; stored item ids are v4")
	assert.Equal(t, uuid.VariantRFC4122, id.Variant())
}

// The input a derived list runs is built in GO, so gqlgen's schema defaults never run on it and the
// zero values are not "unset": `Page: 0` is a negative offset and an empty sort is no sort at all.
func TestDerivedSearchInputSpellsOutWhatTheSchemaWouldHaveDefaulted(t *testing.T) {
	input := derivedSearchInput("anal pov")

	require.NotNil(t, input.Query, "the search's text is the query -- nil here would run everything")
	assert.Equal(t, "anal pov", *input.Query)

	assert.Equal(t, 1, input.Page, "page 0 is a negative offset, not 'the first page'")
	assert.Equal(t, query.MaxPerPage, input.PerPage,
		"a derived read is bounded by the QUERY layer's ceiling, not by the list cap -- the search's "+
			"total is reported separately so 'the list is short' is never the answer")
	assert.NotEqual(t, maxItems, input.PerPage,
		"the list cap bounds what a list may HOLD; nothing is held here")

	assert.Equal(t, models.SceneSortEnumCreatedAt, input.Sort)
	assert.Equal(t, models.SortDirectionEnumDesc, input.Direction)
}

// THE IDENTITY RULE THE WHOLE SLICE TURNS ON. The filter can depend on who is asking, so a published
// derived list run as its VIEWER would show every reader their own results while claiming to be one
// list. The run context is the only place that is decided.
//
// This covers the function; the CALL SITE is covered by
// source_integration_test.go, where a fake runner records the identity it was handed -- a mutation
// that passed the viewer instead would still pass this test.
func TestDerivedRunContextCarriesTheSearchOwnerNotTheViewer(t *testing.T) {
	owner := uuid.Must(uuid.NewV4())
	viewer := uuid.Must(uuid.NewV4())

	ctx := context.WithValue(context.Background(), auth.ContextUser, &auth.AuthUser{ID: viewer, Name: "the viewer"})

	run := auth.GetCurrentUser(derivedRunContext(ctx, owner, "the owner"))
	require.NotNil(t, run)
	assert.Equal(t, owner, run.ID, "the run is the SEARCH's owner")
	assert.Equal(t, "the owner", run.Name)
	assert.NotEqual(t, viewer, run.ID)

	// And the caller's context is untouched: a list read by a viewer must not leave that viewer
	// looking like somebody else afterwards.
	assert.Equal(t, viewer, auth.GetCurrentUser(ctx).ID)
}

// A saved search is a SCENE query, so every entry a derived list presents is a scene. The list
// service accepts five entity types, so this is a choice rather than a tautology.
func TestADerivedEntryIsAScene(t *testing.T) {
	assert.Equal(t, string(models.ListEntityTypeEnumScene), derivedEntryType)
	assert.True(t, validEntityType(derivedEntryType),
		"an entity type the service itself refuses would make every derived entry unwritable on fork")
}
