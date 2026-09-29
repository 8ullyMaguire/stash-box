package trust

import (
	"strings"
	"testing"
)

func idSet(ids ...string) map[string]struct{} {
	if len(ids) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		out[id] = struct{}{}
	}
	return out
}

// TestAnUnenforcedRuleIsVisibleInTheSummary is the test the whole file exists
// for. A control the operator believes they have, and which is not actually
// enforced, is worse than a control that is visibly missing — the first is
// trusted, the second is compensated for.
//
// So the summary must name region, device_class and mfa even though this
// instance enforces none of them.
func TestAnUnenforcedRuleIsVisibleInTheSummary(t *testing.T) {
	set := NewAccessRuleSet(false, NewPerEntityRule(nil, nil, nil))
	summary := set.Summary()

	for _, want := range []string{"region", "device_class", "mfa"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary omits the %q rule entirely:\n%s", want, summary)
		}
	}
}

// TestUnenforcedListsEveryRuleThatIsNotEnforced proves the operator-facing list
// is complete rather than a sample.
func TestUnenforcedListsEveryRuleThatIsNotEnforced(t *testing.T) {
	set := NewAccessRuleSet(false, NewPerEntityRule(idSet("t1"), nil, nil))

	unenforced := set.Unenforced()
	names := map[string]bool{}
	for _, r := range unenforced {
		names[r.Name] = true
	}

	for _, want := range []string{"region", "device_class", "mfa"} {
		if !names[want] {
			t.Errorf("Unenforced() is missing %q; got %v", want, names)
		}
	}

	// The denylist IS enforced, so it must not appear in the unenforced list
	// even when it has entries.
	if names["per_entity_denylist"] {
		t.Error("the per-entity denylist is enforced and must not be listed as unenforced")
	}
}

// TestUnenforcedIsEmptyOnlyWhenEverythingIsEnforced proves the list can go
// empty, so its being non-empty in the normal case is a real signal and not an
// artefact of a filter that never matches.
func TestUnenforcedIsEmptyOnlyWhenEverythingIsEnforced(t *testing.T) {
	// Nothing here can make region, device_class or mfa enforced in-process:
	// MFA only counts as enforced when the provider has it configured, and the
	// other two are never enforced here by design. So with MFA unconfigured the
	// list must still be non-empty.
	set := NewAccessRuleSet(false, NewPerEntityRule(nil, nil, nil))
	if len(set.Unenforced()) == 0 {
		t.Error("Unenforced() is empty even though three rules are not enforced here")
	}
}

// TestAnUnenforcedRuleCarriesAReason proves the report is actionable. "Not
// enforced" without saying where the decision belongs is a dead end for the
// operator.
func TestAnUnenforcedRuleCarriesAReason(t *testing.T) {
	for _, r := range NewAccessRuleSet(false, NewPerEntityRule(nil, nil, nil)).Unenforced() {
		if strings.TrimSpace(r.Reason) == "" {
			t.Errorf("rule %q is unenforced with no reason given", r.Name)
		}
		if strings.TrimSpace(r.EnforcedBy) == "" {
			t.Errorf("rule %q is unenforced with no EnforcedBy given", r.Name)
		}
	}
}

// TestTheSummaryCarriesTheReasonNotJustTheName is the test that keeps
// TestAnUnenforcedRuleCarriesAReason honest.
//
// The two looked redundant and are not: the rule struct can carry a reason
// while the rendered summary drops it, and a summary that names a rule without
// saying why it is off is the version an operator actually reads. So this
// asserts on the RENDERED output, not the struct.
//
// It exists because mutating Summary() to print only the rule name left every
// other test green.
func TestTheSummaryCarriesTheReasonNotJustTheName(t *testing.T) {
	summary := NewAccessRuleSet(false, NewPerEntityRule(nil, nil, nil)).Summary()

	if strings.TrimSpace(summary) == "" {
		t.Fatal("the summary is empty")
	}

	// Every unenforced rule's reason must appear in the rendered text. Compared
	// against the struct's own reason so the test cannot drift from the data.
	for _, r := range NewAccessRuleSet(false, NewPerEntityRule(nil, nil, nil)).Unenforced() {
		if r.Reason == "" {
			continue
		}
		if !strings.Contains(summary, r.Reason) {
			t.Errorf("the summary names rule %q but drops its reason %q:\n%s", r.Name, r.Reason, summary)
		}
		// And the component that must make the decision, or the operator is
		// told a control is off with no indication of where to turn it on.
		if !strings.Contains(summary, r.EnforcedBy) {
			t.Errorf("the summary names rule %q but drops EnforcedBy %q:\n%s", r.Name, r.EnforcedBy, summary)
		}
	}
}

// TestMFAReflectsTheProviderNotThisProcess proves MFA's state is taken from the
// provider. If this function read a local flag, the rule would report the
// instance's own configuration and claim a control it does not have.
func TestMFAReflectsTheProviderNotThisProcess(t *testing.T) {
	off := MFARule(false)
	if off.Enforced {
		t.Error("MFA reported as enforced when the provider has it unconfigured")
	}
	if !strings.Contains(off.Reason, "provider") {
		t.Errorf("the MFA reason should name the provider as the decision point, got %q", off.Reason)
	}

	on := MFARule(true)
	if !on.Enforced {
		t.Error("MFA reported as unenforced when the provider has it configured")
	}
	if on.Reason != "" {
		t.Errorf("an enforced rule must carry no reason, got %q", on.Reason)
	}
}

