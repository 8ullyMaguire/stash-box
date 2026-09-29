package api

import (
	"context"

	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service/trust"
)

// AccessRules reports every access-restriction rule this instance knows about,
// with its enforcement state (SPEC §7.23 D2/D5).
//
// The denylist is read from the service rather than from configuration here,
// because the rule set's job is to REPORT and not to decide. The decision is
// the gate in trust.CanViewContent; a query that read the denylist itself would
// be a second place where the access decision is made, and the two would drift.
//
// The MFA flag is passed as false, and that is the honest answer rather than a
// placeholder. MFA is a property of the authentication provider (plan D3) and
// this process has no way to observe it: there is no provider introspection
// here, so the query reports MFA as unenforced, which tells an operator to go
// and check their provider. Asserting it from a local flag would report this
// instance's own configuration and claim a control it does not have -- the exact
// failure the `enforced` field exists to prevent.
//
// Wiring an actual provider probe is the follow-up, and it belongs at the auth
// layer rather than here.
func (r *queryResolver) AccessRules(ctx context.Context) (*models.AccessRuleSet, error) {
	perEntity, err := r.services.Trust().ContentDenylist(ctx)
	if err != nil {
		return nil, err
	}

	set := trust.NewAccessRuleSet(false, perEntity)
	return &models.AccessRuleSet{
		Rules:      accessRuleModels(set.Rules()),
		Unenforced: accessRuleModels(set.Unenforced()),
	}, nil
}

// accessRuleModels maps the service's rules onto the generated GraphQL types.
//
// The Enforced -> state mapping is a switch on a two-valued field, and getting
// it wrong would render an unenforced rule as "enforced here" -- the exact
// failure this query exists to prevent, so it is not left to a caller to
// remember. Non-nil slices throughout, because the schema declares both lists
// non-null and a nil slice serialises as null.
func accessRuleModels(rules []trust.AccessRule) []models.AccessRule {
	out := make([]models.AccessRule, 0, len(rules))
	for _, rule := range rules {
		state := trust.EnforcedUpstream
		if rule.Enforced {
			state = trust.EnforcedHere
		}
		out = append(out, models.AccessRule{
			Name:       rule.Name,
			Enforced:   rule.Enforced,
			State:      state,
			EnforcedBy: rule.EnforcedBy,
			Reason:     rule.Reason,
		})
	}
	return out
}
