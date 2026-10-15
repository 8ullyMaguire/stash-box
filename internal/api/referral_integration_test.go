//go:build integration

package api_test

import (
	"strings"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/config"
	"github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/invite"
	"github.com/stashapp/stash-box/internal/service/trust"
)

// The referral payment (SPEC §7, plan phase 11 slice 11b).
//
// THE RULE: the member who invited somebody is paid ONCE, when that invitee first reaches
// Registered -- never at signup, never twice, and never for a chain.
//
// THE FIXTURE SIGNS THE INVITEE UP THROUGH THE REAL PATH (a key, then activation), because that is
// the only path that writes `users.invited_by` and half of this slice's finding is that it did not
// write it at all unless the instance required invites. A fixture that set the column with an
// UPDATE would pass while the feature was inert on every instance that does not require them --
// which is every instance by default, including this one.
//
// The trigger under test is the REAL one: an event recorded through the trust service's
// RecordEvent, which is what an applied edit does. Nothing here calls the payment directly except
// where it asserts a REFUSAL by name, and every count is read from the referrer's own rollup.

// inviteeName is unique per call, so a shared database cannot make one run's fixture the next
// run's assertion (see the note on stale twins in the mutation-gate notes).
//
// The TRAILING characters of a v7 are used, not the leading ones: a v7's first 8 hex digits are a
// millisecond timestamp, so two calls in the same millisecond produce the same prefix -- which is
// what "email already in use" was, from a fixture that looked random.
func inviteeName() string {
	return "invited-" + strings.ReplaceAll(uuid.Must(uuid.NewV7()).String(), "-", "")[20:]
}

// signedUpInvitee creates a member through the invite path: the referrer mints a key, the invitee
// registers with it and activates, and the referrer's id is what the activation recorded.
func signedUpInvitee(t *testing.T, referrer uuid.UUID) *models.User {
	t.Helper()

	grantInvite(t, referrer, 1)

	referrerRunner := runnerFor(t, referrer)
	keys, err := referrerRunner.client.generateInviteKeys(1)
	require.NoError(t, err)
	require.Len(t, keys, 1)

	keyID, err := uuid.FromString(keys[0])
	require.NoError(t, err)

	name := inviteeName()
	activationKey, err := asAdmin(t).resolver.Mutation().NewUser(t.Context(), models.NewUserInput{
		Email:     name + "@example.com",
		InviteKey: &keyID,
	})
	require.NoError(t, err, "registering with a key must succeed")
	require.NotNil(t, activationKey,
		"the fixture needs the activation key back, which this instance returns when activation "+
			"is not required; without it there is no way to finish the signup")

	invitee, err := asAdmin(t).resolver.Mutation().ActivateNewUser(t.Context(), models.ActivateNewUserInput{
		Name:          name,
		ActivationKey: *activationKey,
		Password:      "password" + name,
	})
	require.NoError(t, err, "activating must succeed")
	require.NotNil(t, invitee)

	return invitee
}

// runnerFor builds a runner acting as an existing member.
func runnerFor(t *testing.T, userID uuid.UUID) *testRunner {
	t.Helper()
	svc := testutil.Factory().User()
	user, err := svc.FindByID(t.Context(), userID)
	require.NoError(t, err)
	roles := []models.RoleEnum{models.RoleEnumRead}
	return createTestRunner(t, user, roles)
}

// referralCount is how many invitees this member has been paid for, read from their rollup.
//
// A member with no rollup row has been paid for nobody and this reports 0 -- which is the honest
// reading, since the row is created by the payment itself. requirePaidReferrer is the assertion
// that distinguishes "paid for nobody" from "no rollup at all", and it is used wherever the point
// is that a payment DID happen.
func referralCount(t *testing.T, userID uuid.UUID) int {
	t.Helper()
	rollup, err := trustService(t).Rollup(t.Context(), userID)
	require.NoError(t, err)
	if rollup == nil {
		return 0
	}
	return rollup.InvitesActivated
}

