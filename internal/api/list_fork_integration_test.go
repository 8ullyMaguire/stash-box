//go:build integration

package api_test

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/models"
)

// Forking a list (plan phase 8 slice 8c: playlist extensions).
//
// A FORK IS A COPY THAT REMEMBERS WHERE IT CAME FROM AND TRACKS NOTHING. Two claims carry that, and
// each gets its own test: the copy is complete and in ORDER (a curated list whose order is lost is not
// the list anybody wanted), and the two lists are independent afterwards (a fork that followed its
// source would change under the forker when a stranger edited theirs).

func listInput(name string) models.ListCreateInput {
	desc := "a curated list"
	return models.ListCreateInput{Name: name, Description: &desc}
}

// seedList creates a list with n entries at DISTINCT positions, so "same order" is a claim the fixture
// can actually fail.
func seedList(t *testing.T, owner *testRunner, name string, n int) (*models.List, []uuid.UUID) {
	t.Helper()
	created, err := owner.resolver.Mutation().ListCreate(owner.ctx, listInput(name))
	require.NoError(t, err)

	items := make([]*models.ListItem, 0, n)
	ids := make([]uuid.UUID, 0, n)
	for range n {
		entityID := uuid.Must(uuid.NewV7())
		item, err := owner.resolver.Mutation().ListAddItem(owner.ctx, models.ListItemInput{
			ListID:     created.ID,
			EntityType: models.ListEntityTypeEnumPerformer,
			EntityID:   entityID,
		})
		require.NoError(t, err)
		items = append(items, item)
		ids = append(ids, entityID)
	}

	// THE DISPLAY ORDER IS THE REVERSE OF THE INSERTION ORDER, and that is the fixture half of "the
	// order survived". Added in a row, every entry inherits the same position and the display order
	// falls back to the row id -- which, for ids generated in sequence, AGREES with insertion order.
	// So the first version of this fixture could not tell a copy that carried positions from one that
	// dropped them, and the mutation that drops them SURVIVED. Making the two orderings disagree is
	// what gives the assertion something to fail on.
	for i, item := range items {
		_, err := owner.resolver.Mutation().ListReorderItem(owner.ctx, item.ID, (n-i)*10)
		require.NoError(t, err)
	}

	display := make([]uuid.UUID, 0, n)
	for i := range ids {
		display = append(display, ids[n-1-i])
	}
	return created, display
}

func listEntityIDs(t *testing.T, r *testRunner, list *models.List) []uuid.UUID {
	t.Helper()
	out := make([]uuid.UUID, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, list.Items[i].EntityID)
	}
	return out
}

// listWriter makes a NEW member who may write lists, so "the fork is theirs and not the source's" is
// about two different people rather than two handles on one seeded account.
func listWriter(t *testing.T) (*testRunner, *models.User) {
	t.Helper()
	// nil input: the helper generates a unique name and email, and the returned user carries the name
	// the assertions compare against -- so the fixture cannot go stale the way a literal would.
	user, err := asAdmin(t).createTestUser(nil, []models.RoleEnum{models.RoleEnumRead, models.RoleEnumModify})
	require.NoError(t, err)
	return createTestRunner(t, user, []models.RoleEnum{models.RoleEnumRead, models.RoleEnumModify}), user
}

func TestForkCopiesTheListInOrderAndUnpublished(t *testing.T) {
	owner, _ := listWriter(t)
	source, seeded := seedList(t, owner, "Source List "+uuid.Must(uuid.NewV7()).String()[:8], 3)

	published, err := owner.resolver.Mutation().ListPublish(owner.ctx, source.ID)
	require.NoError(t, err)
	require.NotNil(t, published.PublishedAt, "fixture: the source must be published")

	// A DIFFERENT member: the fork is theirs, and the source is not.
	forker, forkerUser := listWriter(t)
	fork, err := forker.resolver.Mutation().ListFork(forker.ctx, models.ListForkInput{
		ID:   source.ID,
		Name: "My Fork " + uuid.Must(uuid.NewV7()).String()[:8],
	})
	require.NoError(t, err)

	require.NotNil(t, fork.Owner)
	assert.Equal(t, forkerUser.Name, fork.Owner.Name, "the fork belongs to the forker")
	assert.Nil(t, fork.PublishedAt, "A FORK OF A PUBLISHED LIST IS NOT PUBLISHED: the source owner's publication is theirs")
	require.NotNil(t, fork.ForkedFromID, "and it remembers where it came from")
	assert.Equal(t, source.ID, *fork.ForkedFromID)
	require.NotNil(t, fork.Description, "the description is inherited when the caller gives none")
	assert.Equal(t, "a curated list", *fork.Description)
	assert.Len(t, fork.Items, 3)
	assert.Equal(t, seeded, listEntityIDs(t, forker, fork), "the entries are copied AND in the source's order")
}

