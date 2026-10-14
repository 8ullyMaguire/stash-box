//go:build integration

package api_test

import (
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/models"
)

// The leaderboard through the real handler (plan phase 6 slice 6f).
//
// Two things can only be proven here. The first is the DEFAULT: the flag is a NOT NULL DEFAULT true column,
// and the mapping from the rollup into the schema's standing is hand-written Go -- a bool left unset is
// false, which would silently report every member in the instance as having opted out. An absent field
// would look exactly like a working opt-out, so this is asserted positively for a member who has never
// touched the setting.
//
// The second is the phase's own verification, at the surface a member actually sees: opting out takes you
// off the board and changes nothing else -- the same standing, the same counters, the same level.

type leaderboardOutput struct {
	Position int `json:"position"`
	Score    int `json:"score"`
	Level    int `json:"level"`
	User     struct {
		ID string `json:"id"`
	} `json:"user"`
}

// trustWithParticipationOutput is the OWNER-GATED standing, wrapped in the `trust` field it lives under.
type trustWithParticipationOutput struct {
	Trust *standingOutput `json:"trust"`
}

// standingOutput is the standing itself, which is what the mutation returns directly.
type standingOutput struct {
	Level             int  `json:"level"`
	ApprovedEdits     int  `json:"approved_edits"`
	LeaderboardListed bool `json:"leaderboard_listed"`
}

func (c *graphqlClient) leaderboard() ([]leaderboardOutput, error) {
	q := `
	query Leaderboard {
		leaderboard(input: { days: 30, limit: 100 }) {
			position
			score
			level
			user {
				id
			}
		}
	}`
	var resp struct {
		Leaderboard []leaderboardOutput
	}
	if err := c.Post(q, &resp); err != nil {
		return nil, err
	}
	return resp.Leaderboard, nil
}

func (c *graphqlClient) setLeaderboardListed(listed bool) (*standingOutput, error) {
	q := `
	mutation SetListed($listed: Boolean!) {
		setLeaderboardListed(listed: $listed) {
			level
			approved_edits
			leaderboard_listed
		}
	}`
	var resp struct {
		SetLeaderboardListed standingOutput
	}
	if err := c.Post(q, &resp, client.Var("listed", listed)); err != nil {
		return nil, err
	}
	return &resp.SetLeaderboardListed, nil
}

func (c *graphqlClient) participationOf(userID uuid.UUID) (*trustWithParticipationOutput, error) {
	q := `
	query Participation($id: ID!) {
		findUser(id: $id) {
			trust {
				level
				approved_edits
				leaderboard_listed
			}
		}
	}`
	var resp struct {
		FindUser *trustWithParticipationOutput `json:"findUser"`
	}
	if err := c.Post(q, &resp, client.Var("id", userID.String())); err != nil {
		return nil, err
	}
	if resp.FindUser == nil {
		return nil, nil
	}
	return resp.FindUser, nil
}

func onBoard(entries []leaderboardOutput, id uuid.UUID) *leaderboardOutput {
	for i, e := range entries {
		if e.User.ID == id.String() {
			return &entries[i]
		}
	}
	return nil
}

func TestLeaderboardParticipationRoundTrips(t *testing.T) {
	read := []models.RoleEnum{models.RoleEnumRead}
	member, err := asAdmin(t).createTestUser(nil, read)
	require.NoError(t, err)
	recordApprovedEdits(t, member.ID, 5)

	// THE DEFAULT IS LISTED, AND THE CASE THAT PROVES IT IS A MEMBER WITH NO ROLLUP AT ALL.
	//
	// The first version of this test asserted the default for a member with contributions -- whose flag is
	// read from the COLUMN, so flipping the mapper's default to false did not redden anything and the probe
	// survived. The trap (a bool left at Go's zero value) is only reachable where there is no row to read,
	// so the member who never contributed is the one that has to be asserted.
	idle, err := asAdmin(t).createTestUser(nil, read)
	require.NoError(t, err)
	_, err = q().GetUserTrust(t.Context(), idle.ID)
	require.Error(t, err, "fixture: this member must have no rollup row")

	idleViewer := createTestRunner(t, idle, read)
	idleStanding, err := idleViewer.client.participationOf(idle.ID)
	require.NoError(t, err)
	require.NotNil(t, idleStanding.Trust)
	assert.True(t, idleStanding.Trust.LeaderboardListed,
		"a member with no row reads as listed: the column defaults to true and Go's zero value does not")

	viewer := createTestRunner(t, member, read)

	// And for a member WITH a rollup, the value comes from the column.
	before, err := viewer.client.participationOf(member.ID)
	require.NoError(t, err)
	require.NotNil(t, before.Trust)
	assert.True(t, before.Trust.LeaderboardListed, "a member who never chose reads as listed, not as opted out")
	levelBefore := before.Trust.Level
	editsBefore := before.Trust.ApprovedEdits

	entries, err := viewer.client.leaderboard()
	require.NoError(t, err)
	entry := onBoard(entries, member.ID)
	require.NotNil(t, entry, "and appears on the board, with the user resolved by the entry's own resolver")
	assert.Equal(t, 5, entry.Score)
	assert.Equal(t, levelBefore, entry.Level, "the board's level is the standing's level")
	assert.Positive(t, entry.Position)

	// Opting out takes them off the board and changes NOTHING ELSE -- the level, the counters and the
	// standing are the same numbers on the way out.
	after, err := viewer.client.setLeaderboardListed(false)
	require.NoError(t, err)
	assert.False(t, after.LeaderboardListed, "the mutation reports the choice it recorded")
	assert.Equal(t, levelBefore, after.Level, "and the level is untouched: opting out is not a demotion")
	assert.Equal(t, editsBefore, after.ApprovedEdits, "nor the counters")

	entries, err = viewer.client.leaderboard()
	require.NoError(t, err)
	assert.Nil(t, onBoard(entries, member.ID), "and they are off the board")

	// And back, because the choice is a preference and not a door.
	relisted, err := viewer.client.setLeaderboardListed(true)
	require.NoError(t, err)
	assert.True(t, relisted.LeaderboardListed)

	entries, err = viewer.client.leaderboard()
	require.NoError(t, err)
	assert.NotNil(t, onBoard(entries, member.ID), "a member who lists again is on the board again")
}
