package trust

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The decay policy (SPEC §5.3, plan phase 6 slice 6b-1).
//
// Every case below is a WHERE IN TIME, written as a timeline rather than as a sequence of calls: the
// clock is a parameter, so "after the grace period" and "in 14 days" are arithmetic and not a test
// that sleeps. The three instants are named once and shared, because a case that re-derived them
// would be a case that could drift from the policy it is testing.
var (
	day0 = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC) // the day the two flags land
	// The phase's own example: a member at TL3 whose flagged totals justify TL2. Which points make
	// which level is the curve's business, not this test's -- so it is ASSERTED below rather than
	// assumed, and a fixture that no longer straddles the boundary fails loudly instead of turning
	// every "still TL3" assertion into a tautology. (The first version of this fixture did exactly
	// that: `LevelRegistered + 1` is TL2, the same level the flagged totals justify, so the guard
	// caught two levels that were in fact one.)
	memberLevel    = LevelCurator
	flaggedTotals  = Totals{ApprovedEdits: 15} // 150 points: TL2
	originalTotals = Totals{ApprovedEdits: 22} // 220 points: TL3 again
)

// THE POLICY'S NUMBERS ARE THE SPEC'S NUMBERS, asserted as values rather than used only as offsets.
//
// Every other case below derives its instants FROM these constants, so a constant that changed to zero
// would move the timeline with it and the narrative cases would still pass -- the same "absence
// assertion whose subject moved" family as a test that reads its own fixture. This is the one place
// where the numbers themselves are the assertion.
func TestThePolicyPeriodsAreTheSpecifiedOnes(t *testing.T) {
	assert.Equal(t, 7*24*time.Hour, GracePeriod, "the grace period is a week")
	assert.Equal(t, 14*24*time.Hour, RecoveryPeriod, "SPEC §5.3 names fourteen days for recovery")
}

func TestDecayFixturesStraddleTheBoundary(t *testing.T) {
	// The guard the cases below depend on: the flagged totals must justify LESS than the member's
	// level, and the restored totals must justify AT LEAST it. A fixture that drifted would turn
	// every "still TL3" assertion below into a tautology.
	flagged, restored := LevelForTotals(flaggedTotals), LevelForTotals(originalTotals)
	require.Less(t, int(flagged), int(memberLevel), "the flagged totals must fall SHORT of the level")
	require.GreaterOrEqual(t, int(restored), int(memberLevel), "the restored totals must justify the level again")
}

// TWO FLAGS, AND THE GRACE HOLDS THE LEVEL.
func TestAShortfallDoesNotDropTheLevelDuringGrace(t *testing.T) {
	state := TrustState{Level: memberLevel}

	next, decision := Decide(state, flaggedTotals, day0)
	assert.Equal(t, KindGraceStarted, decision.Kind)
	assert.Equal(t, memberLevel, next.Level, "the member keeps their level: that is what grace is")
	assert.Equal(t, day0.Add(GracePeriod), decision.EffectiveAt, "and the deadline is named, not implied")

	// Halfway through the grace, still standing.
	mid := day0.Add(GracePeriod / 2)
	next2, decision2 := Decide(next, flaggedTotals, mid)
	assert.Equal(t, KindGraceStanding, decision2.Kind)
	assert.Equal(t, memberLevel, next2.Level)
	assert.Equal(t, next.ShortSince, next2.ShortSince, "the clock is not restarted by looking at it")

	// The instant before the deadline is still inside it.
	justBefore := day0.Add(GracePeriod - time.Second)
	next3, decision3 := Decide(next2, flaggedTotals, justBefore)
	assert.Equal(t, KindGraceStanding, decision3.Kind)
	assert.Equal(t, memberLevel, next3.Level)
}

// THE DEADLINE PASSES WITH THE SHORTFALL STILL STANDING: the level falls, to what the totals justify.
func TestTheLevelFallsWhenTheGraceExpires(t *testing.T) {
	state := TrustState{Level: memberLevel, ShortSince: day0}
	at := day0.Add(GracePeriod)

	next, decision := Decide(state, flaggedTotals, at)

	assert.Equal(t, KindDemoted, decision.Kind)
	assert.Equal(t, LevelForTotals(flaggedTotals), next.Level, "the level now matches the totals")
	assert.Equal(t, at, next.DemotedAt)
	assert.True(t, next.ShortSince.IsZero(), "the shortfall is settled, not still pending")
	assert.Equal(t, at.Add(RecoveryPeriod), decision.EffectiveAt)
}

// THE SHORTFALL CLEARS BEFORE THE DEADLINE: nothing to undo, because nothing moved.
func TestAShortfallThatClearsNeverDemotes(t *testing.T) {
	state := TrustState{Level: memberLevel, ShortSince: day0}

	next, decision := Decide(state, originalTotals, day0.Add(3*24*time.Hour))

	assert.Equal(t, KindShortfallCleared, decision.Kind)
	assert.Equal(t, memberLevel, next.Level)
	assert.Equal(t, memberLevel, decision.From, "the level never moved, so there is no demotion to cancel")
	assert.True(t, next.ShortSince.IsZero())
	assert.True(t, next.DemotedAt.IsZero(), "and no recovery window was opened")

	// A later shortfall starts a FRESH grace rather than resuming the abandoned one.
	next2, decision2 := Decide(next, flaggedTotals, day0.Add(4*24*time.Hour))
	assert.Equal(t, KindGraceStarted, decision2.Kind)
	assert.Equal(t, day0.Add(4*24*time.Hour), next2.ShortSince)
}