// THE TWO LISTS DO NOT TRACK EACH OTHER, in both directions.
func TestAForkIsIndependentOfItsSource(t *testing.T) {
	owner, _ := listWriter(t)
	source, _ := seedList(t, owner, "Independent Source "+uuid.Must(uuid.NewV7()).String()[:8], 2)
	_, err := owner.resolver.Mutation().ListPublish(owner.ctx, source.ID)
	require.NoError(t, err)

	forker, _ := listWriter(t)
	fork, err := forker.resolver.Mutation().ListFork(forker.ctx, models.ListForkInput{
		ID:   source.ID,
		Name: "Independent Fork " + uuid.Must(uuid.NewV7()).String()[:8],
	})
	require.NoError(t, err)

	// A change to the SOURCE does not reach the fork.
	_, err = owner.resolver.Mutation().ListAddItem(owner.ctx, models.ListItemInput{
		ListID:     source.ID,
		EntityType: models.ListEntityTypeEnumPerformer,
		EntityID:   uuid.Must(uuid.NewV7()),
	})
	require.NoError(t, err)

	afterSourceChange, err := forker.resolver.Query().List(forker.ctx, fork.ID)
	require.NoError(t, err)
	assert.Len(t, afterSourceChange.Items, 2, "the source grew; the fork did not")

	// And a change to the FORK does not reach the source.
	_, err = forker.resolver.Mutation().ListRemoveItem(forker.ctx, fork.Items[0].ID)
	require.NoError(t, err)

	afterForkChange, err := owner.resolver.Query().List(owner.ctx, source.ID)
	require.NoError(t, err)
	assert.Len(t, afterForkChange.Items, 3, "the fork shrank; the source kept everything")
}

// A FORK YOU CANNOT SEE IS NOT FOUND, and the refusal is the SAME ONE the read path gives: "no such
// list", not "not yours". Reporting the difference would let a caller enumerate private list ids one
// probe at a time, and a fork route that answered more helpfully than the read route would be the way
// to read a list that cannot be read.
func TestYouCannotForkAListYouCannotSee(t *testing.T) {
	owner, _ := listWriter(t)
	private, _ := seedList(t, owner, "Private Source "+uuid.Must(uuid.NewV7()).String()[:8], 1)

	stranger, _ := listWriter(t)
	_, err := stranger.resolver.Mutation().ListFork(stranger.ctx, models.ListForkInput{
		ID:   private.ID,
		Name: "Stolen " + uuid.Must(uuid.NewV7()).String()[:8],
	})
	require.Error(t, err)
	assert.Equal(t, "no such list", err.Error(), "an invisible list is not found, not refused by name")

	// And the OWNER can fork their own draft, which is the same route working.
	fork, err := owner.resolver.Mutation().ListFork(owner.ctx, models.ListForkInput{
		ID:   private.ID,
		Name: "My Own Fork " + uuid.Must(uuid.NewV7()).String()[:8],
	})
	require.NoError(t, err)
	assert.Len(t, fork.Items, 1)
	assert.Nil(t, fork.PublishedAt)
}

// DELETING A SOURCE LEAVES THE COPY, and the copy forgets it: provenance is not ownership.
func TestDeletingASourceLeavesItsForks(t *testing.T) {
	owner, _ := listWriter(t)
	source, seeded := seedList(t, owner, "Doomed Source "+uuid.Must(uuid.NewV7()).String()[:8], 2)
	_, err := owner.resolver.Mutation().ListPublish(owner.ctx, source.ID)
	require.NoError(t, err)

	forker, _ := listWriter(t)
	fork, err := forker.resolver.Mutation().ListFork(forker.ctx, models.ListForkInput{
		ID:   source.ID,
		Name: "Surviving Fork " + uuid.Must(uuid.NewV7()).String()[:8],
	})
	require.NoError(t, err)

	deleted, err := owner.resolver.Mutation().ListDelete(owner.ctx, source.ID)
	require.NoError(t, err)
	require.True(t, deleted)

	survivor, err := forker.resolver.Query().List(forker.ctx, fork.ID)
	require.NoError(t, err, "the fork survives its source")
	assert.Nil(t, survivor.ForkedFromID, "and forgets it, because a dangling pointer is worse than none")
	assert.Len(t, survivor.Items, 2)
	assert.Equal(t, seeded, listEntityIDs(t, forker, survivor), "its entries were its own rows all along")
}
