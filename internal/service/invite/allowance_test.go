package invite

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/service/trust"
)

// TestAllocationCurveIsTheDocumentedTable is a golden table rather than a walk
// over the levels, because the NUMBERS are a product decision: if one of them
// changes, that change should be visible in a diff of this test and not merely
// make some property test keep passing.
func TestAllocationCurveIsTheDocumentedTable(t *testing.T) {
	golden := map[trust.LevelEnum]int{
		trust.LevelPublic:      0,
		trust.LevelRegistered:  1,
		trust.LevelContributor: 3,
		trust.LevelCurator:     5,
		trust.LevelArchivist:   10,
		trust.LevelSteward:     25,
	}

	for level, want := range golden {
		t.Run(level.String(), func(t *testing.T) {
			assert.Equal(t, want, AllocationFor(level))
		})
	}
}

// TestAllocationNeverDecreasesWithLevel is the property that makes the curve a
// curve: a member who earns the next level must not LOSE capacity. It is checked
// over the levels in order rather than against the golden table above, so a new
// level inserted between two of them cannot quietly invert a pair.
func TestAllocationNeverDecreasesWithLevel(t *testing.T) {
	levels := []trust.LevelEnum{
		trust.LevelPublic,
		trust.LevelRegistered,
		trust.LevelContributor,
		trust.LevelCurator,
		trust.LevelArchivist,
		trust.LevelSteward,
	}

	for i := 1; i < len(levels); i++ {
		prev, cur := levels[i-1], levels[i]
		assert.GreaterOrEqual(t, AllocationFor(cur), AllocationFor(prev),
			"%s must not allow fewer keys than %s", cur, prev)
	}
}

// TestLevelEveryAccountStartsAtGetsNothing is the rule the slice exists for.
//
// It is asserted separately from the golden table because it is the one entry
// whose change would be a security decision rather than a tuning one: a
// non-zero value here means an account created five minutes ago can manufacture
// accounts.
func TestLevelEveryAccountStartsAtGetsNothing(t *testing.T) {
	require.Equal(t, trust.LevelPublic, trust.LevelPublic, "sanity: this is the starting level")
	assert.Zero(t, AllocationFor(trust.LevelPublic),
		"the level every registered account starts at must not mint invitations")
}

// TestUnknownLevelGetsNothing: a level this build does not know must fail
// closed, because forgetting to give a new level an allocation should mean
// "cannot invite", never "can invite without limit".
func TestUnknownLevelGetsNothing(t *testing.T) {
	assert.Zero(t, AllocationFor(trust.LevelEnum(7)))
	assert.Zero(t, AllocationFor(trust.LevelEnum(-1)))
}

func TestSlotsAreSpentBeforeTokens(t *testing.T) {
	// Contributor: 3 slots. One key in use, and an operator has also granted
	// two tokens. The member has an earned slot free, so nothing of the
	// operator's is consumed.
	d := Allowance{Level: trust.LevelContributor, ActiveKeys: 1, Granted: 2}.Decide()

	require.True(t, d.Allowed)
	assert.False(t, d.SpendsToken, "a free level slot must be spent before an operator's grant")
	assert.NoError(t, d.Refusal)
}

func TestTokenIsSpentWhenNoSlotIsFree(t *testing.T) {
	// One slot, and it is in use; the member holds a granted token, so the key
	// is allowed and the token is what pays for it.
	d := Allowance{Level: trust.LevelRegistered, ActiveKeys: 1, Granted: 1}.Decide()

	require.True(t, d.Allowed)
	assert.True(t, d.SpendsToken)
}

func TestRefusalNamesTheLevelWhenItsSlotsAreInUse(t *testing.T) {
	d := Allowance{Level: trust.LevelRegistered, ActiveKeys: 1}.Decide()

	require.False(t, d.Allowed)
	require.Error(t, d.Refusal)
	assert.ErrorIs(t, d.Refusal, ErrLevelAllocationExhausted)
	// The message has to be usable by the member: it names the level they are
	// at and how many slots are busy, which is what tells them whether to
	// rescind a key or go contribute.
	assert.Contains(t, d.Refusal.Error(), "Registered")
	assert.Contains(t, d.Refusal.Error(), "1 of 1")
}

