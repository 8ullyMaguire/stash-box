//go:build integration

package elo_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/elo"
)

// These are the tests that matter for the service, and they cannot be unit tests.
//
// The claim being checked is that a vote moves BOTH participants' ratings inside
// ONE transaction. A unit test with a mocked queries package would prove only that
// the mock was called with the right arguments, and it would keep passing if the
// service moved one rating, moved them in the wrong order, or forgot the
// transaction entirely. The property lives in what the database ends up holding,
// so it needs a database.
//
// TestMain brings up the test database.
//
// The elo package's own tests need no database, but the integration ones do, and
// testutil is the single place that knows how to create and tear one down. A nil
// populater is correct here: every fixture this file needs is created inline,
// because a performer is one INSERT and a shared populator for one test package
// would be a second place to keep in sync.
func TestMain(m *testing.M) {
	testutil.TestWithDatabase(m, nil)
}

// testTime is a fixed clock. Every elapsed-time figure in these tests is supplied
// explicitly rather than measured, so the assertions do not depend on how long
// the test took to run.
var testTime = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

// q returns a fresh queries handle for direct database access in test setup.
//
// Built from testutil.DB() rather than a Factory accessor, because service.Factory
// deliberately does not export its pool or its queries -- the encapsulation is
// correct for production, and a test that needed to reach past it would be
// evidence of a missing service method. The two places that need it (creating a
// performer, writing a known rating) are test FIXTURE setup, not assertions.
func q() *queries.Queries {
	return queries.New(testutil.DB())
}

func newElo(t *testing.T) *elo.Elo {
	t.Helper()
	return elo.NewElo(q(), createWithTxn()).
		WithClock(func() time.Time { return testTime })
}

// createWithTxn builds a transaction function over the test pool.
func createWithTxn() queries.WithTxnFunc {
	pool := testutil.DB()
	return func(fn func(*queries.Queries) error) error {
		return pgx.BeginFunc(context.Background(), pool, func(tx pgx.Tx) error {
			return fn(queries.New(tx))
		})
	}
}

// createPerformer inserts a performer so the vote has a real entity to attach to.
//
// A real performer rather than a bare uuid, because elo_ratings deliberately has
// no foreign key -- see models.go -- so a test using a random uuid would pass
// while proving nothing about the code path a real vote takes.
func createPerformer(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	_, err := q().CreatePerformer(t.Context(), queries.CreatePerformerParams{
		ID:   id,
		Name: "Elo Test Performer " + id.String(),
	})
	require.NoError(t, err, "creating the performer under test")
	return id
}

// createUser inserts a user for the vote's author.
//
// elo_votes.user_id has a real foreign key while elo_ratings.entity_id does not,
// and that asymmetry is deliberate: a vote is an act by a specific user, so it
// must name one, whereas a rating is a cache keyed by whatever is being ranked
// and must survive the ranked thing being merged or deleted. The first version of
// this test used a bare random uuid and failed on the constraint -- correctly.
func createUser(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	_, err := q().CreateUser(t.Context(), queries.CreateUserParams{
		ID:           id,
		Name:         "Elo Voter " + id.String(),
		PasswordHash: "not-a-real-hash-this-test-never-logs-in",
		Email:        id.String() + "@elo.invalid",
		ApiKey:       id.String(),
	})
	require.NoError(t, err, "creating the voter")
	return id
}

// ratedPerformer creates a performer with a KNOWN rating, deviation and vote
// count, so a leaderboard test can set up an exact ordering instead of trying to
// vote a specific number of times and hoping the maths lands where it should.
//
// This is the one place the test reaches past the service, and it is the right
// place: the property under test is how the service RANKS ratings it is given,
// not how it computes them. Computing them is glicko_test.go's job.
//
// All three values have to be set explicitly. The vote count in particular cannot
// be left to the service, because CountEloVotesForEntity derives it from the log
// and a performer whose rating was written directly has no votes -- which means
// two such performers tie at zero and the tiebreak never engages.
func ratedPerformer(t *testing.T, rating int, deviation float64, voteCount int) uuid.UUID {
	t.Helper()
	voter := createUser(t)
	id := createPerformer(t)
	// The votes need a real counterparty so each row has both a winner and a
	// loser; the counterparty is discarded, only the count on `id` matters.
	for range voteCount {
		other := createPerformer(t)
		_, err := q().RecordEloVote(t.Context(), queries.RecordEloVoteParams{
			ID:         uuid.Must(uuid.NewV7()),
			UserID:     voter,
			WinnerID:   id,
			LoserID:    other,
			WinnerType: string(elo.EntityPerformer),
			LoserType:  string(elo.EntityPerformer),
			PickedSide: 0,
			// Explicit, because RecordEloVote now always sends the weight
			// column. Go's zero value is 0.0, and the elo_votes_weight_positive
			// CHECK (migration 85) rejects it -- so omitting this fails the
			// insert with a constraint violation rather than defaulting to the
			// baseline. The column's DEFAULT 1.0 only covers raw SQL that omits
			// the column, not a generated query that sends it.
			//
			// This fixture is a hand-written vote, so there is no voter whose
			// trust to derive a weight from; the baseline is the honest value.
			Weight: 1.0,
		})
		require.NoError(t, err)
	}
	_, err := q().UpsertEloRating(t.Context(), queries.UpsertEloRatingParams{
		EntityType: string(elo.EntityPerformer),
		EntityID:   id,
		Rating:     rating,
		Deviation:  deviation,
		Volatility: 0.06,
	})
	require.NoError(t, err)
	return id
}

