package invite

import (
	"context"
	"errors"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/trust"
)

// entityTypeUser is the entity_type the referral payment records, matching the
// convention the other trust events use ("edit", "authored_quest", "studio").
const entityTypeUser = "user"

// Outcome is what a referral payment did, BY NAME (plan phase 11 slice 11b).
//
// An enumerated outcome rather than a bool or an error, because three of the four
// answers are refusals for different reasons and a caller that cannot tell "not
// eligible yet" from "invited themselves" cannot act on either. It is also what makes
// the tests assert the REFUSAL rather than the absence of a payment: "no event was
// recorded" is true for three different situations, and only one of them is a farm
// attempt.
type Outcome int

const (
	// OutcomeNotYetEligible: the invitee has not reached Registered, so there is
	// nothing to pay for yet. This is the normal answer on signup and stays the
	// answer until they contribute.
	OutcomeNotYetEligible Outcome = iota
	// OutcomeNoReferrer: nobody invited them. invited_by IS NULL for every account
	// an operator creates directly, and for one whose inviter has been deleted
	// (the column is ON DELETE SET NULL), which is an absent referrer rather than
	// a broken one.
	OutcomeNoReferrer
	// OutcomeSelfInvited: the member's inviter IS the member. REFUSED, and named,
	// because it is the cheapest way to farm this: nothing in the schema stops an
	// operator from generating a key and activating it on their own account, so an
	// unrecognised self-invite would be a payment for inviting yourself.
	OutcomeSelfInvited
	// OutcomeCredited: the referrer was credited. A second attempt at the same
	// invitee is also this answer, because the event's dedup key makes it a no-op
	// -- the outcome is "this invitee has been paid for", not "this call wrote".
	OutcomeCredited
)

// String names the outcome, so a log line or a test failure says which one it was.
func (o Outcome) String() string {
	switch o {
	case OutcomeNotYetEligible:
		return "not_yet_eligible"
	case OutcomeNoReferrer:
		return "no_referrer"
	case OutcomeSelfInvited:
		return "self_invited"
	case OutcomeCredited:
		return "credited"
	}
	return "unknown"
}

// Referral pays the member who invited an invitee, once that invitee becomes a real
// member.
//
// The payment is a trust EVENT, not a stored balance: it is keyed to the invitee in
// trust_events, so it happens once ever no matter how often the invitee's level is
// recomputed -- and the level IS recomputed on every contributing event, and can
// oscillate through decay. That is the whole reason there is no transition state to
// track and no "already paid" column.
//
// It reads the users table directly rather than through the user service: one column,
// through the query written for it, so this package does not depend on the shape of a
// user row.
type Referral struct {
	queries *queries.Queries
	trust   *trust.Trust
}

// NewReferral creates a referral payment.
//
// The trust service is the one that will carry the payment, so it is the same value
// the payment's hook was attached to: the recording goes through
// RecordEventWithoutChain, which is what keeps the chain one level deep.
func NewReferral(queries *queries.Queries, t *trust.Trust) *Referral {
	return &Referral{queries: queries, trust: t}
}

// DecideReferral is the whole rule, with no database in it.
//
// Pure so it can be table-tested: every refusal has an outcome of its own, and the
// order of the checks matters -- eligibility first, because an invitee who has not
// reached Registered is not paid for whatever else is true of their account.
func DecideReferral(level trust.LevelEnum, inviteeID uuid.UUID, invitedBy *uuid.UUID) Outcome {
	if level < trust.LevelRegistered {
		return OutcomeNotYetEligible
	}
	if invitedBy == nil {
		return OutcomeNoReferrer
	}
	if *invitedBy == inviteeID {
		return OutcomeSelfInvited
	}
	return OutcomeCredited
}

// Settle reads the inviting fact and pays the referrer if the rule says to.
//
// It returns the outcome so a caller can tell the four answers apart; the trust
// service's hook discards it (its own contract is an error, and none of the refusals
// is one -- a member who invited themselves gets no payment, not a failed request).
func (r *Referral) Settle(ctx context.Context, inviteeID uuid.UUID, level trust.LevelEnum) (Outcome, error) {
	referrer, err := r.queries.GetUserInvitedBy(ctx, inviteeID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// No such member. Nothing to pay and nobody to pay: the caller is asking
		// about an id that does not exist, which is not an error for a payment
		// that is derived from the row.
		return OutcomeNoReferrer, nil
	case err != nil:
		return OutcomeNotYetEligible, err
	}

	var invitedBy *uuid.UUID
	if referrer.Valid {
		invitedBy = &referrer.UUID
	}

	outcome := DecideReferral(level, inviteeID, invitedBy)
	if outcome != OutcomeCredited {
		return outcome, nil
	}

	// The dedup key is (referrer, invite_activated, user, invitee): one payment per
	// referrer PER INVITEE. Keying it to the referrer alone would pay a member for
	// their first invitee and for nobody afterwards, and keying it without the kind
	// would make it collide with any other event about the same invitee.
	if _, err := r.trust.RecordEventWithoutChain(ctx, trust.Event{
		UserID:     *invitedBy,
		Kind:       trust.KindInviteActivated,
		Delta:      1,
		EntityType: entityTypeUser,
		EntityID:   &inviteeID,
	}); err != nil {
		return OutcomeCredited, err
	}
	return OutcomeCredited, nil
}

// Pay is the trust service's hook: the same rule, with the outcome folded away.
func (r *Referral) Pay(ctx context.Context, inviteeID uuid.UUID, level trust.LevelEnum) error {
	_, err := r.Settle(ctx, inviteeID, level)
	return err
}
