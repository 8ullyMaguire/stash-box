//go:build integration

package api_test

import (
	"strconv"
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service/invite"
	"github.com/stashapp/stash-box/internal/service/trust"
)

// The invite allowance (SPEC §7, plan phase 11 slice 11a).
//
// THE RULE THIS CARRIES: how many invitations a member may hand out follows from their trust level,
// counted from the key rows rather than from a stored tally. The interesting assertions are the two
// halves that make that true rather than decorative:
//
//   - a level that RISES frees capacity, and a key that EXPIRES or is RESCINDED frees its slot, with
//     nothing to clean up (that is what "derived" buys);
//   - the two refusals say WHICH allowance ran out, because "ask an operator" and "rescind one of
//     yours" are different next actions.
//
// Two of this codebase's traps shape the fixture below, both also documented by the trust-level test
// next door: findUser demands the READ role, so every runner here is created WITH Read; and the
// allowance is @isUserOwner, so reading it as anybody else fails the query by directive rather than
// returning null.

type inviteAllowanceOutput struct {
	Level       int  `json:"level"`
	Cap         int  `json:"cap"`
	ActiveKeys  int  `json:"active_keys"`
	Tokens      int  `json:"tokens"`
	Exempt      bool `json:"exempt"`
	CanGenerate bool `json:"can_generate"`
}

type userInviteAllowanceOutput struct {
	ID              string                 `json:"id"`
	InviteAllowance *inviteAllowanceOutput `json:"invite_allowance"`
}

func (c *graphqlClient) inviteAllowanceOf(userID uuid.UUID) (*inviteAllowanceOutput, error) {
	q := `
	query UserInviteAllowance($id: ID!) {
		findUser(id: $id) {
			id
			invite_allowance {
				level
				cap
				active_keys
				tokens
				exempt
				can_generate
			}
		}
	}`
	var resp struct {
		FindUser *userInviteAllowanceOutput
	}
	if err := c.Post(q, &resp, client.Var("id", userID.String())); err != nil {
		return nil, err
	}
	if resp.FindUser == nil {
		return nil, nil
	}
	return resp.FindUser.InviteAllowance, nil
}

func (c *graphqlClient) generateInviteKeys(keys int) ([]string, error) {
	q := `
	mutation GenerateInviteCodes($keys: Int) {
		generateInviteCodes(input: {keys: $keys})
	}`
	var resp struct {
		GenerateInviteCodes []string
	}
	if err := c.Post(q, &resp, client.Var("keys", keys)); err != nil {
		return nil, err
	}
	return resp.GenerateInviteCodes, nil
}

func (c *graphqlClient) rescindInviteKey(code string) (bool, error) {
	q := `
	mutation RescindInviteCode($code: ID!) {
		rescindInviteCode(code: $code)
	}`
	var resp struct {
		RescindInviteCode bool
	}
	if err := c.Post(q, &resp, client.Var("code", code)); err != nil {
		return false, err
	}
	return resp.RescindInviteCode, nil
}

// inviteMember creates a member with the READ role (needed to reach findUser at all) and returns a
// runner acting as them.
func inviteMember(t *testing.T) (*models.User, *testRunner) {
	t.Helper()
	roles := []models.RoleEnum{models.RoleEnumRead}
	member, err := asAdmin(t).createTestUser(nil, roles)
	require.NoError(t, err)
	return member, createTestRunner(t, member, roles)
}

// allowanceOf reads the member's own allowance, requiring it to resolve: every assertion about the
// numbers below is worthless if the field came back null.
func allowanceOf(t *testing.T, r *testRunner, member *models.User) *inviteAllowanceOutput {
	t.Helper()
	a, err := r.client.inviteAllowanceOf(member.ID)
	require.NoError(t, err)
	require.NotNil(t, a, "the owner's own allowance must resolve")
	return a
}

// grantInvite is the operator's side of the allowance: the ONE grant path that exists, called the
// way the API calls it. It validates MANAGE_INVITES from the caller's context rather than taking an
// operator as an argument, so it has to be issued as the admin -- which is also what makes it
// evidence that the grant path a real operator uses is the one these tests exercise.
func grantInvite(t *testing.T, userID uuid.UUID, amount int) {
	t.Helper()
	admin := asAdmin(t)
	_, err := admin.resolver.Mutation().GrantInvite(admin.ctx, models.GrantInviteInput{
		UserID: userID,
		Amount: amount,
	})
	require.NoError(t, err)
}