func TestVoteMovesBothRatings(t *testing.T) {
	s := newElo(t)
	user := createUser(t)
	left, right := createPerformer(t), createPerformer(t)

	before, err := s.RatingFor(t.Context(), elo.Entity{Type: elo.EntityPerformer, ID: left})
	require.NoError(t, err)
	assert.Equal(t, 1500, int(before.Rating.Rating),
		"an unrated performer must start at Glickman's default")

	rec, err := s.Vote(t.Context(), elo.VoteInput{
		UserID: user,
		Matchup: elo.Matchup{
			EntityType: elo.EntityPerformer,
			Left:       elo.Entity{Type: elo.EntityPerformer, ID: left},
			Right:      elo.Entity{Type: elo.EntityPerformer, ID: right},
		},
		PickedSide:   elo.PickedLeft,
		ElapsedDaysA: 1,
		ElapsedDaysB: 1,
	})
	require.NoError(t, err)

	assert.Greater(t, rec.Rating.Rating, 1500.0,
		"the side the user chose must go up")
	assert.Equal(t, 1, rec.VoteCount,
		"the returned record is the CHOSEN side's, and this is its first vote")

	// The load-bearing assertion. A vote that moved only the winner -- the bug a
	// service-level test is most likely to have -- would pass everything above.
	picked, err := s.RatingFor(t.Context(), elo.Entity{Type: elo.EntityPerformer, ID: left})
	require.NoError(t, err)
	other, err := s.RatingFor(t.Context(), elo.Entity{Type: elo.EntityPerformer, ID: right})
	require.NoError(t, err)

	assert.Greater(t, picked.Rating.Rating, 1500.0, "the chosen performer rose")
	assert.Less(t, other.Rating.Rating, 1500.0,
		"the performer NOT chosen must also fall: a vote that only credits the "+
			"winner makes the system a reputation dispenser rather than a ranking")
	assert.Equal(t, 1, picked.VoteCount)
	assert.Equal(t, 1, other.VoteCount, "both participants have taken part in one vote")
}

// The vote must be in the log, in DISPLAY order, with picked_side recorded.
//
// This is the position-bias defence from SPEC §9 and the reason picked_side exists
// as a column at all. If the service stored the vote as "winner then loser" the
// test below could not tell the difference, and position bias -- a measurable
// preference for whichever candidate is shown first -- would be silently
// impossible to audit.
func TestVoteIsStoredInDisplayOrder(t *testing.T) {
	s := newElo(t)
	user := createUser(t)
	left, right := createPerformer(t), createPerformer(t)

	_, err := s.Vote(t.Context(), elo.VoteInput{
		UserID: user,
		Matchup: elo.Matchup{
			EntityType: elo.EntityPerformer,
			Left:       elo.Entity{Type: elo.EntityPerformer, ID: left},
			Right:      elo.Entity{Type: elo.EntityPerformer, ID: right},
		},
		PickedSide:   elo.PickedRight,
		ElapsedDaysA: 1,
		ElapsedDaysB: 1,
	})
	require.NoError(t, err)

	votes, err := q().GetEloVotesForUser(t.Context(), user)
	require.NoError(t, err)
	require.Len(t, votes, 1)

	// The user picked RIGHT, yet winner_id is the LEFT performer: the columns mean
	// "the slot shown here", not "the better performer".
	assert.Equal(t, left, votes[0].WinnerID,
		"winner_id must be the LEFT slot, the one shown first, even though the "+
			"user chose the right one; otherwise position bias cannot be audited")
	assert.Equal(t, right, votes[0].LoserID)
	assert.Equal(t, int16(1), votes[0].PickedSide,
		"picked_side records which slot the user actually took")
}

