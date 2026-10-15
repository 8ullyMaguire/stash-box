package seed

import (
	"context"

	"github.com/gofrs/uuid"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/trust"
)

// RunResult is what an operator is told after running a campaign.
//
// THREE NUMBERS RATHER THAN ONE, because "the campaign is done" hides the questions they are actually
// asking. Members is the cohort's size AT THE MOMENT OF THE RUN (which is what makes a member who left
// before it invisible here rather than counted as a failure), Paid is how many received the head start
// now, and Skipped is how many already had it -- from an earlier run, which is the normal answer on a
// re-run and must not read as an error.
type RunResult struct {
	Campaign *queries.SeedCampaign
	Members  int
	Paid     int
	Skipped  int
}

// RunCampaign pays every member of the campaign's cohort the head start, ONCE.
//
// THE ORDER OF THE TWO WRITES IS THE DESIGN. The payments go first and the campaign is marked as run
// last, so a run that dies halfway leaves a campaign that says it has not been run while some members
// have their points -- and the fix is to run it again, which pays only the ones who did not get it
// (the event log's dedup decides, not this function). Marking it run first would produce the opposite
// and much worse state: a campaign that says it was applied with members unpaid, and nothing to
// distinguish it from a finished one.
//
// It is NOT one transaction across all the members, deliberately: each payment is its own event on its
// own member, and wrapping a cohort of a thousand in one transaction would hold locks on a thousand
// rollup rows to make a report tidier. What that costs is atomicity, and what buys it back is that the
// run is RESUMABLE by construction -- which is a property of the ledger rather than of this code.
func (s *Service) RunCampaign(ctx context.Context, campaignID uuid.UUID, runBy uuid.UUID) (*RunResult, error) {
	campaign, err := s.Campaign(ctx, campaignID)
	if err != nil {
		return nil, err
	}

	// MEMBERSHIP, READ NOW. Not at creation, not cached, not from the last run: the cohort as it stands
	// is the answer to "who does this campaign pay".
	memberIDs, err := s.queries.ListSeedCohortMemberIDs(ctx, campaign.CohortID)
	if err != nil {
		return nil, err
	}

	result := &RunResult{Campaign: campaign, Members: len(memberIDs)}
	for _, memberID := range memberIDs {
		inserted, _, err := s.trust.RecordEventIfNew(ctx, trust.Event{
			UserID: memberID,
			Kind:   trust.KindSeedFastTrack,
			// The amount is per-campaign and travels as the delta, which is what the points-only kind
			// is for. The level follows from the same curve as every other point.
			Delta: int(campaign.Points),
			// The campaign's own id is the dedup key: one campaign pays one member once, however many
			// times it is run.
			EntityType: EntityTypeCampaign,
			EntityID:   &campaign.ID,
		})
		if err != nil {
			// The member's failure stops the run, and the campaign is left unmarked on purpose: the
			// operator runs it again and the members already paid are skipped. Returning the counts so
			// far in the error's place would invite treating a partial run as finished.
			return nil, err
		}
		if inserted {
			result.Paid++
		} else {
			result.Skipped++
		}
	}

	marked, err := s.queries.MarkSeedCampaignRun(ctx, queries.MarkSeedCampaignRunParams{
		ID:        campaignID,
		LastRunBy: uuid.NullUUID{UUID: runBy, Valid: true},
	})
	if err != nil {
		return nil, err
	}
	result.Campaign = &marked
	return result, nil
}