// referralEvents lists the invite_activated events a member has, so an assertion can name WHICH
// invitee the payment was for rather than only counting.
func referralEvents(t *testing.T, userID uuid.UUID) []queries.TrustEvent {
	t.Helper()
	events, err := trustService(t).History(t.Context(), userID)
	require.NoError(t, err)

	var referrals []queries.TrustEvent
	for _, e := range events {
		if e.Kind == string(trust.KindInviteActivated) {
			referrals = append(referrals, e)
		}
	}
	return referrals
}

// contribute records what an applied edit records: one counted event for the member.
func contribute(t *testing.T, userID uuid.UUID) {
	t.Helper()
	recordEvent(t, userID, trust.KindEditApproved)
}

// contributeRejected records what a rejected edit records: one point, which moves the member's
// total WITHOUT carrying them to Registered.
//
// It exists because "the invitee has contributed and is STILL below the threshold" is a different
// state from "the invitee has contributed nothing", and only the first one can test the
// eligibility rule at all: an account with no events never reaches the payment code, so a fixture
// that stopped there would pass with the rule deleted.
func contributeRejected(t *testing.T, userID uuid.UUID) {
	t.Helper()
	recordEvent(t, userID, trust.KindEditRejected)
}

func recordEvent(t *testing.T, userID uuid.UUID, kind trust.KindEnum) {
	t.Helper()
	entityID := uuid.Must(uuid.NewV7())
	_, err := trustService(t).RecordEvent(t.Context(), trust.Event{
		UserID:     userID,
		Kind:       kind,
		Delta:      1,
		EntityType: "edit",
		EntityID:   &entityID,
	})
	require.NoError(t, err)
}

// THE INVITER IS RECORDED EVEN WHEN THE INSTANCE DOES NOT REQUIRE INVITES.
//
// This is the half of the slice that makes the other half reachable. The column used to be written
// only under `require_invite`, while the key's use was consumed either way -- so on the default
// configuration a signup paid with a key spent the key and recorded nobody, and a referral payment
// built on this column would have been a feature that pays nothing.
//
// The configuration assertion is the important line: without it this test would pass on an
// instance that requires invites, for the reason the defect depended on, and would prove nothing
// about the case that was broken.
func TestSigningUpWithAKeyRecordsTheInviterWithoutRequiringInvites(t *testing.T) {
	// THIS SUITE RUNS WITH require_invite ON, and it has to be turned off for this one test: with
	// it on, the code this slice fixed recorded the inviter anyway, so a fixture that ran under it
	// would prove nothing about the configuration that was broken. There is no second
	// configuration in the suite, so the flag is set here and restored on cleanup (config.C is
	// read per call, and this package's tests run sequentially against one database).
	require.True(t, config.GetRequireInvite(),
		"the opposite of what this env used to be: if invites stopped being required here, the "+
			"flip below is what needs revisiting")
	previous := config.C.RequireInvite
	config.C.RequireInvite = false
	t.Cleanup(func() { config.C.RequireInvite = previous })

	referrer, err := asAdmin(t).createTestUser(nil, []models.RoleEnum{models.RoleEnumRead})
	require.NoError(t, err)

	invitee := signedUpInvitee(t, referrer.ID)

	var invitedBy *uuid.UUID
	require.NoError(t, testutil.DB().QueryRow(t.Context(),
		`SELECT invited_by FROM users WHERE id = $1`, invitee.ID).Scan(&invitedBy))
	require.NotNil(t, invitedBy, "the signup carried a key, so somebody invited this member")
	assert.Equal(t, referrer.ID.String(), invitedBy.String(),
		"the inviter is the key's generator, which is the fact the referral payment reads")
}

