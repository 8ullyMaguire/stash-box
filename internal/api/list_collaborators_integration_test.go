//go:build integration

package api_test

import (
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/models"
)

// Collaborative lists at the wire (plan phase 8 slice 8e).
//
// Why these exist ON TOP OF the service-level tests in internal/service/list: two of this slice's
// claims are only observable through the GraphQL layer.
//
//  1. `List.collaborators` and `List.canEdit` are FIELD RESOLVERS declared in gqlgen.yml, and a
//     generated struct field the resolver never fills answers with an EMPTY LIST or FALSE -- which is
//     a valid-looking answer to a question nobody asked. Nothing at the Go level can tell those apart
//     from a working resolver; only an executed query can.
//  2. The audit trail publishes an ENUM whose vocabulary is upper case while the column stores lower
//     case. That mismatch has shipped a bug in this very file's neighbour before (action came back
//     "publish"), so the new verbs are asserted BY THEIR EXACT SPELLING rather than by membership of a
//     set -- a set would pass for a row that returned the column's own lower-case text.
//
// Every test builds its OWN owner and member: asRead/asModify/asAdmin hand out one shared user per
// package, and a list name is unique per owner, so reusing them across tests turns a fixture collision
// into a failure that names the wrong cause.

type collabRunners struct {
	owner    *testRunner
	member   *testRunner
	stranger *testRunner
}

// newCollabRunners mints an owner, a member and a stranger, each with the MODIFY role the list
// mutations require.
//
// The stranger deliberately has MODIFY rather than being the package's shared READ user: a READ-only
// caller is refused by the role middleware BEFORE any resolver runs, so it would prove only that the
// roles exist. A stranger who could write any list they owned and still cannot touch this one is the
// assertion this slice needs.
func newCollabRunners(t *testing.T) collabRunners {
	t.Helper()
	admin := asAdmin(t)

	ownerUser, err := admin.createTestUser(nil, []models.RoleEnum{models.RoleEnumModify})
	require.NoError(t, err)
	memberUser, err := admin.createTestUser(nil, []models.RoleEnum{models.RoleEnumModify})
	require.NoError(t, err)
	strangerUser, err := admin.createTestUser(nil, []models.RoleEnum{models.RoleEnumModify})
	require.NoError(t, err)

	return collabRunners{
		owner:    createTestRunner(t, &models.User{ID: ownerUser.ID}, []models.RoleEnum{models.RoleEnumModify}),
		member:   createTestRunner(t, &models.User{ID: memberUser.ID}, []models.RoleEnum{models.RoleEnumModify}),
		stranger: createTestRunner(t, &models.User{ID: strangerUser.ID}, []models.RoleEnum{models.RoleEnumModify}),
	}
}

func (c collabRunners) createList(t *testing.T, name string) string {
	t.Helper()
	var created struct{ ListCreate struct{ ID string } }
	c.owner.post(`mutation ($name: String!) { listCreate(input: {name: $name}) { id } }`,
		&created, client.Var("name", name))
	require.NotEmpty(t, created.ListCreate.ID, "the list was created; without an id nothing below is tested")
	return created.ListCreate.ID
}

func (c collabRunners) addCollaborator(t *testing.T, listID, memberID string) {
	t.Helper()
	var res struct{ ListAddCollaborator bool }
	c.owner.post(`mutation ($list: ID!, $user: ID!) {
		listAddCollaborator(input: {listId: $list, userId: $user})
	}`, &res, client.Var("list", listID), client.Var("user", memberID))
	assert.True(t, res.ListAddCollaborator, "the add reported success")
}

// memberID reads the member's own id from the API rather than from the fixture, so the value the
// mutation is given is one the instance itself reports.
func (c collabRunners) memberID(t *testing.T) string {
	t.Helper()
	var me struct {
		Me struct{ ID string }
	}
	c.member.post(`query { me { id } }`, &me)
	require.NotEmpty(t, me.Me.ID)
	return me.Me.ID
}

type listCollaboratorsOutput struct {
	List *struct {
		ID            string
		Name          string
		CanEdit       bool
		Collaborators []struct {
			ID   string
			Name string
		}
	}
}

