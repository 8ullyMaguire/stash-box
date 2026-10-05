//go:build integration

package api_test

import (
	"fmt"
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/queries"
)

// Tag category hierarchy over GraphQL (growth item 24).
//
// scripts/verify-106.sh proves the cycle guard at the database level. This proves the
// hierarchy is REACHABLE, which is a different claim: a guard that rejects every write
// and a hierarchy that reads back empty both pass a schema-migration test and both fail
// the feature.
//
// The chain is grandchild -> child -> parent -> grandparent, so depth ordering and
// ancestor ordering are both exercised at more than one level.

const categoryTreeQuery = `
	query($id: ID!) {
		findTagCategory(id: $id) {
			id
			name
			parent { id name }
			children { id name }
			descendants { depth category { id name } }
			ancestors  { depth category { id name } }
		}
	}
`

type treeNodeRow struct {
	Depth    int
	Category struct {
		ID   string
		Name string
	}
}

type categoryTreeRow struct {
	ID          string
	Name        string
	Parent      *struct{ ID, Name string }
	Children    []struct{ ID, Name string }
	Descendants []treeNodeRow
	Ancestors   []treeNodeRow
}

type categoryTreeResponse struct {
	FindTagCategory *categoryTreeRow
}

// newCategory creates a category directly, bypassing the GraphQL mutation.
//
// Direct because these tests are about the HIERARCHY, and going through a mutation for
// fixture setup would make every case in this file also a validation test. The mutation
// itself is covered separately, by TestTagCategoryCycleIsRefusedOverGraphQL.
func newCategory(t *testing.T, name string) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	_, err := q().CreateTagCategory(t.Context(), queries.CreateTagCategoryParams{
		ID:    id,
		Name:  name,
		Group: "GENERAL",
	})
	require.NoError(t, err, "creating tag category %q", name)
	return id
}

// setCategoryParent links a category under a parent through the same query the mutation
// uses, so the tree under test is built by the shipping code path.
func setCategoryParent(t *testing.T, child, parent uuid.UUID) {
	t.Helper()
	_, err := q().SetTagCategoryParent(t.Context(), queries.SetTagCategoryParentParams{
		ID:       child,
		ParentID: uuid.NullUUID{UUID: parent, Valid: true},
	})
	require.NoError(t, err, "making a category a child")
}

func askCategoryTree(t *testing.T, r *testRunner, id uuid.UUID) *categoryTreeRow {
	t.Helper()
	var resp categoryTreeResponse
	r.client.MustPost(categoryTreeQuery, &resp, client.Var("id", id))
	return resp.FindTagCategory
}

// nodeNames flattens a tree walk to "Name@depth", which is what makes the ORDERING
// assertions readable. Asserting on raw structs would check the values but say nothing
// about whether the walk came back nearest-first or root-first.
func nodeNames(nodes []treeNodeRow) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, fmt.Sprintf("%s@%d", n.Category.Name, n.Depth))
	}
	return out
}

// The whole chain reads correctly in both directions.
//
// One test rather than one per field, because the thing being proved is that the chain is
// connected: a field that resolves but is never queried behaves identically to one that
// works.
func TestTagCategoryHierarchyReadsInBothDirections(t *testing.T) {
	grandparent := newCategory(t, "Cat Grandparent")
	parent := newCategory(t, "Cat Parent")
	child := newCategory(t, "Cat Child")
	grandchild := newCategory(t, "Cat Grandchild")

	setCategoryParent(t, parent, grandparent)
	setCategoryParent(t, child, parent)
	setCategoryParent(t, grandchild, child)

	// Walk UP from the grandchild: nearest parent first, root last.
	// A separate runner for MODERATE: @hasRole(MODERATE) is a real gate, and asRead
	// would fail the query outright rather than return empty -- so a mistake here reads
	// as "no hierarchy", not as "not authorized".
	got := askCategoryTree(t, asRead(t), grandchild)
	require.NotNil(t, got)

	assert.Equal(t, []string{"Cat Child@0", "Cat Parent@1", "Cat Grandparent@2"},
		nodeNames(got.Ancestors),
		"ancestors must come back NEAREST FIRST so a breadcrumb can read them in reverse")

	require.NotNil(t, got.Parent, "a nested category must resolve its parent")
	assert.Equal(t, "Cat Child", got.Parent.Name)
	assert.Empty(t, got.Children, "the deepest category has no children, and that is not an error")

	// Walk DOWN from the grandparent: nearest first, deepest last.
	top := askCategoryTree(t, asRead(t), grandparent)
	require.NotNil(t, top)
	assert.Nil(t, top.Parent, "a top-level category has no parent")
	assert.Equal(t, []string{"Cat Parent"}, namesOf(top.Children), "direct children only")
	assert.Equal(t, []string{"Cat Parent@0", "Cat Child@1", "Cat Grandchild@2"},
		nodeNames(top.Descendants),
		"descendants must come back NEAREST FIRST with correct depth at every level")
}

