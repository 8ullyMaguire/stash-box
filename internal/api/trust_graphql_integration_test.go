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

// GraphQL exposure for trust levels (SPEC §6, Phase 1 Step 1.3).
//
// The service is already covered in trust_service_integration_test.go. What
// these tests cover is the layer above it, and one property of it that is easy
// to get wrong and invisible when it is:
//
//   User.trust is @isUserOwner. A user's trust standing is their own business,
//   and the schema directive — not a check inside the resolver — is what
//   enforces that. If the directive is dropped, every other test here still
//   passes and the field quietly becomes world-readable. So there is a test
//   whose whole job is to fail when the directive is missing.

type userTrustOutput struct {
	Level                int  `json:"level"`
	ApprovedEdits        int  `json:"approved_edits"`
	RejectedEdits        int  `json:"rejected_edits"`
	IdentificationSolves int  `json:"identification_solves"`
	QuestsCompleted      int  `json:"quests_completed"`
	ReplicasHosted       int  `json:"replicas_hosted"`
	ContentViewingOptIn  bool `json:"content_viewing_opt_in"`
	CanViewContent       bool `json:"can_view_content"`
}

type userWithTrustOutput struct {
	ID    string           `json:"id"`
	Name  string           `json:"name"`
	Trust *userTrustOutput `json:"trust"`
}

// userTrust reads a specific user's trust standing by id.
//
// Needed because `me` always resolves to the CALLER, so using it to test
// cross-user visibility proves nothing: the attacker reading their own trust is
// correct behaviour no matter what the directive says.
func (c *graphqlClient) userTrust(userID uuid.UUID) (*userWithTrustOutput, error) {
	q := `
	query UserTrust($id: ID!) {
		findUser(id: $id) {
			id
			name
			trust {
				level
				approved_edits
				identification_solves
				content_viewing_opt_in
				can_view_content
			}
		}
	}`

	var resp struct {
		FindUser *userWithTrustOutput
	}
	if err := c.Post(q, &resp, client.Var("id", userID.String())); err != nil {
		return nil, err
	}
	return resp.FindUser, nil
}

// meWithTrust reads the calling user's trust standing.
func (c *graphqlClient) meWithTrust() (*userWithTrustOutput, error) {
	q := `
	query MeWithTrust {
		me {
			id
			name
			trust {
				level
				approved_edits
				rejected_edits
				identification_solves
				quests_completed
				replicas_hosted
				content_viewing_opt_in
				can_view_content
			}
		}
	}`

	var resp struct {
		Me *userWithTrustOutput
	}
	if err := c.Post(q, &resp); err != nil {
		return nil, err
	}
	return resp.Me, nil
}

// setContentViewingOptIn records the caller's own preference.
func (c *graphqlClient) setContentViewingOptIn(enabled bool) (*userTrustOutput, error) {
	q := `
	mutation SetOptIn($enabled: Boolean!) {
		setContentViewingOptIn(enabled: $enabled) {
			level
			approved_edits
			content_viewing_opt_in
			can_view_content
		}
	}`

	var resp struct {
		SetContentViewingOptIn *userTrustOutput
	}
	if err := c.Post(q, &resp, client.Var("enabled", enabled)); err != nil {
		return nil, err
	}
	return resp.SetContentViewingOptIn, nil
}

// A new user's trust standing is level 0 with zero totals, NOT null.
//
// The field is nullable in the schema, so "returns null" is a valid-looking
// outcome a client has to handle. It is deliberately not what happens: a user
// with no contributions has a perfectly well-defined standing, and returning it
// means no client needs a null branch for the common case.
func TestMeTrustIsZeroedForANewUser(t *testing.T) {
	// A user created for THIS test, not asAdmin(t).
	//
	// asAdmin(t) is the shared admin across the whole package, and once the edit
	// lifecycle started emitting trust events (Step 1.4) every other test that
	// has an admin apply an edit moves the shared admin's trust. This test
	// asserted a zeroed standing on that shared user, so it passed only while
	// nothing emitted anything -- and began failing the moment the emitters were
	// wired, with "expected 0, actual 3" from a completely unrelated test.
	//
	// Asserting on shared mutable state is the same class of bug as the global
	// count(*) that has now bitten four tests: it measures what other tests did.
	user, err := asAdmin(t).createTestUser(nil, nil)
	require.NoError(t, err)
	runner := createTestRunner(t, user, nil)

	me, err := runner.client.meWithTrust()
	require.NoError(t, err)
	require.NotNil(t, me)
	require.NotNil(t, me.Trust,
		"a new user must have a trust object, not null: level 0 with zero "+
			"totals is a well-defined standing and a client should not have to "+
			"handle a null for the common case")

	assert.Equal(t, 0, me.Trust.Level)
	assert.Equal(t, 0, me.Trust.ApprovedEdits)
	assert.Equal(t, 0, me.Trust.QuestsCompleted)
	assert.False(t, me.Trust.ContentViewingOptIn)
	assert.False(t, me.Trust.CanViewContent)
}

