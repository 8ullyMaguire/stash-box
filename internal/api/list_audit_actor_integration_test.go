//go:build integration

package api_test

import (
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/models"
)

// Regression tests for two bugs that a green suite shipped.
//
// NEITHER WAS FOUND BY A TEST. Both were found by scripts/verify-lists-live.sh querying the
// running server, and both had a passing test that ought to have caught them. That is the
// part worth writing down, so read this before adding a test that "looks like" these ones.

// BUG 1: the audit trail's actor was ALWAYS null.
//
// listAuditToModel built the model with ID, Action and CreatedAt, and nothing set Actor.
// The schema declared `actor: User` -- nullable, documented as "or null if that account no
// longer exists" -- so every audit row reported actor: null, and the frontend's "a former
// member" branch rendered for publications that plainly had an owner.
//
// WHY THE SUITE WAS GREEN: the unit tests constructed an audit row with a nil actor. A test
// that agrees with the bug cannot catch it. The nullable declaration is what made this
// possible -- with `actor: User!` the whole query would have failed loudly instead.
//
// What makes a null actor genuinely ambiguous is that there are three different reasons for
// it, and only one of them is the interesting one:
//
//	a) the account was deleted        -- the documented case, rendered as "a former member"
//	b) the resolver never populated it -- THE BUG, rendered as exactly the same thing
//	c) the lookup failed              -- swallowed by the same nil
//
// A test cannot tell them apart by looking at one row. It has to assert on a row whose actor
// is KNOWN to exist, which is what the first case below does.
func TestAuditTrailResolvesTheActorWhoActuallyExists(t *testing.T) {
	actor := asModify(t)

	var created struct{ ListCreate struct{ ID string } }
	actor.post(`mutation { listCreate(input: {name: "audit actor"}) { id } }`, &created)
	listID := created.ListCreate.ID
	require.NotEmpty(t, listID, "the list was created; without an id the rest proves nothing")

	actor.post(`mutation ($id: ID!) { listPublish(id: $id) { id publishedAt } }`,
		&listPublishResult{}, client.Var("id", listID))

	// Read the trail AS THE OWNER, because the trail is the owner's view of their own
	// history and is gated as such.
	var got struct {
		List struct {
			AuditTrail []struct {
				Action string
				Actor  *struct{ Name string }
			}
		}
	}
	actor.post(`query ($id: ID!) {
		list(id: $id) { auditTrail { action actor { name } } }
	}`, &got, client.Var("id", listID))

	trail := got.List.AuditTrail
	require.NotEmpty(t, trail, "publishing must leave a trail; an empty one means nothing below is tested")

	publish := trail[0]
	// Deliberately NOT assert.Equal("publish"): that is the value the bug produced, and
	// asserting it would have locked the bug in. I wrote exactly that line first, it failed
	// against the fix, and deleting it was the right response -- not changing it to match
	// whatever the code happened to return, which is how a test becomes a mirror.
	assert.Contains(t, []string{"PUBLISH", "UNPUBLISH"}, publish.Action,
		"sanity: this is the publish row, spelled as the enum spells it")

	// THE assertion. The actor exists -- it is the user making this very request -- so a
	// null here can only mean the resolver did not populate it.
	require.NotNil(t, publish.Actor,
		"the actor is the user performing this mutation, so it exists; a null actor means "+
			"the resolver never resolved it, which is the bug this test exists for")
	assert.NotEmpty(t, publish.Actor.Name, "an actor with no name is as useless as a nil one")
}

// BUG 2: auditTrail.action was 'publish' where the schema declares ListAuditActionEnum.
//
// The column stores the lower-case verb, the enum is upper case, and the resolver cast the
// raw string. gqlgen serialises whatever the Go string holds, so a field declared
// ListAuditActionEnum! came back as "publish" -- a value no client could ever have selected,
// and one that would break a generated TypeScript union at compile time on the frontend.
//
// WHY THE SUITE WAS GREEN: TestListPublishAndAddItemRoundTripTheirEnums asserts that
// entityType round-trips PERFORMER, and does not look at auditTrail.action at all. The test
// named "round trip their enums" was checking one of the two enums.
//
// Decoding into a Go string cannot catch this either, which is the trap: `Action string`
// accepts anything. The check has to be against the ENUM MEMBERSHIP, not the value.
func TestAuditTrailActionIsAMemberOfTheDeclaredEnum(t *testing.T) {
	s := asModify(t)

	var created struct{ ListCreate struct{ ID string } }
	s.post(`mutation { listCreate(input: {name: "enum casing"}) { id } }`, &created)
	listID := created.ListCreate.ID

	s.post(`mutation ($id: ID!) { listPublish(id: $id) { id } }`,
		&listPublishResult{}, client.Var("id", listID))

	var got struct {
		List struct {
			AuditTrail []struct{ Action string }
		}
	}
	s.post(`query ($id: ID!) { list(id: $id) { auditTrail { action } } }`,
		&got, client.Var("id", listID))
	require.NotEmpty(t, got.List.AuditTrail, "publishing must leave a trail")

	action := got.List.AuditTrail[0].Action

	// Assert MEMBERSHIP rather than equality with a literal. A literal pins today's value;
	// membership is the property the schema actually promises, and it fails the same way for
	// any future verb added to the column without the enum. The set is written out rather
	// than read from the schema because reading it from the schema would need introspection,
	// which this instance disables.
	assert.Contains(t, []string{"PUBLISH", "UNPUBLISH"}, action,
		"action %q is not a member of ListAuditActionEnum; the column stores lower case and "+
			"the resolver must normalise it", action)
}

