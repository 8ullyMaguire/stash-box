package trust

import "fmt"

// AccessRule is a restriction the operator can configure.
//
// The central fact about every rule in this file: a rule that this type reports
// as UNENFORCED still exists, and hiding it is worse than the rule not existing.
// An operator who believes a region or device-class restriction is active when
// it is only being logged has a false picture of their own instance's security,
// and would not learn otherwise until something they rely on failed.
//
// So `Enforced` is a first-class field, and a rule that is recorded but not
// enforced is surfaced as such rather than quietly omitted.
type AccessRule struct {
	// Name identifies the rule in operator output.
	Name string
	// Enforced reports whether this instance actually blocks on the rule.
	Enforced bool
	// EnforcedBy names the component that makes the decision, so "unenforced"
	// points at where the decision must be made instead of nowhere.
	EnforcedBy string
	// Reason explains why it is not enforced here, when it is not. Empty when
	// Enforced is true.
	Reason string
}

// Enforcement states, so an operator tool has one vocabulary rather than a
// bool and a string it has to interpret.
const (
	// EnforcedHere means this instance makes the decision itself.
	EnforcedHere = "enforced here"
	// EnforcedUpstream means the decision belongs to a proxy or gateway in
	// front of the app, and this instance only records it.
	EnforcedUpstream = "enforced upstream"
	// RecordedOnly means nothing enforces it; it is logged as an anomaly
	// signal for a human to act on.
	RecordedOnly = "recorded only"
)

// RegionRule restricts content access by the requester's region.
//
// D2: recorded, not enforced. An application-level check on a client-supplied
// geo header is a check the client controls — the header is a claim, and a
// claim is not a region. Real enforcement belongs in the proxy, which sees the
// actual connection, and which is also the only component here that can be
// configured outside a code deploy.
//
// That reasoning is why this is not merely deferred: enforcing it in this
// process would be *worse* than not enforcing it, because it would create the
// appearance of a control while being trivially bypassed.
func RegionRule() AccessRule {
	return AccessRule{
		Name:       "region",
		Enforced:   false,
		EnforcedBy: "reverse proxy / geo module",
		Reason: "a client-supplied geo header is a claim, not a region; only the " +
			"proxy sees the real connection. Enforcing it here would create the " +
			"appearance of a control while being trivially bypassed",
	}
}

// DeviceClassRule restricts content access by the class of client device.
//
// D2: recorded, not enforced, for the same reason as RegionRule. A User-Agent
// header is entirely client-controlled, and there is no trustworthy device
// signal available to the application itself. Anything derived from it is a
// usability hint, not an authorisation input.
func DeviceClassRule() AccessRule {
	return AccessRule{
		Name:       "device_class",
		Enforced:   false,
		EnforcedBy: "reverse proxy / client-certificate policy",
		Reason: "User-Agent is entirely client-controlled and there is no trustworthy " +
			"device signal in-process; a device check here would be a check the " +
			"client could lie past",
	}
}

// MFARule records whether MFA is required of the authentication provider.
//
// D3: this is a requirement ON the auth provider, not a code path here. stash-box
// does not implement MFA; it delegates authentication entirely. So the honest
// statement is that MFA is out of this instance's hands, and this type records
// the requirement so an operator can see it is unmet rather than assuming it is
// met by the existence of a login form.
func MFARule(mfaConfiguredByProvider bool) AccessRule {
	if mfaConfiguredByProvider {
		return AccessRule{
			Name:       "mfa",
			Enforced:   true,
			EnforcedBy: "authentication provider",
		}
	}
	return AccessRule{
		Name:       "mfa",
		Enforced:   false,
		EnforcedBy: "authentication provider",
		Reason: "not configured on the auth provider; stash-box delegates " +
			"authentication entirely and cannot enforce MFA itself",
	}
}

// PerEntityRule is the denylist by tag, studio, or performer.
//
// This is the ONE access rule actually enforced in this process, and the
// distinction is structural rather than a matter of degree: the entity identity
// is data this instance owns, so it can be checked authoritatively. A region or
// a User-Agent is a claim from the client; a tag is a row in this database.
//
// It can only ever REMOVE access. A rule that could grant access would be a
// rule naming an unlisted entity as visible, which is a grant and not a
// restriction — and a denylist with a grant path is an allowlist wearing the
// wrong name.
type PerEntityRule struct {
	// DeniedTagIDs, DeniedStudioIDs and DeniedPerformerIDs are the denylisted
	// entities. A nil map means "nothing denied", which is distinct from an
	// empty map for the same reason taste_vectors has no row for a user with
	// no votes.
	DeniedTagIDs       map[string]struct{}
	DeniedStudioIDs    map[string]struct{}
	DeniedPerformerIDs map[string]struct{}
}

