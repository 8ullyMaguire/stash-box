package award

import (
	"context"
	"errors"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/trust"
)

// recordingTrust is a stub that RECORDS what it was asked to do.
//
// A stub rather than the real service, and the reason is the assertion that
// matters: "a quest is awarded as exactly two events, and the second one carries
// the magnitude". Against a real database that is three queries and an inference
// about what a row implies; against a recorder it is a list.
type recordingTrust struct {
	events []trust.Event
	// failOnKind makes one kind error, to test the base-then-bounty ordering.
	failOnKind trust.KindEnum
}

func (r *recordingTrust) RecordEvent(ctx context.Context, event trust.Event) (*queries.UserTrust, error) {
	if event.Kind == r.failOnKind {
		return nil, errors.New("stub refuses " + string(event.Kind))
	}
	r.events = append(r.events, event)
	return &queries.UserTrust{}, nil
}

func (r *recordingTrust) Level(ctx context.Context, userID uuid.UUID) (trust.LevelEnum, error) {
	return trust.LevelRegistered, nil
}

func (r *recordingTrust) kinds() []trust.KindEnum {
	out := make([]trust.KindEnum, 0, len(r.events))
	for _, e := range r.events {
		out = append(out, e.Kind)
	}
	return out
}

func (r *recordingTrust) eventFor(kind trust.KindEnum) (trust.Event, bool) {
	for _, e := range r.events {
		if e.Kind == kind {
			return e, true
		}
	}
	return trust.Event{}, false
}

func newRecorder() (*Service, *recordingTrust) {
	rec := &recordingTrust{}
	return NewService(rec), rec
}

// A quest is TWO events, because a rollup moves one column per kind.
//
// This is the central claim of the package and it is the one most easily written
// wrong: recording delta=520 on quest_completed credits 520 completed quests, and
// a curator who finishes one bountied quest jumps two levels with no error
// anywhere. The test asserts the SPLIT, not the total.
func TestAQuestIsAwardedAsACountAndABonus(t *testing.T) {
	svc, rec := newRecorder()
	questID := uuid.Must(uuid.NewV7())
	curator := uuid.Must(uuid.NewV7())

	total, err := svc.QuestCompletion(context.Background(), QuestCompletionInput{
		CuratorID: curator, QuestID: questID, BountyPoints: 500,
	})
	require.NoError(t, err)

	assert.Equal(t, 520, total, "20 base plus the 500-point bounty")
	require.Len(t, rec.events, 2, "one event cannot move both a count and a bonus")

	base, ok := rec.eventFor(trust.KindQuestCompleted)
	require.True(t, ok, "the quest's own value is a COUNT")
	assert.Equal(t, 1, base.Delta,
		"the base event is delta 1 -- it counts ONE quest. The 500 lives in the "+
			"bounty event, and putting the total here would credit 520 quests")

	bounty, ok := rec.eventFor(trust.KindBountyBonus)
	require.True(t, ok)
	assert.Equal(t, 500, bounty.Delta, "the bounty carries the magnitude")

	assert.Equal(t, curator, bounty.UserID, "both halves belong to the curator")
	assert.Equal(t, "authored_quest", base.EntityType)
	assert.Equal(t, "authored_quest", bounty.EntityType,
		"both halves are keyed on the QUEST, so a retry of either is the same "+
			"dedup key. A per-item key would let a second curator claiming a "+
			"different item of the same quest award it again")
}

// The dedup key has to be the QUEST, and that is testable without a database.
func TestTheDedupEntityIsTheQuestNotAnItem(t *testing.T) {
	svc, rec := newRecorder()
	questID := uuid.Must(uuid.NewV7())

	_, err := svc.QuestCompletion(context.Background(), QuestCompletionInput{
		CuratorID: uuid.Must(uuid.NewV7()), QuestID: questID, BountyPoints: 100,
	})
	require.NoError(t, err)

	for _, kind := range rec.kinds() {
		e, _ := rec.eventFor(kind)
		require.NotNil(t, e.EntityID)
		assert.Equal(t, questID, *e.EntityID,
			"every half of a quest award is keyed on the quest id. An item id here "+
				"would make each item independently claimable, so a five-item quest "+
				"would pay five bounties")
	}
}

// A quest with NO bounty is ONE event, and specifically not a zero bounty event.
func TestABountylessQuestIsOneEvent(t *testing.T) {
	svc, rec := newRecorder()

	total, err := svc.QuestCompletion(context.Background(), QuestCompletionInput{
		CuratorID: uuid.Must(uuid.NewV7()), QuestID: uuid.Must(uuid.NewV7()),
	})
	require.NoError(t, err)

	assert.Equal(t, trust.PointsPerKind[trust.KindQuestCompleted], total)
	require.Len(t, rec.events, 1, "a quest with no bounty is one event")
	assert.Equal(t, trust.KindQuestCompleted, rec.events[0].Kind)
}

