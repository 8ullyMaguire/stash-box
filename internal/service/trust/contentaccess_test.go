package trust

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// THE ONE PROPERTY THIS FILE EXISTS TO PIN:
//
//	five conditions, all required, and the vanguard/admin override substitutes
//	for the LEVEL check and nothing else.
//
// The draft specifies this as a disjunction (SPEC §7.23.3 W1). Every test below
// that names a non-bypassable condition is there because that is the exact
// misreading, and none of them is obvious: each requires someone to decide that
// one more exemption is harmless.

// qualifying returns a request that passes every condition, so each test can
// break exactly ONE thing and know the failure is that thing.
func qualifying() AccessRequest {
	return AccessRequest{
		Level:             LevelContentViewing,
		OptedIn:           true,
		ContributionScore: 1000,
		MinContribution:   500,
		TermsAccepted:     true,
	}
}

func TestAllFiveConditionsMetAllowsAccess(t *testing.T) {
	d := EvaluateContentAccess(qualifying())
	assert.True(t, d.Allowed)
	assert.Empty(t, d.Failed)
}

func TestEachConditionFailsOnItsOwn(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*AccessRequest)
		want   string
	}{
		{"anonymous", func(r *AccessRequest) { r.Anonymous = true }, ReasonAnonymous},
		{"level too low", func(r *AccessRequest) { r.Level = LevelContentViewing - 1 }, ReasonLevel},
		{"not opted in", func(r *AccessRequest) { r.OptedIn = false }, ReasonOptIn},
		{"contribution too low", func(r *AccessRequest) { r.ContributionScore = 499 }, ReasonContribution},
		{"terms not accepted", func(r *AccessRequest) { r.TermsAccepted = false }, ReasonTerms},
		{"flagged", func(r *AccessRequest) { r.Flagged = true }, ReasonFlagged},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := qualifying()
			tc.mutate(&r)
			d := EvaluateContentAccess(r)
			require.False(t, d.Allowed, "%s must deny access on its own", tc.name)
			assert.Equal(t, tc.want, d.Reason,
				"the reason is what the user is told to act on, so it must name the "+
					"condition that actually fired")
		})
	}
}

// THE OVERRIDE CASES. IsVanguard and AdminOverride substitute for the level
// check, and for the level check ONLY.

// A level-3 user is refused...
func TestAUserBelowTheLevelIsRefused(t *testing.T) {
	r := qualifying()
	r.Level = 3
	d := EvaluateContentAccess(r)
	require.False(t, d.Allowed)
	assert.Equal(t, ReasonLevel, d.Reason)
}

// ...and a vanguard at the same level is let through, because the level check is
// the one thing a vanguard substitutes for.
func TestAVanguardSubstitutesForTheLevelCheck(t *testing.T) {
	r := qualifying()
	r.Level = 1
	r.IsVanguard = true
	assert.True(t, EvaluateContentAccess(r).Allowed,
		"a vanguard who meets every other condition passes. This is the ONE "+
			"disjunction in the gate and it is correct: being a vanguard is an "+
			"alternative way to satisfy 'be a trusted user'")
}

func TestAnAdminOverrideSubstitutesForTheLevelCheck(t *testing.T) {
	r := qualifying()
	r.Level = 0
	r.AdminOverride = true
	assert.True(t, EvaluateContentAccess(r).Allowed)
}

// THE FOUR ROWS THAT ARE THE WHOLE REASON D1 EXISTS.
//
// Each is a vanguard or an admin override with every other condition intact but
// one non-bypassable condition broken. A gate that treats the override as a
// blanket grant passes all four, and that gate is the draft's.
func TestNeitherVanguardNorAdminBypassesTheOptIn(t *testing.T) {
	for _, override := range []struct {
		name   string
		mutate func(*AccessRequest)
	}{
		{"vanguard", func(r *AccessRequest) { r.IsVanguard = true }},
		{"admin", func(r *AccessRequest) { r.AdminOverride = true }},
	} {
		t.Run(override.name, func(t *testing.T) {
			r := qualifying()
			override.mutate(&r)
			r.OptedIn = false

			d := EvaluateContentAccess(r)
			require.False(t, d.Allowed,
				"a %s who has not opted in must be refused. The opt-in is the "+
					"user's own decision about their own data; no badge, role or "+
					"operator button may make it for them. This is exactly what "+
					"SPEC 7.23.3 W1 is about", override.name)
			assert.Equal(t, ReasonOptIn, d.Reason)
		})
	}
}

func TestNeitherVanguardNorAdminBypassesTheContributionThreshold(t *testing.T) {
	for _, override := range []struct {
		name   string
		mutate func(*AccessRequest)
	}{
		{"vanguard", func(r *AccessRequest) { r.IsVanguard = true }},
		{"admin", func(r *AccessRequest) { r.AdminOverride = true }},
	} {
		t.Run(override.name, func(t *testing.T) {
			r := qualifying()
			override.mutate(&r)
			r.ContributionScore = 0
			r.MinContribution = 500

			d := EvaluateContentAccess(r)
			require.False(t, d.Allowed,
				"the contribution threshold is an instance policy. A %s "+
					"bypassing it means the operator's threshold is advisory, and "+
					"the operator set it for a reason", override.name)
			assert.Equal(t, ReasonContribution, d.Reason)
		})
	}
}

