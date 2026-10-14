package trust

import (
	"context"
	"fmt"
	"time"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/models"
)

// MinContribution is the instance's floor for gate condition 4 (ReasonContribution), which the gate's
// own comment calls NEVER bypassed.
//
// The score counts contributions ACCEPTED BY THE INSTANCE (approved edits, identifications, quests,
// replicas), so the floor is a record of participation rather than a quantity of activity: in the low
// tens, high enough that ten accepted things is a real history and low enough that a member who has
// done ten of them is past it. It is a Go constant because no per-instance settings surface exists for
// it yet, and it is the ONLY place the number appears -- the request the gate is given and the
// `required` field the wizard is shown both read it here, which is the whole drift guard.
const MinContribution int64 = 10

// Setup answers, for one member, what the content gate reads about THEM: the conditions they decide
// (terms, the content-viewing opt-in) and the ones they earn (level aside -- see below), plus the tier
// ladder with the cooling period applied.
//
// WHICH CONDITIONS ARE ABSENT IS A DECISION. The gate has seven. Level, flagged and the tier condition
// are not reported: level is not something a wizard advances by pressing a button, a viewer's flag state
// is not a step, and the tier condition depends on WHICH entity is being looked at (the ladder is
// reported instead, because that is the part the member controls). The three that remain are exactly
// the ones a member can act on -- and the denylist is not about the viewer at all.
//
// The comparison `current >= required` is made HERE rather than left to the client, for the same reason
// the tiers are computed from the array: a client re-implementing the gate's arithmetic is a second
// definition of the gate.
func (t *Trust) Setup(ctx context.Context, userID uuid.UUID) (models.ContentAccessSetup, error) {
	row, err := t.queries.UserAccessSetup(ctx, userID)
	if err != nil {
		return models.ContentAccessSetup{}, fmt.Errorf("reading the viewer's content access standing: %w", err)
	}

	tiers := make([]models.TierStanding, 0, MaxContentTier+1)
	for tier := 0; tier <= MaxContentTier; tier++ {
		optedIn := containsTier(row.VisibleTiers, tier)

		// THE T3 COOLING, REPORTED RATHER THAN HIDDEN. A consent that includes tier 3 is recorded
		// immediately and effective T3CoolingHours later, so "the member opted in" and "the member has
		// tier 3" are two different facts for that window. A wizard that reported only the first would
		// tell a member to do what they had just done; reporting when it takes effect is the whole
		// point of having a cooling period at all.
		var availableAt *time.Time
		if tier == MaxContentTier && optedIn && row.T3ConsentedAt.Valid {
			if effective := row.T3ConsentedAt.Time.Add(T3CoolingHours * time.Hour); effective.After(time.Now()) {
				availableAt = &effective
			}
		}

		tiers = append(tiers, models.TierStanding{
			Tier:        tier,
			OptedIn:     optedIn,
			AvailableAt: availableAt,
		})
	}

	return models.ContentAccessSetup{
		TermsAccepted: row.TermsAccepted,
		OptedIn:       row.OptedIn,
		ContributionScore: &models.ContributionStanding{
			Current:  int(row.ContributionScore),
			Required: int(MinContribution),
			Met:      row.ContributionScore >= MinContribution,
		},
		Tiers: tiers,
	}, nil
}

// AcceptContentTerms records the member's acceptance of the content-viewing terms (gate condition 5).
//
// Idempotent and first-write-wins in SQL: the column keeps the earliest acceptance, so a member
// re-reading the terms and pressing accept does not rewrite when they first agreed. The upsert covers
// a member with no user_trust row, since every column there has a default.
func (t *Trust) AcceptContentTerms(ctx context.Context, userID uuid.UUID) error {
	if err := t.queries.AcceptContentTerms(ctx, userID); err != nil {
		return fmt.Errorf("recording acceptance of the content terms: %w", err)
	}
	return nil
}

// containsTier reports whether the member's consent array includes a tier. The array is int4[] in
// Postgres, not smallint[]: the tier numbers are small but the column was created as INTEGER[] by
// migration 120, and a conversion here would be one more place for the two to disagree.
func containsTier(tiers []int, tier int) bool {
	for _, t := range tiers {
		if t == tier {
			return true
		}
	}
	return false
}
