package badge

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/service/trust"
)

// Badges are a PURE function of the rollup, so every rule can be tested
// exhaustively without a database -- and should be, because the alternative is an
// integration test per badge tier.
//
// The properties worth pinning, in order of how quietly they break when wrong:
// the boundary (is the threshold inclusive), the maxed case (no tier above), the
// negative count (the rollup is signed), and the plural.

// A badge is earned AT the threshold, not one short of it.
//
// The direction matters: an inclusive threshold is what a user expects after doing
// the work, and exclusive thresholds are the classic source of "I did 100 edits
// and nothing happened".
func TestABadgeIsEarnedAtItsThreshold(t *testing.T) {
	cases := []struct {
		edits int
		want  bool
	}{
		{0, false},
		{1, true},  // the first tier
		{9, true},  // still earned
		{10, true}, // exactly the second tier
		{11, true},
	}

	for _, tc := range cases {
		got := badgeByID(t, Derive(trust.Totals{ApprovedEdits: tc.edits}), "first_edit")
		assert.Equal(t, tc.want, got.Earned, "%d approved edits", tc.edits)
	}
}

// Every definition's FIRST tier must be reachable by a count of 1.
//
// A definition whose first tier is 10 is not a broken threshold, it is a badge
// most users can never see, and nothing about it looks wrong in a list.
func TestEveryFirstTierIsReachableByOneContribution(t *testing.T) {
	// Saturate every counter AND the bonus, so the points badge is saturated too.
	// A definition that reads points and no counter is only reachable this way.
	everything := Derive(trust.Totals{
		ApprovedEdits: 1 << 20, RejectedEdits: 1 << 20,
		IdentificationSolves: 1 << 20, QuestsCompleted: 1 << 20,
		ReplicasHosted: 1 << 20, BonusPoints: 1 << 20,
	})
	for _, id := range AllIDs() {
		assert.True(t, badgeByID(t, everything, id).Earned,
			"badge %q is unearned for a user with every counter maxed, so its "+
				"first tier is above 1 and most users can never see it", id)
	}
}

// A user past the highest tier is MAXED, not broken.
//
// This is what a naive "index of the next tier" turns into an out-of-range panic,
// and a panic on a profile page is reachable by anyone with more contributions
// than the last tier.
func TestAUserBeyondTheHighestTierIsMaxedNotBroken(t *testing.T) {
	got := badgeByID(t, Derive(trust.Totals{ApprovedEdits: 1_000_000}), "first_edit")

	assert.True(t, got.Earned)
	assert.Equal(t, float64(1), got.Progress,
		"a maxed badge is 100% complete; progress against a tier that does not "+
			"exist would divide past the end of the slice")
	assert.NotContains(t, got.Description, "more",
		"and the description must not promise a gap to a tier that is not there: "+
			"%q tells a user with 1,000,000 edits to keep going", got.Description)
}

// Progress is measured against the CURRENT tier.
//
// Against the first tier instead, a user with 9 of 10 edits reads as 900% -- the
// bar overflows and the number is nonsense, which is worse than no bar because it
// looks like a bug in the app rather than in the arithmetic.
func TestProgressIsRelativeToTheCurrentTier(t *testing.T) {
	nine := badgeByID(t, Derive(trust.Totals{ApprovedEdits: 9}), "first_edit")
	assert.InDelta(t, 0.9, nine.Progress, 0.001, "9 of 10 edits is 90% of the way")

	one := badgeByID(t, Derive(trust.Totals{ApprovedEdits: 1}), "first_edit")
	assert.Greater(t, nine.Progress, one.Progress,
		"progress must RISE with the count; a definition reporting the same value "+
			"for 1 and 9 edits is measuring something other than the distance to "+
			"the next badge")
}

// A NEGATIVE count must not produce a negative progress, and must not earn a badge.
func TestANegativeCountIsClampedAndEarnsNothing(t *testing.T) {
	// The rollup is signed and a reversal can drive a counter below zero, so this
	// is a reachable state rather than a defensive thought experiment.
	badges := Derive(trust.Totals{ApprovedEdits: -5, IdentificationSolves: -1})
	got := badgeByID(t, badges, "first_edit")

	assert.False(t, got.Earned,
		"a user whose approved edits were REVERSED has not earned the edit badge")
	assert.GreaterOrEqual(t, got.Progress, float64(0),
		"progress must not be negative: a fill bar rendering backwards is worse "+
			"than an empty one")
}

// The plural is on the REMAINING count, which is easy to get wrong by counting the
// current total instead.
func TestThePluralMatchesTheRemainingCount(t *testing.T) {
	// 9 of 10: one remaining -> singular.
	one := badgeByID(t, Derive(trust.Totals{ApprovedEdits: 9}), "first_edit")
	assert.Contains(t, one.Description, "1 more edit.")
	assert.NotContains(t, one.Description, "1 more edits",
		"'1 more edits' is the wrong plural and the only kind of wrong a user "+
			"notices immediately")

	// 0 edits: the NEXT tier is the first one, so the gap is one. I asserted ten
	// here first, assuming the badge skipped to a "real" milestone -- and the
	// implementation was right: a new user is ONE edit from their first badge, and
	// "10 more" would understate it.
	zero := badgeByID(t, Derive(trust.Totals{ApprovedEdits: 0}), "first_edit")
	assert.Contains(t, zero.Description, "1 more edit.")

	// A plural gap, to prove the singular branch is not the only one that works.
	plural := badgeByID(t, Derive(trust.Totals{ApprovedEdits: 6}), "first_edit")
	assert.Contains(t, plural.Description, "4 more edits.")
}

