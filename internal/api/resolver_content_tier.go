package api

import (
	"context"
	"fmt"

	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/converter"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service/trust"
)

// Content tiers and the viewer's own consent (phase 5, migration 120).
//
// The moderation half and the user half live in one file because they are the
// two ends of one mechanism: a tag is classified, and a viewer opts into a tier
// ceiling. They are the only two ways a tier is ever set, and reading them
// together is how a reviewer checks that neither can GRANT visibility -- the
// tier raises an entity out of a view and the consent is the viewer's own.

// TagSetContentTier classifies a tag's tier.
//
// The range is checked HERE rather than left to the column's CHECK: a constraint
// violation reaches the moderator as `pq: new row for relation "tags" violates
// check constraint "tags_content_tier_range"`, and the useful sentence -- "a tier
// is 0 to 3" -- is one Go can say before the row is attempted.
func (r *mutationResolver) TagSetContentTier(ctx context.Context, input models.TagContentTierInput) (*models.Tag, error) {
	if input.Tier < 0 || input.Tier > trust.MaxContentTier {
		return nil, fmt.Errorf("a content tier is 0 to %d, got %d", trust.MaxContentTier, input.Tier)
	}

	tag, err := r.services.Trust().SetTagContentTier(ctx, input.ID, int16(input.Tier), input.RequiresOptIn)
	if err != nil {
		return nil, err
	}
	model := converter.TagToModel(*tag)
	return &model, nil
}

// UserSetVisibleTiers records the tiers the caller has opted into.
//
// Self-only and it takes no user id, because consent is an act the user
// performs and no role may perform it for them.
//
// The value is stored AS SENT. Collapsing it to its maximum here would destroy
// the record of what was consented to, which is what an audit reads; the gate
// derives its ceiling from the array when it compares.
func (r *mutationResolver) UserSetVisibleTiers(ctx context.Context, tiers []int) ([]int, error) {
	user := auth.GetCurrentUser(ctx)
	if user == nil {
		return nil, fmt.Errorf("content tier consent requires an account")
	}

	// Deduplicated: two entries for one tier is a UI bug rather than a different
	// consent, and the gate takes the maximum, so a duplicate would be invisible
	// in the verdict.
	seen := make(map[int16]bool, len(tiers))
	stored := make([]int16, 0, len(tiers))
	for _, tier := range tiers {
		if tier < 0 || tier > trust.MaxContentTier {
			return nil, fmt.Errorf("a content tier is 0 to %d, got %d", trust.MaxContentTier, tier)
		}
		value := int16(tier)
		if seen[value] {
			continue
		}
		seen[value] = true
		stored = append(stored, value)
	}

	updated, err := r.services.Trust().SetVisibleTiers(ctx, user.ID, stored)
	if err != nil {
		return nil, err
	}
	return int16sToInts(updated), nil
}

// VisibleContentTiers is the caller's own consent, as stored.
//
// Every tier in the array rather than the ceiling, because the screen that shows
// this is the one asking "which did you agree to" -- and a ceiling alone cannot
// render that: [3] and [1,2,3] are the same ceiling and different consents.
func (r *queryResolver) VisibleContentTiers(ctx context.Context) ([]int, error) {
	user := auth.GetCurrentUser(ctx)
	if user == nil {
		return nil, fmt.Errorf("content tier consent requires an account")
	}
	tiers, err := r.services.Trust().VisibleTiers(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	return int16sToInts(tiers), nil
}

func int16sToInts(values []int16) []int {
	out := make([]int, 0, len(values))
	for _, v := range values {
		out = append(out, int(v))
	}
	return out
}

// ContentAccessSetup answers the onboarding query, computing every field from the gate's own inputs.
//
// The account check is the gate's condition 1 restated as an API rule: an anonymous caller HAS no
// standing, and "you are anonymous" is not a step a wizard can offer.
func (r *queryResolver) ContentAccessSetup(ctx context.Context) (*models.ContentAccessSetup, error) {
	user := auth.GetCurrentUser(ctx)
	if user == nil {
		return nil, fmt.Errorf("content access setup requires an account")
	}

	setup, err := r.services.Trust().Setup(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	return &setup, nil
}

// UserAcceptContentTerms records the member's acceptance and returns their standing afterwards.
//
// The standing is re-read rather than assembled from the parts, because the point of returning it is
// that it is what the gate will see -- a response built from the local variables of this mutation would
// be the mutation's opinion of the member, which is exactly the drift the access setup query exists to
// prevent.
func (r *mutationResolver) UserAcceptContentTerms(ctx context.Context) (*models.ContentAccessSetup, error) {
	user := auth.GetCurrentUser(ctx)
	if user == nil {
		return nil, fmt.Errorf("content terms acceptance requires an account")
	}

	if err := r.services.Trust().AcceptContentTerms(ctx, user.ID); err != nil {
		return nil, err
	}

	setup, err := r.services.Trust().Setup(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	return &setup, nil
}