// THE CAP IS THE LEVEL'S, and it moves when the level does.
func TestInviteAllowanceFollowsTheLevel(t *testing.T) {
	member, runner := inviteMember(t)

	// A fresh account: the level every registered account starts at grants nothing. This is the
	// rule, not an edge case -- it is what stops an account created five minutes ago from
	// manufacturing accounts.
	start := allowanceOf(t, runner, member)
	assert.Equal(t, int(trust.LevelPublic), start.Level, "the level comes from the trust service")
	assert.Zero(t, start.Cap, "a fresh account must not be able to mint invitations")
	assert.Zero(t, start.ActiveKeys)
	assert.Zero(t, start.Tokens, "and it has no operator grant either")
	assert.False(t, start.Exempt, "Read is not the operator's delegation")
	assert.False(t, start.CanGenerate)

	// One applied edit is 10 points: Registered, the first earned level, whose cap is 1.
	recordApprovedEdits(t, member.ID, 1)
	registered := allowanceOf(t, runner, member)
	assert.Equal(t, int(trust.LevelRegistered), registered.Level)
	assert.Equal(t, invite.AllocationFor(trust.LevelRegistered), registered.Cap,
		"the cap must be the curve's answer, not a number this field invented")
	assert.Zero(t, registered.ActiveKeys)
	assert.True(t, registered.CanGenerate, "an earned level may invite")

	// Minting occupies the slot, and the key counts IMMEDIATELY: the same query says so.
	keys, err := runner.client.generateInviteKeys(1)
	require.NoError(t, err)
	require.Len(t, keys, 1)

	atCap := allowanceOf(t, runner, member)
	assert.Equal(t, 1, atCap.ActiveKeys, "the key that was just minted must be counted")
	assert.False(t, atCap.CanGenerate, "one cap, one key: there is nothing left")

	// And the mutation refuses, naming the level the allowance came from.
	_, err = runner.client.generateInviteKeys(1)
	require.Error(t, err, "a member at their cap must be refused")
	assert.Contains(t, err.Error(), "Registered", "the refusal must name the member's level")
	assert.Contains(t, err.Error(), invite.ErrLevelAllocationExhausted.Error(),
		"and it must be the cap refusal, not the token one")

	// Four more edits reach 50 points: Contributor, whose cap is 3. A level that rises must free
	// capacity WITHOUT anything being reset -- this is the assertion that separates "derived from
	// the level" from "a quota that was consumed".
	recordApprovedEdits(t, member.ID, 4)
	risen := allowanceOf(t, runner, member)
	assert.Equal(t, int(trust.LevelContributor), risen.Level)
	assert.Equal(t, invite.AllocationFor(trust.LevelContributor), risen.Cap)
	assert.Equal(t, 1, risen.ActiveKeys, "the key is still held, and still counted")
	assert.True(t, risen.CanGenerate, "the level rose, so the slot the level grants is free again")

	// The positive control for the whole test: the member can now actually mint again.
	more, err := runner.client.generateInviteKeys(1)
	require.NoError(t, err)
	require.Len(t, more, 1)
	assert.Equal(t, 2, allowanceOf(t, runner, member).ActiveKeys)
}