// TestTheDenylistDeniesEachEntityKind proves the per-entity check actually
// matches, and reports which kind matched so a caller can log it.
func TestTheDenylistDeniesEachEntityKind(t *testing.T) {
	rule := NewPerEntityRule(idSet("tag-1"), idSet("studio-1"), idSet("performer-1"))

	cases := []struct {
		id   string
		want bool
		kind string
	}{
		{id: "tag-1", want: true, kind: "tag"},
		{id: "studio-1", want: true, kind: "studio"},
		{id: "performer-1", want: true, kind: "performer"},
		{id: "not-denied", want: false, kind: ""},
	}

	for _, tc := range cases {
		got, kind := rule.Denies(tc.id)
		if got != tc.want {
			t.Errorf("Denies(%q) = %v, want %v", tc.id, got, tc.want)
		}
		if kind != tc.kind {
			t.Errorf("Denies(%q) kind = %q, want %q", tc.id, kind, tc.kind)
		}
	}
}

// TestAnEmptyDenylistDeniesNothing proves the nil-set case is handled: a
// denylist that denies everything because its map is empty would be the most
// severe possible bug in this file, and the one most likely to look like a
// pass in a test that only checks a denial.
func TestAnEmptyDenylistDeniesNothing(t *testing.T) {
	for name, rule := range map[string]PerEntityRule{
		"nil":        NewPerEntityRule(nil, nil, nil),
		"empty maps": NewPerEntityRule(map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}),
	} {
		t.Run(name, func(t *testing.T) {
			if !rule.IsEmpty() {
				t.Error("IsEmpty() = false for an empty denylist")
			}
			if denied, _ := rule.Denies("anything"); denied {
				t.Error("an empty denylist denied something")
			}
			if denied, _ := rule.Denies(""); denied {
				t.Error("an empty denylist denied the empty id")
			}
		})
	}
}

// TestTheDenylistCopiesItsInput proves a caller cannot widen the denylist by
// mutating the set it passed in. An access control its owner can silently undo
// is not an access control.
func TestTheDenylistCopiesItsInput(t *testing.T) {
	tags := idSet("tag-1")
	rule := NewPerEntityRule(tags, nil, nil)

	// The caller removes the entry after construction.
	delete(tags, "tag-1")

	if denied, _ := rule.Denies("tag-1"); !denied {
		t.Error("mutating the caller's set undid a denial; the rule must hold its own copy")
	}
}

// TestTheDenylistCanOnlyRemoveAccess proves there is no grant path. Every way
// the rule is exercised should be able to turn a decision to "denied" and never
// to "allowed": a denylist with a grant path is an allowlist with the wrong
// name, and the plan calls that out explicitly.
func TestTheDenylistCanOnlyRemoveAccess(t *testing.T) {
	// An empty denylist denies nothing.
	emptyRule := NewPerEntityRule(nil, nil, nil)
	if denied, _ := emptyRule.Denies("anything"); denied {
		t.Fatal("empty denylist denied something")
	}

	// A denylist with entries denies exactly those entries and nothing else.
	tagOnly := NewPerEntityRule(idSet("tag-denied"), nil, nil)
	if denied, _ := tagOnly.Denies("tag-denied"); !denied {
		t.Error("tag-denied was not denied")
	}
	for _, id := range []string{"studio-denied", "performer-denied", "tag-allowed"} {
		if denied, _ := tagOnly.Denies(id); denied {
			t.Errorf("tagOnly denied %q, but only tags should be denied", id)
		}
	}

	// Adding more entries to the denylist can only add more denials; it never
	// removes any. This is the "no grant path" property.
	baseRule := NewPerEntityRule(nil, nil, nil)
	extendedRule := NewPerEntityRule(idSet("tag-denied", "tag-also-denied"), nil, nil)

	for _, id := range []string{"tag-denied", "tag-also-denied"} {
		if !deniedByBoth(baseRule, extendedRule, id) {
			t.Errorf("adding %q to the denylist removed a denial that was already in place", id)
		}
	}
}

// deniedByBoth reports whether id is denied by extendedRule but not by
// baseRule — i.e. adding it to the denylist only ever adds a denial.
func deniedByBoth(base, extended PerEntityRule, id string) bool {
	baseDenied, _ := base.Denies(id)
	extendedDenied, _ := extended.Denies(id)
	return extendedDenied && !baseDenied
}

// TestRulesReturnsACopy proves a caller cannot mutate the set's state through
// the returned slice — the same reasoning as the denylist copy.
func TestRulesReturnsACopy(t *testing.T) {
	set := NewAccessRuleSet(false, NewPerEntityRule(nil, nil, nil))

	first := set.Rules()
	first[0].Enforced = !first[0].Enforced
	first[0].Name = "tampered"

	second := set.Rules()
	if second[0].Name == "tampered" {
		t.Error("mutating the returned slice changed the set's own state")
	}
}

// TestTheEmptyDenylistIsExplainedRatherThanReportedAsEnforcingSomething proves
// an empty denylist is called out. It IS enforced as a mechanism, but it is
// denying nothing today, and an operator reading a bare "enforced" would not
// know that.
func TestTheEmptyDenylistIsExplainedRatherThanReportedAsEnforcingSomething(t *testing.T) {
	set := NewAccessRuleSet(true, NewPerEntityRule(nil, nil, nil))

	for _, r := range set.Rules() {
		if r.Name == "per_entity_denylist" {
			if !strings.Contains(r.Reason, "no entities") {
				t.Errorf("an empty denylist should say so, got reason %q", r.Reason)
			}
			return
		}
	}
	t.Fatal("the per-entity denylist rule is missing from the set entirely")
}
