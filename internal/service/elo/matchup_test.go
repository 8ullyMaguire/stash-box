package elo

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newEntity is a distinct performer for a pool entry. The pairing rule only reads
// the rating, but a Candidate whose Entity is the nil uuid would make a failing
// assertion unreadable ("expected partner to be entity 00000000-...").
func newEntity() Entity {
	return Entity{Type: EntityPerformer, ID: uuid.Must(uuid.NewV7())}
}

// The pairing rule, tested without a database.
//
// This is the part of the matchup flow that can be wrong in a way nothing else
// notices. A matchup between two performers of nearly equal strength is the case
// where the user's opinion is worth the most; a matchup between a 1900 and a 1200
// is a foregone conclusion that moves the 1900 slightly and teaches the system
// nothing. Nothing about picking the wrong pair produces an error, a bad rating,
// or a failing vote -- it just quietly wastes everyone's votes.

// fixedShuffler returns a scripted sequence of draws, so a test can say exactly
// which candidate is picked first and what the display-order coin says.
type fixedShuffler struct {
	draws []int
	calls int
}

func (f *fixedShuffler) Intn(n int) int {
	if f.calls >= len(f.draws) {
		f.calls++
		// Past the end of the script, return a valid value rather than panicking:
		// a test that over-drew has a bug, and panicking here would hide which.
		return 0
	}
	d := f.draws[f.calls]
	f.calls++
	if d >= n {
		// Out of range means the test's script disagrees with the pool size, which
		// is worth failing on rather than silently clamping -- a clamped draw would
		// make a broken test pass.
		panic("fixedShuffler: scripted draw out of range for pool")
	}
	return d
}

// bandOf must bucket negatives correctly, because Go's / truncates toward zero.
func TestBandOfFloorsRatherThanTruncates(t *testing.T) {
	assert.Equal(t, 1500, bandOf(1500))
	assert.Equal(t, 1500, bandOf(1599), "1599 is in the 1500 band")
	assert.Equal(t, 1600, bandOf(1600), "1600 starts the next band")
	assert.Equal(t, 1500, bandOf(1501))
	assert.Equal(t, 1400, bandOf(1400))

	// The interesting cases, and the ones that failed when the code truncated.
	//
	// Floor semantics: -1 belongs to the band [-100, 0), not [0, 100). I wrote
	// the assertion as `bandOf(-1) == 0` on the reasoning that "1 below zero is
	// still roughly zero" -- which is true of the NUMBER and false of the BAND.
	// The whole point of flooring is that -1 and -99 are 98 points apart, and
	// truncation would have put them in one band together, which is the exact
	// mismatch the banding exists to prevent.
	assert.Equal(t, -100, bandOf(-1), "-1 is in the band below zero, not the one above")
	assert.Equal(t, -100, bandOf(-99), "-1 and -99 are 98 points apart, so they "+
		"must not share a band")
	assert.Equal(t, -100, bandOf(-100))
	assert.Equal(t, -200, bandOf(-101), "past -100 crosses into the next band")
	assert.Equal(t, -200, bandOf(-199), "-101 and -199 are also 98 apart and must "+
		"not share a band either")
}

// The search order: own band first, then widening. Never jumping straight to a
// distant band is the rule, so the order is the property.
func TestBandsNearStartsAtTheOwnBand(t *testing.T) {
	bands := bandsNear(1537)
	require.NotEmpty(t, bands)
	assert.Equal(t, 1500, bands[0], "the entity's own band is searched first")

	// Each subsequent band is 100 away, and the widening is symmetric.
	assert.Equal(t, []int{1500, 1600, 1400, 1700, 1300}, bands,
		"bands widen by one step at a time in each direction, so a same-band "+
			"partner is always preferred over a near one and a near one over a "+
			"distant one")
}

