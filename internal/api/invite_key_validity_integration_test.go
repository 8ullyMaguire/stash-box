//go:build integration

package api_test

import (
	"strconv"
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/config"
	"github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/models"
)

// THE PRESENTED KEY'S OWN FACTS, in both configurations (SPEC §7, plan phase 11 slices 11a-11b).
//
// THE DEFECT THIS PINS: `validateInviteKey` wrapped EVERYTHING in `if config.GetRequireInvite()` --
// including "is this key expired" and "has this key been used up". So on a configuration that does
// not require invites, a key is optional (correct) and, because the two checks sat behind the same
// flag, also UNVALIDATED (not correct): a key that had already signed somebody up could sign up the
// next, and an expiry was enforced nowhere.
//
// It is the same family as slice 11b's `invited_by` finding -- a POLICY flag gating a FACT ABOUT A
// KEY -- and the rule that falls out is the one this file enforces: **require_invite decides whether
// a key is MANDATORY; it must never decide whether the key you were handed is any good.** Whether a
// key is required is policy about admission, and the two checks are facts that are equally true in
// both configurations.
//
// WHY THERE IS A TEST PER CONFIGURATION rather than one: the refusal has to hold when invites are
// optional (where the checks used to be skipped entirely) AND still be a NAMED refusal when they are
// required (where the spent-key path used to surface an opaque database error instead of a message a
// member can act on). Both configurations are reached by setting the flag, asserting its state first
// so a test cannot pass because the environment changed under it.

// generateInviteKeysWith mints keys through the REAL mutation, including the use count: the
// documented default is part of what is under test, so the argument has to be expressible.
func (c *graphqlClient) generateInviteKeysWith(keys int, uses *int) ([]string, error) {
	q := `
	mutation GenerateInviteCodes($keys: Int, $uses: Int) {
		generateInviteCodes(input: {keys: $keys, uses: $uses})
	}`
	var resp struct {
		GenerateInviteCodes []string
	}
	opts := []client.Option{client.Var("keys", keys)}
	if uses != nil {
		opts = append(opts, client.Var("uses", strconv.Itoa(*uses)))
	}
	if err := c.Post(q, &resp, opts...); err != nil {
		return nil, err
	}
	return resp.GenerateInviteCodes, nil
}

// signUp registers an address with a key (or with none) and returns the activation key.
func signUp(t *testing.T, email string, key *uuid.UUID) (*uuid.UUID, error) {
	t.Helper()
	return asAdmin(t).resolver.Mutation().NewUser(t.Context(), models.NewUserInput{
		Email:     email,
		InviteKey: key,
	})
}

// signUpName registers an address, completes the activation, and returns the new member.
// It fails the test on any refusal, for the fixtures where the signup is not the subject.
func signUpName(t *testing.T, name string, key *uuid.UUID) *models.User {
	t.Helper()
	activationKey, err := signUp(t, name+"@example.com", key)
	require.NoError(t, err, "the fixture's signup is not the subject here")
	require.NotNil(t, activationKey)
	member, err := asAdmin(t).resolver.Mutation().ActivateNewUser(t.Context(), models.ActivateNewUserInput{
		Name:          name,
		ActivationKey: *activationKey,
		Password:      "password" + name,
	})
	require.NoError(t, err)
	require.NotNil(t, member)
	return member
}

// requiredInvitees flips require_invite OFF for one test and restores it, asserting the environment's
// own default first so the flip cannot be a no-op.
func withoutRequiringInvites(t *testing.T) {
	t.Helper()
	require.True(t, config.GetRequireInvite(),
		"this suite runs with invites required; if that changed, the flip below is what to revisit")
	previous := config.C.RequireInvite
	config.C.RequireInvite = false
	t.Cleanup(func() { config.C.RequireInvite = previous })
}

// freshKeyOwner is a member who can mint: they are given the tokens to pay for the keys.
func freshKeyOwner(t *testing.T, tokens int) (*models.User, *testRunner) {
	t.Helper()
	member, runner := inviteMember(t)
	grantInvite(t, member.ID, tokens)
	return member, runner
}

func keyIDs(t *testing.T, keys []string) []uuid.UUID {
	t.Helper()
	require.NotEmpty(t, keys)
	out := make([]uuid.UUID, 0, len(keys))
	for _, k := range keys {
		id, err := uuid.FromString(k)
		require.NoError(t, err)
		out = append(out, id)
	}
	return out
}

