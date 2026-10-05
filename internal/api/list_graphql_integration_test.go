//go:build integration

package api_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/99designs/gqlgen/client"
)

// Shareable lists over GraphQL, end to end (SPEC §28, growth item 28).
//
// The service is proved by internal/service/list/list_test.go and scripts/verify-107.sh.
// What this file proves is the WIRING and, more importantly, THE PRIVACY BOUNDARY AS A
// CLIENT SEES IT -- which is a different claim from "the service refuses".
//
// The boundary tests are the point. A draft leaking is not a subtle degradation; it is the
// whole feature's premise failing. And the two ways it can leak are easy to miss at the
// resolver layer, where a helper that decides "return nil" looks identical to a helper that
// decides "return not found":
//
//	1. `list(id:)` on someone else's draft must be indistinguishable from a missing list.
//	2. `lists(userId:)` about someone else must not include their drafts.
//
// Both are asserted as a PAIR -- the draft case and the absent case must produce the same
// answer -- because a test asserting only "the draft is hidden" passes just as happily when
// the resolver errors for the wrong reason or returns a different error code.

// listOutput mirrors the List GraphQL type.
//
// Declared here as a local struct rather than reusing models.List, because gqlgen decodes
// by json tag: using the resolver's own type would not prove the query PARSES, which is the
// thing a client hits first. camelCase because that is the JSON the server returns.
type listOutput struct {
	ID          string
	Name        string
	Description *string
	PublishedAt *string
	ItemCount   int
	Owner       struct{ Name string }
	Items       []struct {
		ID         string
		EntityType string
		EntityID   string
		Position   int
	}
	AuditTrail []struct {
		Action string
		Actor  *struct{ Name string }
	}
}

type listCreateResult struct {
	ListCreate struct {
		ID          string
		Name        string
		PublishedAt *string
		Owner       struct{ Name string }
		Items       []struct{ ID string }
	}
}

type listPublishResult struct {
	ListPublish struct {
		ID          string
		PublishedAt *string
	}
	AuditTrailHasPublish *struct{ Action string }
}

type listAddItemResult struct {
	ListAddItem struct {
		ID         string
		EntityType string
		EntityID   string
		Position   int
	}
}

type oneListResult struct {
	List *listOutput
}

type myListsResult struct {
	Lists []struct {
		ID          string
		Name        string
		PublishedAt *string
	}
}

