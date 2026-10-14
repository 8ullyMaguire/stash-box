//go:build integration

package api_test

import (
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/models"
)

// The wiring of the contribution heatmap (plan phase 6 slice 6e).
//
// The field is bound by gqlgen and gated by @isUserOwner, and a directive lives in GENERATED code -- which
// is the trap the trust-level test next door paid for: the harness alone would leave an unapplied
// directive inert, and every unit test would still pass. So the gate is proven the only way it can be,
// through the real handler.
//
// THE PROOF IS THE PAIR, not the failure: the owner's query on `contributionHeatmap` has to SUCCEED with
// real numbers, and the same query against the same id from a different member has to fail. A field that
// did not exist would fail for both, and asserting only the failure would call that a working gate.

type heatmapDayOutput struct {
	Day   string `json:"day"`
	Count int    `json:"count"`
}

func (c *graphqlClient) contributionHeatmapOf(userID uuid.UUID, days int) ([]heatmapDayOutput, error) {
	q := `
	query ContributionHeatmap($id: ID!, $days: Int) {
		findUser(id: $id) {
			contributionHeatmap(days: $days) {
				day
				count
			}
		}
	}`
	var resp struct {
		FindUser *struct {
			ContributionHeatmap []heatmapDayOutput `json:"contributionHeatmap"`
		}
	}
	if err := c.Post(q, &resp, client.Var("id", userID.String()), client.Var("days", days)); err != nil {
		return nil, err
	}
	if resp.FindUser == nil {
		return nil, nil
	}
	return resp.FindUser.ContributionHeatmap, nil
}

func TestContributionHeatmapIsOwnerGated(t *testing.T) {
	// Read is what lets a member reach findUser at all, so both actors have it: without it the stranger
	// would be refused before any visibility rule was reached, and the gate would look correct for a
	// reason that has nothing to do with it.
	read := []models.RoleEnum{models.RoleEnumRead}
	member, err := asAdmin(t).createTestUser(nil, read)
	require.NoError(t, err)
	stranger, err := asAdmin(t).createTestUser(nil, read)
	require.NoError(t, err)

	// Two contributions today, stamped by the DATABASE's clock -- the heatmap's window ends on that day,
	// not on a day this test picks.
	for range 2 {
		entityID := uuid.Must(uuid.NewV7())
		_, err := testutil.DB().Exec(t.Context(),
			`INSERT INTO trust_events (user_id, kind, entity_type, entity_id, delta, created_at)
			 VALUES ($1, 'edit_approved', 'performer', $2, 1, now())`,
			member.ID, entityID)
		require.NoError(t, err, "recording a contribution")
	}

	own, err := createTestRunner(t, member, read).client.contributionHeatmapOf(member.ID, 7)
	require.NoError(t, err, "the member reads their own heatmap")
	require.Len(t, own, 1, "one day of activity")
	assert.Equal(t, 2, own[0].Count, "and the count is the real number, not a placeholder")

	_, err = createTestRunner(t, stranger, read).client.contributionHeatmapOf(member.ID, 7)
	require.Error(t, err, "a stranger asking for the same member's heatmap is refused")
}
