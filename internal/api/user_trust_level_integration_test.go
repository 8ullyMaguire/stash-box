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
	"github.com/stashapp/stash-box/internal/service/trust"
)

// The public half of a trust standing (SPEC §5.2, plan phase 6 slice 6a).
//
// THE RULE THIS CARRIES: level public, counters private. The interesting case is a STRANGER reading
// somebody ELSE, because reading your own is correct whatever the rule says.
//
// Two facts about this codebase shape every assertion below, both learned the hard way and both
// already written down by the test above it in trust_graphql_integration_test.go:
//
//  1. findUser is @hasRole(role: READ), so a user with no roles is stopped BEFORE any visibility rule
//     is reached. That failure looks exactly like a broken gate, and an absence assertion built on it
//     would pass for the wrong reason -- so the reader is created WITH Read, and the runner is given
//     it too.
//  2. @isUserOwner REJECTS the field with a GraphQL error rather than nulling it, so "the counters are
//     hidden" is asserted as A FAILED QUERY, not as a null field. Asking for both fields in one query
//     makes the whole query fail and proves nothing about the level.

type userTrustLevelOutput struct {
	ID         string `json:"id"`
	TrustLevel int    `json:"trust_level"`
}

type userTrustCountersOutput struct {
	Trust *struct {
		Level         int `json:"level"`
		ApprovedEdits int `json:"approved_edits"`
	} `json:"trust"`
}

// trustLevelOf reads a user's PUBLIC level -- the field this slice is about.
func (c *graphqlClient) trustLevelOf(userID uuid.UUID) (*userTrustLevelOutput, error) {
	q := `
	query UserTrustLevel($id: ID!) {
		findUser(id: $id) {
			id
			trust_level
		}
	}`
	var resp struct {
		FindUser *userTrustLevelOutput
	}
	if err := c.Post(q, &resp, client.Var("id", userID.String())); err != nil {
		return nil, err
	}
	return resp.FindUser, nil
}

// trustCountersOf asks for the OWNER-GATED object. For anybody but the owner this is EXPECTED to fail
// with the directive's error -- that failure IS the assertion, so the caller gets the error back
// rather than a fatal.
func (c *graphqlClient) trustCountersOf(userID uuid.UUID) (*userTrustCountersOutput, error) {
	q := `
	query UserTrustCounters($id: ID!) {
		findUser(id: $id) {
			trust {
				level
				approved_edits
			}
		}
	}`
	var resp struct {
		FindUser *userTrustCountersOutput
	}
	if err := c.Post(q, &resp, client.Var("id", userID.String())); err != nil {
		return nil, err
	}
	return resp.FindUser, nil
}

// recordApprovedEdits gives a user n points worth of applied edits, at n DISTINCT entities.
//
// Distinct because the trust store dedups per entity: three approvals of one entity are ONE event, and
// a test reusing one id would read the resulting 1 as a service bug. Same trap as
// trust_graphql_integration_test.go documents.
func recordApprovedEdits(t *testing.T, userID uuid.UUID, n int) {
	t.Helper()
	for range n {
		entityID := uuid.Must(uuid.NewV7())
		_, err := testutil.Factory().Trust().RecordEvent(t.Context(), trust.Event{
			UserID:     userID,
			Kind:       trust.KindEditApproved,
			Delta:      1,
			EntityType: "performer",
			EntityID:   &entityID,
		})
		require.NoError(t, err)
	}
}

func TestTrustLevelIsPublicAndTheCountersAreNot(t *testing.T) {
	// Read is what lets a user reach findUser at all (fact 1 above).
	read := []models.RoleEnum{models.RoleEnumRead}
	viewer, err := asAdmin(t).createTestUser(nil, read)
	require.NoError(t, err)
	subject, err := asAdmin(t).createTestUser(nil, read)
	require.NoError(t, err)
	other, err := asAdmin(t).createTestUser(nil, read)
	require.NoError(t, err)

	// The subject is put at level 3 and the other at level 1, so that "a hard-coded level" and "the
	// viewer's own level" are both WRONG ANSWERS rather than coincidences.
	const subjectEdits, otherEdits = 20, 1
	recordApprovedEdits(t, subject.ID, subjectEdits) // 200 points
	recordApprovedEdits(t, other.ID, otherEdits)     // 10 points

	wantSubject, err := testutil.Factory().Trust().Level(t.Context(), subject.ID)
	require.NoError(t, err)
	wantOther, err := testutil.Factory().Trust().Level(t.Context(), other.ID)
	require.NoError(t, err)
	require.NotEqual(t, int(wantSubject), int(wantOther),
		"fixture is wrong: the two subjects must sit at DIFFERENT levels, or a hard-coded answer passes")

	stranger := createTestRunner(t, viewer, read)

	// What the viewer's OWN level is, so the assertion below can RULE IT OUT rather than hope for it:
	// a role is granted by hand here, and a hand-granted role is not necessarily zero points.
	viewerOwn, err := stranger.client.trustLevelOf(viewer.ID)
	require.NoError(t, err)
	require.NotEqual(t, int(wantSubject), viewerOwn.TrustLevel,
		"fixture is wrong: the viewer must not already sit at the subject's level")

	// THE SUBJECT IS COUNTED BEFORE THE ABSENCE IS ASSERTED. A hidden counter proves nothing if the
	// user never resolved: this query has to succeed, and answer with a real number, first.
	got, err := stranger.client.trustLevelOf(subject.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, subject.ID.String(), got.ID, "the user resolved, so what follows is about the rule")
	assert.Equal(t, int(wantSubject), got.TrustLevel,
		"the LEVEL is public: a stranger reads what the service computes, not the viewer's own level")
	assert.NotZero(t, got.TrustLevel, "and it is a real standing, not a zeroed placeholder")

	// The other subject reads a DIFFERENT level, through the same viewer and the same query.
	elsewhere, err := stranger.client.trustLevelOf(other.ID)
	require.NoError(t, err)
	require.NotNil(t, elsewhere)
	assert.Equal(t, int(wantOther), elsewhere.TrustLevel)

	// THE COUNTERS ARE NOT: the stranger's query for them fails outright (fact 2 above).
	_, err = stranger.client.trustCountersOf(subject.ID)
	require.Error(t, err, "a stranger must not read another user's approved/rejected edits")
	assert.Contains(t, err.Error(), "authorized",
		"it must be the directive's refusal by name -- anything else means the LEVEL field is what broke")

	// THE POSITIVE CONTROL, and the half that keeps "gated" from being indistinguishable from
	// "broken": the SUBJECT reads their own standing, counters and all, in the same query.
	own, err := createTestRunner(t, subject, read).client.trustCountersOf(subject.ID)
	require.NoError(t, err)
	require.NotNil(t, own)
	require.NotNil(t, own.Trust, "the owner's own trust object must still resolve")
	assert.Equal(t, subjectEdits, own.Trust.ApprovedEdits,
		"the counters are real, to the event: the fixture's count is the assertion's, so the two cannot drift")
	assert.Equal(t, int(wantSubject), own.Trust.Level, "and both fields agree about the level")
}