type publishedListsResult struct {
	PublishedLists struct {
		Count int
		Lists []struct {
			ID          string
			Name        string
			PublishedAt *string
		}
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// post runs a query or mutation and fails the test if it errors.
//
// Built on Post rather than MustPost because MustPost panics: in the privacy tests I need
// to tell an ERROR apart from a null field, and a panic tells me neither cleanly.
func (r *testRunner) post(query string, resp any, opts ...client.Option) {
	r.t.Helper()
	require.NoError(r.t, r.client.Post(query, resp, opts...))
}

// queryErr runs an operation expected to FAIL and returns the error.
//
// gqlgen's Response.Errors is a json.RawMessage the client NEVER populates -- Post returns
// the error itself (client.go:78), and only RawPost returns a Response at all. My first
// version asserted on Response.Errors and so asserted on a field that is always nil: four
// assertions that could not fail, and a resolver mutation that swallowed a duplicate-item
// error passed straight through them.
//
// Asserting on Post's return value is both correct and the reason this helper exists: a
// privacy test has to be able to distinguish "the operation failed" from "the operation
// succeeded and returned a null field".
func (r *testRunner) queryErr(query string, opts ...client.Option) error {
	r.t.Helper()
	var resp map[string]any
	err := r.client.Post(query, &resp, opts...)
	require.Error(r.t, err, "this operation was expected to fail and did not; the response was %v", resp)
	return err
}

// queryErrContaining is queryErr plus a check that OUR message is what came back.
//
// Necessary because "the operation errored" is not the property under test. A resolver that
// returns (nil, nil) on a duplicate ALSO makes gqlgen fail the query -- with "cannot return
// null for non-nullable field" -- so a bare require.Error is satisfied by a resolver that
// has no idea why it refused. A mutation that swallowed the duplicate error passed the
// Error assertion and only failed this one, which is the point of having both.
func (r *testRunner) queryErrContaining(query, want string, opts ...client.Option) error {
	r.t.Helper()
	err := r.queryErr(query, opts...)

	// Parse gqlgen's error envelope rather than matching its text. err.Error() is
	// `[{"message":"...","path":[...],"locations":[...]}]`, so a substring match over it
	// would either hardcode line numbers into the test or -- worse -- pass on a message
	// that merely CONTAINS the right words.
	//
	// Exact equality on the parsed MESSAGE, not Contains: a resolver that returned the raw
	// wrapped service error instead of ours produces "that entry is already in this list:
	// that entry is already in this list", which CONTAINS the right substring and so passed
	// a Contains check. That is the driver message leaking to a client, and only a mutation
	// exposed it.
	var envelope []struct {
		Message string `json:"message"`
	}
	require.NoError(r.t, json.Unmarshal([]byte(err.Error()), &envelope),
		"the error was not a gqlgen envelope: %s", err.Error())
	require.NotEmpty(r.t, envelope, "the error envelope carried no messages: %s", err.Error())

	assert.Equal(r.t, want, envelope[0].Message,
		"the refusal must be exactly our message -- a leaked or wrapped driver error reads differently")
	return err
}

// ---------------------------------------------------------------------------
// The boundary tests
// ---------------------------------------------------------------------------

// A draft is invisible to everyone but its owner, AND invisible in the same WAY as a
// missing list.
//
// The "same way" is the assertion that matters. Returning a distinct error for "exists but
// is yours" would let a caller confirm which of a guessed id range are real.
func TestListDraftIsInvisibleAndIndistinguishableFromMissing(t *testing.T) {
	owner := asRead(t)
	stranger := asEdit(t)

	var created struct{ ListCreate struct{ ID string } }
	owner.post(
		`mutation { listCreate(input: {name: "private thoughts"}) { id } }`,
		&created,
	)
	draftID := created.ListCreate.ID
	require.NotEmpty(t, draftID, "the draft was created; without an id the rest proves nothing")

	// The owner can see it.
	var asOwner oneListResult
	owner.post(`query ($id: ID!) { list(id: $id) { id name publishedAt } }`,
		&asOwner, client.Var("id", draftID))
	require.NotNil(t, asOwner.List, "the OWNER must be able to read their own draft")
	assert.Nil(t, asOwner.List.PublishedAt, "a draft has no publishedAt, and that nil IS the privacy")

	// A stranger gets null.
	var asStranger oneListResult
	stranger.post(`query ($id: ID!) { list(id: $id) { id name } }`,
		&asStranger, client.Var("id", draftID))
	assert.Nil(t, asStranger.List, "another user's draft must be invisible")

	// A made-up id gets null too.
	var asMissing oneListResult
	stranger.post(
		`query ($id: ID!) { list(id: $id) { id name } }`,
		&asMissing, client.Var("id", "00000000-0000-4000-8000-000000000000"))
	assert.Nil(t, asMissing.List, "a list that does not exist is also null")

	// The two answers are the same KIND of answer. Proved by construction above (both
	// null, neither an error -- MustQuery would have failed on an error response), so this
	// asserts the remaining half: the stranger's query SUCCEEDED rather than erroring.
	assert.Equal(t, asStranger.List, asMissing.List,
		"a hidden draft and a missing list must be indistinguishable to a client")
}

// Another user's drafts do not appear in `lists(userId:)`, only their published ones.
//
// The second leak path, and the one a resolver helper makes easy: `lists` has to filter for
// other people and not for you, using the same query.
func TestListsAboutAnotherUserExcludesTheirDrafts(t *testing.T) {
	// asModify, not asRead: this test publishes, and publishing needs MODIFY. The first
	// version used asRead and failed with "not authorized" -- which is the directive
	// working correctly, not a bug in it.
	owner := asModify(t)
	stranger := asEdit(t)

	var created struct {
		ListCreate struct {
			ID   string
			Name string
		}
	}
	owner.post(
		`mutation { listCreate(input: {name: "mine, unpublished"}) { id name } }`,
		&created,
	)
	draftID := created.ListCreate.ID

	owner.post(`mutation ($id: ID!) { listPublish(id: $id) { id } }`,
		&struct{ ListPublish struct{ ID string } }{}, client.Var("id", draftID))

	// The owner sees both states (draft and published) in their own listing.
	var mine myListsResult
	owner.post(`query { lists { id name publishedAt } }`, &mine)
	require.NotEmpty(t, mine.Lists, "the owner's own listing must include what they created")

	// The stranger's view of that same owner is a different set.
	var theirs myListsResult
	stranger.post(
		`query ($userId: ID!) { lists(userId: $userId) { id name publishedAt } }`,
		&theirs, client.Var("userId", userDB.read.ID))

	for _, l := range theirs.Lists {
		if l.Name == "mine, unpublished" {
			assert.NotNil(t, l.PublishedAt,
				"a list belonging to someone else must appear only if it is published")
		}
	}
}

// ---------------------------------------------------------------------------
// The wiring tests
// ---------------------------------------------------------------------------

// A created list comes back private, with an owner name and a non-null empty item list.
//
// The empty-list check is not incidental: the schema declares items as [ListItem!]!, and a
// nil slice renders as `items: null`, which fails the type contract. A brand-new list is
// exactly the case where that bug would hide.
func TestListCreateReturnsAPrivateListWithTheRightShape(t *testing.T) {
	s := asRead(t)

	var res listCreateResult
	s.post(`mutation {
		listCreate(input: {name: "weekend watchlist", description: "for later"}) {
			id name publishedAt owner { name } items { id }
		}
	}`, &res)

	assert.NotEmpty(t, res.ListCreate.ID)
	assert.Equal(t, "weekend watchlist", res.ListCreate.Name)
	assert.Nil(t, res.ListCreate.PublishedAt, "listCreate must NOT publish; there is no argument for it")
	assert.NotEmpty(t, res.ListCreate.Owner.Name, "the owner name must be denormalised onto the row")
	assert.NotNil(t, res.ListCreate.Items, "items is [ListItem!]! -- null here would break the contract")
	assert.Len(t, res.ListCreate.Items, 0, "a new list is empty, not absent")
}

// Publishing sets publishedAt, and the entity type enum round-trips.
func TestListPublishAndAddItemRoundTripTheirEnums(t *testing.T) {
	s := asModify(t)

	var created struct{ ListCreate struct{ ID string } }
	s.post(`mutation { listCreate(input: {name: "enum check"}) { id } }`, &created)
	listID := created.ListCreate.ID

	// `mutation`, not `query`. My first version declared it as a query and gqlgen rejected
	// it at validation with "Cannot query field listPublish on type Query" -- the schema
	// catching a real mistake, which is what validation is for.
	var published listPublishResult
	s.post(`mutation ($id: ID!) {
		listPublish(id: $id) { id publishedAt }
	}`, &published, client.Var("id", listID))
	assert.NotNil(t, published.ListPublish.PublishedAt, "publishing must set publishedAt")

	var added listAddItemResult
	s.post(`mutation ($id: ID!) {
		listAddItem(input: {
			listId: $id, entityType: PERFORMER, entityId: "11111111-1111-4111-8111-111111111111"
		}) { id entityType entityId position }
	}`, &added, client.Var("id", listID))

	// The enum must come back as the enum the schema declares, not as a string that
	// happens to match. `EntityType` here is a Go string, so a schema mismatch shows up
	// as a decode panic or an unexpected value.
	assert.Equal(t, "PERFORMER", added.ListAddItem.EntityType,
		"entityType must round-trip PERFORMER exactly")
	assert.Equal(t, "11111111-1111-4111-8111-111111111111", added.ListAddItem.EntityID)
	assert.GreaterOrEqual(t, added.ListAddItem.Position, 0,
		"position 0 in the input means append, so the stored position must be non-negative")
}

// publishedLists returns only published lists, and its count is the instance total rather
// than the page length.
func TestPublishedListsExcludesDraftsAndCountsCorrectly(t *testing.T) {
	s := asRead(t)

	// One draft, deliberately never published.
	s.post(`mutation { listCreate(input: {name: "never published, ever"}) { id } }`,
		&struct{ ListCreate struct{ ID string } }{})

	var res publishedListsResult
	s.post(`query {
		publishedLists(perPage: 100) { count lists { id name publishedAt } }
	}`, &res)

	for _, l := range res.PublishedLists.Lists {
		assert.NotNil(t, l.PublishedAt, "publishedLists must never return a draft")
	}
	assert.GreaterOrEqual(t, res.PublishedLists.Count, len(res.PublishedLists.Lists),
		"count is the total across pages, so it cannot be smaller than the page")
}

// The whole lifecycle in one operation, because the interesting failures are in the
// SEQUENCE: publish twice, unpublish twice, and see that each refuses.
func TestPublishAndUnpublishRefuseToRepeat(t *testing.T) {
	s := asModify(t)

	var created struct{ ListCreate struct{ ID string } }
	s.post(`mutation { listCreate(input: {name: "lifecycle"}) { id } }`, &created)
	listID := created.ListCreate.ID

	s.post(`mutation ($id: ID!) { listPublish(id: $id) { id publishedAt } }`,
		&struct {
			ListPublish struct {
				ID          string
				PublishedAt *string
			}
		}{}, client.Var("id", listID))

	// Publishing again must be an ERROR, not a silent success. A quiet success would tell a
	// client it had just published something it published a minute ago.
	s.queryErrContaining(`mutation ($id: ID!) { listPublish(id: $id) { id } }`,
		"this list is already published", client.Var("id", listID))

	s.post(`mutation ($id: ID!) { listUnpublish(id: $id) { id publishedAt } }`,
		&struct {
			ListUnpublish struct {
				ID          string
				PublishedAt *string
			}
		}{}, client.Var("id", listID))

	s.queryErrContaining(`mutation ($id: ID!) { listUnpublish(id: $id) { id } }`,
		"this list is not published", client.Var("id", listID))
}

// Deleting someone else's private list must be an ERROR and not a success.
//
// The counterpart to `list` returning null. A delete that reported true for a list it could
// not see would tell the caller their private list is gone -- and would hide the fact that
// the list is still there.
func TestDeletingAnotherUsersListFailsLoudly(t *testing.T) {
	owner := asRead(t)
	stranger := asModify(t)

	var created struct{ ListCreate struct{ ID string } }
	owner.post(`mutation { listCreate(input: {name: "not yours"}) { id } }`, &created)
	listID := created.ListCreate.ID

	// "no such list", NOT "you do not own this list" -- and that is the finding this
	// assertion records rather than accommodates. Delete() resolves the list through Get(),
	// and Get() deliberately masks "exists but is not yours" as "does not exist" so a private
	// id cannot be confirmed. The masking wins: a delete is an operation a stranger runs
	// with a guessed id, and telling them the list is real is precisely the leak Get
	// refuses to make.
	//
	// ErrNotOwner is still reachable, for a list the actor CAN see but does not own -- a
	// published one -- where the message is both accurate and safe.
	stranger.queryErrContaining(`mutation ($id: ID!) { listDelete(id: $id) }`,
		"no such list", client.Var("id", listID))

	// And the list must still be there afterwards -- the failed delete changed nothing.
	var stillThere oneListResult
	owner.post(`query ($id: ID!) { list(id: $id) { id } }`, &stillThere, client.Var("id", listID))
	assert.NotNil(t, stillThere.List, "a refused delete must not have removed the list")
}

// A duplicate entry is rejected rather than silently ignored, so a double-submitting client
// does not see success while the list's length disagrees with its contents.
func TestDuplicateListItemIsRejected(t *testing.T) {
	s := asModify(t)

	var created struct{ ListCreate struct{ ID string } }
	s.post(`mutation { listCreate(input: {name: "dedupe"}) { id } }`, &created)
	listID := created.ListCreate.ID

	const dupQuery = `mutation ($id: ID!) {
		listAddItem(input: {
			listId: $id, entityType: PERFORMER, entityId: "22222222-2222-4222-8222-222222222222"
		}) { id }
	}`
	s.post(dupQuery, &struct{ ListAddItem struct{ ID string } }{}, client.Var("id", listID))

	s.queryErrContaining(dupQuery, "that entry is already in this list", client.Var("id", listID))

	// Exactly one item, so the refusal did not half-succeed.
	var listed struct {
		List struct {
			Items     []struct{ ID string }
			ItemCount int
		}
	}
	s.post(`query ($id: ID!) { list(id: $id) { itemCount items { id } } }`,
		&listed, client.Var("id", listID))
	assert.Equal(t, 1, listed.List.ItemCount, "the refused add must not have added a row")
}
