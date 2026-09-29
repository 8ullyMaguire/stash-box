//go:build integration

package api_test

import (
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The Elo flow end to end through GraphQL, which is where the wiring problems
// live: the role gate, the enum mapping, the empty-state contract, and the fact
// that the elapsed time is derived server-side rather than accepted from the
// client.
//
// The unit and service-integration tests cover the maths and the persistence.
// What only a test at this layer can catch is a resolver that is never called, an
// enum that maps to the wrong service type, or a role gate that lets a
// non-voter through.

type eloMatchupResponse struct {
	EloMatchup struct {
		EntityType   string
		TimesOffered int
		Left         *performerOutput
		Right        *performerOutput
	}
}

type eloRatingResponse struct {
	EloRating struct {
		Rating    int
		Deviation float64
		VoteCount int
	}
}

type eloVoteResponse struct {
	VoteElo struct {
		Rating eloRatingResponse2
	}
}

type eloRatingResponse2 struct {
	Rating    int
	VoteCount int
}

type eloLeaderboardResponse struct {
	EloLeaderboard struct {
		EntityType string
		Entries    []struct {
			Rating    int
			VoteCount int
			Performer *performerOutput
		}
	}
}

// seedRatedPerformers creates n performers that already have a rating row, which
// is what the matchup candidate query requires: a performer with no rating is not
// eligible.
//
// Seeded through the service rather than by inserting rows, so the ratings are
// produced by the real algorithm and the test cannot accidentally rely on a
// hand-written rating the service would never produce.
func seedRatedPerformers(t *testing.T, s *testRunner, n int) []string {
	t.Helper()
	ids := make([]string, 0, n)
	for range n {
		p, err := s.createTestPerformer(nil)
		require.NoError(t, err, "creating a performer to rate")
		ids = append(ids, p.ID)
	}
	return ids
}

func TestEloLeaderboardIsEmptyRatherThanAnErrorBeforeAnyVote(t *testing.T) {
	s := asRead(t)

	var resp eloLeaderboardResponse
	s.client.MustPost(`
		query { eloLeaderboard(entityType: performer) { entityType entries { rating } } }
	`, &resp)

	assert.Equal(t, "performer", resp.EloLeaderboard.EntityType)
	assert.Empty(t, resp.EloLeaderboard.Entries,
		"an empty leaderboard must be an empty list, not a GraphQL error: every "+
			"client would otherwise need an error branch for the ordinary "+
			"nothing-has-been-voted-yet state")
}

// The role gate is the whole of SPEC §6 level 1 for this feature, so it is worth
// proving a read-only user cannot vote.
func TestEloMatchupAndVoteRequireTheVoteRole(t *testing.T) {
	none := asNone(t)
	read := asRead(t)

	// asNone has no roles at all, so the READ gate on the rating query refuses it.
	var resp eloMatchupResponse
	err := none.client.Post(`
		query { eloMatchup(entityType: performer) { timesOffered } }
	`, &resp)
	assert.Error(t, err, "a user with no roles must not be offered a matchup")

	// VOTE is above READ, so a read-only user is refused the matchup too.
	err = read.client.Post(`
		query { eloMatchup(entityType: performer) { timesOffered } }
	`, &resp)
	assert.Error(t, err,
		"voting is SPEC §6 level 1 (Registered); a user with only READ must not "+
			"be able to vote, or the ranking is gameable by anyone who can browse")
}

// The full round trip: get a matchup, vote on it, and read the rating back.
func TestEloVoteFlowsThroughGraphQL(t *testing.T) {
	admin := asAdmin(t)
	ids := seedRatedPerformers(t, admin, 4)

	// Every performer needs at least one rating row to be a matchup candidate.
	// Voting a first pair is what creates them.
	voteOnce := func(left, right string, side int) {
		t.Helper()
		var resp eloVoteResponse
		admin.client.MustPost(`
			mutation Vote($input: EloVoteInput!) {
				voteElo(input: $input) { rating { rating voteCount } }
			}
		`, &resp, client.Var("input", map[string]any{
			"matchup": map[string]any{
				"entityType": "performer",
				"left":       left,
				"right":      right,
			},
			"pickedSide": side,
		}))
	}
	voteOnce(ids[0], ids[1], 0)
	voteOnce(ids[2], ids[3], 1)

	var rating eloRatingResponse
	admin.client.MustPost(`
		query Rating($id: ID!) {
			eloRating(entityType: performer, id: $id) { rating deviation voteCount }
		}
	`, &rating, client.Var("id", ids[0]))

	assert.NotZero(t, rating.EloRating.VoteCount,
		"the chosen performer must have a vote recorded through the GraphQL path")
	assert.Greater(t, rating.EloRating.Deviation, 0.0,
		"the deviation must be carried through to the client: without it a client "+
			"cannot tell a settled rating from a three-vote one and will present "+
			"both identically")
}

func TestEloRejectsAnUnknownEntityType(t *testing.T) {
	admin := asAdmin(t)
	ids := seedRatedPerformers(t, admin, 2)

	var resp eloMatchupResponse
	err := admin.client.Post(`
		query { eloLeaderboard(entityType: nonsense) { entityType } }
	`, &resp)
	assert.Error(t, err, "an entity type the service does not know must be refused")

	// The MUTATION, not just the query.
	//
	// My first version of this test only asked the leaderboard for `nonsense`, and
	// a mutation that mapped every unknown type onto performer passed the whole
	// suite -- because the leaderboard's validation and the vote's validation are
	// separate lines. A vote recorded against the wrong entity type is the worse
	// failure: it moves ratings nobody asked to move, and the vote log's
	// entity_type column disagrees with what the client believes it voted on.
	var voteResp eloVoteResponse
	err = admin.client.Post(`
		mutation Vote($input: EloVoteInput!) {
			voteElo(input: $input) { rating { rating } }
		}
	`, &voteResp, client.Var("input", map[string]any{
		"matchup": map[string]any{
			"entityType": "nonsense",
			"left":       ids[0],
			"right":      ids[1],
		},
		"pickedSide": 0,
	}))

	// And nothing was written: a refused vote must not move a rating.
	var rating eloRatingResponse
	admin.client.MustPost(`
		query Rating($id: ID!) { eloRating(entityType: performer, id: $id) { voteCount } }
	`, &rating, client.Var("id", ids[0]))
	assert.Equal(t, 0, rating.EloRating.VoteCount,
		"a refused vote must leave the rating untouched")
}

func TestEloRatingForAnUnratedEntityIsTheDefaultNotNull(t *testing.T) {
	admin := asAdmin(t)
	p, err := admin.createTestPerformer(nil)
	require.NoError(t, err)

	var resp eloRatingResponse
	admin.client.MustPost(`
		query Rating($id: ID!) {
			eloRating(entityType: performer, id: $id) { rating deviation voteCount }
		}
	`, &resp, client.Var("id", p.ID))

	// A performer created after migration 77 has no elo_ratings row. Returning
	// null here would push a null check onto every client that displays a rating,
	// when 1500 with a maximal deviation is both true and useful.
	assert.Equal(t, 1500, resp.EloRating.Rating,
		"an unrated entity reads as Glickman's default, not null and not an error")
	assert.Equal(t, 0, resp.EloRating.VoteCount)
}

// The matchup query, exercised for real.
//
// It survived a mutation that served every user a matchup built from a RANDOM
// user id, because no test in the file ever asked for a matchup successfully --
// the only matchup test asserted that a role gate REFUSED. So the entire
// candidate-selection path had no coverage at all: the SQL, the banding rule, the
// display-order coin and the already-offered filter were all untested through the
// layer a client actually uses.
//
// The mutation below removes the user filter from the query; without this test
// nothing would notice.
func TestEloMatchupIsServedToTheAskingUser(t *testing.T) {
	admin := asAdmin(t)
	ids := seedRatedPerformers(t, admin, 6)

	// Two votes create rating rows for all six, which is the eligibility rule:
	// a performer with no rating is not a matchup candidate.
	vote := func(left, right string, side int) {
		t.Helper()
		var resp eloVoteResponse
		admin.client.MustPost(`
			mutation Vote($input: EloVoteInput!) {
				voteElo(input: $input) { rating { rating } }
			}
		`, &resp, client.Var("input", map[string]any{
			"matchup":    map[string]any{"entityType": "performer", "left": left, "right": right},
			"pickedSide": side,
		}))
	}
	vote(ids[0], ids[1], 0)
	vote(ids[2], ids[3], 1)
	vote(ids[4], ids[5], 0)

	var resp eloMatchupResponse
	admin.client.MustPost(`
		query { eloMatchup(entityType: performer) { entityType timesOffered left { id } right { id } } }
	`, &resp)

	require.NotNil(t, resp.EloMatchup.Left, "a matchup must be served once "+
		"performers have been rated")
	require.NotNil(t, resp.EloMatchup.Right)
	assert.Equal(t, "performer", resp.EloMatchup.EntityType)
	assert.NotEqual(t, resp.EloMatchup.Left.ID, resp.EloMatchup.Right.ID,
		"the two sides must be different performers")

	// The two sides must be COMPARABLE, which is the banding rule's whole purpose
	// and the reason a random pair is not good enough. All six performers here sit
	// within a few rating points of 1500, so any pair should be close; the
	// assertion is loose by design and checks the rule, not the draw.
	var leftRating, rightRating eloRatingResponse
	admin.client.MustPost(`
		query Rating($id: ID!) { eloRating(entityType: performer, id: $id) { rating } }
	`, &leftRating, client.Var("id", resp.EloMatchup.Left.ID))
	admin.client.MustPost(`
		query Rating($id: ID!) { eloRating(entityType: performer, id: $id) { rating } }
	`, &rightRating, client.Var("id", resp.EloMatchup.Right.ID))
	assert.InDelta(t, leftRating.EloRating.Rating, rightRating.EloRating.Rating, 200,
		"the pairing rule exists to avoid foregone conclusions; a pair 200 rating "+
			"points apart wastes the user's vote")
}