// THE FIELD-RESOLVER PROOF, and the slice's headline at the wire: a member who is not the owner can
// OPEN the draft, is told they may edit it, sees the set including themselves, and their ADD works.
func TestACollaboratorReadsWritesAndIsToldTheyMayAtTheWire(t *testing.T) {
	c := newCollabRunners(t)
	memberID := c.memberID(t)
	listID := c.createList(t, "shared at the wire")
	c.addCollaborator(t, listID, memberID)

	var got listCollaboratorsOutput
	c.member.post(`query ($id: ID!) {
		list(id: $id) { id name canEdit collaborators { id name } }
	}`, &got, client.Var("id", listID))

	require.NotNil(t, got.List, "a collaborator may read the draft they may write")
	assert.Equal(t, listID, got.List.ID)

	// canEdit true. If the gqlgen.yml entry were missing, the generated struct field would answer
	// FALSE here with no error anywhere -- which is why this assertion, not the mutation below, is the
	// one that proves the resolver is wired.
	assert.True(t, got.List.CanEdit, "a member is told they may write; a bound struct field would say false")

	// The set includes the member and NOT the owner: the owner is not a row in it.
	require.Len(t, got.List.Collaborators, 1, "the owner is not a collaborator, so exactly one member is")
	assert.Equal(t, memberID, got.List.Collaborators[0].ID)

	// The write half, through the real handler.
	var added listAddItemResult
	c.member.post(`mutation ($id: ID!) {
		listAddItem(input: {
			listId: $id, entityType: PERFORMER, entityId: "11111111-1111-4111-8111-222222222222"
		}) { id entityType }
	}`, &added, client.Var("id", listID))
	assert.NotEmpty(t, added.ListAddItem.ID, "a collaborator's add lands")
}

// The owner's own view is the same query, and it says the same thing -- plus the set is visible to
// them because they are the person who maintains it.
func TestTheOwnerSeesTheirWriteSetAndMayEdit(t *testing.T) {
	c := newCollabRunners(t)
	memberID := c.memberID(t)
	listID := c.createList(t, "owner's view of the write set")
	c.addCollaborator(t, listID, memberID)

	var got listCollaboratorsOutput
	c.owner.post(`query ($id: ID!) { list(id: $id) { canEdit collaborators { id } } }`,
		&got, client.Var("id", listID))

	require.NotNil(t, got.List)
	assert.True(t, got.List.CanEdit)
	require.Len(t, got.List.Collaborators, 1)
	assert.Equal(t, memberID, got.List.Collaborators[0].ID)
}

// A reader of a PUBLISHED list may read the contents and may not read the set or edit. The two
// answers are asserted with the refusals asserted as REFUSALS -- a field error propagates and the
// query fails, so "no such list" is the observable, and an empty list would be the bug.
func TestAStrangerOnAPublishedListMayReadNeitherTheSetNorWrite(t *testing.T) {
	c := newCollabRunners(t)
	memberID := c.memberID(t)
	listID := c.createList(t, "published with a hidden write set")
	c.addCollaborator(t, listID, memberID)

	stranger := c.stranger

	// FIRST, while it is still a draft: every stranger's answer is not-found, including the write,
	// because the list itself is invisible. This is the arm that keeps private ids unprobeable.
	stranger.queryErrContaining(`mutation ($id: ID!) {
		listAddItem(input: {listId: $id, entityType: PERFORMER, entityId: "11111111-1111-4111-8111-333333333333"}) { id }
	}`, "no such list", client.Var("id", listID))

	c.owner.post(`mutation ($id: ID!) { listPublish(id: $id) { id } }`,
		&listPublishResult{}, client.Var("id", listID))

	// Readable: it is published.
	var readable struct {
		List *struct {
			ID      string
			CanEdit bool
		}
	}
	stranger.post(`query ($id: ID!) { list(id: $id) { id canEdit } }`, &readable, client.Var("id", listID))
	require.NotNil(t, readable.List, "publishing made the list readable by a stranger")
	assert.False(t, readable.List.CanEdit, "a reader may not write, and that is false rather than an error")

	// The write is now refused as NOT-OWNER rather than not-found, and the difference is the point: the
	// list is public, so its existence is no secret -- what is refused is the write. A test that
	// expected "no such list" here would be pinning the wrong answer for a published list, and code
	// that returned not-found would be lying about a list anybody can browse.
	stranger.queryErrContaining(`mutation ($id: ID!) {
		listAddItem(input: {listId: $id, entityType: PERFORMER, entityId: "11111111-1111-4111-8111-333333333333"}) { id }
	}`, "you do not own this list", client.Var("id", listID))

	// The set, however, is still a refusal rather than an empty list -- and for a PUBLISHED list that
	// refusal is not-found, because this field's visibility is not the list's visibility and "no such
	// list" is this package's one word for "you may not see this". A reader who got [] would be told
	// "nobody collaborates on this", which is a fact about the members.
	stranger.queryErrContaining(`query ($id: ID!) { list(id: $id) { collaborators { id } } }`,
		"no such list", client.Var("id", listID))
}