// A zero-valued bounty event would occupy the dedup slot and silently block a
// REAL bounty for the same quest later.
func TestNoZeroBountyEventIsRecorded(t *testing.T) {
	svc, rec := newRecorder()
	questID := uuid.Must(uuid.NewV7())

	_, err := svc.QuestCompletion(context.Background(), QuestCompletionInput{
		CuratorID: uuid.Must(uuid.NewV7()), QuestID: questID, BountyPoints: 0,
	})
	require.NoError(t, err)

	_, recorded := rec.eventFor(trust.KindBountyBonus)
	assert.False(t, recorded,
		"a bounty_bonus event with delta 0 would move nothing and still occupy "+
			"(user, kind, entity_type, entity_id) -- so if an operator added a "+
			"bounty to this quest later, the real award would be discarded as a "+
			"duplicate and the curator would silently lose it. RecordEvent refuses "+
			"a zero delta for exactly this reason")
}

// A failed bounty must not leave a half-paid quest, and the BASE goes first so
// the failure mode is "no bounty" rather than "bounty with no quest".
func TestAFailedBountyLeavesTheBaseRecorded(t *testing.T) {
	rec := &recordingTrust{failOnKind: trust.KindBountyBonus}
	svc := NewService(rec)

	_, err := svc.QuestCompletion(context.Background(), QuestCompletionInput{
		CuratorID: uuid.Must(uuid.NewV7()), QuestID: uuid.Must(uuid.NewV7()),
		BountyPoints: 500,
	})
	require.Error(t, err, "the failure is reported, not swallowed")

	base, ok := rec.eventFor(trust.KindQuestCompleted)
	require.True(t, ok,
		"the base is recorded FIRST, so a bounty failure leaves a curator who "+
			"finished the quest with the quest's own points and no bounty. The "+
			"reverse order would leave a bounty with no quest completed, which "+
			"reads as unexplained points in the history and cannot be explained "+
			"by any quest")
	assert.Equal(t, 1, base.Delta)
}

// A negative bounty is refused at the boundary, not stored.
func TestANegativeBountyIsRefused(t *testing.T) {
	svc, rec := newRecorder()

	_, err := svc.QuestCompletion(context.Background(), QuestCompletionInput{
		CuratorID: uuid.Must(uuid.NewV7()), QuestID: uuid.Must(uuid.NewV7()),
		BountyPoints: -100,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "negative")
	assert.Empty(t, rec.events,
		"nothing is recorded on the way out -- a negative bounty that slipped "+
			"through would subtract points with no event able to explain it, "+
			"since the log's positive contribution is still there")
}

// A counted contribution is always delta 1.
//
// The asymmetry with a bounty is the point: edits and identifications are COUNTS,
// and a count of three is three events. Passing a magnitude would credit three
// from one, which is the same bug the bounty would have been had they shared a
// path.
func TestACountedContributionIsAlwaysDeltaOne(t *testing.T) {
	svc, rec := newRecorder()
	editID := uuid.Must(uuid.NewV7())

	err := svc.Contribution(context.Background(), trust.KindEditApproved,
		uuid.Must(uuid.NewV7()), "edit", editID)
	require.NoError(t, err)

	require.Len(t, rec.events, 1)
	assert.Equal(t, 1, rec.events[0].Delta,
		"a count is one per contribution; the magnitude belongs only to a bounty")
	assert.Equal(t, editID, *rec.events[0].EntityID)
}

// THE INVARIANT, and the one a mutation of Points() would break.
//
// points = (counts x weights) + bonus. A Totals that forgets BonusPoints reports
// the right counts and the wrong score, and the two disagree with nothing
// reporting an error -- which is the failure mode this whole column exists to
// prevent, reproduced one level up.
func TestBonusPointsArePartOfTheScore(t *testing.T) {
	base := trust.Totals{QuestsCompleted: 1}
	assert.Equal(t, trust.PointsPerKind[trust.KindQuestCompleted], base.Points(),
		"one quest with no bounty is worth the kind's weight and nothing else")

	withBounty := trust.Totals{QuestsCompleted: 1, BonusPoints: 500}
	assert.Equal(t, trust.PointsPerKind[trust.KindQuestCompleted]+500, withBounty.Points(),
		"a bounty ADDS to the quest's own weight. A multiplier here would be a "+
			"second interpretation of the same number in a second place, and the "+
			"two would disagree about what a quest is worth")

	// And the bonus is additive across several bounties, not compounded.
	twoQuests := trust.Totals{QuestsCompleted: 2, BonusPoints: 500}
	assert.Equal(t, 2*trust.PointsPerKind[trust.KindQuestCompleted]+500, twoQuests.Points())
}

// The bounty kind must be KNOWN to the service, or it is recorded and never
// rolled up -- a contribution that is invisible, which is what knownKind's refusal
// is there to prevent.
func TestTheBountyKindIsKnownButNotCounted(t *testing.T) {
	// It is absent from PointsPerKind, which is why knownKind cannot be keyed off
	// that map alone -- and its absence is deliberate, because a points-only kind
	// given a weight would either be counted twice or need a zero weight, which
	// is pointless by definition.
	_, weighted := trust.PointsPerKind[trust.KindBountyBonus]
	assert.False(t, weighted,
		"a points-only kind must NOT be in PointsPerKind: it is summed into "+
			"BonusPoints, and giving it a per-contribution weight would make it a "+
			"count as well")

	for _, kind := range trust.AllKinds {
		_, ok := trust.PointsPerKind[kind]
		require.True(t, ok, "kind %q is in AllKinds (the COUNTED set) so it must "+
			"have a weight -- the counts are summed as delta x weight", kind)
	}
}