// NOTHING AT SIGNUP, ONCE AT REGISTERED, AND NOTHING MORE AFTERWARDS.
//
// The rule the plan states, and the three fixtures that make each half of it falsifiable: an
// invitee who has registered and done nothing pays nothing; one applied edit later the referrer is
// paid; a second contribution does not pay again. The payment is asserted BY IDENTITY (which
// invitee the event is for) as well as by count, because a count alone cannot tell a correct
// payment from a payment keyed to the wrong row.
func TestAReferralPaysNothingAtSignupAndOnceAtRegistered(t *testing.T) {
	referrer, err := asAdmin(t).createTestUser(nil, []models.RoleEnum{models.RoleEnumRead})
	require.NoError(t, err)

	invitee := signedUpInvitee(t, referrer.ID)

	assert.Zero(t, referralCount(t, referrer.ID),
		"registering is not growth: an account that has contributed nothing pays nobody, "+
			"which is also why the payment cannot be farmed with email addresses")

	// AND THE STATE THAT CAN ACTUALLY FAIL: an invitee who HAS contributed and is STILL below the
	// threshold. The assertion above cannot catch a missing eligibility rule -- an account with no
	// events never reaches the payment code at all -- so a rejected edit (one point, still Public)
	// is what makes "not yet eligible" a claim the test can lose.
	contributeRejected(t, invitee.ID)

	inviteeRollup, err := trustService(t).Rollup(t.Context(), invitee.ID)
	require.NoError(t, err)
	require.NotNil(t, inviteeRollup)
	require.Equal(t, int(trust.LevelPublic), inviteeRollup.Level,
		"the fixture must leave the invitee BELOW Registered, or there is no eligibility to test")

	assert.Zero(t, referralCount(t, referrer.ID),
		"a contribution that does not reach Registered pays nothing: the payment is for a member "+
			"becoming real, not for them having tried")

	contribute(t, invitee.ID)

	assert.Equal(t, 1, referralCount(t, referrer.ID),
		"one applied edit by the invitee reaches Registered, which is when the referrer is paid")

	referrals := referralEvents(t, referrer.ID)
	require.Len(t, referrals, 1, "exactly one payment event")
	require.NotNil(t, referrals[0].EntityID, "the payment is keyed to the invitee it was for")
	assert.Equal(t, invitee.ID.String(), referrals[0].EntityID.UUID.String(),
		"the dedup key is the INVITEE, so one referrer can be paid for each of many")

	totals, err := trustService(t).TotalsFor(t.Context(), referrer.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, totals.InvitesActivated)
	assert.Equal(t, trust.PointsPerKind[trust.KindInviteActivated], totals.Points(),
		"the payment is count x weight like every other contribution, so the referrer's own "+
			"standing moves by exactly one referral's worth")

	contribute(t, invitee.ID)

	assert.Equal(t, 1, referralCount(t, referrer.ID),
		"a second contribution by the same invitee pays nothing more: the payment is for the "+
			"invitee reaching Registered, not for everything they do afterwards")
	assert.Len(t, referralEvents(t, referrer.ID), 1, "and there is still one event, not two")
}

// A REFERRER IS PAID FOR EACH INVITEE, which is the other half of the dedup key: keyed to the
// invitee, so a second member brought in by the same member is a second payment.
//
// Without this test the key could be keyed to the REFERRER and every assertion above would still
// pass -- one payment, once, forever, for the first invitee and nobody after them.
func TestAReferrerIsPaidForEachInviteeTheyBringIn(t *testing.T) {
	referrer, err := asAdmin(t).createTestUser(nil, []models.RoleEnum{models.RoleEnumRead})
	require.NoError(t, err)

	first := signedUpInvitee(t, referrer.ID)
	second := signedUpInvitee(t, referrer.ID)
	require.NotEqual(t, first.ID.String(), second.ID.String())

	contribute(t, first.ID)
	assert.Equal(t, 1, referralCount(t, referrer.ID))

	contribute(t, second.ID)
	assert.Equal(t, 2, referralCount(t, referrer.ID),
		"two members brought in and made real are two payments")

	keyed := map[string]bool{}
	for _, e := range referralEvents(t, referrer.ID) {
		require.NotNil(t, e.EntityID)
		keyed[e.EntityID.UUID.String()] = true
	}
	assert.True(t, keyed[first.ID.String()], "the first invitee's payment is on record")
	assert.True(t, keyed[second.ID.String()], "and the second's, as its own event")
}

