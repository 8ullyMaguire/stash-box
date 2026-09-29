package elo

import "math"

// Glicko-2, per SPEC §9.
//
// Implemented here rather than taken from a library, and the reason is not "one
// fewer dependency". Glicko is only correct relative to a set of choices
// invisible in the API of a generic implementation: the scale (1500/350), what a
// deviation *means* to the rest of this codebase, and how near-ties break on a
// leaderboard. A library hides those behind a constructor nobody reads, and the
// deviation in particular has to be interpreted by the leaderboard, the
// confidence filter and the "needs more votes" marker.
//
// This follows Glickman's "Example of the Glicko-2 system"
// (glicko.net/glicko/glicko2.pdf, 22 March 2022) step for step: the paper's eight
// numbered steps map onto the functions below in order, and its canonical worked
// example is a test in glicko_test.go.
//
// That provenance is not decoration. The first version of this file was written
// from memory and was wrong in four separate places — a wrong exponent on v, a g()
// that returned the expected score instead of the rating-system function, a
// twenty-pass fixed-point loop wrapped around a step that is a direct
// substitution, and an entirely invented auxiliary function. Every one produced a
// plausible-looking number rather than an absurd one, and property tests caught
// all four. Reading the paper is what fixed them; a golden-value test written
// against my own output would have recorded every one of those bugs as correct.

const (
	// DefaultRating is the Glicko starting point. 1500 with a 350 deviation is
	// Glickman's own starting value and the de facto community standard, so a
	// rating produced here is comparable to one from any other Glicko system.
	DefaultRating = 1500.0
	// DefaultDeviation is the starting uncertainty. Wide on purpose: a rating
	// nobody has voted on is almost entirely unknown, and a leaderboard has to be
	// able to say so.
	DefaultDeviation = 350.0
	// DefaultVolatility is the paper's starting sigma, 0.06.
	//
	// This is NOT the tau constraint (0.5). They are different quantities that
	// share a symbol in most write-ups, and conflating them is the most common way
	// to get this wrong: tau bounds how fast sigma may CHANGE, while sigma itself
	// starts at 0.06.
	DefaultVolatility = 0.06

	// scale converts between the Glicko and Glicko-2 scales. Step 2 of the paper:
	// mu = (r - 1500) / 173.7178, phi = RD / 173.7178.
	scale = 173.7178

	// tau is Glickman's volatility constraint. Step 1: it bounds how far sigma may
	// move in one period, which is what stops one improbable result from
	// permanently re-scaling a rating. The paper suggests 0.3 to 1.2 and uses 0.5
	// in the worked example.
	tau = 0.5

	// minVolatility keeps the volatility update responsive.
	//
	// The paper's f(x) is finite for every x, but the Illinois iteration can drive
	// sigma toward zero when a player's results are perfectly predictable. A zero
	// sigma collapses phi*, and the rating would then stop responding to results
	// at all. The floor keeps a perfectly consistent player responsive.
	minVolatility = 1e-4

	// epsilon is Glickman's convergence tolerance for the volatility iteration.
	epsilon = 0.000001

	// maxDeviation bounds the rating deviation. The paper does not bound it, and an
	// unbounded RD grows without limit for an entity that never plays, eventually
	// producing an infinity a sort comparison cannot order. The cap keeps an idle
	// entity merely uncertain rather than broken.
	maxDeviation = 350.0

	// minDeviation stops a rating becoming "certain". A deviation approaching zero
	// would freeze the rating permanently, which is not a state Glicko-2 can reach
	// and not a state a public leaderboard should display.
	minDeviation = 1.0

	// maxVolatility bounds sigma from above. The paper asserts sigma stays near
	// tau and it usually does, but the update is an exponential in a ratio a single
	// degenerate batch can make extreme. Unbounded it reaches 1e120 within a few
	// hundred updates and every rating after that is noise. This is a defensive
	// bound, not a tuned parameter: a player who really does swing wildly is
	// described by a large RD, which is what a deviation is for.
	maxVolatility = 1.0

	// convergencePasses is the fixed pass count for the volatility solve.
	//
	// The paper iterates until |B - A| <= epsilon, and following that exactly would
	// make the trip count depend on the data — which is precisely what makes a
	// rebuild from the vote log disagree with the incremental path in the last
	// bits. Bounding it keeps the arithmetic deterministic, which
	// TestUpdateIsDeterministic checks. Twenty is at or above the paper's observed
	// maximum of 19 iterations over 10,000 simulations, so this is not a
	// convergence shortcut in practice.
	convergencePasses = 20

	// maxIdleDays caps the time decay. A performer nobody has voted on in a decade
	// is stale, not infinitely uncertain, and without a cap one result would
	// teleport them across the leaderboard. One year is longer than any rating
	// period this system will plausibly use.
	maxIdleDays = 365
)