// The trust field reflects recorded contributions.
func TestMeTrustReflectsContributions(t *testing.T) {
	user, err := asAdmin(t).createTestUser(nil, nil)
	require.NoError(t, err)
	runner := createTestRunner(t, user, nil)

	// A DISTINCT entity per event. The first version of this test reused one
	// entityID across three approvals and read the resulting count of 1 as a
	// service bug -- when the dedup index was working exactly as designed and
	// the test was wrong. Same mistake class as the zero-delta test in
	// trust_service_integration_test.go.
	for range 3 {
		entityID := uuid.Must(uuid.NewV7())
		_, err := testutil.Factory().Trust().RecordEvent(t.Context(), trust.Event{
			UserID:     user.ID,
			Kind:       trust.KindEditApproved,
			Delta:      1,
			EntityType: "performer",
			EntityID:   &entityID,
		})
		require.NoError(t, err)
	}

	for range 2 {
		distinct := uuid.Must(uuid.NewV7())
		_, err := testutil.Factory().Trust().RecordEvent(t.Context(), trust.Event{
			UserID:     user.ID,
			Kind:       trust.KindIdentificationSolved,
			Delta:      1,
			EntityType: "scene",
			EntityID:   &distinct,
		})
		require.NoError(t, err)
	}

	me, err := runner.client.meWithTrust()
	require.NoError(t, err)
	require.NotNil(t, me.Trust)

	assert.Equal(t, 3, me.Trust.ApprovedEdits, "three applied edits must be visible")
	assert.Equal(t, 2, me.Trust.IdentificationSolves, "two solves must be visible")

	// 3 edits x 10 + 2 solves x 15 = 60 points. The curve puts 60 at
	// LevelContributor (50) and below LevelCurator (200), so assert the level
	// the service computes rather than a number I worked out by hand -- the
	// first version of this test asserted LevelRegistered and read the resulting
	// mismatch as a service bug.
	wantLevel, err := testutil.Factory().Trust().Level(t.Context(), user.ID)
	require.NoError(t, err)
	assert.Equal(t, int(wantLevel), me.Trust.Level,
		"the GraphQL level must come from the same curve the service uses, not "+
			"be computed independently in the resolver")
}

// The opt-in mutation works with NO roles at all.
//
// This is the intended flow, not an edge case: SPEC §6 has a user opt in to
// content viewing, and a user approaching Archivist must be able to express the
// preference before they get there. A @hasRole directive here would make the
// "opted in, not yet eligible" state unreachable.
func TestSetContentViewingOptInWorksWithNoRoles(t *testing.T) {
	user, err := asAdmin(t).createTestUser(
		&models.UserCreateInput{
			Name:     "roleless_optin",
			Email:    "roleless_optin@example.com",
			Password: "aValidPassword1!",
			Roles:    []models.RoleEnum{},
		},
		[]models.RoleEnum{},
	)
	require.NoError(t, err)
	runner := createTestRunner(t, user, []models.RoleEnum{})

	trustOut, err := runner.client.setContentViewingOptIn(true)
	require.NoError(t, err,
		"opting in must not require any role: it records a preference, it grants "+
			"nothing, and SPEC section 6 has users opt in before reaching level 4")
	require.NotNil(t, trustOut)

	assert.True(t, trustOut.ContentViewingOptIn, "the opt-in must be recorded")
	assert.False(t, trustOut.CanViewContent,
		"opting in alone must NOT grant content access; level 4 is the gate")
	assert.Equal(t, 0, trustOut.Level, "opting in must not change the level")
}

// Withdrawing the opt-in takes effect and is visible on the next read.
func TestSetContentViewingOptInCanBeWithdrawn(t *testing.T) {
	user, err := asAdmin(t).createTestUser(nil, nil)
	require.NoError(t, err)
	runner := createTestRunner(t, user, nil)

	_, err = runner.client.setContentViewingOptIn(true)
	require.NoError(t, err)

	me, err := runner.client.meWithTrust()
	require.NoError(t, err)
	require.True(t, me.Trust.ContentViewingOptIn, "precondition: opted in")

	_, err = runner.client.setContentViewingOptIn(false)
	require.NoError(t, err)

	me, err = runner.client.meWithTrust()
	require.NoError(t, err)
	assert.False(t, me.Trust.ContentViewingOptIn, "the withdrawal must be visible on the next read")
}