// THE TWO REFUSALS ARE DIFFERENT, and so are their remedies.
func TestInviteRefusalNamesWhichAllowanceRanOut(t *testing.T) {
	// (a) a member at the starting level: their LEVEL grants no keys at all.
	levelLess, levelLessRunner := inviteMember(t)

	_, err := levelLessRunner.client.generateInviteKeys(1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), invite.ErrNoInviteTokens.Error(),
		"a member whose level grants nothing must be told to ask an operator")
	assert.Contains(t, err.Error(), "Public", "and the message must name the level that refused them")
	assert.NotContains(t, err.Error(), invite.ErrLevelAllocationExhausted.Error(),
		"telling them their slots are in use would describe a member who has none")

	// An operator's grant is the remedy, and it works: the same member can then mint a key, which
	// spends the grant -- their level still grants nothing.
	grantInvite(t, levelLess.ID, 1)

	before := allowanceOf(t, levelLessRunner, levelLess)
	require.Equal(t, 1, before.Tokens)
	require.True(t, before.CanGenerate, "a grant buys a key for a member with no level slots")

	keys, err := levelLessRunner.client.generateInviteKeys(1)
	require.NoError(t, err)
	require.Len(t, keys, 1)

	after := allowanceOf(t, levelLessRunner, levelLess)
	assert.Zero(t, after.Tokens, "the grant is what paid for the key")
	assert.Zero(t, after.Cap, "and it did not raise the level's cap: the level is the level")
	assert.False(t, after.CanGenerate, "with no slots and no tokens, there is nothing left to spend")

	// (b) a member at Registered whose single slot is busy: their LEVEL is what ran out.
	busy, busyRunner := inviteMember(t)
	recordApprovedEdits(t, busy.ID, 1)
	require.Equal(t, 1, allowanceOf(t, busyRunner, busy).Cap)

	_, err = busyRunner.client.generateInviteKeys(1)
	require.NoError(t, err)

	_, err = busyRunner.client.generateInviteKeys(1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), invite.ErrLevelAllocationExhausted.Error())
	assert.Contains(t, err.Error(), "1 of 1", "the refusal must say how many slots are busy")
	assert.NotContains(t, err.Error(), invite.ErrNoInviteTokens.Error(),
		"they hold slots; the operator is not the remedy")
}

// A KEY THAT STOPS BEING USABLE FREES ITS SLOT, with nothing cleaning it up.
//
// This is the half of "derived" that a stored tally cannot do: the expired row is still in the table
// -- asserted, so the test cannot pass because something deleted it -- and the count no longer
// includes it.
func TestAnExpiredKeyFreesItsSlotWithoutBeingDeleted(t *testing.T) {
	member, runner := inviteMember(t)
	recordApprovedEdits(t, member.ID, 1)

	keys, err := runner.client.generateInviteKeys(1)
	require.NoError(t, err)
	require.Len(t, keys, 1)
	keyID, err := uuid.FromString(keys[0])
	require.NoError(t, err)

	require.False(t, allowanceOf(t, runner, member).CanGenerate, "fixture: the slot must be busy first")

	// Expire the key in the past, leaving the row where it is.
	_, err = testutil.DB().Exec(t.Context(),
		`UPDATE invite_keys SET expire_time = NOW() - interval '1 hour' WHERE id = $1`, keyID)
	require.NoError(t, err)

	freed := allowanceOf(t, runner, member)
	assert.Zero(t, freed.ActiveKeys, "an expired key must stop being counted")
	assert.True(t, freed.CanGenerate)

	// The row is still there: the slot was freed by the COUNT, not by a cleanup job.
	var rows int
	require.NoError(t, testutil.DB().QueryRow(t.Context(),
		`SELECT COUNT(*) FROM invite_keys WHERE generated_by = $1`, member.ID).Scan(&rows))
	assert.Equal(t, 1, rows, "the expired key must still exist, or this test proves nothing")

	// And minting again works, reading as one key in use rather than two.
	more, err := runner.client.generateInviteKeys(1)
	require.NoError(t, err)
	require.Len(t, more, 1)
	assert.Equal(t, 1, allowanceOf(t, runner, member).ActiveKeys)
}

// A BATCH CANNOT OUTRUN THE CAP, and a batch that does not fit leaves nothing behind.
//
// This is the assertion behind re-reading the allowance INSIDE the mint loop: with one slot free and
// two keys requested, the second check sees the key the first iteration just created. The count is
// asserted from the DATABASE and not only through the API, so a rolled-back insert cannot pass by
// being invisible to a derived field.
func TestABatchCannotOutrunTheCap(t *testing.T) {
	member, runner := inviteMember(t)
	recordApprovedEdits(t, member.ID, 1) // Registered: cap 1

	_, err := runner.client.generateInviteKeys(2)
	require.Error(t, err, "two keys against one slot must be refused")
	assert.Contains(t, err.Error(), invite.ErrLevelAllocationExhausted.Error())
	assert.Contains(t, err.Error(), "Registered")

	// Nothing was left behind: the mint of the first key must have been rolled back with the refusal.
	assert.Zero(t, allowanceOf(t, runner, member).ActiveKeys)

	var rows int
	require.NoError(t, testutil.DB().QueryRow(t.Context(),
		`SELECT COUNT(*) FROM invite_keys WHERE generated_by = $1`, member.ID).Scan(&rows))
	assert.Zero(t, rows, "a refused batch must not leave the keys it managed to create before refusing")
}

