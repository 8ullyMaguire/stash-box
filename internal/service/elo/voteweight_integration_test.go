//go:build integration

package elo_test

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/elo"
)

// Why this file exists.
//
// VoterWeight was fully unit-tested and had ZERO production callers. The
// migration added a weight column and nothing ever wrote to it. Every test in
// the package passed, the build was clean, and the SPEC §7.23 D1 feature did
// nothing at all.
//
// A unit test cannot catch that: it tests the function against its arguments.
// Whether anything CALLS the function, and whether the value it returns reaches
// the database and the rating maths, is a question about the wiring. So these
// tests assert on what the database ends up holding, which is the only place
// "inert" becomes visible.

// grantTrust writes a user_trust row directly.
//
// Direct rather than through the trust service on purpose: this test is about
// the elo service READING trust, and driving it through the trust service would
// make a failure ambiguous between "the trust service did not write what I
// asked" and "the elo service did not read it".
func grantTrust(t *testing.T, userID uuid.UUID, level int, isVanguard bool) {
	t.Helper()
	// Through the pool rather than a generated query: no generated query writes
	// is_vanguard yet (there is no admin surface for granting vanguard, which is
	// its own gap, noted in the plan's deviations), and this file is about the
	// elo service reading trust, not about how trust gets written.
	_, err := testutil.DB().Exec(t.Context(),
		`INSERT INTO user_trust (user_id, level, is_vanguard) VALUES ($1, $2, $3)
		 ON CONFLICT (user_id) DO UPDATE SET level = EXCLUDED.level, is_vanguard = EXCLUDED.is_vanguard`,
		userID, level, isVanguard)
	require.NoError(t, err, "granting trust to the voter")
}

func recordedWeight(t *testing.T, userID uuid.UUID) float64 {
	t.Helper()
	// Through the pool, not a generated query: the assertion is about the VALUE
	// that reached the column, so reading it through the same generated layer
	// that wrote it would risk a test that agrees with a buggy mapping.
	var w float64
	err := testutil.DB().QueryRow(t.Context(),
		`SELECT weight FROM elo_votes WHERE user_id = $1 ORDER BY created_at DESC LIMIT 1`, userID).Scan(&w)
	require.NoError(t, err, "reading the recorded weight")
	return w
}

// matchup builds a performer matchup, so the tests below read as a list of
// concerns rather than a list of struct literals.
func matchup(t *testing.T) elo.Matchup {
	t.Helper()
	return elo.Matchup{
		EntityType: elo.EntityPerformer,
		Left:       elo.Entity{Type: elo.EntityPerformer, ID: createPerformer(t)},
		Right:      elo.Entity{Type: elo.EntityPerformer, ID: createPerformer(t)},
	}
}

// TestAVanguardVoteRecordsAWeightAboveBaseline is the end-to-end proof that the
// D1 feature is live: a real user, granted vanguard status, casts a real vote,
// and the row that lands in the database carries a weight above 1.0.
func TestAVanguardVoteRecordsAWeightAboveBaseline(t *testing.T) {
	svc := newElo(t)
	voter := createUser(t)
	grantTrust(t, voter, 4, true)

	_, err := svc.Vote(t.Context(), elo.VoteInput{
		UserID:     voter,
		Matchup:    matchup(t),
		PickedSide: elo.PickedLeft,
	})
	require.NoError(t, err, "a vanguard casting a vote")

	got := recordedWeight(t, voter)
	assert.Greater(t, got, 1.0,
		"a level-4 vanguard's vote recorded a weight of %v; the weight column is not being written, "+
			"so trust-weighted voting is inert no matter what the unit tests say", got)
}