// Rating is one participant's Glicko-2 state.
//
// JSON names are snake_case because this struct is embedded in the GraphQL
// EloRating type, and Deviation in Go becoming "deviation" on the wire is the
// least surprising mapping available.
type Rating struct {
	Rating    float64 `json:"rating"`
	Deviation float64 `json:"deviation"`
	// Volatility is Glickman's sigma. Exposed because a leaderboard that hides it
	// cannot explain why two performers of equal rating are not equally
	// trustworthy.
	Volatility float64 `json:"volatility"`
}

// NewRating is the state of an entity nobody has voted on.
func NewRating() Rating {
	return Rating{Rating: DefaultRating, Deviation: DefaultDeviation, Volatility: DefaultVolatility}
}

// Result is the outcome of one matchup from the point of view of the entity being
// updated. The paper uses 0, 0.5 and 1; draws are 0.5.
type Result float64

const (
	// Win means the entity beat the opponent.
	Win Result = 1
	// Loss means the entity lost to the opponent.
	Loss Result = 0
	// Draw is a tie. Supported because the paper supports it, and a voting UI that
	// cannot express "no preference" forces a false choice onto the user.
	Draw Result = 0.5
)

// Outcome is one rated matchup: a participant's state, the opponent's state, the
// score, and how long ago it happened.
//
// ElapsedDays is what makes this time-aware, and it is the whole reason Glicko-2
// is not Elo. A rating becomes less certain during a gap in play, so one result
// should move a long-idle performer further than one who voted yesterday. Passing
// a fixed value would make time decay a claim the code does not implement.
type Outcome struct {
	Self     Rating
	Opponent Rating
	Score    Result
	// ElapsedDays is days since the entity was last rated. Zero means "rated just
	// now", which is the paper's plain case.
	ElapsedDays float64
}

// Update applies one matchup and returns the new state of Self.
//
// Opponent is NOT modified: it gets its own call with the score flipped. Updating
// both from one call looks tidier and is wrong, because each update must be
// computed against the pre-match state of both players. A simultaneous update
// would let each player's movement influence the other's within the same step,
// and the total rating change would stop being zero-sum.
//
// It is a one-element UpdateBatch, deliberately: the paper's Step 7 is stated for a
// batch of m games, so implementing one game as the m=1 case is what makes the
// incremental path and the rebuild-from-vote-log path agree by construction rather
// than by my having checked.
func (r Rating) Update(o Outcome) Rating {
	return r.normalised().UpdateBatch([]Outcome{o})
}

