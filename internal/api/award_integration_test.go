//go:build integration

package api_test

import (
	"context"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/service/award"
	"github.com/stashapp/stash-box/internal/service/trust"
)

// The XP award path against the real rollup (SPEC §12, Phase 2 step 6).
//
// The unit tests pin what the award service ASKS for -- two events, the magnitude
// in the second, the quest as the dedup key. None of that proves the rollup moves
// the right columns, and the rollup is where a magnitude either survives or is
// silently rounded to 1.
//
// The load-bearing property: a 500-point bounty must arrive as 500 points on ONE
// completed quest. If it arrived as 500 quests, the curator's level would jump by
// two thresholds and nothing anywhere would report an error.

// newAwardService builds the award service over the test database's factory, and
// returns the trust service too so a test can read the rollup back.
//
// Through the factory, not assembled by hand: the factory owns the transaction
// function the trust service needs, and a hand-assembled trust service here would
// be a different object from the one production constructs.
func newAwardService(t *testing.T) (*award.Service, *trust.Trust) {
	t.Helper()
	f := dbtest.Factory()
	return award.NewService(f.Trust()), f.Trust()
}

// A bounty arrives as points, and as ONE quest.
func TestABountyArrivesAsPointsNotQuests(t *testing.T) {
	awards, trustSvc := newAwardService(t)
	ctx := context.Background()

	curator := createUserForQuest(t, "A Bountied Curator")
	questID := uuid.Must(uuid.NewV7())

	total, err := awards.QuestCompletion(ctx, award.QuestCompletionInput{
		CuratorID: curator, QuestID: questID, BountyPoints: 500,
	})
	require.NoError(t, err)
	assert.Equal(t, trust.PointsPerKind[trust.KindQuestCompleted]+500, total)

	rollup, err := trustSvc.Rollup(ctx, curator)
	require.NoError(t, err)
	require.NotNil(t, rollup)

	assert.Equal(t, 1, rollup.QuestsCompleted,
		"ONE quest completed. A 500-point bounty recorded as delta=500 on the "+
			"quest kind would show 500 here and 10,000 points -- the exact bug the "+
			"split into two kinds exists to prevent, and nothing else would report it")
	assert.Equal(t, 500, rollup.BonusPoints,
		"the magnitude arrives in bonus_points, which is where a sum belongs")

	totals, err := trustSvc.TotalsFor(ctx, curator)
	require.NoError(t, err)
	assert.Equal(t, total, totals.Points(),
		"the score is the count's weight plus the bonus, and TotalsFor is the "+
			"read every level check goes through")
}

// A bounty's points must reach the LEVEL, or the whole feature is decorative.
func TestABountyCanRaiseTheLevel(t *testing.T) {
	awards, trustSvc := newAwardService(t)
	ctx := context.Background()

	curator := createUserForQuest(t, "A Curator Levelled By A Bounty")

	// The Curator threshold is 200 points. Ten plain quests is 200 exactly, and a
	// single 500-point bounty crosses it alone -- which is the entire reason an
	// operator sets one.
	for i := 0; i < 9; i++ {
		_, err := awards.QuestCompletion(ctx, award.QuestCompletionInput{
			CuratorID: curator, QuestID: uuid.Must(uuid.NewV7()),
		})
		require.NoError(t, err)
	}

	level, err := trustSvc.Level(ctx, curator)
	require.NoError(t, err)
	assert.Equal(t, trust.LevelContributor, level,
		"nine quests is 180 points, short of the 200 needed for Curator")

	_, err = awards.QuestCompletion(ctx, award.QuestCompletionInput{
		CuratorID: curator, QuestID: uuid.Must(uuid.NewV7()), BountyPoints: 500,
	})
	require.NoError(t, err)

	level, err = trustSvc.Level(ctx, curator)
	require.NoError(t, err)
	assert.Equal(t, trust.LevelCurator, level,
		"the bounty is what crossed the threshold, and the level follows the points")
}

// REPLAYING an award must not pay twice. The dedup index is the guarantee, and
// this is the assertion that would catch its removal.
func TestReplayingAQuestAwardDoesNotPayTwice(t *testing.T) {
	awards, trustSvc := newAwardService(t)
	ctx := context.Background()

	curator := createUserForQuest(t, "A Replayed Quest Curator")
	questID := uuid.Must(uuid.NewV7())
	in := award.QuestCompletionInput{
		CuratorID: curator, QuestID: questID, BountyPoints: 500,
	}

	_, err := awards.QuestCompletion(ctx, in)
	require.NoError(t, err)
	first, err := trustSvc.Rollup(ctx, curator)
	require.NoError(t, err)

	// The retry a dropped connection produces.
	_, err = awards.QuestCompletion(ctx, in)
	require.NoError(t, err,
		"a duplicate is a no-op rather than an error: the dedup index rejects it "+
			"and the rollup is left alone, so a retried request is safe")

	second, err := trustSvc.Rollup(ctx, curator)
	require.NoError(t, err)

	assert.Equal(t, first.QuestsCompleted, second.QuestsCompleted,
		"one quest, however many times the request arrives")
	assert.Equal(t, first.BonusPoints, second.BonusPoints,
		"and ONE bounty. A doubled bounty would be the quiet version of the same "+
			"bug: 1000 points for one quest, no error, one level too high")
}