// THIS IS THE PHASE'S SENTENCE: the member regains the level in 14 days -- and not before, even once
// the totals are back, because a level that flips twice a day means nothing.
func TestTheLevelIsHeldForFourteenDaysAndThenFollowsTheTotals(t *testing.T) {
	demotedAt := day0.Add(GracePeriod)
	state := TrustState{Level: memberLevel - 1, DemotedAt: demotedAt, DemotedFrom: memberLevel}

	// Day 13, with the points already earned back: still held down.
	next, decision := Decide(state, originalTotals, demotedAt.Add(RecoveryPeriod-time.Hour))
	assert.Equal(t, KindHeld, decision.Kind)
	assert.Equal(t, memberLevel-1, next.Level, "the totals justify more; the window does not care")
	assert.Equal(t, demotedAt.Add(RecoveryPeriod), decision.EffectiveAt)

	// Day 14: the window closes and the level follows the totals again -- which, the points being
	// back, is the regain.
	final, decision2 := Decide(next, originalTotals, demotedAt.Add(RecoveryPeriod))
	assert.Equal(t, KindRecovered, decision2.Kind)
	assert.Equal(t, LevelForTotals(originalTotals), final.Level)
	assert.Equal(t, memberLevel, final.Level, "the level the member had is the level they are back at")
	assert.True(t, final.DemotedAt.IsZero(), "and the window is closed for good")

	// NOT A RESTORE ON A TIMER: with nothing earned back, the same window ending lands the member
	// where the totals are, not where they were.
	stillFlagged, _ := Decide(TrustState{Level: memberLevel - 1, DemotedAt: demotedAt, DemotedFrom: memberLevel}, flaggedTotals, demotedAt.Add(RecoveryPeriod))
	assert.Equal(t, LevelForTotals(flaggedTotals), stillFlagged.Level)
}

// A PROMOTION IS IMMEDIATE. Grace protects a member from losing standing over a moment; there is no
// such moment on the way up, and a member who earns a level has earned it.
func TestAPromotionIsImmediate(t *testing.T) {
	state := TrustState{Level: LevelRegistered}

	next, decision := Decide(state, originalTotals, day0)

	assert.Equal(t, KindPromoted, decision.Kind)
	assert.Equal(t, LevelForTotals(originalTotals), next.Level)
	assert.True(t, decision.EffectiveAt.IsZero(), "nothing to wait for")
}

// RUNNING THE DECISION TWICE AT ONE INSTANT DOES NOTHING THE SECOND TIME. This is what makes it safe
// on a schedule that can overlap itself, and it is the reason the grace clock starts only when there
// is no shortfall on record.
func TestDecidingTwiceAtOneInstantIsIdempotent(t *testing.T) {
	state := TrustState{Level: memberLevel}

	once, _ := Decide(state, flaggedTotals, day0)
	twice, decision := Decide(once, flaggedTotals, day0)

	assert.Equal(t, once, twice, "the same input at the same instant is the same state")
	assert.Equal(t, KindGraceStanding, decision.Kind, "and the caller is told the deadline is standing")
	assert.Equal(t, day0.Add(GracePeriod), decision.EffectiveAt, "the deadline has not moved")

	// The same for a demotion: a second call inside the window does not reopen it.
	demoted, _ := Decide(TrustState{Level: memberLevel, ShortSince: day0}, flaggedTotals, day0.Add(GracePeriod))
	held, decision2 := Decide(demoted, flaggedTotals, day0.Add(GracePeriod))
	assert.Equal(t, demoted, held)
	assert.Equal(t, KindHeld, decision2.Kind)
}

// A STANDING THE MEMBER NEVER HAD CANNOT BE HELD AGAINST THEM -- and this case exists because the
// integration test found the bug, not because the policy's prose suggested it. The window is a hold on
// the level the member was DROPPED FROM; a member whose totals now justify more than that has earned
// something new. The first version of decideWhileDemoted ignored that and held every rise, so a
// demoted member was pinned at the level they fell to for fourteen days however much they earned.
func TestAPromotionAboveTheLostLevelEndsTheWindow(t *testing.T) {
	demotedAt := day0.Add(GracePeriod)
	// Dropped from TL3 to TL2 an hour ago, still well inside the window.
	state := TrustState{Level: LevelContributor, DemotedAt: demotedAt, DemotedFrom: LevelCurator}

	// One rung ABOVE where they were dropped from: immediate, and the window closes with it.
	next, decision := Decide(state, Totals{ApprovedEdits: 80}, demotedAt.Add(time.Hour))
	require.Equal(t, LevelArchivist, LevelForTotals(Totals{ApprovedEdits: 80}), "fixture: 800 points is TL4")
	assert.Equal(t, KindPromoted, decision.Kind)
	assert.Equal(t, LevelArchivist, next.Level)
	assert.True(t, next.DemotedAt.IsZero(), "nothing left to hold")
	assert.Equal(t, LevelEnum(0), next.DemotedFrom)

	// Exactly the lost level, in the same window, is still held: that is the recovery the window exists
	// to stop, and it is the case the two rules have to keep apart.
	held, decision2 := Decide(state, originalTotals, demotedAt.Add(time.Hour))
	assert.Equal(t, KindHeld, decision2.Kind)
	assert.Equal(t, LevelContributor, held.Level)
}

// A MERELY-UNCHANGED MEMBER IS NOT A SHORTFALL. The ordinary case -- totals that still justify the
// level -- must not start a grace clock, or every member in the instance would be permanently eight
// days from a demotion.
func TestAnUnchangedMemberStartsNoClock(t *testing.T) {
	next, decision := Decide(TrustState{Level: memberLevel}, originalTotals, day0)

	assert.Equal(t, KindUnchanged, decision.Kind)
	assert.True(t, next.ShortSince.IsZero())
	assert.True(t, next.DemotedAt.IsZero())
}