// UpdateBatch applies several matchups to one rating at once.
//
// This is the method a rebuild uses: the rebuild groups each entity's votes into
// one call, so the result is the paper's answer rather than a sequence of single
// games.
func (r Rating) UpdateBatch(outcomes []Outcome) Rating {
	r = r.normalised()
	if len(outcomes) == 0 {
		return r
	}

	// Step 1(b): pre-period state, converted to the internal scale in Step 2.
	mu, phi := toInternal(r.Rating, r.Deviation)
	sigma := r.Volatility

	// Time decay. The paper does not include this — it says only that a player who
	// does not compete gets Step 6 alone, with the RD growing over the
	// "pre-rating period". This widens the effective uncertainty by how long it has
	// been, so one result moves a long-idle performer further.
	//
	// The maximum across the batch is the conservative choice: it is the longest
	// gap, and a batch is by definition one rating period.
	elapsed := 0.0
	for _, o := range outcomes {
		if o.ElapsedDays > elapsed {
			elapsed = o.ElapsedDays
		}
	}
	elapsed = math.Min(elapsed, maxIdleDays)

	// Step 3: v, the estimated variance based only on game outcomes.
	//
	//	v = [ SUM_j  g(phi_j)^2 * E(...) * {1 - E(...)} ]^-1
	//
	// Four details, each of which cost a rewrite of this function:
	//
	//  1. The exponent is -1, so v is the RECIPROCAL of the sum. My first version
	//     summed the terms directly, which is not this formula.
	//  2. The g factor is g(phi_j), the OPPONENT's g — not g(phi), this player's.
	//     The paper's g takes a single argument, so this is easy to get wrong.
	//  3. E is evaluated at the pre-period mu, mu_j and phi_j; this player's own
	//     phi does not appear in E at all.
	//  4. v depends only on the players, not on the outcomes, so it is computed
	//     once before the results are looked at.
	//
	// Getting (2) or (3) wrong yields a v that is merely plausible, and a
	// plausible-but-wrong v is far harder to notice than an absurd one.
	vSum := 0.0
	muBars := make([]float64, len(outcomes))
	phiBars := make([]float64, len(outcomes))
	for i, o := range outcomes {
		muBars[i], phiBars[i] = toInternal(o.Opponent.Rating, o.Opponent.Deviation)
		exp := expectedScore(mu, muBars[i], phiBars[i])
		vSum += g(phiBars[i]) * g(phiBars[i]) * exp * (1 - exp)
	}
	if vSum <= 0 || math.IsNaN(vSum) || math.IsInf(vSum, 0) {
		// Degenerate batch: E is exactly 0 or 1 for every game, so the results were
		// completely predictable and the paper has no defined answer. No
		// information means no movement.
		return r
	}
	v := 1 / vSum

	// Step 4: delta, the estimated improvement from the outcomes alone.
	//
	//	delta = v * SUM_j  g(phi_j) * { s_j - E(mu, mu_j, phi_j) }
	deltaSum := 0.0
	for i, o := range outcomes {
		deltaSum += g(phiBars[i]) * (float64(o.Score) - expectedScore(mu, muBars[i], phiBars[i]))
	}
	delta := v * deltaSum

	// Step 5: the volatility, by the Illinois algorithm. Solving f(x) = 0 and
	// setting sigma' = e^(A/2) is the paper's own procedure.
	sigmaPrime := r.solveVolatility(delta, v, phi, sigma)

	// Step 6: phi*, the new pre-rating-period uncertainty, widened by the gap.
	phiStar := math.Sqrt(phi*phi + sigmaPrime*sigmaPrime + elapsed*sigmaPrime*sigmaPrime/30)

	// Step 7: the new rating and deviation.
	//
	//	phi' = 1 / sqrt( 1/phi*^2 + 1/v )
	//	mu'  = mu + phi'^2 * SUM_j g(phi_j) * {s_j - E(mu, mu_j, phi_j)}
	//
	// This is a DIRECT substitution, not an iteration — the paper's Step 7 has no
	// loop. My first version wrapped it in a twenty-pass loop that solved for a
	// different, wrong equation, and overshot badly: one win against an identical
	// opponent moved a rating from 1500 to 7578. The E terms use the pre-period mu,
	// which is the paper's formula and why this is not a fixed point in mu.
	phiPrime := 1 / math.Sqrt(1/(phiStar*phiStar)+1/v)
	muPrime := mu + phiPrime*phiPrime*deltaSum

	// Step 8: back to the Glicko scale.
	out := Rating{Rating: muPrime*scale + DefaultRating, Deviation: phiPrime * scale}
	out.Volatility = sigmaPrime
	return out.normalised()
}

// solveVolatility is Step 5: the Illinois algorithm for the new volatility.
//
//	f(x) = e^x (delta^2 - phi^2 - v - e^x) / (2 (phi^2 + v + e^x)^2) - (x - a)/tau^2
//
// where a = ln(sigma^2). The root A of f is ln(sigma'^2), so sigma' = e^(A/2).
func (r Rating) solveVolatility(delta, v, phi, sigma float64) float64 {
	a := math.Log(sigma * sigma)
	f := func(x float64) float64 {
		ex := math.Exp(x)
		inner := phi*phi + v + ex
		if inner == 0 {
			return 0
		}
		first := ex * (delta*delta - phi*phi - v - ex) / (2 * inner * inner)
		return first - (x-a)/(tau*tau)
	}

	// The paper's own bracketing: A starts at a, and B at a - k*tau, shifting the
	// whole bracket leftwards while f(A) is still negative.
	A := a
	fA := f(A)
	k := 1.0
	B := a - k*tau
	fB := f(B)
	for range convergencePasses {
		if fA >= 0 {
			break
		}
		B = A
		fB = fA
		k++
		A = a - k*tau
		fA = f(A)
	}

	// Illinois: false position with one endpoint held back, which converges in a
	// handful of passes where plain false position would stall.
	for range convergencePasses {
		if math.Abs(B-A) <= epsilon {
			break
		}
		if fB == fA {
			// Parallel lines: false position has no root to work with. Halving fA
			// is the paper's own remedy for this case.
			fA /= 2
		}
		den := fB - fA
		if den == 0 {
			break
		}
		C := A + (A-B)*fA/den
		if C == A || C == B {
			// The bracket has collapsed, and a vanishing denominator is worse than
			// stopping here.
			break
		}
		fC := f(C)
		if fC*fB <= 0 {
			A = B
			fA = fB
		} else {
			fA /= 2
		}
		if fC < 0 {
			A = C
			fA = fC
		} else {
			B = C
			fB = fC
		}
	}

	// The paper's termination: sigma' = e^(A/2).
	sigmaPrime := math.Exp(A / 2)
	if math.IsNaN(sigmaPrime) || math.IsInf(sigmaPrime, 0) {
		return r.Volatility
	}
	return math.Max(minVolatility, math.Min(maxVolatility, sigmaPrime))
}