// The first vote on a brand-new performer is the common path, not an edge case.
//
// Migration 77 seeds ratings for performers that existed at migration time, so
// every performer created since has no row in elo_ratings. A load that treated a
// missing row as an error would break voting on every new performer, and the only
// way to notice is to test the new-performer path specifically.
func TestFirstVoteOnANewPerformerWorks(t *testing.T) {
	s := newElo(t)
	user := createUser(t)
	first, second := createPerformer(t), createPerformer(t)

	rating, err := s.RatingFor(t.Context(), elo.Entity{Type: elo.EntityPerformer, ID: first})
	require.NoError(t, err)
	assert.Equal(t, 0, rating.VoteCount)

	_, err = s.Vote(t.Context(), elo.VoteInput{
		UserID: user,
		Matchup: elo.Matchup{
			EntityType: elo.EntityPerformer,
			Left:       elo.Entity{Type: elo.EntityPerformer, ID: first},
			Right:      elo.Entity{Type: elo.EntityPerformer, ID: second},
		},
		PickedSide:   elo.PickedLeft,
		ElapsedDaysA: 1,
		ElapsedDaysB: 1,
	})
	assert.NoError(t, err, "a performer with no rating row must be votable")
}

// Volatility must survive a round trip through the database.
//
// Glickman's sigma is persistent state: the volatility update reads the previous
// value to bound how far it may move. A schema that stores rating and deviation
// but not volatility therefore does not merely lose a number -- it silently
// restarts every player at 0.06 on every vote, which is exactly the signal that
// separates a consistent performer from an erratic one. That bug is invisible in
// any test that only votes once, so it needs a test that votes twice and checks
// the column.
func TestVolatilityIsPersisted(t *testing.T) {
	s := newElo(t)
	user := createUser(t)
	left, right := createPerformer(t), createPerformer(t)

	for range 2 {
		_, err := s.Vote(t.Context(), elo.VoteInput{
			UserID: user,
			Matchup: elo.Matchup{
				EntityType: elo.EntityPerformer,
				Left:       elo.Entity{Type: elo.EntityPerformer, ID: left},
				Right:      elo.Entity{Type: elo.EntityPerformer, ID: right},
			},
			PickedSide:   elo.PickedLeft,
			ElapsedDaysA: 1,
			ElapsedDaysB: 1,
		})
		require.NoError(t, err)
	}

	row, err := q().GetEloRating(t.Context(), queries.GetEloRatingParams{
		EntityType: string(elo.EntityPerformer),
		EntityID:   left,
	})
	require.NoError(t, err)

	// Not 0.06 exactly -- the point is that it MOVED and was read back, which it
	// cannot do if the column does not exist or is not written.
	assert.NotEqual(t, 0.06, row.Volatility,
		"volatility must be written on every vote, not left at the default: the "+
			"paper's volatility update reads the previous sigma to bound the change")
	assert.Greater(t, row.Volatility, 0.0)
	assert.Less(t, row.Volatility, 1.0, "volatility must stay a plausible probability scale")
}

// A malformed matchup must be refused, and must change nothing.
func TestInvalidMatchupsAreRefused(t *testing.T) {
	s := newElo(t)
	user := createUser(t)
	performer := createPerformer(t)
	entity := elo.Entity{Type: elo.EntityPerformer, ID: performer}

	for _, tc := range []struct {
		name  string
		input elo.VoteInput
		want  error
	}{
		{
			name: "same entity on both sides",
			input: elo.VoteInput{UserID: user, PickedSide: elo.PickedLeft,
				Matchup: elo.Matchup{EntityType: elo.EntityPerformer, Left: entity, Right: entity}},
			want: elo.ErrSameEntity,
		},
		{
			name: "unknown entity type",
			input: elo.VoteInput{UserID: user, PickedSide: elo.PickedLeft,
				Matchup: elo.Matchup{
					EntityType: elo.EntityType("nonsense"),
					Left:       entity,
					Right:      elo.Entity{Type: elo.EntityType("nonsense"), ID: uuid.Must(uuid.NewV7())},
				}},
			want: elo.ErrUnknownEntityType,
		},
		{
			name: "side out of range",
			input: elo.VoteInput{UserID: user, PickedSide: elo.PickedSide(7),
				Matchup: elo.Matchup{EntityType: elo.EntityPerformer, Left: entity,
					Right: elo.Entity{Type: elo.EntityPerformer, ID: uuid.Must(uuid.NewV7())}}},
			want: elo.ErrInvalidSide,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.Vote(t.Context(), tc.input)
			assert.ErrorIs(t, err, tc.want)

			// Nothing was written. A validation failure that still moved a rating
			// would be the worst outcome of all, because the caller's instinct on
			// seeing an error is to assume nothing happened.
			rating, err := s.RatingFor(t.Context(), entity)
			require.NoError(t, err)
			assert.Equal(t, 0, rating.VoteCount,
				"a refused vote must not be recorded at all")
		})
	}
}