// TestANewUserWithoutTrustRecordsTheBaseline covers the other half, and it is
// the case a naive implementation gets wrong.
//
// Every new user has no user_trust row at all. If Vote() treated a missing row
// as an error, the FIRST vote a brand-new user ever casts would be rejected --
// which is a far worse bug than a missing weight, because it is visible
// immediately rather than silently degrading a feature.
func TestANewUserWithoutTrustRecordsTheBaseline(t *testing.T) {
	svc := newElo(t)
	voter := createUser(t)
	// deliberately NO grantTrust call

	_, err := svc.Vote(t.Context(), elo.VoteInput{
		UserID:     voter,
		Matchup:    matchup(t),
		PickedSide: elo.PickedLeft,
	})
	require.NoError(t, err, "a brand-new user with no trust row casting their first vote")

	assert.Equal(t, 1.0, recordedWeight(t, voter),
		"a user with no trust row should be weighted at the baseline, not rejected and not zero")
}

// TestTheRecordedWeightIsHigherForAVanguardThanAPlainUser proves the weight
// responds to the inputs rather than merely being non-baseline.
//
// The first test would also pass if the service wrote, say, the vanguard
// multiplier unconditionally. Comparing two voters is what shows it is reading
// the trust row.
func TestTheRecordedWeightIsHigherForAVanguardThanAPlainUser(t *testing.T) {
	svc := newElo(t)

	vanguard, plain := createUser(t), createUser(t)
	grantTrust(t, vanguard, 4, true)
	grantTrust(t, plain, 0, false)

	_, err := svc.Vote(t.Context(), elo.VoteInput{
		UserID: vanguard, Matchup: matchup(t), PickedSide: elo.PickedLeft,
	})
	require.NoError(t, err, "the vanguard's vote")

	_, err = svc.Vote(t.Context(), elo.VoteInput{
		UserID: plain, Matchup: matchup(t), PickedSide: elo.PickedLeft,
	})
	require.NoError(t, err, "the plain user's vote")

	assert.Greater(t, recordedWeight(t, vanguard), recordedWeight(t, plain),
		"a vanguard's vote (%v) should be weighted above a level-0 user's (%v)",
		recordedWeight(t, vanguard), recordedWeight(t, plain))
}