// THE SPENT KEY, on a configuration that does not require invites: the one that was broken.
//
// TWO STATES ARE CALLED "SPENT" and they are refused for two different reasons, which is why this
// test walks both:
//
//   - a use is PROMISED but not consumed yet (one signup pending activation): the key has none left
//     to give, and the refusal names the use count;
//   - a use has been CONSUMED at activation: the key row is deleted, so "this key is used up" and
//     "there is no such key" are the same state, and the refusal is the invalid-key one.
func TestASpentInviteKeyIsRefusedEvenWhenInvitesAreNotRequired(t *testing.T) {
	withoutRequiringInvites(t)

	_, runner := freshKeyOwner(t, 1)
	uses := 1
	keys := keyIDs(t, mustKeys(t, runner, 1, &uses))
	key := keys[0]

	// The key works once: that is what it is for, and a test that skipped it would not know the
	// refusals below were about the USE COUNT rather than about a key that never worked.
	first, err := signUp(t, inviteeName()+"@example.com", &key)
	require.NoError(t, err, "the fixture's first signup is not the subject here")
	require.NotNil(t, first)
	require.Equal(t, 1, pendingSignups(t, key), "the signup is pending, which is the state below")

	name := inviteeName()
	activationKey, err := signUp(t, name+"@example.com", &key)
	require.Error(t, err,
		"a key with ONE use has a signup in flight; handing it to a second must be refused")
	assert.Contains(t, err.Error(), "used",
		"and the refusal has to SAY what is wrong with the key: a member holding a spent link "+
			"cannot act on an opaque message, and the operator reading a log cannot either")
	assert.Nil(t, activationKey)

	// Now CONSUME the one use: activating the signup that holds it is what spends it, and THAT is
	// the second half of the defect -- the consumption itself was gated on require_invite, so with
	// the flag off the key was never spent and stayed usable for the next person.
	member := activate(t, *first)
	require.NotNil(t, member)
	assert.Zero(t, pendingSignups(t, key), "activation consumed the token that held the use")
	assert.True(t, keyIsGone(t, key),
		"and a key whose last use was consumed is DELETED, so \"spent\" and \"no longer exists\" "+
			"are the same state -- which is why the refusal below cannot say \"used\"")

	name = inviteeName()
	activationKey, err = signUp(t, name+"@example.com", &key)
	require.Error(t, err, "so a third signup with the same key is refused: the use was consumed")
	assert.Contains(t, err.Error(), "invite key", "with a NAMED refusal")
	assert.NotContains(t, err.Error(), "no rows",
		"and not with what the database driver said, which is what this used to leak")
	assert.Nil(t, activationKey)
}

// THE EXPIRED KEY, same configuration.
func TestAnExpiredInviteKeyIsRefusedEvenWhenInvitesAreNotRequired(t *testing.T) {
	withoutRequiringInvites(t)

	_, runner := freshKeyOwner(t, 1)
	keys := keyIDs(t, mustKeys(t, runner, 1, nil))

	// A TTL is seconds from now, so the past is written directly: the state under test is the state,
	// however it got there.
	_, err := testutil.DB().Exec(t.Context(),
		`UPDATE invite_keys SET expire_time = now() - interval '1 day' WHERE id = $1`, keys[0])
	require.NoError(t, err)
	require.True(t, isExpired(t, keys[0]), "the fixture's key is in the past before the attempt")

	name := inviteeName()
	activationKey, err := signUp(t, name+"@example.com", &keys[0])
	require.Error(t, err, "an expired key must be refused at signup in BOTH configurations")
	assert.Contains(t, err.Error(), "expir")
	assert.Nil(t, activationKey)
}

// AND THE POLICY HALF MUST KEEP WORKING: not requiring invites still means a key is not needed.
//
// This is the control for the fix. Validating a PRESENTED key must not make a key mandatory, and a
// change that moved the "invite key required" refusal out of the flag's reach would break every
// instance that does not require invites while every test above still passed.
func TestNoKeyIsStillAcceptedWhenInvitesAreNotRequired(t *testing.T) {
	withoutRequiringInvites(t)

	name := inviteeName()
	member := signUpName(t, name, nil)
	require.NotNil(t, member)

	var invitedBy *uuid.UUID
	require.NoError(t, testutil.DB().QueryRow(t.Context(),
		`SELECT invited_by FROM users WHERE id = $1`, member.ID).Scan(&invitedBy))
	assert.Nil(t, invitedBy, "nobody invited this member, and nothing may claim otherwise")
}