// NewPerEntityRule builds a denylist from the three entity id sets. It copies
// the sets, so a caller mutating its input afterwards cannot widen the denylist
// behind the rule's back — an access control that its owner can silently
// un-deny is not an access control.
func NewPerEntityRule(tags, studios, performers map[string]struct{}) PerEntityRule {
	return PerEntityRule{
		DeniedTagIDs:       copyIDSet(tags),
		DeniedStudioIDs:    copyIDSet(studios),
		DeniedPerformerIDs: copyIDSet(performers),
	}
}

func copyIDSet(in map[string]struct{}) map[string]struct{} {
	if in == nil {
		return nil
	}
	out := make(map[string]struct{}, len(in))
	for k := range in {
		out[k] = struct{}{}
	}
	return out
}

// Denies reports whether the given entity is denied by ANY denylist, and which
// kind of entity it was treated as.
//
// A single call over all three sets rather than a per-kind lookup, because a
// caller holding an entity id alone frequently does not know or care which kind
// it is, and a lookup that needs the kind to be supplied invites a caller to
// guess wrong and miss a denial.
func (r PerEntityRule) Denies(id string) (bool, string) {
	if _, ok := r.DeniedTagIDs[id]; ok {
		return true, "tag"
	}
	if _, ok := r.DeniedStudioIDs[id]; ok {
		return true, "studio"
	}
	if _, ok := r.DeniedPerformerIDs[id]; ok {
		return true, "performer"
	}
	return false, ""
}

// AccessRuleSet is the full picture for one instance: the rules that are
// enforced, the rules that are recorded, and the reason for each of the latter.
type AccessRuleSet struct {
	rules []AccessRule
}

// NewAccessRuleSet assembles the set, taking the provider's MFA state as an
// input rather than reading it, so the function stays pure and testable.
func NewAccessRuleSet(mfaConfiguredByProvider bool, perEntity PerEntityRule) AccessRuleSet {
	perEntityStatus := AccessRule{
		Name:       "per_entity_denylist",
		Enforced:   true,
		EnforcedBy: "internal/service/trust",
	}
	if perEntity.IsEmpty() {
		// Not unenforced — an empty denylist is a denylist that denies nothing
		// today. Reporting it as unenforced would be wrong, and reporting it as
		// enforcing something it has no entries for would be equally wrong, so
		// it is called out explicitly in the reason.
		perEntityStatus.Reason = "no entities are currently denylisted"
	}

	return AccessRuleSet{
		rules: []AccessRule{
			perEntityStatus,
			RegionRule(),
			DeviceClassRule(),
			MFARule(mfaConfiguredByProvider),
		},
	}
}

// IsEmpty reports whether the denylist has any entries at all.
func (r PerEntityRule) IsEmpty() bool {
	return len(r.DeniedTagIDs) == 0 && len(r.DeniedStudioIDs) == 0 && len(r.DeniedPerformerIDs) == 0
}

// Rules returns the full set, for an operator dashboard or a config dump.
func (s AccessRuleSet) Rules() []AccessRule {
	out := make([]AccessRule, len(s.rules))
	copy(out, s.rules)
	return out
}

// Unenforced returns only the rules this instance does not enforce, each with
// the reason and the component that must.
//
// This is the function the plan's warning is about: an unenforced rule must be
// VISIBLE. It exists so the operator UI can render "these controls are not
// active here, and here is where they would be" rather than omitting them and
// leaving the operator to assume they work.
func (s AccessRuleSet) Unenforced() []AccessRule {
	var out []AccessRule
	for _, r := range s.rules {
		if !r.Enforced {
			out = append(out, r)
		}
	}
	return out
}

// Summary renders the set for an operator. It names every rule and its state,
// which is the point: a summary that lists only the enforced rules is an
// incomplete report that reads as a complete one.
func (s AccessRuleSet) Summary() string {
	out := ""
	for _, r := range s.rules {
		if r.Enforced {
			out += fmt.Sprintf("%s: %s (%s)\n", r.Name, EnforcedHere, r.EnforcedBy)
			continue
		}
		out += fmt.Sprintf("%s: %s — %s. %s\n", r.Name, EnforcedUpstream, r.EnforcedBy, r.Reason)
	}
	return out
}
