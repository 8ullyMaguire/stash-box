// Package seed manages seed cohorts and the campaigns that pay them a head start
// (plan phase 11 slice 11d).
//
// TWO RULES DECIDE EVERYTHING IN HERE, and both come from the trust phase rather than from growth:
//
//  1. THE FAST-TRACK IS A TRUST EVENT, NEVER A LEVEL OVERRIDE. A campaign awards POINTS; the level then
//     follows from the one curve (`LevelForTotals`). A campaign that wrote a level directly would be a
//     second answer to "what level is this person" -- the exact mistake the trust phase exists to
//     avoid -- and the two answers would disagree the first time a member contributed anything.
//
//  2. MEMBERSHIP IS READ AT RUN TIME. A campaign pays the members its cohort has WHEN IT RUNS, so a
//     member who left the cohort before the run is not paid, and a member added after the last run is
//     paid by the next one. Nothing about membership is cached into a payment.
//
// The head start is paid ONCE per (member, campaign), and that is enforced by trust_events' own dedup
// index rather than by any flag in this package: the campaign's id travels in the event's entity_id, so
// a second run conflicts and inserts nothing. A re-run is therefore safe, and it is also how a run that
// failed halfway finishes -- see RunCampaign.
package seed

import (
	"context"
	"errors"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/trust"
)

// EntityTypeCampaign is the entity_type stamped on a campaign's trust events.
//
// It is what makes the dedup key unique to THIS KIND of payment: (user, kind, entity_type, entity_id)
// with the campaign's id in entity_id means one campaign pays one member once, and two campaigns each
// pay once. A bare "seed" would fold every campaign's payment for a member into one slot.
const EntityTypeCampaign = "seed_campaign"

var (
	// ErrNoCohort is returned when a cohort id names nothing.
	ErrNoCohort = errors.New("no such seed cohort")
	// ErrNoCampaign is returned when a campaign id names nothing.
	ErrNoCampaign = errors.New("no such seed campaign")
)

// Recorder is the trust service as this package needs it: something that records a points event and
// says whether it was NEW.
//
// An interface rather than the concrete *trust.Trust, so the run's own rules -- read the membership, pay
// each member once, report paid versus already-paid -- can be tested without a database. Note which
// method it is: `RecordEventIfNew` exists for this caller, because "paid" and "already paid" are two
// different lines in the report the operator asked for, and `RecordEvent` deliberately does not tell
// them apart.
type Recorder interface {
	RecordEventIfNew(ctx context.Context, event trust.Event) (bool, *queries.UserTrust, error)
}

type Service struct {
	queries *queries.Queries
	withTxn queries.WithTxnFunc
	trust   Recorder
}

func NewService(queries *queries.Queries, withTxn queries.WithTxnFunc, recorder Recorder) *Service {
	return &Service{
		queries: queries,
		withTxn: withTxn,
		trust:   recorder,
	}
}

// MemberCount is how many members the cohort has RIGHT NOW.
//
// Not how many a campaign paid, and the two are different questions: this is the roster, that is the
// ledger. A member who left after a run is still in the ledger and no longer in the roster.
func (s *Service) MemberCount(ctx context.Context, cohortID uuid.UUID) (int, error) {
	return s.queries.CountSeedCohortMembers(ctx, cohortID)
}

// CreateCohort makes a named set of members. The name is unique, and the constraint's error is what a
// duplicate name returns -- there is no check-then-insert, which is a race with a period of one
// request.
func (s *Service) CreateCohort(ctx context.Context, name string, description *string) (*queries.SeedCohort, error) {
	cohort, err := s.queries.CreateSeedCohort(ctx, queries.CreateSeedCohortParams{
		Name:        name,
		Description: description,
	})
	if err != nil {
		return nil, err
	}
	return &cohort, nil
}

func (s *Service) Cohorts(ctx context.Context) ([]queries.SeedCohort, error) {
	return s.queries.ListSeedCohorts(ctx)
}

func (s *Service) Cohort(ctx context.Context, id uuid.UUID) (*queries.SeedCohort, error) {
	cohort, err := s.queries.FindSeedCohort(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoCohort
	}
	if err != nil {
		return nil, err
	}
	return &cohort, nil
}

// CohortMembers lists the cohort's members with names, for the operator's screen.
func (s *Service) CohortMembers(ctx context.Context, cohortID uuid.UUID) ([]queries.ListSeedCohortMembersRow, error) {
	return s.queries.ListSeedCohortMembers(ctx, cohortID)
}

// AddMembers puts accounts in a cohort and reports HOW MANY were newly added.
//
// The count is the difference, not the length of the input: adding somebody who is already a member is
// a no-op (the pair is the fact), and an operator who selected a list of twenty and is told "twenty
// added" when three were already there has been told something false.
func (s *Service) AddMembers(ctx context.Context, cohortID uuid.UUID, userIDs []uuid.UUID) (int, error) {
	if _, err := s.Cohort(ctx, cohortID); err != nil {
		return 0, err
	}

	before, err := s.queries.CountSeedCohortMembers(ctx, cohortID)
	if err != nil {
		return 0, err
	}

	err = s.withTxn(func(tx *queries.Queries) error {
		for _, userID := range userIDs {
			if err := tx.AddSeedCohortMember(ctx, queries.AddSeedCohortMemberParams{
				CohortID: cohortID,
				UserID:   userID,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}

	after, err := s.queries.CountSeedCohortMembers(ctx, cohortID)
	if err != nil {
		return 0, err
	}
	return after - before, nil
}

// RemoveMember takes an account out of a cohort and reports whether it was in it.
//
// The boolean is not decoration: "removed" and "was never a member" are different answers, and the
// second usually means the operator is looking at the wrong cohort. Nothing else happens -- the members
// a previous run already paid keep their points, because those points are in the ledger.
func (s *Service) RemoveMember(ctx context.Context, cohortID uuid.UUID, userID uuid.UUID) (bool, error) {
	removed, err := s.queries.RemoveSeedCohortMember(ctx, queries.RemoveSeedCohortMemberParams{
		CohortID: cohortID,
		UserID:   userID,
	})
	if err != nil {
		return false, err
	}
	return removed > 0, nil
}

func (s *Service) CreateCampaign(ctx context.Context, cohortID uuid.UUID, name string, points int, createdBy uuid.UUID) (*queries.SeedCampaign, error) {
	if _, err := s.Cohort(ctx, cohortID); err != nil {
		return nil, err
	}

	// The database's CHECK is the rule; this is the message. A campaign is a head start, and a
	// zero-or-negative one would either record an event that moves nothing (which the trust service
	// refuses outright -- it would still occupy the dedup slot) or pay people to lose standing.
	if points <= 0 {
		return nil, errors.New("a campaign's head start must be positive")
	}

	campaign, err := s.queries.CreateSeedCampaign(ctx, queries.CreateSeedCampaignParams{
		CohortID:  cohortID,
		Name:      name,
		Points:    points,
		CreatedBy: uuid.NullUUID{UUID: createdBy, Valid: true},
	})
	if err != nil {
		return nil, err
	}
	return &campaign, nil
}

func (s *Service) Campaigns(ctx context.Context) ([]queries.SeedCampaign, error) {
	return s.queries.ListSeedCampaigns(ctx)
}

func (s *Service) Campaign(ctx context.Context, id uuid.UUID) (*queries.SeedCampaign, error) {
	campaign, err := s.queries.FindSeedCampaign(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoCampaign
	}
	if err != nil {
		return nil, err
	}
	return &campaign, nil
}
