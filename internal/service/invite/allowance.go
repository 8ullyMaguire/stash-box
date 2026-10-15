// Package invite holds phase 11's rules about invitations: how many keys a
// member may hold (the allowance), what a referral pays (slice 11b), what a
// member can see of their keys' counters (11c) and what a cohort campaign
// awards (11d).
//
// The allowance is first because the other three depend on it: an activation
// needs a key, and how many keys a member may hold is a question about their
// STANDING -- which is exactly what phase 6 built the trust level to answer.
//
// Everything in this file is PURE. It takes a level and counts and returns a
// decision; the counts come from the key rows (the database's answer), never
// from a stored tally, so nothing here can drift from the rows it describes.
// That is what makes "regeneration" free: an expired or consumed key stops
// being counted, and its slot is free again with nothing to clean up.
package invite

import (
	"errors"
	"fmt"

	"github.com/stashapp/stash-box/internal/service/trust"
)

// ErrLevelAllocationExhausted is the refusal a member gets when their level
// grants them key slots and all of them are in use. Its remedy is the member's
// own: rescind a key, or contribute enough to reach the next level.
var ErrLevelAllocationExhausted = errors.New("all invite key slots for your level are in use")

// ErrNoInviteTokens is the refusal a member gets when their level grants them NO
// key slots at all and an operator has not granted them a token either. Its
// remedy is not the member's: an operator has to grant tokens, or the member has
// to contribute enough to earn a level that carries slots.
//
// The two refusals are separate values rather than one message because their
// remedies differ, and a message that says "no invite tokens available" to a
// member whose level simply grants none sends them to the wrong place.
var ErrNoInviteTokens = errors.New("no invite tokens available")

// allocation is how many keys a member of each level may hold USABLE at once.
//
// Public is ZERO, and that is the rule's whole point: a level every registered
// account starts at must not carry the power to mint invitations, or an account
// created five minutes ago can manufacture accounts. This is the same argument
// the level curve's own comment makes about making Archivist cost three times
// Curator -- capability is earned by contributing -- applied to the one
// capability whose abuse is a sybil attack.
//
// An unknown or unhandled level falls to the default arm and gets ZERO: a level
// added later must be given its allocation deliberately, and the failure mode of
// forgetting is "cannot invite", not "can invite without limit".
func AllocationFor(level trust.LevelEnum) int {
	switch level {
	case trust.LevelPublic:
		return 0
	case trust.LevelRegistered:
		return 1
	case trust.LevelContributor:
		return 3
	case trust.LevelCurator:
		return 5
	case trust.LevelArchivist:
		return 10
	case trust.LevelSteward:
		return 25
	default:
		return 0
	}
}

// Allowance is everything the decision needs, and nothing it does not: a level,
// how many keys that member holds usable RIGHT NOW (counted from the rows), how
// many tokens an operator has granted, and whether the member's role makes them
// the operator's delegate.
type Allowance struct {
	Level      trust.LevelEnum
	ActiveKeys int
	Granted    int
	// Exempt is the INVITE / MANAGE_INVITES role: the operator's own delegation.
	// It is a field on the struct rather than an argument read at the call site
	// so that a test can assert the bypass is a DECISION -- and so that the
	// decision is visible in the one place the rule lives.
	Exempt bool
}

// Cap is how many keys this member may hold usable at once from their level
// alone. It is a fact about the level, so it does not change as keys are minted;
// what changes is ActiveKeys.
func (a Allowance) Cap() int { return AllocationFor(a.Level) }

// Decision is what the allowance permits, and what it costs.
type Decision struct {
	Allowed bool
	// SpendsToken is true when the mint is paid for by an operator's grant,
	// which is the only case in which the member's token balance moves.
	SpendsToken bool
	// Refusal is nil when Allowed, and otherwise wraps one of the two sentinels
	// above with the details of THIS member's allowance.
	Refusal error
}

// Decide answers the member's only question about their allowance: may I mint
// another key, and what does it cost me?
//
// THE ORDER IS THE RULE: a free level slot is spent before a token. A member who
// holds both is a member whose standing already covers the key, and consuming an
// operator's discretionary grant for a key the member had earned would make the
// grant a tax on contributing.
func (a Allowance) Decide() Decision {
	if a.Exempt {
		return Decision{Allowed: true}
	}

	if a.ActiveKeys < a.Cap() {
		return Decision{Allowed: true}
	}

	if a.Granted > 0 {
		return Decision{Allowed: true, SpendsToken: true}
	}

	if a.Cap() == 0 {
		// The level itself is the binding constraint: no key was ever free, so
		// the remedy is a token or a level, and saying "slots are in use" would
		// describe a member who has none.
		return Decision{Refusal: fmt.Errorf(
			"%w: your level (%s) grants no invite keys", ErrNoInviteTokens, a.Level)}
	}

	return Decision{Refusal: fmt.Errorf(
		"%w: %d of %d are in use at level %s",
		ErrLevelAllocationExhausted, a.ActiveKeys, a.Cap(), a.Level)}
}