// THE RECOVERY PATH. A rebuild from the event log must produce the same answer as
// the incremental award, or a routine repair silently changes everyone's score.
func TestARebuildReproducesTheAward(t *testing.T) {
	awards, trustSvc := newAwardService(t)
	ctx := context.Background()

	curator := createUserForQuest(t, "A Rebuilt Curator")
	for i := 0; i < 3; i++ {
		_, err := awards.QuestCompletion(ctx, award.QuestCompletionInput{
			CuratorID: curator, QuestID: uuid.Must(uuid.NewV7()),
		})
		require.NoError(t, err)
	}
	_, err := awards.QuestCompletion(ctx, award.QuestCompletionInput{
		CuratorID: curator, QuestID: uuid.Must(uuid.NewV7()), BountyPoints: 750,
	})
	require.NoError(t, err)

	before, err := trustSvc.Rollup(ctx, curator)
	require.NoError(t, err)
	require.Equal(t, 4, before.QuestsCompleted)
	require.Equal(t, 750, before.BonusPoints)

	// Drift the rollup by hand -- the exact situation the recompute exists for.
	_, err = dbtest.DB().Exec(ctx, `UPDATE user_trust SET bonus_points = 0, quests_completed = 0
		WHERE user_id = $1`, curator)
	require.NoError(t, err, "the drift is what makes the assertion below meaningful")

	require.NoError(t, trustSvc.RebuildLevels(ctx))

	after, err := trustSvc.Rollup(ctx, curator)
	require.NoError(t, err)
	assert.Equal(t, before.QuestsCompleted, after.QuestsCompleted,
		"the rebuild replays the log and recovers the count")
	assert.Equal(t, before.BonusPoints, after.BonusPoints,
		"and the BOUNTY. A recompute query that grew the bonus_points column but "+
			"not its CASE term would pass every award test and lose every bounty on "+
			"the first rebuild -- which is the recovery path, so the loss would "+
			"surface exactly when the rollup was already known to be wrong")
}

// A bounty with no magnitude is a plain quest, and leaves no trace that could
// block a later real bounty on the same quest.
func TestALaterBountyOnTheSameQuestIsNotBlockedByAnEarlierEmptyOne(t *testing.T) {
	awards, trustSvc := newAwardService(t)
	ctx := context.Background()

	curator := createUserForQuest(t, "An Upgraded Quest Curator")
	questID := uuid.Must(uuid.NewV7())

	// Authored with no bounty...
	_, err := awards.QuestCompletion(ctx, award.QuestCompletionInput{
		CuratorID: curator, QuestID: questID, BountyPoints: 0,
	})
	require.NoError(t, err)

	// ...and an operator later adds one, and the same award is replayed. A
	// zero-valued bounty_bonus event recorded on the first pass would occupy the
	// dedup key and make this a silent no-op.
	_, err = awards.QuestCompletion(ctx, award.QuestCompletionInput{
		CuratorID: curator, QuestID: questID, BountyPoints: 300,
	})
	require.NoError(t, err)

	rollup, err := trustSvc.Rollup(ctx, curator)
	require.NoError(t, err)
	assert.Equal(t, 300, rollup.BonusPoints,
		"the later bounty is paid. It is a different KIND from the quest event, so "+
			"the quest's own dedup slot is untouched by the first award and the "+
			"bounty's slot was never occupied by a zero")
}

// A negative rollup is refused by the database, so a bug that reverses a bounty
// cannot leave a score nobody can reproduce.
func TestANegativeBonusTotalIsRefusedByTheDatabase(t *testing.T) {
	curator := createUserForQuest(t, "A Curator With A Negative Bonus")

	_, err := dbtest.DB().Exec(context.Background(),
		`INSERT INTO user_trust (user_id, bonus_points) VALUES ($1, -50)`, curator)
	require.Error(t, err,
		"the CHECK is the last line of defence. The event log is signed, so a "+
			"legitimate reversal exists -- but a negative RUNNING TOTAL of bonus "+
			"points is a score that cannot be reproduced from the log's positive "+
			"contributions, and an unreproducible score is worse than a failed "+
			"write")
	assert.Contains(t, err.Error(), "user_trust_bonus_points_nonnegative")
}

// A user with no rollup at all still reports zero points, so a client does not
// have to distinguish "no row" from "a row of zeroes".
func TestAUserWithNoEventsHasZeroPoints(t *testing.T) {
	_, trustSvc := newAwardService(t)

	totals, err := trustSvc.TotalsFor(context.Background(), uuid.Must(uuid.NewV7()))
	require.NoError(t, err)
	assert.Equal(t, 0, totals.Points())
	assert.Equal(t, 0, totals.BonusPoints)
}