// A LEVEL THAT OSCILLATES PAYS ONCE.
//
// The level is recomputed on every contributing event and can move down through decay and back up,
// so "the transition into Registered" is not a state that can be tracked by watching for a change:
// a member who decays and climbs pays the same single payment. The decayed state is written
// directly, which is the state the sweep produces (level down, shortfall recorded).
func TestAReferredReferralSurvivesADecayRoundTrip(t *testing.T) {
	referrer, err := asAdmin(t).createTestUser(nil, []models.RoleEnum{models.RoleEnumRead})
	require.NoError(t, err)

	invitee := signedUpInvitee(t, referrer.ID)
	contribute(t, invitee.ID)
	require.Equal(t, 1, referralCount(t, referrer.ID))

	// The decayed state, written as the machinery produces it: the level taken away, the demotion
	// recorded with the level it came from, and the recovery window ALREADY CLOSED (fifteen days
	// back, against a fourteen-day window). The window matters -- a member inside it is deliberately
	// held at the lower level, and a fixture that left the window open would test that hold rather
	// than the climb back.
	_, err = testutil.DB().Exec(t.Context(),
		`UPDATE user_trust
		 SET level = 0, short_since = NULL, demoted_at = now() - interval '15 days', demoted_from_level = 1
		 WHERE user_id = $1`, invitee.ID)
	require.NoError(t, err)

	decayed, err := trustService(t).Rollup(t.Context(), invitee.ID)
	require.NoError(t, err)
	require.NotNil(t, decayed)
	require.Zero(t, decayed.Level,
		"the fixture must actually have taken the invitee's level away, or there is no climb back")

	// And the climb: another applied edit recomputes the level from the totals, which bring it back
	// to Registered -- so the payment IS asked for a second time, and the dedup key is what refuses.
	contribute(t, invitee.ID)

	rollup, err := trustService(t).Rollup(t.Context(), invitee.ID)
	require.NoError(t, err)
	require.NotNil(t, rollup)
	require.Equal(t, int(trust.LevelRegistered), rollup.Level,
		"the fixture must have brought the invitee back to Registered, or the second payment "+
			"attempt never happened and this test proves nothing")

	assert.Equal(t, 1, referralCount(t, referrer.ID),
		"the payment is keyed to the invitee, so a level that decays and climbs back pays once")
	assert.Len(t, referralEvents(t, referrer.ID), 1)
}

// A SELF-INVITED MEMBER PAYS NOBODY, REFUSED BY NAME.
//
// The fixture writes the invitee's own id into invited_by, which is the state a direct database
// edit -- or any future field that lets an operator set an inviter -- produces. Nothing in the
// signup path can construct it, which is exactly why the guard has to be tested here: it is a
// guard against a state the schema permits and the happy path does not create.
//
// The REFUSAL is asserted by name, through the same decision the payment uses, because "no payment
// happened" is also what "the feature is unwired" looks like.
func TestASelfInvitedMemberPaysNobody(t *testing.T) {
	member, err := asAdmin(t).createTestUser(nil, []models.RoleEnum{models.RoleEnumRead})
	require.NoError(t, err)

	_, err = testutil.DB().Exec(t.Context(),
		`UPDATE users SET invited_by = $1 WHERE id = $1`, member.ID)
	require.NoError(t, err)

	outcome, err := invite.NewReferral(queries.New(testutil.DB()), trustService(t)).
		Settle(t.Context(), member.ID, trust.LevelRegistered)
	require.NoError(t, err)
	assert.Equal(t, invite.OutcomeSelfInvited, outcome,
		"inviting yourself is refused by name: it is the cheapest way to farm this")

	contribute(t, member.ID)

	assert.Zero(t, referralCount(t, member.ID),
		"the member has no invitees, so their own rollup stays at zero")
	assert.Empty(t, referralEvents(t, member.ID),
		"and no payment event was recorded for them")
	require.NotNil(t, member)
}

