package elo

import "math"

// Gravity is the product that makes the instance's taste bend discovery
// (SPEC §7.4, §7.17.3).
//
// Four factors multiply, and the multiplication is the design, not a
// convenience: every one of them can only pull a result DOWN, never up. An
// operator who dislikes their instance's gravity cannot amplify a result by
// turning one slider to zero, and a user with no taste history cannot get a
// strongly-recommended item from a strong instance signal alone. A weighted sum
// would have let a large personal taste score cancel a policy decision; a
// product cannot.
//
// The consequence — and it is the rule that matters most here — is that a zero
// ANYWHERE zeroes the whole score. That is not a bug to be smoothed over, it
// is the semantics, and the zero cases below exist to stop a zero from being
// reached accidentally.
const (
	// maxPeerSimilarity is the ceiling on cosine taste similarity to a peer.
	// Cosine is defined on [-1,1], so 1.0 is the mathematical maximum: a peer
	// identical to this instance's taste. Bounding it here (rather than
	// trusting the caller) is what stops an +Inf or a mis-scaled score from
	// producing an infinite or NaN gravity product.
	maxPeerSimilarity = 1.0
	// minPersonalTaste is the value substituted for a user with NO taste
	// history.
	//
	// This is the decision §7.4 does not make and the plan flags as the case
	// that matters most, because it is what a brand-new user hits on day one.
	// The two candidate readings are:
	//
	//   (a) a zero personal taste means "no results at all", or
	//   (b) a zero personal taste means "no PERSONAL signal yet", so the
	//       instance's gravity decides until the user casts their first vote.
	//
	// (b) is correct. (a) hands every new user an empty feed on their first
	// visit and makes the instance look broken; the mesh's job is discovery,
	// and discovery has to start before the user has contributed anything.
	//
	// The cost, stated plainly: a new user's feed is the instance's taste, not
	// theirs. That is a real loss of personalisation, and it is the price of
	// showing anything at all.
	//
	// This is a SUBSTITUTION, not a clamp. There is deliberately no lower
	// clamp on personal taste: a user with a weak but real signal (0.05) must
	// rank BELOW the substitution (0.1), or "no taste" and "a little taste"
	// become the same number and the recommender can no longer tell them apart
	// — the same distinction taste_vectors preserves by storing no row at all
	// for a user who has not voted. Clamping up to the floor destroys exactly
	// the information the fallback exists to keep. maxPersonalTaste is the
	// only bound applied.
	minPersonalTaste = 0.1
	// maxPersonalTaste is the ceiling on personal pull. Unbounded personal
	// taste would let one user's vector pin a result in place, and the instance
	// gravity an operator configured would become unreachable.
	maxPersonalTaste = 2.0
	// maxLocalGravity bounds the operator slider. An instance that can be
	// pushed arbitrarily high is an instance that can dominate every ranking
	// in the mesh, which is a governance question rather than a tuning one.
	maxLocalGravity = 5.0
)

// GravityInput is the four factors the product consumes.
//
// Every field is a plain number and the struct performs no I/O, so the whole
// policy is testable without a database and cannot drift from the documented
// semantics.
type GravityInput struct {
	// LocalGravity is the instance's operator-set pull. Zero means "no
	// instance opinion", which zeroes the result — see the type comment.
	LocalGravity float64
	// PeerSimilarity is cosine taste similarity to a peering instance, in
	// [0,1]. Zero means "maximally dissimilar", which zeroes the result.
	PeerSimilarity float64
	// PersonalTaste is this user's own signal strength. Zero is replaced by
	// minPersonalTaste per the rule above.
	PersonalTaste float64
	// TrustWeight is the reader's weight, from VoterWeight. Zero is replaced
	// by MinVoterWeight: a reader with no trust still gets a baseline, they
	// are not excluded.
	TrustWeight float64
}

// GravityScore is the product, with the normalisation factors kept so a caller
// can explain a result rather than just report a number.
type GravityScore struct {
	// Score is the final value. Higher ranks higher.
	Score float64
	// PersonalTasteUsed is the personal term after the zero-floor was
	// applied. Exposed so the recommendation layer can say "this is shown to
	// you because the instance's taste, you have no votes yet" instead of
	// implying a personal match that does not exist.
	PersonalTasteUsed float64
	// FellBackToInstanceTaste is true when a zero personal score was replaced
	// by the floor. The operator dashboard needs this: a feed that is entirely
	// instance-driven is a signal that onboarding is failing.
	FellBackToInstanceTaste bool
}

// Gravity applies the product, after normalising the terms whose zero needs a
// documented meaning.
//
// Every factor is bounded to a finite, non-negative range first. That is not
// defensive tidiness: a product of four factors multiplies infinities into
// infinities and 0*Inf into NaN, and a NaN sort key silently drops a result out
// of a ranking instead of failing. Normalising at the boundary means Gravity
// can never return a value that is NaN, infinite, or negative, whatever the
// caller supplies.
func Gravity(in GravityInput) GravityScore {
	// A non-positive or NaN personal taste means "no signal". Substitution,
	// not a clamp: see the note on minPersonalTaste for why a weak real signal
	// must stay below the substituted value.
	personal := in.PersonalTaste
	fellBack := false
	if !(personal > 0) { // the negation also catches NaN
		personal = minPersonalTaste
		fellBack = true
	}
	personal = clampRange(personal, 0, maxPersonalTaste)

	// Trust gates content access, not discovery: an untrusted reader is
	// down-weighted to the voter-weight floor, never excluded.
	trust := in.TrustWeight
	if !(trust > 0) {
		trust = MinVoterWeight
	}
	trust = clampRange(trust, MinVoterWeight, MaxVoterWeight)

	// Zero instance gravity and zero peer similarity are honoured, not
	// smoothed: an operator must be able to say "no pull" and a completely
	// dissimilar peer must not contribute. Both therefore clamp to [0, max],
	// which still turns -Inf into 0 rather than a negative score.
	local := clampRange(in.LocalGravity, 0, maxLocalGravity)
	similarity := clampRange(in.PeerSimilarity, 0, maxPeerSimilarity)

	return GravityScore{
		Score:                   local * similarity * personal * trust,
		PersonalTasteUsed:       personal,
		FellBackToInstanceTaste: fellBack,
	}
}

func clampRange(v, lo, hi float64) float64 {
	switch {
	case math.IsNaN(v):
		// NaN has no position on an interval and every comparison against it
		// is false, so it would pass a naive bounds check and reach the sort.
		// The lower bound is the honest default: "no signal" rather than a
		// number nobody can justify.
		return lo
	case v < lo:
		return lo
	case v > hi:
		return hi
	default:
		return v
	}
}

// WithInstanceGravity returns a GravityInput with the instance pull set, so a
// caller does not have to know the default. Pure convenience; no policy.
func WithInstanceGravity(in GravityInput, local float64) GravityInput {
	in.LocalGravity = local
	return in
}