// The taste vector is the per-user profile of SPEC §2, and it must be rebuilt
// from the whole vote log rather than added to incrementally.
func TestTasteVectorReflectsEveryVote(t *testing.T) {
	s := newElo(t)
	user := createUser(t)
	a, b, c := createPerformer(t), createPerformer(t), createPerformer(t)

	castVote := func(picked, other uuid.UUID) {
		t.Helper()
		_, err := s.Vote(t.Context(), elo.VoteInput{
			UserID: user,
			Matchup: elo.Matchup{
				EntityType: elo.EntityPerformer,
				Left:       elo.Entity{Type: elo.EntityPerformer, ID: picked},
				Right:      elo.Entity{Type: elo.EntityPerformer, ID: other},
			},
			PickedSide:   elo.PickedLeft,
			ElapsedDaysA: 1,
			ElapsedDaysB: 1,
		})
		require.NoError(t, err)
	}
	castVote(a, b)
	castVote(a, c)
	castVote(b, c)

	tv, err := s.TasteVectorFor(t.Context(), user)
	require.NoError(t, err)

	assert.Equal(t, 3, tv.VoteCount)
	assert.Equal(t, 2.0, tv.Vector["performer:"+a.String()],
		"a was chosen twice and never passed over: +2")
	assert.Equal(t, 0.0, tv.Vector["performer:"+b.String()],
		"b was chosen once and passed over once: 0, and a key that nets to zero "+
			"must still be present so a recommender can tell 'indifferent' from "+
			"'never seen'")
	assert.Equal(t, -2.0, tv.Vector["performer:"+c.String()], "c was passed over twice")
}

// A user who has never voted has no taste_vectors row, and that is a normal
// state rather than an error.
func TestTasteVectorForANonVoterIsEmpty(t *testing.T) {
	s := newElo(t)

	tv, err := s.TasteVectorFor(t.Context(), uuid.Must(uuid.NewV7()))
	require.NoError(t, err, "never having voted is not a failure")
	assert.Equal(t, 0, tv.VoteCount)
	assert.Empty(t, tv.Vector)
}

// The leaderboard must not put a three-vote rating above a three-hundred-vote one
// just because it is numerically higher.
func TestLeaderboardRanksByObservationNotJustRating(t *testing.T) {
	s := newElo(t)

	// A near-tie, not a rout. Rankable only breaks ties by observation when the
	// ratings differ by less than a fifth of the combined deviation, so a test
	// using 1900 against 1750 would be asserting the rating sort -- the exact thing
	// the leaderboard is supposed to do differently -- and would pass even if
	// Leaderboard ignored Rankable entirely.
	//
	// My first two attempts at this got it wrong in exactly that way. With
	// deviations 2 and 20 the combined threshold is 4.4, so the ratings have to sit
	// within 4.4 points of each other for the vote counts to matter at all.
	thin := ratedPerformer(t, 1753, 20, 3)
	thick := ratedPerformer(t, 1750, 2, 300)

	// The database is shared across every test in this package, so the other
	// tests' performers are on this leaderboard too. Comparing absolute positions
	// would assert on their ratings rather than on the ranking rule, and the test
	// would pass or fail depending on which tests ran first. Compare the RELATIVE
	// order of the two performers this test created.
	board, err := s.Leaderboard(t.Context(), elo.EntityPerformer, 500)
	require.NoError(t, err)

	position := map[uuid.UUID]int{}
	for i, entry := range board.Entries {
		position[entry.EntityID] = i
	}
	if !assert.Contains(t, position, thin) || !assert.Contains(t, position, thick) {
		return
	}
	assert.Less(t, position[thick], position[thin],
		"a 1750 rating built on a deviation of 2 outranks a 1900 built on a "+
			"deviation of 20: the first is settled and the second is three votes of "+
			"noise, and a leaderboard that cannot tell them apart is gameable with "+
			"three votes")
}
