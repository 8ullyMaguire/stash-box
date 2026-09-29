package trust

// The content access gate (SPEC §7.23.3 W1, plan feature-03b §1).
//
// WHY THIS FILE IS A PURE FUNCTION. Every condition here is a boolean, and the
// bug that matters is a boolean wired the wrong way -- a disjunction where a
// conjunction belongs, or a check ordered after the thing it is supposed to
// constrain. Neither shows up in a test that walks a database; both show up
// instantly in a decision table. So the rules live here with no I/O, and the DB
// wrapper in service.go does nothing but fetch rows and hand them here.

// AccessRequest is everything the gate is allowed to know about a request.
//
// A struct rather than a parameter list because the list is five booleans and an
// int, and a caller that transposes two of them gets a gate that opens for the
// wrong users with no compiler error. Named fields make that mistake visible.
type AccessRequest struct {
	// Anonymous is true for an unauthenticated request.
	Anonymous bool

	// Level is the requester's recomputed trust level.
	//
	// Recomputed from rows, never read from a stored column -- SPEC §7.17.2. A
	// gate that reads a cached level inherits every bug in the thing that wrote
	// the cache, and the failure is a user with access they no longer qualify
	// for.
	Level LevelEnum

	// IsVanguard substitutes for the Level check ONLY. See IsVanguard's own
	// comment, and the comment on the gate below, because this is the one field
	// whose blast radius is routinely misjudged.
	IsVanguard bool

	// AdminOverride is an operator's explicit selection of this user.
	//
	// Also substitutes for the Level check only. An operator override that
	// bypassed the opt-in or the terms acceptance would be a way for one
	// operator mistake to grant permanent content access to someone who never
	// consented to it.
	AdminOverride bool

	// OptedIn is the user's own explicit choice to view content.
	//
	// NEVER bypassable. This is the user's decision about their own data, and
	// no role, badge, or operator override may make it for them.
	OptedIn bool

	// ContributionScore is the requester's contribution total.
	ContributionScore int64

	// MinContribution is the instance's configured threshold.
	MinContribution int64

	// TermsAccepted records the requester accepting the instance's content terms.
	//
	// NEVER bypassable. Accepting terms is an act, and an act cannot be inferred
	// from a trust level.
	TermsAccepted bool

	// Flagged marks viewing behaviour flagged for abuse.
	//
	// NEVER bypassable, and deliberately not modifiable by AdminOverride: an
	// override exists to let a legitimate user in, and a flagged account is not
	// a legitimate user the override was issued to. If an operator needs to
	// clear a flag, that is an action on the flag, not a content gate.
	Flagged bool
}

// AccessDecision is the gate's verdict.
type AccessDecision struct {
	// Allowed is the answer. True only if every condition held.
	Allowed bool

	// Reason is the FIRST failed condition in evaluation order, in the form a
	// user can act on.
	//
	// One reason rather than a list, because a user who is refused needs to be
	// told the next thing to DO. "Needs level 4, needs the opt-in, and needs to
	// accept the terms" is three pieces of homework; "accept the content terms"
	// is one. The full set is in Failed, for the operator.
	//
	// Human-readable strings, not sentinel errors. This value is shown to a
	// user, and an error type a user cannot read is a worse answer than a
	// sentence.
	Reason string

	// Failed lists EVERY failed condition, in evaluation order.
	//
	// For the operator dashboard and the admin override UI, where "why is this
	// user blocked" is a question about the whole picture and answering it with
	// one condition would be misleading.
	Failed []string
}

// Gate access conditions. Exported because the plan's tests assert on the
// condition being the one that fired, and a test that has to hardcode a string
// literal breaks when someone fixes a typo in a message.
const (
	ReasonAnonymous    = "content requires an account"
	ReasonLevel        = "requires trust level 4"
	ReasonOptIn        = "content opt-in is not set"
	ReasonContribution = "contribution score below the instance threshold"
	ReasonTerms        = "content terms not accepted"
	ReasonFlagged      = "viewing behaviour is flagged"
	ReasonRestricted   = "access to this content is restricted"
)