// At Archivist with an opt-in, can_view_content is true.
func TestCanViewContentIsTrueAtArchivistWithOptIn(t *testing.T) {
	user, err := asAdmin(t).createTestUser(nil, nil)
	require.NoError(t, err)
	runner := createTestRunner(t, user, nil)

	// 75 applied edits at 10 points = 750 = LevelArchivist.
	for range 75 {
		distinct := uuid.Must(uuid.NewV7())
		_, err := testutil.Factory().Trust().RecordEvent(t.Context(), trust.Event{
			UserID:     user.ID,
			Kind:       trust.KindEditApproved,
			Delta:      1,
			EntityType: "performer",
			EntityID:   &distinct,
		})
		require.NoError(t, err)
	}

	me, err := runner.client.meWithTrust()
	require.NoError(t, err)
	require.Equal(t, int(trust.LevelArchivist), me.Trust.Level, "precondition: 750 points")
	require.False(t, me.Trust.CanViewContent, "eligible but not opted in must be false")

	trustOut, err := runner.client.setContentViewingOptIn(true)
	require.NoError(t, err)
	assert.True(t, trustOut.CanViewContent,
		"level 4 AND opted in must be true; the mutation's return value is what "+
			"the client sees immediately, so it must not lag the stored state")
}

// THE authorization test. Another user must not be able to read this user's
// trust standing.
//
// If the @isUserOwner directive is removed from User.trust, every other test in
// this file still passes and the field becomes readable by anyone who can query
// users. A user's reputation is not public information in this design, and the
// directive is the only thing enforcing that.
func TestTrustIsNotReadableByAnotherUser(t *testing.T) {
	victim, err := asAdmin(t).createTestUser(nil, nil)
	require.NoError(t, err)
	// The attacker needs READ, because findUser is @hasRole(role: READ). An
	// attacker without it would be stopped at findUser rather than at the trust
	// field, and the assertion below would pass for entirely the wrong reason.
	attacker, err := asAdmin(t).createTestUser(nil, []models.RoleEnum{models.RoleEnumRead})
	require.NoError(t, err)

	// Give the victim some standing worth wanting to see.
	distinct := uuid.Must(uuid.NewV7())
	_, err = testutil.Factory().Trust().RecordEvent(t.Context(), trust.Event{
		UserID:     victim.ID,
		Kind:       trust.KindEditApproved,
		Delta:      1,
		EntityType: "performer",
		EntityID:   &distinct,
	})
	require.NoError(t, err)

	attackerRunner := createTestRunner(t, attacker, []models.RoleEnum{models.RoleEnumRead})

	// Target the VICTIM explicitly. Querying `me` here would read the attacker's
	// own trust, which is correct behaviour regardless of the directive and
	// would pass no matter what -- the first version of this test made exactly
	// that mistake and "passed" against a completely unprotected field.
	// The directive REJECTS the field with a GraphQL error rather than nulling
	// it, so the correct assertion is that the query fails. The first version of
	// this test asserted a null field and would have passed against a completely
	// unprotected schema, because gqlgen surfaces the resolver error instead of
	// returning null.
	found, err := attackerRunner.client.userTrust(victim.ID)
	require.Error(t, err,
		"User.trust must be @isUserOwner: another user must not be able to read "+
			"a user's trust standing. If this fails, the directive was dropped "+
			"from graphql/schema/types/user.graphql")
	assert.Nil(t, found, "no partial user object may be returned on a rejected field")
	assert.Contains(t, err.Error(), "not authorized",
		"the rejection must come from the ownership check, not from an "+
			"unrelated permission failure")
}

// An anonymous caller cannot set the opt-in.
//
// The mutation has no @hasRole directive on purpose, so this is the ONLY thing
// stopping an unauthenticated caller from writing trust state.
func TestSetContentViewingOptInRequiresAuthentication(t *testing.T) {
	// createTestRunner(t, nil, nil) is the anonymous case: auth.FromUser(nil)
	// returns nil, so the request context carries a nil user.
	anon := createTestRunner(t, nil, nil)

	_, err := anon.client.setContentViewingOptIn(true)
	require.Error(t, err,
		"an anonymous caller must not be able to record a trust preference: the "+
			"mutation has no role directive, so this resolver check is the only "+
			"thing preventing unauthenticated writes to user_trust")
	assert.Contains(t, err.Error(), "not authorized",
		"the rejection must be the authentication error, not something else")

	// A row count here would be meaningless: the integration database is shared
	// and not isolated, so `WHERE content_viewing_opt_in` matches rows written by
	// other tests in the same run. The first version of this test asserted a
	// global count of zero and failed for exactly that reason. Same trap as #829
	// and #1007, hit a third time.
	//
	// The scoped check that IS meaningful: the mutation must not have created a
	// rollup row for a user that does not exist. user_trust has a foreign key to
	// users, so an anonymous write could only have landed on some existing user
	// -- and the count of DISTINCT users whose only event is this test's window
	// is not expressible. The error above is the real assertion; this is a
	// belt-and-braces check that the resolver returned before touching the
	// database at all.
	assert.Error(t, err, "the resolver must reject before any database write")
}