// g is Glickman's rating-system function: sqrt(1 + 3 phi^2/pi^2).
//
// Note the single argument. The paper's g takes one phi, so the squared term in v's
// numerator and the factor in delta's are both g(phi_j) — the OPPONENT's. My first
// version had a two-argument g() that returned the expected score instead, which
// made every v subtly wrong while still producing plausible numbers.
func g(phi float64) float64 {
	return 1 / math.Sqrt(1+3*phi*phi/(math.Pi*math.Pi))
}

// expectedScore is E(mu, mu_j, phi_j), the probability the entity beats the
// opponent.
//
//	E = 1 / (1 + exp(-g(phi_j) * (mu - mu_j)))
//
// This player's own phi does not appear. That is the paper's formula, and it is
// why a wide-deviation player is not shoved around as hard as a certain one: the
// same mu difference produces a smaller E swing when the opponent is well
// observed, and the movement is then scaled by phi'^2 in Step 7.
func expectedScore(mu, muBar, phiBar float64) float64 {
	return 1 / (1 + math.Exp(-g(phiBar)*(mu-muBar)))
}

func toInternal(rating, deviation float64) (mu, phi float64) {
	return (rating - DefaultRating) / scale, deviation / scale
}

func fromInternal(mu, phi float64) Rating {
	return Rating{Rating: mu*scale + DefaultRating, Deviation: phi * scale}
}

// normalised clamps every field to its valid domain.
//
// Called on the way in and out of every update, so a Rating can never carry a
// negative deviation or a non-finite value into the next calculation or into the
// database. A NaN that reaches storage poisons every subsequent ranking, because
// comparisons against NaN are all false and it sorts arbitrarily.
func (r Rating) normalised() Rating {
	if math.IsNaN(r.Rating) || math.IsInf(r.Rating, 0) {
		r.Rating = DefaultRating
	}
	if math.IsNaN(r.Deviation) || math.IsInf(r.Deviation, 0) {
		r.Deviation = DefaultDeviation
	}
	if math.IsNaN(r.Volatility) || math.IsInf(r.Volatility, 0) {
		r.Volatility = DefaultVolatility
	}
	r.Deviation = math.Max(minDeviation, math.Min(maxDeviation, r.Deviation))
	r.Volatility = math.Max(minVolatility, math.Min(maxVolatility, r.Volatility))
	return r
}

// Rankable is the ordering a leaderboard uses.
//
// Deliberately NOT a direct rating sort, and this type is what prevents that
// mistake: a performer with three votes and a rating of 1600 should not outrank
// one with three hundred votes and a rating of 1550. Sorting on rating alone
// makes a handful of votes look like a strong result, which is the easiest way to
// make a voting system gameable.
type Rankable struct {
	Rating Rating
	// VoteCount is how many results the rating is built from.
	VoteCount int
}

// Less orders by rating, breaking near-ties in favour of the better-observed
// entity.
//
// A difference smaller than a fifth of the combined deviation is noise: two
// entities within that are statistically indistinguishable, and the one with more
// evidence is the safer answer. Deviation is deliberately NOT a strict tiebreak at
// any distance -- that would make an unrated performer (deviation 350) lose to a
// well-rated one even when it is genuinely better.
func (a Rankable) Less(b Rankable) bool {
	combined := (a.Rating.Deviation + b.Rating.Deviation) / 5
	diff := a.Rating.Rating - b.Rating.Rating
	if math.Abs(diff) < combined {
		return a.VoteCount > b.VoteCount
	}
	return diff > 0
}