// THE AUDIT AT THE WIRE: the actor and the enum's exact spelling.
//
// Two assertions, and the second is the one this repo has been bitten by: `action` is a
// ListAuditActionEnum and the COLUMN stores lower case, so a missing normalisation returns "add_item"
// -- a value no client could have selected. Asserting membership of a set would pass for a row that
// returned the column's own text if the set were lower case too; asserting the exact member does not.
func TestTheAuditTrailNamesTheActorOfACollaboratorsEntryWrite(t *testing.T) {
	c := newCollabRunners(t)
	memberID := c.memberID(t)
	listID := c.createList(t, "audited collaboration")
	c.addCollaborator(t, listID, memberID)

	c.member.post(`mutation ($id: ID!) {
		listAddItem(input: {
			listId: $id, entityType: PERFORMER, entityId: "11111111-1111-4111-8111-444444444444"
		}) { id }
	}`, &listAddItemResult{}, client.Var("id", listID))

	var got struct {
		List *struct {
			AuditTrail []struct {
				Action string
				Actor  *struct {
					ID   string
					Name string
				}
			}
		}
	}
	c.owner.post(`query ($id: ID!) { list(id: $id) { auditTrail { action actor { id name } } } }`,
		&got, client.Var("id", listID))

	require.NotNil(t, got.List)
	require.Len(t, got.List.AuditTrail, 2, "the membership change and the entry add are both recorded")

	// Newest first.
	added := got.List.AuditTrail[0]
	assert.Equal(t, "ADD_ITEM", added.Action,
		"the enum member exactly: the column stores 'add_item' and the resolver must normalise it")
	require.NotNil(t, added.Actor, "an entry write carries its actor, or a list with several writers cannot say who")
	assert.Equal(t, memberID, added.Actor.ID,
		"attributed to the COLLABORATOR who added it, not to the list's owner")

	membership := got.List.AuditTrail[1]
	assert.Equal(t, "COLLABORATOR_ADD", membership.Action)
	require.NotNil(t, membership.Actor)
}

// The seven owner-only acts, refused at the wire with the same message the service uses. Three of them
// are exercised here: the ones a client can reach with a plain id. The others are covered at the
// service level, where all seven are enumerated in one test.
func TestACollaboratorIsRefusedTheOwnerOnlyMutationsAtTheWire(t *testing.T) {
	c := newCollabRunners(t)
	memberID := c.memberID(t)
	listID := c.createList(t, "owner-only acts")
	c.addCollaborator(t, listID, memberID)

	c.member.queryErrContaining(`mutation ($id: ID!) { listUpdate(input: {id: $id, name: "renamed by a member"}) { id } }`,
		"you do not own this list", client.Var("id", listID))

	c.member.queryErrContaining(`mutation ($id: ID!) { listPublish(id: $id) { id } }`,
		"you do not own this list", client.Var("id", listID))

	c.member.queryErrContaining(`mutation ($list: ID!, $user: ID!) {
		listAddCollaborator(input: {listId: $list, userId: $user})
	}`, "you do not own this list", client.Var("list", listID), client.Var("user", memberID))
}

// The two refusals that are about the SET rather than about ownership, by their own messages: adding
// the owner, and removing somebody who was never a member.
func TestTheWriteSetsOwnRefusalsAtTheWire(t *testing.T) {
	c := newCollabRunners(t)
	memberID := c.memberID(t)
	listID := c.createList(t, "write set refusals")

	var ownerID struct {
		Me struct{ ID string }
	}
	c.owner.post(`query { me { id } }`, &ownerID)

	c.owner.queryErrContaining(`mutation ($list: ID!, $user: ID!) {
		listAddCollaborator(input: {listId: $list, userId: $user})
	}`, "the list's owner is not a collaborator on it",
		client.Var("list", listID), client.Var("user", ownerID.Me.ID))

	c.owner.queryErrContaining(`mutation ($list: ID!, $user: ID!) {
		listRemoveCollaborator(input: {listId: $list, userId: $user})
	}`, "that member is not a collaborator on this list",
		client.Var("list", listID), client.Var("user", memberID))

	c.addCollaborator(t, listID, memberID)
	c.owner.queryErrContaining(`mutation ($list: ID!, $user: ID!) {
		listAddCollaborator(input: {listId: $list, userId: $user})
	}`, "that member is already a collaborator on this list",
		client.Var("list", listID), client.Var("user", memberID))
}

// A member may leave without the owner's cooperation, at the wire -- and the list becomes invisible to
// them again in the same breath.
//
// "Invisible" is asserted as NULL WITH NO ERROR, because that is this package's privacy rule at the
// wire: `list(id)` is nullable and answers null for a draft that is not yours, exactly as it does for
// an id that does not exist. Asserting an error here would pin a behaviour the schema does not have.
func TestACollaboratorMayRemoveThemselvesAtTheWire(t *testing.T) {
	c := newCollabRunners(t)
	memberID := c.memberID(t)
	listID := c.createList(t, "leaving a shared list")
	c.addCollaborator(t, listID, memberID)

	var removed struct{ ListRemoveCollaborator bool }
	c.member.post(`mutation ($list: ID!, $user: ID!) {
		listRemoveCollaborator(input: {listId: $list, userId: $user})
	}`, &removed, client.Var("list", listID), client.Var("user", memberID))
	assert.True(t, removed.ListRemoveCollaborator)

	var after struct {
		List *struct{ ID string }
	}
	c.member.post(`query ($id: ID!) { list(id: $id) { id } }`, &after, client.Var("id", listID))
	assert.Nil(t, after.List, "a former collaborator sees the draft as absent, with no error to tell the two apart")
}