// The deleted-actor case, and a DESIGN FINDING rather than a test.
//
// The schema documents `actor: User` as "null if that account no longer exists", and the
// frontend renders that as "a former member". Both are unreachable, and the reason is a
// contradiction between two foreign keys in migration 107:
//
//	list_audit.list_id  ON DELETE CASCADE   -> delete the owner, delete the list
//	list_audit.actor_id ON DELETE SET NULL  -> ...which deletes the trail before SET NULL runs
//
// The actor of a publish is the owner by construction: only the owner may publish. So the
// only way to null an actor_id is to delete the owner, and deleting the owner cascades the
// list and therefore the whole trail. A row can never be observed with actor_id NULL.
//
// My first version of this test deleted the owner and asserted the record survived. It failed
// with an empty trail -- correctly, because the trail really is gone. Nothing about the
// resolver was involved.
//
// Two ways this could be fixed, both product decisions, so neither is made here:
//
//	1. lists.owner_id ON DELETE SET NULL too, and treat an ownerless list as undeletable.
//	   Larger blast radius: every ownership check has to handle a null owner.
//	2. Drop the "or null" wording and render nothing for a missing actor, accepting that the
//	   column is nullable only so the FK can be SET NULL for a row that never renders.
//
// Until one of those is chosen, the honest thing is to pin the behaviour that DOES hold and
// say plainly that the documented case is dead.
func TestAuditTrailIsDeletedWithItsListBecauseTheActorIsTheOwner(t *testing.T) {
	admin := asAdmin(t)

	created, err := admin.createTestUser(nil, []models.RoleEnum{models.RoleEnumModify})
	require.NoError(t, err)
	actorID := created.ID
	asUser := createTestRunner(t, &models.User{ID: actorID}, []models.RoleEnum{models.RoleEnumModify})

	var owner struct{ ListCreate struct{ ID string } }
	asUser.post(`mutation { listCreate(input: {name: "cascade check"}) { id } }`, &owner)
	listID := owner.ListCreate.ID
	require.NotEmpty(t, listID)

	asUser.post(`mutation ($id: ID!) { listPublish(id: $id) { id } }`,
		&listPublishResult{}, client.Var("id", listID))

	// The trail exists, with a resolvable actor. Same assertion as the first test, and it is
	// repeated here on purpose: without it, an empty trail at the end would be
	// indistinguishable from a trail that never existed.
	var before struct {
		List struct {
			AuditTrail []struct {
				Action string
				Actor  *struct{ Name string }
			}
		}
	}
	asUser.post(`query ($id: ID!) { list(id: $id) { auditTrail { action actor { name } } } }`,
		&before, client.Var("id", listID))
	require.NotEmpty(t, before.List.AuditTrail, "precondition: the publish was recorded")
	require.NotNil(t, before.List.AuditTrail[0].Actor,
		"precondition: the actor resolves before anything is deleted")

	destroyed, err := admin.resolver.Mutation().UserDestroy(admin.ctx,
		models.UserDestroyInput{ID: actorID})
	require.NoError(t, err)
	require.True(t, destroyed, "UserDestroy reported it did not destroy anything")

	// Documented as SET NULL, which would leave one row with a null actor. It is empty,
	// because list_id CASCADE fires first. Pinning the ACTUAL behaviour, with the reason in
	// the message, so the next person to look at this reads why rather than assuming a bug.
	var after struct {
		List struct {
			AuditTrail []struct {
				Action string
				Actor  *struct{ Name string }
			}
		}
	}
	admin.post(`query ($id: ID!) { list(id: $id) { auditTrail { action } } }`,
		&after, client.Var("id", listID))
	assert.Empty(t, after.List.AuditTrail,
		"the trail goes with the list: deleting the owner cascades lists, and "+
			"list_audit.list_id ON DELETE CASCADE removes the rows before actor_id's "+
			"SET NULL can matter. See the comment above for the two ways to fix this.")
}