// TestARefusalSaysWhichAllowanceRanOut is the distinction the two sentinels
// exist for. A member whose level grants nothing must not be told that their
// slots are in use: they have none, and the remedy (ask an operator, or
// contribute) is a different action than rescinding a key.
func TestARefusalSaysWhichAllowanceRanOut(t *testing.T) {
	atStartingLevel := Allowance{Level: trust.LevelPublic}.Decide()
	require.False(t, atStartingLevel.Allowed)
	assert.ErrorIs(t, atStartingLevel.Refusal, ErrNoInviteTokens)
	assert.NotErrorIs(t, atStartingLevel.Refusal, ErrLevelAllocationExhausted)
	assert.Contains(t, atStartingLevel.Refusal.Error(), "Public")

	atCap := Allowance{Level: trust.LevelRegistered, ActiveKeys: 1}.Decide()
	require.False(t, atCap.Allowed)
	assert.ErrorIs(t, atCap.Refusal, ErrLevelAllocationExhausted)
	assert.NotErrorIs(t, atCap.Refusal, ErrNoInviteTokens)
}

// TestExemptionBypassesBothAllowances is asserted for the bypass being a
// DECISION: the operator's delegate is not a member's standing, so neither the
// level's cap nor the token balance constrains them, and minting must not
// consume a grant they did not need either.
func TestExemptionBypassesBothAllowances(t *testing.T) {
	d := Allowance{Level: trust.LevelPublic, ActiveKeys: 12, Granted: 0, Exempt: true}.Decide()

	require.True(t, d.Allowed)
	assert.False(t, d.SpendsToken, "an exempt mint must not consume an operator's grant")
	assert.NoError(t, d.Refusal)
}

// TestCapIsLevelDerivedAndExemptionDoesNotRaiseIt: there is deliberately no
// "remaining" number in this API. A free-slot count means different things to an
// at-cap member (0), an exempt member (who is not bound by the cap at all) and a
// member holding more keys than their cap allows, and a single integer that
// means three things is a rendering bug waiting to happen. Callers get the facts
// -- cap, ActiveKeys, Exempt -- and Decide() for permission.
func TestCapIsLevelDerivedAndExemptionDoesNotRaiseIt(t *testing.T) {
	over := Allowance{Level: trust.LevelRegistered, ActiveKeys: 4}
	assert.Equal(t, 1, over.Cap())
	assert.Greater(t, over.ActiveKeys, over.Cap(), "a member may hold more keys than their cap")

	delegate := Allowance{Level: trust.LevelContributor, ActiveKeys: 1, Exempt: true}
	assert.Equal(t, 3, delegate.Cap())
	assert.True(t, delegate.Decide().Allowed)
}

// TestCapComesFromTheLevelAndNotFromGrants: the cap is a fact about the member's
// standing. A grant buys a key; it does not raise the level's cap, or a member
// with ten granted tokens would read as a member of a level they have not
// earned.
func TestCapComesFromTheLevelAndNotFromGrants(t *testing.T) {
	a := Allowance{Level: trust.LevelRegistered, ActiveKeys: 1, Granted: 10}
	assert.Equal(t, 1, a.Cap(), "a grant must not raise the level's cap")

	d := a.Decide()
	assert.True(t, d.Allowed, "the grant buys the key")
	assert.True(t, d.SpendsToken, "and the grant is what pays for it")
}

// TestDecideIsPure: the same allowance must always give the same answer, and
// deciding must not mutate the allowance it decided about (the caller keeps
// using its counts after the decision).
func TestDecideIsPure(t *testing.T) {
	a := Allowance{Level: trust.LevelContributor, ActiveKeys: 3}
	first := a.Decide()
	second := a.Decide()

	assert.False(t, first.Allowed)
	require.Error(t, first.Refusal)
	assert.Equal(t, first, second)
	assert.Equal(t, 3, a.ActiveKeys)
	// The refusal leads with the sentinel's own text, so a client that has only
	// the message (a GraphQL error, a log line) still reads the reason first and
	// the details after.
	assert.True(t, strings.HasPrefix(first.Refusal.Error(), ErrLevelAllocationExhausted.Error()))
}