// THE CHAIN IS ONE LEVEL DEEP, ASSERTED BY IDENTITY.
//
// A invitee who reaches Registered pays the member who invited them -- and if THAT payment is what
// carries the referrer to Registered, the referrer's own inviter must still be paid nothing.
// Otherwise a chain of accounts pays every ancestor, which is the shape the plan refuses.
//
// The assertion is on the grandparent's id by name: 0 for them, 1 for the middle member, so a test
// that accidentally measured the wrong member could not pass.
func TestTheReferralChainIsOneLevelDeep(t *testing.T) {
	grandparent, err := asAdmin(t).createTestUser(nil, []models.RoleEnum{models.RoleEnumRead})
	require.NoError(t, err)

	// The grandparent contributes FIRST, and the reason is that the fixture has to make the two
	// behaviours DIFFERENT: a chain-following payment would only reach them if they were at
	// Registered-or-above when the middle member was paid. A grandparent sitting at Public would be
	// skipped by the payment's own level check, so the mutant would be unobservable and the test
	// would pass for a reason that has nothing to do with depth.
	contribute(t, grandparent.ID)

	parent := signedUpInvitee(t, grandparent.ID)
	child := signedUpInvitee(t, parent.ID)

	contribute(t, child.ID)

	assert.Equal(t, 1, referralCount(t, parent.ID),
		"the member who invited the invitee is paid")

	// The parent's own payment is itself a trust event, and it may well have carried them to
	// Registered -- that is the case this test is for, and the fixture asserts it happened rather
	// than assuming it.
	parentRollup, err := trustService(t).Rollup(t.Context(), parent.ID)
	require.NoError(t, err)
	require.NotNil(t, parentRollup)
	require.GreaterOrEqual(t, parentRollup.Level, int(trust.LevelRegistered),
		"the fixture must have carried the parent to Registered through the payment itself, or "+
			"the grandparent was never at risk of being paid")

	grandparentRollup, err := trustService(t).Rollup(t.Context(), grandparent.ID)
	require.NoError(t, err)
	require.NotNil(t, grandparentRollup)
	require.GreaterOrEqual(t, grandparentRollup.Level, int(trust.LevelRegistered),
		"and the grandparent must be above the threshold too, or a chain-following payment would "+
			"skip them for the wrong reason: the fixture has to make the two behaviours differ")

	assert.Zero(t, referralCount(t, grandparent.ID),
		"the grandparent is NOT paid: the payment's own event must not trigger another payment, "+
			"which is what keeps a chain of accounts from paying every ancestor")
	assert.Empty(t, referralEvents(t, grandparent.ID))
}

// THE COUNT IS PUBLISHED, so a member can see what their invitations produced.
//
// The field is on the owner-gated `trust` object, so this also pins that the new field did not
// arrive without the directive that protects the rest of the object.
func TestTheReferralCountIsPublishedOnTheOwnersTrustObject(t *testing.T) {
	referrer, runner := inviteMember(t)

	invitee := signedUpInvitee(t, referrer.ID)
	contribute(t, invitee.ID)

	q := `
	query MeWithReferrals {
		me {
			id
			trust {
				invites_activated
			}
		}
	}`
	var resp struct {
		Me *struct {
			ID    string `json:"id"`
			Trust *struct {
				InvitesActivated int `json:"invites_activated"`
			} `json:"trust"`
		} `json:"me"`
	}
	require.NoError(t, runner.client.Post(q, &resp))
	require.NotNil(t, resp.Me, "me must resolve for the member")
	require.NotNil(t, resp.Me.Trust, "and so must their own trust object")
	assert.Equal(t, 1, resp.Me.Trust.InvitesActivated)

	// And it is still owner-gated: another member reading THIS member's trust is refused by the
	// directive rather than being shown the number.
	other := runnerFor(t, invitee.ID)
	_, err := other.client.userTrust(referrer.ID)
	assert.Error(t, err, "trust is owner-and-admin only, and a new field on it must not open it")
}
