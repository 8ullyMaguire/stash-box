//go:build integration

package api_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// GraphQL exposure for the access-rule set (SPEC §7.23 D2/D5).
//
// The service layer is covered by accessrules_test.go. What is untested until now
// is the query, and the query is where an unenforced rule can go missing: the
// resolver builds the response, and a resolver that returns only the enforced
// rules produces a perfectly valid response that tells an operator they have
// four controls when they have one.
//
// So the property under test is not "the query works" but "the unenforced rules
// survive the trip through GraphQL".

type accessRuleOutput struct {
	Name       string `json:"name"`
	Enforced   bool   `json:"enforced"`
	State      string `json:"state"`
	EnforcedBy string `json:"enforced_by"`
	Reason     string `json:"reason"`
}

type accessRuleSetOutput struct {
	AccessRules accessRuleSetBody `json:"accessRules"`
}

type accessRuleSetBody struct {
	Rules      []accessRuleOutput `json:"rules"`
	Unenforced []accessRuleOutput `json:"unenforced"`
}

func (c *graphqlClient) accessRules() (*accessRuleSetOutput, error) {
	var out accessRuleSetOutput
	err := c.Post(`query { accessRules { rules { name enforced state enforced_by reason } unenforced { name enforced state enforced_by reason } } }`, &out)
	return &out, err
}

// TestAccessRulesReportsUnenforcedRulesOverGraphQL is the query-level version of
// the invariant the service tests. Admin-only by directive, and it returns the
// rules this instance does NOT enforce.
func TestAccessRulesReportsUnenforcedRulesOverGraphQL(t *testing.T) {
	out, err := asAdmin(t).client.accessRules()
	require.NoError(t, err, "an admin querying the access rules")

	byName := map[string]accessRuleOutput{}
	for _, r := range out.AccessRules.Rules {
		byName[r.Name] = r
	}

	for _, want := range []string{"region", "device_class", "mfa", "per_entity_denylist"} {
		if _, ok := byName[want]; !ok {
			t.Errorf("accessRules omitted %q entirely; got %v", want, ruleNames(out.AccessRules.Rules))
		}
	}

	// The point of the whole feature: the unenforced list is populated.
	assert.NotEmpty(t, out.AccessRules.Unenforced,
		"accessRules returned an empty unenforced list, so an admin cannot tell which controls are inactive")

	// And it is a SUBSET of the full list, not a separate hand-maintained one.
	// A rule appearing only in `unenforced` would mean the two disagree.
	inRules := map[string]bool{}
	for _, r := range out.AccessRules.Rules {
		inRules[r.Name] = true
	}
	for _, r := range out.AccessRules.Unenforced {
		assert.True(t, inRules[r.Name],
			"rule %q appears in unenforced but not in rules", r.Name)
		assert.False(t, r.Enforced,
			"rule %q is listed as unenforced but reports enforced=true", r.Name)
	}
}

// TestThePerEntityDenylistIsReportedAsEnforcedHere proves the one rule this
// instance does enforce is not mislabelled.
//
// The per-entity denylist is enforced by trust.CanViewContent against rows in
// this database. Reporting it as "enforced upstream" would send an operator to
// look at their proxy for a control that has always been in the app.
func TestThePerEntityDenylistIsReportedAsEnforcedHere(t *testing.T) {
	out, err := asAdmin(t).client.accessRules()
	require.NoError(t, err)

	var found bool
	for _, r := range out.AccessRules.Rules {
		if r.Name != "per_entity_denylist" {
			continue
		}
		found = true
		assert.True(t, r.Enforced, "the per-entity denylist should report enforced=true")
		assert.Equal(t, "enforced here", r.State,
			"the per-entity denylist is decided in this process, so its state should say so")
	}
	assert.True(t, found, "accessRules did not report the per-entity denylist")
}

// TestRegionAndDeviceClassAreReportedAsUnenforced proves the two rules that
// cannot be enforced in-process say so, rather than being quietly omitted.
func TestRegionAndDeviceClassAreReportedAsUnenforced(t *testing.T) {
	out, err := asAdmin(t).client.accessRules()
	require.NoError(t, err)

	unenforced := map[string]accessRuleOutput{}
	for _, r := range out.AccessRules.Unenforced {
		unenforced[r.Name] = r
	}

	for _, name := range []string{"region", "device_class"} {
		r, ok := unenforced[name]
		if !ok {
			t.Errorf("%q is not in the unenforced list; got %v", name, ruleNames(out.AccessRules.Unenforced))
			continue
		}
		assert.NotEmpty(t, r.Reason, "%q is unenforced with no reason", name)
		assert.NotEmpty(t, r.EnforcedBy, "%q is unenforced with no EnforcedBy", name)
	}
}

// TestAccessRulesIsAdminOnly proves the @hasRole directive is actually on the
// field.
//
// Without this test, dropping the directive would leave every other test here
// passing and quietly turn a security posture report into a public one.
func TestAccessRulesIsAdminOnly(t *testing.T) {
	// Every role below ADMIN. A non-admin reading this query learns which
	// controls are NOT active on the instance, which is exactly the information
	// the @hasRole directive exists to withhold.
	for name, runner := range map[string]*testRunner{
		"read":     asRead(t),
		"none":     asNone(t),
		"edit":     asEdit(t),
		"modify":   asModify(t),
		"moderate": asModerate(t),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := runner.client.accessRules()
			assert.Error(t, err,
				"a %s-role user read accessRules successfully; the @hasRole(ADMIN) "+
					"directive is not doing its job", name)
		})
	}
}

func ruleNames(rules []accessRuleOutput) []string {
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		out = append(out, r.Name)
	}
	return out
}
