package invite

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/stashapp/stash-box/internal/service/trust"
)

// The referral payment's RULE, with no database in it (plan phase 11 slice 11b).
//
// The rule is four lines long and three of its four answers are refusals, which is why they are
// enumerated rather than collapsed into a bool: a caller that cannot tell "has not earned it yet"
// from "invited themselves" cannot act on either, and a test cannot assert the right refusal. Every
// refusal here has a fixture of its own for that reason -- "no payment was made" is true of all
// three, and only one of them is a farm attempt.

func ptr(id uuid.UUID) *uuid.UUID { return &id }

// TestDecideReferralNamesEveryAnswer pins the rule as a table, including the ORDER of the checks.
func TestDecideReferralNamesEveryAnswer(t *testing.T) {
	invitee := uuid.Must(uuid.NewV7())
	referrer := uuid.Must(uuid.NewV7())

	cases := []struct {
		name      string
		level     trust.LevelEnum
		invitedBy *uuid.UUID
		want      Outcome
		why       string
	}{
		{
			name:      "public with a referrer",
			level:     trust.LevelPublic,
			invitedBy: ptr(referrer),
			want:      OutcomeNotYetEligible,
			why: "signup is not growth: an account that registers and leaves must pay nobody, " +
				"and a payment at signup is a payment for an email address",
		},
		{
			name:      "registered with no referrer",
			level:     trust.LevelRegistered,
			invitedBy: nil,
			want:      OutcomeNoReferrer,
			why: "an operator-created account, or one whose inviter was deleted " +
				"(users.invited_by is ON DELETE SET NULL): nobody to pay, which is the truth " +
				"rather than an error",
		},
		{
			name:      "registered and self-invited",
			level:     trust.LevelRegistered,
			invitedBy: ptr(invitee),
			want:      OutcomeSelfInvited,
			why: "the cheapest way to farm this: nothing in the schema stops a key from being " +
				"activated on its own generator's account",
		},
		{
			name:      "registered with another member as referrer",
			level:     trust.LevelRegistered,
			invitedBy: ptr(referrer),
			want:      OutcomeCredited,
			why:       "the case the feature exists for",
		},
		{
			name:      "contributor with another member as referrer",
			level:     trust.LevelContributor,
			invitedBy: ptr(referrer),
			want:      OutcomeCredited,
			why: "the threshold is Registered-OR-ABOVE, so a member who arrives already above it " +
				"(a threshold change, a restored rollup) is still paid for",
		},
		{
			name:      "public and self-invited",
			level:     trust.LevelPublic,
			invitedBy: ptr(invitee),
			want:      OutcomeNotYetEligible,
			why: "ORDER: eligibility is decided first, so a self-invited member who has not " +
				"reached Registered is refused for the reason that comes first, not the one " +
				"that happens to be checked second",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DecideReferral(tc.level, invitee, tc.invitedBy)
			assert.Equal(t, tc.want, got, tc.why)
		})
	}
}

// TestEveryOutcomeNamesItself: an outcome that prints as "unknown" in a test failure or a log line
// is an outcome nobody can act on, and the zero value is a real answer (not eligible yet) rather
// than a missing one, so the naming test has to cover it too.
func TestEveryOutcomeNamesItself(t *testing.T) {
	seen := map[string]bool{}
	for _, o := range []Outcome{OutcomeNotYetEligible, OutcomeNoReferrer, OutcomeSelfInvited, OutcomeCredited} {
		name := o.String()
		assert.NotEqual(t, "unknown", name, "outcome %d has no name", int(o))
		assert.False(t, seen[name], "outcome name %q is used twice", name)
		seen[name] = true
	}
	assert.Equal(t, "unknown", Outcome(99).String(), "an out-of-range outcome says so")
}

// TestTheReferralWeightCannotSubstituteForContributing pins the NUMBER as a rule rather than as a
// value: what a referral is worth decides whether inviting is a shortcut past contributing.
//
// Two assertions, one for each direction the number could be wrong in. Too high and a member buys
// their way to Contributor by inviting people; too low and the referral is a gesture that does not
// move anything, which the plan's own argument (an invitee who reaches Registered has by definition
// contributed) rules out.
func TestTheReferralWeightCannotSubstituteForContributing(t *testing.T) {
	weight, ok := trust.PointsPerKind[trust.KindInviteActivated]
	assert.True(t, ok, "the referral kind must have a weight, or the rollup counts it for nothing")
	assert.GreaterOrEqual(t, weight, trust.PointsPerKind[trust.KindEditApproved],
		"a referral must be worth at least one applied edit: it has one of those behind it, "+
			"plus the member who made it")

	// The upper bound is expressed through the CURVE, not as a literal, so a threshold change
	// re-derives what the sentence means instead of leaving a stale number in a comment.
	assert.Equal(t, trust.LevelRegistered, trust.LevelForPoints(weight),
		"one referral may not reach the level above the one it is paid at, and it may not be "+
			"worth so little that it leaves the invitee's referrer at Public")
	assert.NotEqual(t, trust.LevelContributor, trust.LevelForPoints(weight),
		"one referral must not make a Contributor out of a member who has contributed nothing")
}

// TestTheReferralKindIsCountedAndScored: the kind has to be in BOTH sets, and they mean different
// things -- AllKinds is what the rollup sums over (so the count is real and answerable) and
// PointsPerKind is what it is worth. A kind in one and not the other is either a counted
// contribution earning nothing or a weight that no column holds.
//
// TestEveryKindHasPoints next door enforces the same pairing across the whole vocabulary; this
// names the kind and says why it is on the counted side rather than the points-only one, which is
// the decision a future reader is most likely to get wrong (the bounty kind next door is the
// precedent for points-only).
func TestTheReferralKindIsCountedAndScored(t *testing.T) {
	assert.Contains(t, trust.AllKinds, trust.KindInviteActivated,
		"invite_activated is a COUNTED kind: the number of people a member brought in is a fact "+
			"about that member that nothing else answers")
	assert.Contains(t, trust.PointsPerKind, trust.KindInviteActivated,
		"and it is scored, so the payment is count x weight like every other contribution")

	totals := trust.Totals{InvitesActivated: 2}
	assert.Equal(t, 2*trust.PointsPerKind[trust.KindInviteActivated], totals.Points(),
		"two invitees are worth two payments in the point total")
	assert.Zero(t, trust.Totals{}.Points(), "and nobody owes anything for a member who invited nobody")
}
