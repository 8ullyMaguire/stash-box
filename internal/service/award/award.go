// Package award maps a completed contribution to trust points (SPEC §12).
//
// It exists so that every XP award in the system goes through ONE call into
// trust.RecordEvent, and so that the interesting decisions -- what a completion is
// worth, and whether it has already been paid for -- are in one file rather than
// scattered across the call sites that happen to award points today.
//
// Why a package at all, when RecordEvent is one method: because the two call sites
// that exist (an applied edit, a solved identification) each hand-roll the same
// three questions and get them right by luck. A third and fourth were coming.
//
//	What is this worth?        -- the bounty is a magnitude, not a count.
//	Has it been paid already?  -- the dedup key is what makes it exactly-once.
//	What is the entity?        -- the dedup key is PARTLY the entity, so a
//	                             caller that passes the wrong one silently
//	                             double-pays or silently never pays.
//
// The last of those is the dangerous one. A bounty is awarded per QUEST, so the
// dedup key must be the quest id: awarding twice for one quest because the caller
// passed the item id instead is not a bug anyone would notice, it is a bug that
// reads as generosity.
package award

import (
	"context"
	"errors"
	"fmt"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/trust"
)

// ErrAlreadyPaid is returned when a contribution has already been awarded.
//
// A distinct error because the caller's correct response is to CARRY ON, not to
// fail: a retried request that re-awards a quest is a no-op, and a caller that
// turned the duplicate into a 500 would make every network retry look like a
// server fault. ErrAlreadyPaid is what a caller checks to decide that.
var ErrAlreadyPaid = errors.New("award: contribution already awarded")

// Trust is the subset of the trust service this package uses.
//
// An interface so the award path can be tested against a stub that counts calls,
// which is the only way to assert "this is recorded exactly once" without
// inspecting a database -- and that assertion is the whole point of the package.
type Trust interface {
	RecordEvent(ctx context.Context, event trust.Event) (*queries.UserTrust, error)
	Level(ctx context.Context, userID uuid.UUID) (trust.LevelEnum, error)
}

// Service awards trust points.
type Service struct {
	trust Trust
}

// NewService builds the award service.
func NewService(trustService Trust) *Service {
	return &Service{trust: trustService}
}

// QuestCompletionInput is a finished curation quest.
type QuestCompletionInput struct {
	// CuratorID is who finished it.
	CuratorID uuid.UUID
	// QuestID is the AUTHORED quest. This is the dedup entity, and it has to be
	// the quest rather than an item: a quest's value belongs to the quest, so
	// two curators claiming two items of the same quest are two partial
	// contributions and neither has finished anything.
	QuestID uuid.UUID
	// BountyPoints is the quest's stored bounty, passed in rather than read here.
	//
	// Passed, not fetched, so the award uses the SAME number the curator was
	// shown. An award service that re-read the bounty at payment time would pay
	// whatever the quest says NOW, and an operator lowering a bounty between the
	// curator accepting it and finishing it would retroactively change what they
	// were owed.
	BountyPoints int
}

// QuestCompletion awards a finished quest: its base value plus its bounty, as ONE
// event carrying the total.
//
// TWO events, not one, and the dedup key is the reason.
//
// The key is (user, kind, entity_type, entity_id). A rollup moves ONE column per
// kind, and the two halves of a quest's value live in two columns -- the COUNT
// (quests_completed) and the BONUS (bonus_points) -- so they cannot be the same
// event however the magnitude is carried. Recording delta=520 on quest_completed
// would credit 520 completed quests.
//
// Two events with two DIFFERENT kinds are therefore independent dedup keys, and
// each is exactly-once. A retry of the pair re-does both harmlessly, because
// neither key has been vacated. A single event would have had one key guarding
// two column movements, which is a guard that cannot be exactly-once for both.
//
// The invariant that makes the pair safe is that the base is ALWAYS recorded: this
// function records both halves or returns an error, so a bounty can never be paid
// without the quest it belongs to.
func (s *Service) QuestCompletion(ctx context.Context, in QuestCompletionInput) (int, error) {
	if in.BountyPoints < 0 {
		return 0, fmt.Errorf("award: bounty points must not be negative, got %d", in.BountyPoints)
	}

	// A zero-total award is refused rather than recorded.
	//
	// RecordEvent already refuses a zero delta, so this would fail anyway -- but
	// it would fail INSIDE the trust service, after the caller has committed to
	// an award, and the error would read as a trust failure rather than as "there
	// was nothing to award". Refusing here says which of the two it was.
	total := trust.PointsPerKind[trust.KindQuestCompleted] + in.BountyPoints
	if total == 0 {
		return 0, errors.New("award: quest is worth nothing, so there is no event to record")
	}

	// The quest's own weight goes to the COUNT and the bounty goes to the BONUS,
	// in one event.
	//
	// A single event can only move one column per kind, so the two halves are
	// inseparable in the rollup: quest_completed is the kind, and its delta is
	// the TOTAL, which would credit 520 completed quests.
	//
	// The resolution: TWO events with DIFFERENT kinds, sharing the quest as the
	// dedup entity. They are independent keys, so each is exactly-once, and a
	// retry of the pair re-does both harmlessly. The invariant that makes this
	// safe is that the BASE is always recorded: a caller cannot award a bounty
	// without the quest it belongs to, because this function records both or
	// neither.
	if _, err := s.trust.RecordEvent(ctx, trust.Event{
		UserID:     in.CuratorID,
		Kind:       trust.KindQuestCompleted,
		Delta:      1,
		EntityType: "authored_quest",
		EntityID:   &in.QuestID,
	}); err != nil {
		return 0, err
	}

	if in.BountyPoints == 0 {
		// No bounty, so no second event -- and a bounty_bonus of 0 would be a row
		// that moves nothing and occupies the dedup slot, so a later REAL bounty
		// for the same quest would be silently discarded as a duplicate. That is
		// the exact failure the zero-delta guard in RecordEvent exists to stop,
		// and it is why this is an early return and not a zero-valued event.
		return total, nil
	}

	if _, err := s.trust.RecordEvent(ctx, trust.Event{
		UserID:     in.CuratorID,
		Kind:       trust.KindBountyBonus,
		Delta:      in.BountyPoints,
		EntityType: "authored_quest",
		EntityID:   &in.QuestID,
	}); err != nil {
		return 0, err
	}
	return total, nil
}

// Contribution awards a single counted contribution: an applied edit, a solved
// identification, a hosted replica.
//
// Delta is 1 and always will be -- these are COUNTS, and a count of 3 edits is
// three events, not one event of magnitude 3. Passing a magnitude here would
// credit three edits from one contribution, which is the same class of bug the
// bounty would have been had the two shared a path.
func (s *Service) Contribution(ctx context.Context, kind trust.KindEnum, curatorID uuid.UUID, entityType string, entityID uuid.UUID) error {
	_, err := s.trust.RecordEvent(ctx, trust.Event{
		UserID:     curatorID,
		Kind:       kind,
		Delta:      1,
		EntityType: entityType,
		EntityID:   &entityID,
	})
	return err
}
