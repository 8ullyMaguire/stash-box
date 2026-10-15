//go:build integration

package api_test

import (
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/database/testutil"
)

// THE THREE COUNTERS (SPEC §11, plan phase 11 slice 11c).
//
// THE FINDING THIS FILE ENFORCES: the rows the plan named for these counters do not exist anymore --
// `pending_activations` was dropped by migration 37, and the signup and activation rows are both
// `user_tokens` rows that activation DELETES -- so the counters rest on a row written for the
// purpose (`invite_key_events`) that keeps the REFERRER's id even after the key itself is deleted.
// The last test here is the one that says whether that worked: a spent key is gone, and the member's
// counters must still report what it did.

type inviteStatsOutput struct {
	Scans       int `json:"scans"`
	Signups     int `json:"signups"`
	Activations int `json:"activations"`
}

// inviteStats reads the member's own counters through `me`, which is the only way this field is
// reachable (owner-only by directive) and the way a client reads it.
func (c *graphqlClient) inviteStats() (*inviteStatsOutput, error) {
	// No `id` in the selection: the gqlgen test client refuses a response key its struct does not
	// have, which is a useful strictness and a trap for a copy-pasted query.
	q := `{ me { invite_stats { scans signups activations } } }`
	var resp struct {
		Me *struct {
			InviteStats *inviteStatsOutput `json:"invite_stats"`
		}
	}
	if err := c.Post(q, &resp); err != nil {
		return nil, err
	}
	if resp.Me == nil {
		return nil, nil
	}
	return resp.Me.InviteStats, nil
}

func statsOf(t *testing.T, runner *testRunner) inviteStatsOutput {
	t.Helper()
	stats, err := runner.client.inviteStats()
	require.NoError(t, err)
	require.NotNil(t, stats, "the member can read their own counters")
	return *stats
}

// THE COUNTERS MOVE ONE AT A TIME, which is the whole point of there being three of them: a signup
// is an address and an activation is a person, and a member needs to know which happened.
func TestTheInviteCountersMoveOneAtATime(t *testing.T) {
	// The counter can only be read by its owner, so the fixture keeps the owner's runner.
	member, runner := inviteMember(t)
	grantInvite(t, member.ID, 1)

	keys := keyIDs(t, mustKeys(t, runner, 1, nil))
	key := keys[0]

	// Before anything: all three at zero, which is the state the "absent kind" case has to render
	// right -- nothing happened, and the member is told zero times three rather than nothing.
	assert.Equal(t, inviteStatsOutput{}, statsOf(t, runner),
		"a member who has never been scanned, signed up or activated reads 0/0/0")

	// A SIGNUP moves signups, and moves nothing else.
	name := inviteeName()
	activationKey, err := signUp(t, name+"@example.com", &key)
	require.NoError(t, err)
	require.NotNil(t, activationKey)

	afterSignup := statsOf(t, runner)
	assert.Equal(t, 1, afterSignup.Signups, "the signup is counted")
	assert.Zero(t, afterSignup.Activations,
		"and NOTHING ELSE moved: an address is not a person until somebody activates it")
	assert.Zero(t, afterSignup.Scans, "and a signup is not a scan: nobody opened a landing page here")

	// An ACTIVATION moves activations, and leaves the signup count where it was: they are two
	// facts about one person, not two steps of one counter.
	activate(t, *activationKey)

	afterActivation := statsOf(t, runner)
	assert.Equal(t, 1, afterActivation.Activations, "the account is counted as activated")
	assert.Equal(t, 1, afterActivation.Signups,
		"and the signup that led to it is STILL a signup: one invitation, two facts about it")
	assert.Zero(t, afterActivation.Scans)
}

// THE ASSERTION THE FINDING EXISTS FOR: a key whose last use is consumed is DELETED, and the member's
// counters must report what it did anyway. A counter that dies with the key it counts reports zero
// for the one invitation a member actually asks about.
func TestTheInviteCountersSurviveTheDeletionOfTheKeyTheyCount(t *testing.T) {
	member, runner := inviteMember(t)
	grantInvite(t, member.ID, 1)

	uses := 1
	keys := keyIDs(t, mustKeys(t, runner, 1, &uses))
	key := keys[0]

	signUpName(t, inviteeName(), &key)

	require.True(t, keyIsGone(t, key),
		"the fixture needs the key to be SPENT and deleted, or this test is about nothing")
	assert.Equal(t, 2, eventsWithNoKey(t, member.ID),
		"and both events still exist with NO key: invite_key_id is set to NULL on delete, so the "+
			"row keeps the referrer and lets go of the key")

	stats := statsOf(t, runner)
	assert.Equal(t, 1, stats.Signups,
		"the signup a deleted key produced is STILL COUNTED: this is the finding's own verification")
	assert.Equal(t, 1, stats.Activations,
		"and the activation it produced is too, which is the number a member actually wants")
}

// AND THE COUNTERS ARE THE MEMBER'S OWN, like the allowance and the trust object: what a member's
// invitations produced is part of their standing.
func TestInviteStatsAreTheOwnersOwn(t *testing.T) {
	member, runner := inviteMember(t)
	grantInvite(t, member.ID, 1)
	keys := keyIDs(t, mustKeys(t, runner, 1, nil))
	signUpName(t, inviteeName(), &keys[0])

	// The SUBJECT is counted first: the owner reads one signup, so the refusal below is a statement
	// about the directive and not about a member with nothing to show.
	own := statsOf(t, runner)
	require.Equal(t, 1, own.Signups, "the owner sees their own counters")

	// A stranger -- another member with READ, reaching findUser the way the API serves it -- is
	// refused the counters.
	stranger, strangerRunner := inviteMember(t)
	require.NotEqual(t, member.ID, stranger.ID)
	var resp struct {
		FindUser *struct {
			InviteStats *inviteStatsOutput `json:"invite_stats"`
		}
	}
	err := strangerRunner.client.Post(
		`query ($id: ID!) { findUser(id: $id) { id invite_stats { scans signups activations } } }`,
		&resp, client.Var("id", member.ID.String()))
	require.Error(t, err, "another member may not read what this member's invitations did")
	assert.Contains(t, err.Error(), "not authorized",
		"and the refusal is the owner-directive's own, not a missing field")
}

// eventsWithNoKey counts this member's event rows that no longer name a key, which is what a
// deleted key's events look like: the referrer stays, the key is let go.
func eventsWithNoKey(t *testing.T, member uuid.UUID) int {
	t.Helper()
	var n int
	require.NoError(t, testutil.DB().QueryRow(t.Context(),
		`SELECT count(*)::int FROM invite_key_events
		 WHERE generated_by = $1 AND invite_key_id IS NULL`, member).Scan(&n))
	return n
}