// DEPTH IS THE POINT of the tree node type: without it a client cannot tell a child from
// a grandchild. Asserted separately because a wrong depth is a plausible-looking answer --
// every name is right, only the distances are wrong.
func TestTagCategoryDepthIsCorrectAtEveryLevel(t *testing.T) {
	a := newCategory(t, "Depth A")
	b := newCategory(t, "Depth B")
	c := newCategory(t, "Depth C")
	d := newCategory(t, "Depth D")

	setCategoryParent(t, b, a)
	setCategoryParent(t, c, b)
	setCategoryParent(t, d, c)

	top := askCategoryTree(t, asRead(t), a)
	require.NotNil(t, top)
	require.Len(t, top.Descendants, 3)

	// Depth must be the DISTANCE, not a running counter.
	assert.Equal(t, []string{"Depth B@0", "Depth C@1", "Depth D@2"}, nodeNames(top.Descendants))

	// And from the middle of the chain, depth restarts at 0 relative to THAT category.
	mid := askCategoryTree(t, asRead(t), b)
	require.NotNil(t, mid)
	assert.Equal(t, []string{"Depth C@0", "Depth D@1"}, nodeNames(mid.Descendants),
		"depth is relative to the category asked about, not to the root")
}

// A cycle is refused by the database, so the GraphQL layer must surface that as an ERROR
// rather than silently accepting the write. Silently accepting would leave a corrupt tree
// that only fails later, in an unrelated walk.
func TestTagCategoryCycleIsRefusedOverGraphQL(t *testing.T) {
	admin := asAdmin(t)

	a := newCategory(t, "Cycle A")
	b := newCategory(t, "Cycle B")

	setCategoryParent(t, b, a) // the legal move, as a control: this one must succeed

	// Now try to make A a child of B, closing A -> B -> A.
	var out struct {
		SetTagCategoryParent struct {
			ID string
		}
	}
	err := admin.client.Post(
		`mutation($input: TagCategoryParentInput!) { tagCategorySetParent(input: $input) { id } }`,
		&out,
		client.Var("input", map[string]any{"id": a.String(), "parentId": b.String()}))
	// An ERROR IS THE CORRECT OUTCOME. I first wrote require.NoError here, on the theory
	// that the mutation would succeed and the tree would be intact -- and it failed with
	// the trigger's own message, which is exactly right. The test was backwards.
	require.Error(t, err, "closing a cycle must be refused, not accepted")
	assert.Contains(t, err.Error(), "cycle",
		"the error must SAY it is a cycle, so a caller relinking a deep tree knows why")

	// NO WALK IS QUERIED HERE, and that is deliberate. I checked by mutation: with the
	// recursive check removed, this test does not FAIL -- it HANGS, because the accepted
	// cycle makes the recursive tree walk non-terminating. A walk query added after this
	// line would turn a passing test into an unbounded hang the moment the guard
	// regressed. A hang is excellent evidence and a terrible gate.
	//
	// So the state check is a plain row read: it proves the refused write changed
	// nothing, which is the property that matters, and it terminates either way.
	assert.False(t, categoryHasParent(t, a), "A must not have become a child of its own descendant")
}

// categoryHasParent reads the raw row rather than walking the tree.
//
// A plain read for a plain claim: the question is whether the refused write took effect,
// and that is a property of one column. Using the tree walk here would make the assertion
// depend on the very recursion whose guard is under test.
func categoryHasParent(t *testing.T, id uuid.UUID) bool {
	t.Helper()
	var has bool
	err := testutil.DB().QueryRow(t.Context(),
		`SELECT parent_id IS NOT NULL FROM tag_categories WHERE id = $1`, id).Scan(&has)
	require.NoError(t, err)
	return has
}

func namesOf(cs []struct{ ID, Name string }) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Name)
	}
	return out
}