// THE OPERATOR'S DELEGATION IS EXEMPT, at the wire and not just in the rule.
//
// An INVITE-role member starts at the level that grants nothing and holds no grant, so the only thing
// that can let them mint is the exemption. Without this test, "exempt" would be a field nothing
// asserts end to end -- and a member who cannot invite at any level is exactly what a broken
// exemption looks like.
func TestTheOperatorDelegationIsExemptAtTheWire(t *testing.T) {
	roles := []models.RoleEnum{models.RoleEnumRead, models.RoleEnumInvite}
	delegate, err := asAdmin(t).createTestUser(nil, roles)
	require.NoError(t, err)
	runner := createTestRunner(t, delegate, roles)

	a := allowanceOf(t, runner, delegate)
	require.Equal(t, int(trust.LevelPublic), a.Level, "fixture: the delegate starts at the level that grants nothing")
	require.Zero(t, a.Cap)
	require.Zero(t, a.Tokens)
	assert.True(t, a.Exempt, "the INVITE role is the operator's delegation")
	assert.True(t, a.CanGenerate, "and it is not constrained by a cap it never earned")

	keys, err := runner.client.generateInviteKeys(2)
	require.NoError(t, err, "the delegate must be able to mint without a level or a grant")
	require.Len(t, keys, 2)
	assert.Equal(t, 2, allowanceOf(t, runner, delegate).ActiveKeys, "the keys really exist")
}

// A KEY THAT BROUGHT SOMEBODY IN IS NOT A REFUND, and the positive control proves the refund itself
// still works rather than being switched off.
func TestRescindingAnActivatedKeyDoesNotRefundTheGrant(t *testing.T) {
	// A member with no level slots, so any key they hold was paid for by a grant: that makes the
	// token balance the only evidence, with no level slots to confuse the reading.
	member, runner := inviteMember(t)

	grantInvite(t, member.ID, 2)

	activated, err := runner.client.generateInviteKeys(1)
	require.NoError(t, err)
	require.Len(t, activated, 1)
	require.Equal(t, 1, allowanceOf(t, runner, member).Tokens, "one grant spent")

	// The key is used: a registration with it records a token carrying the key, which is exactly
	// what the repo's own "key already used" check reads.
	activatedID, err := uuid.FromString(activated[0])
	require.NoError(t, err)

	userSuffix++
	_, err = asAdmin(t).resolver.Mutation().NewUser(t.Context(), models.NewUserInput{
		Email:     "invited" + strconv.Itoa(userSuffix) + "@example.com",
		InviteKey: &activatedID,
	})
	require.NoError(t, err, "registering with the key is what makes it activated")

	_, err = runner.client.rescindInviteKey(activated[0])
	require.NoError(t, err)

	assert.Equal(t, 1, allowanceOf(t, runner, member).Tokens,
		"a key that signed somebody up must NOT be rescinded for a fresh grant: the slot it frees "+
			"was never its cost, and refunding it would buy a slot it did not free")

	// POSITIVE CONTROL: the same fixture, with a key nobody ever used, DOES refund.
	unused, err := runner.client.generateInviteKeys(1)
	require.NoError(t, err)
	require.Len(t, unused, 1)
	require.Zero(t, allowanceOf(t, runner, member).Tokens, "the second grant is spent on it")

	_, err = runner.client.rescindInviteKey(unused[0])
	require.NoError(t, err)

	assert.Equal(t, 1, allowanceOf(t, runner, member).Tokens,
		"an unused key returns its grant: without this the test above would pass on a refund that "+
			"never happens at all")
}