// EvaluateContentAccess is THE content access gate.
//
// FIVE CONDITIONS, ALL OF THEM REQUIRED. This is the whole point of the function
// and the reason it is written as a flat sequence of early returns rather than a
// clever expression: SPEC §7.23.3 W1 records that the draft specifies these as a
// disjunction -- "trust level >= 4 OR vanguard OR selected by admin" -- which
// makes the weakest of the controls the effective one. A vanguard or an override
// substitutes for the LEVEL check and for nothing else.
//
// The order is fixed and load-bearing, and it is:
//
//	anonymous -> level -> opt-in -> contribution -> terms -> flagged
//
// Anonymous first, because there is nothing else to check. Opt-in, contribution,
// terms and flagged BEFORE the restriction rules, because a restriction can only
// ever remove access, so a request that already failed cannot be rescued by it.
// And the level check before the opt-in, so an ineligible user's opt-in flag is
// never the thing that appears to grant access -- a user who opts in and is
// refused should be told they are not yet eligible, not sent to the opt-in
// screen they already completed.
//
// Every condition after the level check is one an override CANNOT bypass. That
// asymmetry is deliberate and is the only asymmetry: it is what makes the gate
// safe to expose to a UI with an "override" button on it.
func EvaluateContentAccess(r AccessRequest) AccessDecision {
	var failed []string

	fail := func(reason string) AccessDecision {
		failed = append(failed, reason)
		return AccessDecision{Allowed: false, Reason: reason, Failed: failed}
	}

	// 1. Anonymous. There is no opt-in to check, no score, no terms.
	if r.Anonymous {
		return fail(ReasonAnonymous)
	}

	// 2. Level, with the two substitutions.
	//
	// The disjunction here is CORRECT and is the only one in the function.
	// IsVanguard and AdminOverride are alternative ways to satisfy the
	// requirement "be a trusted user"; neither says anything about whether this
	// person consented to view content, which is what the remaining conditions
	// are about.
	if r.Level < LevelContentViewing && !r.IsVanguard && !r.AdminOverride {
		fail(ReasonLevel)
	}

	// 3. Opt-in. NEVER bypassed.
	if !r.OptedIn {
		fail(ReasonOptIn)
	}

	// 4. Contribution threshold. NEVER bypassed.
	if r.ContributionScore < r.MinContribution {
		fail(ReasonContribution)
	}

	// 5. Content terms. NEVER bypassed.
	if !r.TermsAccepted {
		fail(ReasonTerms)
	}

	// 6. Abuse flag. NEVER bypassed.
	if r.Flagged {
		fail(ReasonFlagged)
	}

	if len(failed) > 0 {
		// The FIRST failure is the one the user is told to act on, which is why
		// the reasons are appended in evaluation order and not sorted.
		return AccessDecision{Allowed: false, Reason: failed[0], Failed: failed}
	}
	return AccessDecision{Allowed: true}
}

// Restricted reports whether a content restriction rule removes access.
//
// SEPARATE from EvaluateContentAccess on purpose, and evaluated after it.
//
// A restriction is a DENYLIST: tag, studio, performer, region, time window, device
// class. It can only ever REMOVE access. If it were folded into the gate as
// another condition it would still only remove access, but folding it in would
// invite the mistake of treating a match as a grant -- "this entity is listed,
// therefore the user may see it" -- which is the failure that turns a denylist
// into an allowlist with confusing naming.
//
// SPEC §7.23.3 W2 and W4: the region and device-class rules are NOT evaluated
// here at all. They key on values the client supplies, so an application-level
// check is a check the client controls. They are recorded on the instance policy
// and enforced at the proxy, or reported as unenforced. See AccessRules.
func Restricted(restrictions []ContentRestriction, target EntityRef) bool {
	for _, r := range restrictions {
		if !r.AppliesTo(target) {
			continue
		}
		switch r.Kind {
		case RestrictionTag, RestrictionStudio, RestrictionPerformer:
			// A real denylist entry. It removes access, full stop.
			return true
		case RestrictionRegion, RestrictionDeviceClass, RestrictionTimeWindow:
			// Recorded but NOT enforced here, and the whole point of naming them
			// is that a caller who tries to enforce them here is reintroducing
			// W2/W4. They return false, which means "does not restrict", and the
			// policy reports them as unenforced.
			//
			// A control that appears to exist and does not is worse than one
			// that is visibly missing, so IsEnforceable exists to make the
			// difference observable.
			continue
		}
	}
	return false
}

// RestrictionKind is what an access restriction is keyed on.
type RestrictionKind string

const (
	RestrictionTag       RestrictionKind = "tag"
	RestrictionStudio    RestrictionKind = "studio"
	RestrictionPerformer RestrictionKind = "performer"
	// The three below are RECORDED, not enforced in this process. See Restricted.
	RestrictionRegion      RestrictionKind = "region"
	RestrictionDeviceClass RestrictionKind = "device_class"
	RestrictionTimeWindow  RestrictionKind = "time_window"
)

// IsEnforceable reports whether a rule can be checked by this process.
//
// A rule keyed on a value the client supplies cannot. A tag, studio or performer
// comes from our own database and is a fact; a region, device class or clock
// window comes from the request and is a claim.
func (k RestrictionKind) IsEnforceable() bool {
	switch k {
	case RestrictionTag, RestrictionStudio, RestrictionPerformer:
		return true
	default:
		// Deliberately a default-deny on the UNKNOWN kind too, not just on the
		// three named ones: a kind added later without a decision here must not
		// silently become enforceable.
		return false
	}
}

// ContentRestriction is one configured access rule.
type ContentRestriction struct {
	Kind RestrictionKind
	// Value is the entity id, tag name, region code, device class or time
	// window, per Kind.
	Value string
	// Until, for RestrictionTimeWindow, is the exclusive end of the window.
	Until string
}

// AppliesTo reports whether the restriction is about this entity.
//
// For the non-enforceable kinds there is no entity to match -- a region is not a
// property of a scene -- so they return false and never restrict. That is
// correct rather than a stub: the whole point is that this function cannot
// decide them.
func (r ContentRestriction) AppliesTo(e EntityRef) bool {
	switch r.Kind {
	case RestrictionTag:
		return contains(e.Tags, r.Value)
	case RestrictionStudio:
		return e.StudioID != "" && e.StudioID == r.Value
	case RestrictionPerformer:
		return contains(e.PerformerIDs, r.Value)
	default:
		// Region, device class and time window are not properties of the entity.
		// See AppliesTo's doc comment.
		return false
	}
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

// EntityRef is the content a request is for, in the terms the denylist matches on.
type EntityRef struct {
	StudioID     string
	PerformerIDs []string
	Tags         []string
}
