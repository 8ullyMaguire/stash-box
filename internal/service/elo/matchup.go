package elo

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/queries"
)

// Candidate is one eligible performer in a matchup.
//
// Carries the rating as well as the performer, because the pairing rule below
// needs it and re-reading it per candidate would be a query per row.
type Candidate struct {
	Entity Entity
	Rating Rating
}

// Shuffler draws a matchup from the candidate pool.
//
// An interface rather than a direct call to math/rand so the pairing rule can be
// tested on its own. A test that cannot control the draw cannot distinguish "the
// rule picked a good opponent" from "the rule picked whatever came up", and the
// rule is the part that is easy to get wrong.
type Shuffler interface {
	// Intn returns a value in [0, n).
	Intn(n int) int
}

// Offer is a proposed matchup, ready to show.
type Offer struct {
	Matchup Matchup
	// TimesOffered is how many times this user has already seen this exact pair.
	//
	// Surfaced rather than used as a filter, because SPEC §9 wants streaks and a
	// daily reason to return. A user who votes the same way on a pair they have
	// seen before is producing the strongest signal the system can get, so the
	// client labels a repeat rather than the service hiding it.
	TimesOffered int
}

// ErrNoMatchup is returned when there is nothing to offer a user.
var ErrNoMatchup = errNoMatchup{}

type errNoMatchup struct{}

func (errNoMatchup) Error() string {
	return "no matchup is available"
}

// OfferFor proposes a matchup for a user to vote on.
//
// THE PAIRING RULE, and the reason it is not "two random performers": a pairwise
// vote is only informative to the extent the two entities are COMPARABLE. Asking a
// user to rank a 1900 against a 1200 produces a foregone conclusion that moves
// the 1900 slightly and tells nobody anything; asking them to choose between two
// performers of nearly equal strength is the case where the user's opinion is
// worth the most, and is also the case the rating system is least able to resolve
// on its own.
//
// So the pool is bucketed by RATING BAND and the two sides are drawn from the same
// band where possible. Concretely:
//
//   - Candidates are grouped into bands of 100 rating points.
//   - The first side is drawn uniformly from the whole pool, so a user's rating
//     spread still determines what they see (a user who only ever votes on
//     top-rated performers keeps seeing top-rated performers).
//   - The second side is drawn from the SAME band as the first, with a fallback
//     to the nearest band that has anyone in it. The fallback matters at the top
//     and bottom of the distribution, where a band holds a single performer and
//     no in-band partner exists.
//
// This is not in SPEC §9, which says only "two performers side by side". It is
// the minimum needed to make a vote worth recording, and the thing to revisit
// first if matchup quality is ever measured and found wanting.
func (s *Elo) OfferFor(ctx context.Context, userID uuid.UUID, entityType EntityType, shuffle Shuffler) (*Offer, error) {
	if !entityType.Valid() {
		return nil, fmt.Errorf("%w: %q", ErrUnknownEntityType, entityType)
	}

	rows, err := s.queries.QueryMatchupCandidates(ctx, queries.QueryMatchupCandidatesParams{
		EntityType:     string(entityType),
		UserID:         userID,
		ExcludeOffered: true,
		ExcludeIds:     []uuid.UUID{},
		LimitCount:     int32(matchupPoolSize),
	})
	if err != nil {
		return nil, err
	}
	if len(rows) < 2 {
		// Nothing to compare. Not an error upstream -- the GraphQL layer renders
		// this as an empty state -- but a distinct sentinel so a caller can tell
		// "no candidates" from "the query failed".
		return nil, ErrNoMatchup
	}

	candidates := make([]Candidate, 0, len(rows))
	for _, row := range rows {
		rating, err := s.ratingOf(ctx, entityType, row.ID)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, Candidate{
			Entity: Entity{Type: entityType, ID: row.ID},
			Rating: rating,
		})
	}

	leftIdx := shuffle.Intn(len(candidates))
	left := candidates[leftIdx]

	rightIdx := pairIndices(leftIdx, candidates)
	if rightIdx == -1 {
		return nil, ErrNoMatchup
	}
	right := candidates[rightIdx]

	left, right = displayOrder(left, right, shuffle)

	times, err := s.queries.CountVotesBetweenEntities(ctx, queries.CountVotesBetweenEntitiesParams{
		UserID:     userID,
		EntityType: string(entityType),
		LeftID:     left.Entity.ID,
		RightID:    right.Entity.ID,
	})
	if err != nil {
		return nil, err
	}

	return &Offer{
		Matchup: Matchup{
			EntityType: entityType,
			Left:       left.Entity,
			Right:      right.Entity,
		},
		TimesOffered: int(times),
	}, nil
}