// AND WHEN INVITES ARE REQUIRED, where the checks did run, the refusals must still be NAMED: the
// spent-key path used to surface a raw database error ("no rows in result set") because a spent key
// is deleted, so the lookup failed and its error was returned untouched.
func TestTheRefusalsAreNamedWhenInvitesAreRequired(t *testing.T) {
	require.True(t, config.GetRequireInvite(), "the default this instance runs")

	_, runner := freshKeyOwner(t, 3)
	uses := 1
	pending := keyIDs(t, mustKeys(t, runner, 1, &uses))
	consumed := keyIDs(t, mustKeys(t, runner, 1, &uses))
	expired := keyIDs(t, mustKeys(t, runner, 1, nil))

	// The first key's single use is PROMISED but not consumed.
	_, err := signUp(t, inviteeName()+"@example.com", &pending[0])
	require.NoError(t, err)
	_, err = signUp(t, inviteeName()+"@example.com", &pending[0])
	require.Error(t, err, "a key with all its uses promised is refused")
	assert.Contains(t, err.Error(), "used", "naming the use count")

	// The second key's single use is CONSUMED, which deletes the key.
	signUpName(t, inviteeName(), &consumed[0])
	assert.True(t, keyIsGone(t, consumed[0]), "the consumed key is deleted, so the lookup finds nothing")
	_, err = signUp(t, inviteeName()+"@example.com", &consumed[0])
	require.Error(t, err, "and a signup with it is refused")
	assert.Contains(t, err.Error(), "invite key",
		"with the named invalid-key refusal, not the driver's error")
	assert.NotContains(t, err.Error(), "no rows")

	// The third is expired.
	_, err = testutil.DB().Exec(t.Context(),
		`UPDATE invite_keys SET expire_time = now() - interval '1 day' WHERE id = $1`, expired[0])
	require.NoError(t, err)
	_, err = signUp(t, inviteeName()+"@example.com", &expired[0])
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expir")

	// And a key nobody minted is refused too: a signup presenting a credential that does not exist
	// has not been invited by anybody.
	unknown := uuid.Must(uuid.NewV7())
	_, err = signUp(t, inviteeName()+"@example.com", &unknown)
	require.Error(t, err, "a key that does not exist is not a valid key")
	assert.Contains(t, err.Error(), "invite key")
}

func TestAKeyMintedWithoutAUseCountTakesExactlyOneSignup(t *testing.T) {
	withoutRequiringInvites(t)

	_, runner := freshKeyOwner(t, 1)
	keys := keyIDs(t, mustKeys(t, runner, 1, nil))

	// THE DOCUMENTED PROMISE: "If not set, the invite key will have one use". The code stored NULL,
	// which every query that reads `uses` treats as UNLIMITED -- so the schema and the database
	// disagreed about what an unqualified key is, and the schema was the one a client believes.
	assert.Equal(t, 1, useCount(t, keys[0]),
		"an unqualified key is ONE use, which is what the schema documents")

	signUpName(t, inviteeName(), &keys[0])

	_, err := signUp(t, inviteeName()+"@example.com", &keys[0])
	require.Error(t, err, "so it takes exactly one signup, like any other single-use key")
}

// ---------------------------------------------------------------- small readers

// mustKeys mints one key through the mutation, failing the test if it is refused.
func mustKeys(t *testing.T, runner *testRunner, keys int, uses *int) []string {
	t.Helper()
	out, err := runner.client.generateInviteKeysWith(keys, uses)
	require.NoError(t, err, "minting is not the subject of these tests")
	return out
}

func useCount(t *testing.T, key uuid.UUID) int {
	t.Helper()
	var uses *int
	require.NoError(t, testutil.DB().QueryRow(t.Context(),
		`SELECT uses FROM invite_keys WHERE id = $1`, key).Scan(&uses))
	require.NotNil(t, uses,
		"uses is NULL, which every query reads as UNLIMITED: the schema promises one")
	return *uses
}

// activate completes the signup that holds a key's use.
func activate(t *testing.T, activationKey uuid.UUID) *models.User {
	t.Helper()
	name := inviteeName()
	member, err := asAdmin(t).resolver.Mutation().ActivateNewUser(t.Context(), models.ActivateNewUserInput{
		Name:          name,
		ActivationKey: activationKey,
		Password:      "password" + name,
	})
	require.NoError(t, err, "activating is not the subject of these tests")
	require.NotNil(t, member)
	return member
}

// pendingSignups counts the signups holding this key that have not activated yet.
func pendingSignups(t *testing.T, key uuid.UUID) int {
	t.Helper()
	var n int
	require.NoError(t, testutil.DB().QueryRow(t.Context(),
		`SELECT count(*)::int FROM user_tokens WHERE (data->>'invite_key')::UUID = $1`, key).Scan(&n))
	return n
}

// keyIsGone reports whether the key's row has been deleted -- which is what spending its last use
// does, and the reason a spent key and an unknown key are refused the same way.
func keyIsGone(t *testing.T, key uuid.UUID) bool {
	t.Helper()
	var n int
	require.NoError(t, testutil.DB().QueryRow(t.Context(),
		`SELECT count(*)::int FROM invite_keys WHERE id = $1`, key).Scan(&n))
	return n == 0
}

func isExpired(t *testing.T, key uuid.UUID) bool {
	t.Helper()
	var expired bool
	require.NoError(t, testutil.DB().QueryRow(t.Context(),
		`SELECT expire_time IS NOT NULL AND expire_time < now() FROM invite_keys WHERE id = $1`,
		key).Scan(&expired))
	return expired
}

// countRows counts what a query returns, so an absence assertion can be preceded by a count of its
// subject -- the rule that a "nothing happened" assertion without a counted subject is vacuous.
func countRows(t *testing.T, from string, args ...any) string {
	t.Helper()
	var n string
	require.NoError(t, testutil.DB().QueryRow(t.Context(),
		"SELECT count(*)::text FROM "+from, args...).Scan(&n))
	return n
}