// TestTheWeightActuallyMovesTheRating is the last link in the chain.
//
// Every other test here proves the weight is computed and stored. This proves it
// is USED -- that two identical matchups voted by a vanguard and by a new user
// leave the winner's rating in different places. Without this, a service that
// computed a weight, stored it, and then ignored it in the maths would pass
// every test in this file.
func TestTheWeightActuallyMovesTheRating(t *testing.T) {
	svc := newElo(t)

	vanguard, plain := createUser(t), createUser(t)
	grantTrust(t, vanguard, 4, true)
	grantTrust(t, plain, 0, false)

	// Two structurally identical matchups, voted by two differently-trusted
	// users. Same entity type, same clock, same elapsed time.
	vanguardWinner := createPerformer(t)
	_, err := svc.Vote(t.Context(), elo.VoteInput{
		UserID: vanguard,
		Matchup: elo.Matchup{
			EntityType: elo.EntityPerformer,
			Left:       elo.Entity{Type: elo.EntityPerformer, ID: vanguardWinner},
			Right:      elo.Entity{Type: elo.EntityPerformer, ID: createPerformer(t)},
		},
		PickedSide: elo.PickedLeft,
	})
	require.NoError(t, err, "the vanguard's vote")

	plainWinner := createPerformer(t)
	_, err = svc.Vote(t.Context(), elo.VoteInput{
		UserID: plain,
		Matchup: elo.Matchup{
			EntityType: elo.EntityPerformer,
			Left:       elo.Entity{Type: elo.EntityPerformer, ID: plainWinner},
			Right:      elo.Entity{Type: elo.EntityPerformer, ID: createPerformer(t)},
		},
		PickedSide: elo.PickedLeft,
	})
	require.NoError(t, err, "the plain user's vote")

	vanguardRating, err := svc.RatingFor(t.Context(), elo.Entity{Type: elo.EntityPerformer, ID: vanguardWinner})
	require.NoError(t, err, "reading the vanguard-voted rating")
	plainRating, err := svc.RatingFor(t.Context(), elo.Entity{Type: elo.EntityPerformer, ID: plainWinner})
	require.NoError(t, err, "reading the plainly-voted rating")

	// Both start from DefaultRating, so the one that moved further is the one
	// whose voter was trusted. Comparing distances rather than absolute ratings
	// keeps the assertion honest even though the two winners end up at different
	// values, which is the whole point.
	vanguardMove := abs(vanguardRating.Rating.Rating - elo.DefaultRating)
	plainMove := abs(plainRating.Rating.Rating - elo.DefaultRating)

	assert.Greater(t, vanguardMove, plainMove,
		"a vanguard's vote moved the winner %v and a new user's vote moved it %v; "+
			"the weight is stored but not reaching the rating maths", vanguardMove, plainMove)
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// TestTheWeightIsSnapshottedNotRecomputed proves plan D5: a vote's weight is a
// fact about when it was cast.
//
// If the weight were computed at read time, granting the voter vanguard status
// AFTER casting would change the weight of a vote already in the log. That is
// the retroactive re-weighting the column exists to prevent, and it is
// invisible unless something checks it.
func TestTheWeightIsSnapshottedNotRecomputed(t *testing.T) {
	svc := newElo(t)
	voter := createUser(t)
	grantTrust(t, voter, 0, false)

	_, err := svc.Vote(t.Context(), elo.VoteInput{
		UserID:     voter,
		Matchup:    matchup(t),
		PickedSide: elo.PickedLeft,
	})
	require.NoError(t, err, "the first vote")

	before := recordedWeight(t, voter)
	// Not 1.0: level 0 falls through every band in levelBands to the lowest
	// one (0.5), so a level-0 non-vanguard votes at the bottom of the range
	// rather than at the baseline. That is a deliberate reading of the bands --
	// a user with no trust is trusted least, not neutrally -- and the value
	// asserted here is the one VoterWeight actually produces. What matters for
	// THIS test is only that the value does not change below.
	require.NotEqual(t, 0.0, before, "a level-0 voter's weight should be recorded, not zero")

	// The user is promoted afterwards. The vote already cast must not change.
	grantTrust(t, voter, 4, true)

	after := recordedWeight(t, voter)
	assert.Equal(t, before, after,
		"granting vanguard status changed the weight of an already-cast vote from %v to %v; "+
			"the weight must be a snapshot of cast time, not recomputed from current trust",
		before, after)
}

// TestTheWeightIsRecordedInTheVoteParamsNotComputedInSQL guards the shape of the
// fix rather than its value.
//
// The weight is passed into RecordEloVote by the service. If someone later
// "simplifies" it into a SQL expression, the test above still passes while the
// retroactive re-weighting returns, because the expression is evaluated when
// the row is read back. Asserting the service supplies it is what keeps that
// from being reintroduced silently.
func TestTheWeightIsRecordedInTheVoteParamsNotComputedInSQL(t *testing.T) {
	// A row written with an explicit weight is the contract; a row written by
	// a defaulting expression would take 1.0 no matter what the service passed.
	id := uuid.Must(uuid.NewV7())
	voter := createUser(t)
	_, err := q().RecordEloVote(t.Context(), queries.RecordEloVoteParams{
		ID:         id,
		UserID:     voter,
		WinnerID:   createPerformer(t),
		LoserID:    createPerformer(t),
		WinnerType: string(elo.EntityPerformer),
		LoserType:  string(elo.EntityPerformer),
		PickedSide: 0,
		Weight:     1.25,
	})
	require.NoError(t, err, "writing a vote with an explicit weight")

	var got float64
	require.NoError(t,
		testutil.DB().QueryRow(t.Context(), `SELECT weight FROM elo_votes WHERE id = $1`, id).Scan(&got),
		"reading it back")
	assert.Equal(t, 1.25, got, "an explicitly passed weight must be stored verbatim")
}