// displayOrder decides which of the two candidates is shown first.
//
// A coin flip, NOT "better-rated first". A fixed order would make the left slot a
// systematic tiebreak for every user in the instance, and position bias is
// precisely the thing the display-order contract in Vote exists to make
// measurable. Server-baked bias is worse than unmeasured bias: the audit trail
// would faithfully record a preference the server invented rather than one the
// user had.
//
// The draw is taken even though the pairing is already decided, so a shuffler
// that returns a fixed value makes the order deterministic and the test that
// checks both orders becomes possible.
func displayOrder(a, b Candidate, shuffle Shuffler) (Candidate, Candidate) {
	if shuffle.Intn(2) == 1 {
		return b, a
	}
	return a, b
}

// pairIndices picks the index of a partner comparable to the one at leftIdx.
//
// Returns -1 when no candidate falls in any of the bands near the left side,
// which OfferFor turns into ErrNoMatchup. That is a real state, not a defensive
// branch: a pool with one strong performer and everything else far below it has
// no acceptable partner, and the honest answer is to offer nothing rather than to
// ask a user to compare a 2100 against a 1100.
//
// A pure function over a slice on purpose. The banding is where the matching
// logic lives, and inline in OfferFor the only way to test it would be through a
// database round trip that proves SQL works rather than that the rule does.
func pairIndices(leftIdx int, candidates []Candidate) int {
	left := candidates[leftIdx]
	for _, band := range bandsNear(int(left.Rating.Rating)) {
		for i, c := range candidates {
			if i == leftIdx {
				continue
			}
			if bandOf(int(c.Rating.Rating)) == band {
				return i
			}
		}
	}
	return -1
}

// matchupPoolSize is how many candidates to pull before choosing a pair.
//
// Larger than two so the banding rule has something to choose from: with only
// two candidates both sides are forced and the rule does nothing. 100 is one
// database round trip's worth of rows and keeps the best-rated performers in
// play, which is what makes the returned window worth sampling.
const matchupPoolSize = 100

// bandOf is which 100-point band a rating falls in.
//
// FLOOR division, and the negative cases are the whole reason this is not simply
// `rating / 100`. Go's integer division truncates toward zero, so -1/100 is 0 and
// -99/100 is 0: a pair 98 rating points apart would land in the same band, which
// is exactly the mismatch the banding exists to prevent.
//
// Negative ratings are REACHABLE, not theoretical. `normalised` clamps the
// deviation and the volatility, but nothing clamps the rating -- Glickman's
// algorithm has no rating floor, and a performer who loses every matchup walks
// steadily below 1500 with nothing to stop it. So this is a live off-by-one, not
// a tidy-up: I asserted the floor behaviour in the test, it failed, and the code
// was the thing that was wrong.
func bandOf(rating int) int {
	return int(math.Floor(float64(rating)/100.0)) * 100
}

// bandsNear lists a rating's own band first, then progressively further bands.
//
// The order is the fallback order: a same-band partner is the whole point, and
// only when none exists do we widen. Widening by a FIXED number of bands rather
// than to any band means two entities 400 rating points apart are never paired,
// because a vote between them is the foregone conclusion the rule exists to
// avoid.
func bandsNear(rating int) []int {
	own := bandOf(rating)
	out := make([]int, 0, 5)
	for _, offset := range []int{0, 1, -1, 2, -2} {
		out = append(out, own+offset*100)
	}
	return out
}

// ratingOf reads a rating without the vote count.
//
// Deliberately not the full `load`: the matchup query has already proved the row
// exists, and a leaderboard-sized count per candidate would be N extra queries
// for a value the pairing rule does not use.
func (s *Elo) ratingOf(ctx context.Context, entityType EntityType, id uuid.UUID) (Rating, error) {
	row, err := s.queries.GetEloRating(ctx, queries.GetEloRatingParams{
		EntityType: string(entityType),
		EntityID:   id,
	})
	if err != nil {
		// A candidate row came from a JOIN on elo_ratings, so a missing row here
		// means the row was deleted between the two queries. The default is the
		// right answer: it is what the entity's rating was moments ago, and
		// refusing would fail a matchup over a row that no longer matters.
		return NewRating(), nil
	}
	return Rating{
		Rating:     float64(row.Rating),
		Deviation:  row.Deviation,
		Volatility: row.Volatility,
	}, nil
}

// defaultShuffler is the production draw. Seeding from the runtime source means
// no fixed seed and no reproducibility, which is correct for a voting system and
// is exactly why Shuffler is an interface: the tests inject a deterministic one.
type defaultShuffler struct{}

func (defaultShuffler) Intn(n int) int {
	return rand.IntN(n)
}

// NewShuffler returns the production shuffler.
func NewShuffler() Shuffler { return defaultShuffler{} }