// The pairing rule itself, over a pool the test controls.
//
// Simulated rather than exercised through OfferFor, because OfferFor's candidate
// pool comes from a JOIN against elo_ratings and the interesting question here is
// what the rule DOES with a set of ratings, not whether SQL returns them. The
// integration test covers the wiring.
func TestPartnerComesFromTheSameRatingBand(t *testing.T) {
	pool := []Candidate{
		{Entity: newEntity(), Rating: Rating{Rating: 1750, Deviation: 50, Volatility: 0.06}},
		{Entity: newEntity(), Rating: Rating{Rating: 1760, Deviation: 50, Volatility: 0.06}},
		{Entity: newEntity(), Rating: Rating{Rating: 1200, Deviation: 50, Volatility: 0.06}},
		{Entity: newEntity(), Rating: Rating{Rating: 1210, Deviation: 50, Volatility: 0.06}},
		{Entity: newEntity(), Rating: Rating{Rating: 2100, Deviation: 50, Volatility: 0.06}},
	}

	// First draw picks the 1750 performer; the coin says keep that order.
	const leftIdx = 0
	partner := pairIndices(leftIdx, pool)
	assert.Equal(t, 1, partner,
		"the partner must be the 1760 performer: 10 rating points away and in "+
			"the same band. Pairing the 1750 against the 1200 would be a 550-point "+
			"mismatch and a wasted vote")
	assert.Less(t, absInt(int(pool[partner].Rating.Rating)-int(pool[leftIdx].Rating.Rating)), 100,
		"a paired matchup must be within one band, or the user's choice is a "+
			"foregone conclusion")
}

// When a band holds only the left side, the partner comes from the NEAREST band
// rather than failing or reaching across the distribution.
func TestPartnerFallsBackToTheNearestBand(t *testing.T) {
	// A pool of one at 2100 and everything else much lower: the top band has a
	// single member, which is the case the fallback exists for.
	pool := []Candidate{
		{Entity: newEntity(), Rating: Rating{Rating: 2100, Deviation: 50, Volatility: 0.06}},
		{Entity: newEntity(), Rating: Rating{Rating: 1900, Deviation: 50, Volatility: 0.06}},
		{Entity: newEntity(), Rating: Rating{Rating: 1890, Deviation: 50, Volatility: 0.06}},
	}

	partner := pairIndices(0, pool)
	assert.Equal(t, 1, partner,
		"with no in-band partner, the rule takes the nearest band available; "+
			"200 rating points apart beats either failing or reaching to 1890 "+
			"arbitrarily far away")
}

// Widening stops. Two entities 400 points apart must never be paired, because
// that vote is worth nothing.
func TestWideningDoesNotReachAcrossTheDistribution(t *testing.T) {
	// A left side at 1500 with a partner available only 400+ points away.
	pool := []Candidate{
		{Entity: newEntity(), Rating: Rating{Rating: 1500, Deviation: 50, Volatility: 0.06}},
		{Entity: newEntity(), Rating: Rating{Rating: 1100, Deviation: 50, Volatility: 0.06}},
	}

	partner := pairIndices(0, pool)
	// The rule searched 1500, 1600, 1400, 1700, 1300. The partner at 1100 is in
	// none of them, so there is no acceptable partner at all.
	assert.Equal(t, -1, partner,
		"no band within the widening window contains another candidate, so the "+
			"rule must report no partner rather than pair a 400-point mismatch")
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// The display order must be a coin flip, not a fixed "better-rated first".
//
// A fixed order would make the left slot a systematic tiebreak for every user in
// the instance, and position bias is exactly the thing the display-order contract
// in Vote exists to make measurable. Server-baked bias is worse than unmeasured
// bias: the audit trail would faithfully record a preference the server invented.
func TestDisplayOrderIsNotFixed(t *testing.T) {
	pool := []Candidate{
		{Entity: newEntity(), Rating: Rating{Rating: 1750, Deviation: 50, Volatility: 0.06}},
		{Entity: newEntity(), Rating: Rating{Rating: 1760, Deviation: 50, Volatility: 0.06}},
	}

	// ONE draw each: displayOrder makes exactly one call, and that call IS the
	// coin. I wrote {0,0} and {0,1} on the assumption that the first entry picked
	// the candidate -- but the candidate was already chosen by pairIndices, so the
	// second entry was never read and both shufflers returned the same order.
	// The trailing 1 looked like it was testing the swap; it was dead script.
	kept := &fixedShuffler{draws: []int{0}}
	swapped := &fixedShuffler{draws: []int{1}}

	partner := pairIndices(0, pool)
	require.NotEqual(t, -1, partner, "a two-candidate pool in one band must pair")

	// The SAME pair both times, captured in variables. Both shufflers draw from
	// the same pool[0]/pool[partner], so the ONLY difference between the two calls
	// is the coin -- which is the property under test.
	a, b := pool[0], pool[partner]

	first, _ := displayOrder(a, b, kept)
	second, _ := displayOrder(a, b, swapped)

	assert.Equal(t, a.Entity.ID, first.Entity.ID, "coin 0 keeps the drawn order")
	assert.Equal(t, b.Entity.ID, second.Entity.ID,
		"coin 1 reverses it, so the same pairing can be presented in either order "+
			"and neither slot is systematically favoured")
}