// A user with nothing gets every badge, unearned, not an empty list.
func TestAUserWithNoContributionsGetsUnearnedBadgesNotAnEmptyList(t *testing.T) {
	badges := Derive(trust.Totals{})

	require.NotEmpty(t, badges,
		"an empty list is indistinguishable from 'this instance has no badges', "+
			"which is a much worse thing to show a user than a list they have not "+
			"earned yet")
	for _, b := range badges {
		assert.False(t, b.Earned, "badge %q", b.ID)
		assert.Equal(t, float64(0), b.Progress, "badge %q", b.ID)
	}
}

// Earned badges sort first, then rarest-first, and the order is TOTAL.
func TestTheOrderIsEarnedThenRarestAndIsStable(t *testing.T) {
	totals := trust.Totals{
		ApprovedEdits:        100, // first_edit, rarity 1, and 1000 points -> taste_maker
		ReplicasHosted:       1,   // preserver, rarity 4
		IdentificationSolves: 1,   // detective, rarity 2
	}
	badges := Derive(totals)
	require.Len(t, badges, len(AllIDs()))

	// The invariant is a PARTITION, not a pairwise check: once an unearned badge
	// appears, no earned badge may follow. "Every earlier badge is unearned" is
	// the same idea written wrong -- it fails on the earned badges at the top,
	// which is where they belong.
	sawUnearned := false
	for _, b := range badges {
		if b.Earned {
			require.False(t, sawUnearned,
				"badge %q is earned but sorts after an unearned one", b.ID)
			continue
		}
		sawUnearned = true
	}

	// Four earned badges, not three, and I wrote three first. 100 approved edits is
	// 1000 points, which crosses taste_maker's 100-point tier -- so the points
	// badge is earned by a fixture that never mentions points. That is the
	// behaviour under test; an expected value that forgot it was measuring my
	// memory of the fixture rather than the ordering.
	rarities, earnedIDs := []int{}, []string{}
	for _, b := range badges {
		if b.Earned {
			rarities = append(rarities, b.Rarity)
			earnedIDs = append(earnedIDs, b.ID)
		}
	}
	assert.Equal(t, []int{4, 3, 2, 1}, rarities,
		"rarest first, so the badge worth looking at is at the top")
	assert.Equal(t, []string{"preserver", "taste_maker", "detective", "first_edit"},
		earnedIDs, "and the ids in that order, so a rare badge cannot be traded "+
			"for a common one at equal rarity")

	assert.Equal(t, badges, Derive(totals),
		"and the same input twice gives the same order, which is what keeps a "+
			"rendered profile from flickering")
}

// Points are a valid counter, and a curator who crossed a level through BOUNTIES
// has points without a matching raw count.
func TestPointsDrivesTheTasteMakerBadge(t *testing.T) {
	byPoints := badgeByID(t,
		Derive(trust.Totals{QuestsCompleted: 1, BonusPoints: 500}), "taste_maker")
	assert.True(t, byPoints.Earned,
		"a curator who finished a 500-point bounty has 520 points and must "+
			"register on the points badge. Without it the reward for a bounty is a "+
			"level number and nothing else")

	byEdits := badgeByID(t, Derive(trust.Totals{ApprovedEdits: 52}), "taste_maker")
	assert.True(t, byEdits.Earned,
		"520 points is 520 points: the badge must not care which contribution "+
			"produced them, or the same badge means two different things")
}

// Every definition must name a counter the switch actually knows, or it silently
// reads zero and is never earned.
func TestEveryDefinitionNamesAKnownCounter(t *testing.T) {
	known := map[string]bool{
		"approved_edits": true, "identification_solves": true,
		"quests_completed": true, "replicas_hosted": true, "points": true,
	}
	for _, d := range packageDefinitions {
		require.NotEmpty(t, d.tiers, "badge %q has no tiers", d.id)
		assert.Positive(t, d.tiers[0],
			"badge %q starts at a non-positive tier, so no count can earn it", d.id)
		assert.True(t, known[d.counter],
			"badge %q names counter %q, which the switch does not know, so it "+
				"always reads zero and the badge is never earned", d.id, d.counter)

		// Tiers must ASCEND: a descending or repeated list makes the "next tier"
		// search pick the wrong value and progress meaningless.
		for i := 1; i < len(d.tiers); i++ {
			assert.Greater(t, d.tiers[i], d.tiers[i-1],
				"badge %q has tiers out of order at %d: %v", d.id, i, d.tiers)
		}
	}
}

// An unknown counter reads zero rather than panicking, and this test is the only
// thing that can execute that arm -- which is why CounterValue is exported.
func TestAnUnknownCounterIsZeroNotAPanic(t *testing.T) {
	assert.Equal(t, 0, CounterValue(trust.Totals{ApprovedEdits: 100}, "not_a_counter"),
		"a typo in a definition should produce a badge that is never earned -- "+
			"visible as 'you have none of these' -- rather than a server error on "+
			"every profile page. A test that could not reach this arm could not "+
			"protect it, which is why the lookup is exported")
}

// AllIDs is the catalogue, and it must not be an alias of the internal slice.
func TestAllIDsIsACopyNotTheBackingArray(t *testing.T) {
	ids := AllIDs()
	require.NotEmpty(t, ids)
	ids[0] = "mutated"

	assert.NotEqual(t, "mutated", AllIDs()[0],
		"AllIDs returned the backing array, so a caller sorting it in place "+
			"reorders the definitions for everyone")
}

func badgeByID(t *testing.T, badges []Badge, id string) Badge {
	t.Helper()
	for _, b := range badges {
		if b.ID == id {
			return b
		}
	}
	t.Fatalf("no badge with id %q; have %v", id, AllIDs())
	return Badge{}
}