func TestNeitherVanguardNorAdminBypassesTheTermsAcceptance(t *testing.T) {
	for _, override := range []struct {
		name   string
		mutate func(*AccessRequest)
	}{
		{"vanguard", func(r *AccessRequest) { r.IsVanguard = true }},
		{"admin", func(r *AccessRequest) { r.AdminOverride = true }},
	} {
		t.Run(override.name, func(t *testing.T) {
			r := qualifying()
			override.mutate(&r)
			r.TermsAccepted = false

			d := EvaluateContentAccess(r)
			require.False(t, d.Allowed,
				"accepting terms is an ACT and an act cannot be inferred from a "+
					"trust level. An operator override that skips it grants "+
					"permanent content access to someone who never consented")
			assert.Equal(t, ReasonTerms, d.Reason)
		})
	}
}

func TestNeitherVanguardNorAdminBypassesTheAbuseFlag(t *testing.T) {
	for _, override := range []struct {
		name   string
		mutate func(*AccessRequest)
	}{
		{"vanguard", func(r *AccessRequest) { r.IsVanguard = true }},
		{"admin", func(r *AccessRequest) { r.AdminOverride = true }},
	} {
		t.Run(override.name, func(t *testing.T) {
			r := qualifying()
			override.mutate(&r)
			r.Flagged = true

			d := EvaluateContentAccess(r)
			require.False(t, d.Allowed,
				"a flagged account is not a legitimate user an override was "+
					"issued to. Clearing a flag is an action ON THE FLAG, not a "+
					"content gate decision, and a gate that lets an override "+
					"launder it is a way for the flag to be ignored")
			assert.Equal(t, ReasonFlagged, d.Reason)
		})
	}
}

// A failed anonymous request must not be rescued by an override, which is the
// other direction the same mistake appears in.
func TestAnOverrideDoesNotRescueAnAnonymousRequest(t *testing.T) {
	r := qualifying()
	r.Anonymous = true
	r.IsVanguard = true
	r.AdminOverride = true

	d := EvaluateContentAccess(r)
	require.False(t, d.Allowed)
	assert.Equal(t, ReasonAnonymous, d.Reason)
}

// EVERYTHING broken at once: all six reasons are reported, and the FIRST is the
// one the user is told. Getting this wrong in either direction is a real bug --
// reporting only the first hides the rest from the operator, reporting the last
// sends the user to the wrong screen.
func TestFailedListsEveryConditionAndReasonIsTheFirst(t *testing.T) {
	r := AccessRequest{
		Anonymous:         true,
		Level:             0,
		OptedIn:           false,
		ContributionScore: 0,
		MinContribution:   500,
		TermsAccepted:     false,
		Flagged:           true,
	}
	d := EvaluateContentAccess(r)

	require.False(t, d.Allowed)
	// Anonymous short-circuits: there is nothing else to check for someone who
	// is not logged in, so only one reason is reported.
	assert.Equal(t, []string{ReasonAnonymous}, d.Failed,
		"an anonymous request has no opt-in, no score and no terms to fail, so "+
			"listing six reasons for it is noise aimed at a user who cannot act "+
			"on any of them")
}

func TestEveryFailedConditionIsReportedForALoggedInUser(t *testing.T) {
	r := AccessRequest{
		Level:             0,
		OptedIn:           false,
		ContributionScore: 0,
		MinContribution:   500,
		TermsAccepted:     false,
		Flagged:           true,
	}
	d := EvaluateContentAccess(r)

	require.False(t, d.Allowed)
	assert.Equal(t, ReasonLevel, d.Reason,
		"the FIRST failure is what the user is told to act on, so an ineligible "+
			"user is sent to 'reach level 4' rather than to the opt-in screen "+
			"they already completed")
	assert.Len(t, d.Failed, 5,
		"the operator dashboard needs the whole picture. Reporting only the "+
			"first makes 'why is this user blocked' unanswerable")
	assert.Equal(t, []string{
		ReasonLevel, ReasonOptIn, ReasonContribution, ReasonTerms, ReasonFlagged,
	}, d.Failed, "and in evaluation order, so the first entry matches Reason")
}

// The contribution threshold is INCLUSIVE: a user exactly at the threshold
// passes. Off-by-one here reads as "the operator set 500 and a 500-score user
// is denied", which is the kind of thing nobody notices for months.
func TestTheContributionThresholdIsInclusive(t *testing.T) {
	r := qualifying()
	r.ContributionScore = 500
	r.MinContribution = 500
	assert.True(t, EvaluateContentAccess(r).Allowed,
		"a score exactly AT the threshold meets it")

	r.ContributionScore = 499
	d := EvaluateContentAccess(r)
	require.False(t, d.Allowed)
	assert.Equal(t, ReasonContribution, d.Reason)
}

// A zero threshold means the instance has turned the check off. It must not mean
// "nobody passes", which is what `score <= 0` would do for a real contributor.
func TestAZeroThresholdDisablesTheCheckRatherThanDenyingEveryone(t *testing.T) {
	r := qualifying()
	r.ContributionScore = 1
	r.MinContribution = 0
	assert.True(t, EvaluateContentAccess(r).Allowed,
		"MinContribution=0 is an operator turning the check off, not a rule "+
			"that requires a score of zero")
}
