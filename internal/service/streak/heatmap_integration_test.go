//go:build integration

package streak_test

import (
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/database/testutil"
)

// The contribution heatmap against a real database (plan phase 6 slice 6e).
//
// The pure tests next door prove the window's arithmetic. These prove the COUNTS, and the count is where
// a heatmap and a streak disagree: they come from the same log and mean different things, so the test
// that matters is the one asserting they agree about the DAYS while differing on the amounts.
//
// ONE THING IT CANNOT REUSE IS THE FREEZE SUITE'S REFERENCE DAY. The heatmap's window ends on the
// DATABASE's today, not on a fixed instant a test chose, so fixtures anchored to a hard-coded date fall
// outside it -- which is exactly how the first version of these tests failed, with every count coming back
// empty against a working query. The events are therefore backdated from the database's own clock, at the
// same time of day, so a backdated row cannot land on the day either side of the one intended.
//
// TestMain lives in freeze_integration_test.go.

// contributedDaysAgo records one event per offset, counted back from the DATABASE's today.
func contributedDaysAgo(t *testing.T, userID uuid.UUID, offsets ...int) {
	t.Helper()
	today, err := q().DatabaseNow(t.Context())
	require.NoError(t, err)
	for _, offset := range offsets {
		entityID := uuidNew(t)
		_, err := testutil.DB().Exec(t.Context(),
			`INSERT INTO trust_events (user_id, kind, entity_type, entity_id, delta, created_at)
			 VALUES ($1, 'edit_approved', 'performer', $2, 1, $3)`,
			userID, entityID, today.AddDate(0, 0, -offset))
		require.NoError(t, err, "backdating an event %d days before the database's today", offset)
	}
}

// HOW MUCH, as opposed to WHICH DAYS: three contributions on one afternoon and one on another are two
// days, and the heatmap says 3 and 1 where the streak says nothing about amounts at all.
func TestTheHeatmapCountsContributionsPerDay(t *testing.T) {
	userID := createUser(t)
	contributedDaysAgo(t, userID, 0, 0, 0, 2) // three today, one two days ago

	got, err := newStreak().ContributionHeatmap(t.Context(), userID, 7)
	require.NoError(t, err)
	require.Len(t, got, 2, "two days with activity")

	assert.Equal(t, 3, got[0].Count, "newest first, and three contributions on one day is three")
	today, err := q().DatabaseNow(t.Context())
	require.NoError(t, err)
	assert.Equal(t, today.Format("2006-01-02"), got[0].Day.Format("2006-01-02"))
	assert.Equal(t, 1, got[1].Count)
	assert.Equal(t, today.AddDate(0, 0, -2).Format("2006-01-02"), got[1].Day.Format("2006-01-02"))

	// AND IT AGREES WITH THE STREAK ABOUT THE DAYS. The heatmap counts events where the streak counts
	// distinct days; if they ever disagreed about WHICH days, a member would see a grid that contradicts
	// the run rendered beside it.
	streak, err := newStreak().For(t.Context(), userID, today)
	require.NoError(t, err)
	assert.Equal(t, len(got), streak.TotalActiveDays,
		"every day in the heatmap is an active day and the other way round")

	// One day of the two is today, which the streak also reports.
	assert.True(t, streak.ActiveToday)
}

// A REJECTION IS A CONTRIBUTION. The member showed up and did the work; the outcome of the work is not
// the heatmap's business, and a grid that went blank on a member whose edits were all rejected would
// erase the days they spent.
func TestTheHeatmapCountsRejectedWorkAsContribution(t *testing.T) {
	userID := createUser(t)
	entityID := uuidNew(t)
	_, err := testutil.DB().Exec(t.Context(),
		`INSERT INTO trust_events (user_id, kind, entity_type, entity_id, delta, created_at)
		 VALUES ($1, 'edit_rejected', 'performer', $2, -1, $3)`,
		userID, entityID, todaysStamp(t))
	require.NoError(t, err, "backdating a rejection")

	got, err := newStreak().ContributionHeatmap(t.Context(), userID, 7)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, 1, got[0].Count, "a rejected edit is a day of work")
}

// THE WINDOW EXCLUDES OLDER DAYS, and includes today: an event 400 days back is outside a year, and one
// today is inside a one-day window.
func TestTheHeatmapWindow(t *testing.T) {
	userID := createUser(t)
	contributedDaysAgo(t, userID, 400, 1) // one long gone, one yesterday

	year, err := newStreak().ContributionHeatmap(t.Context(), userID, 365)
	require.NoError(t, err)
	require.Len(t, year, 1, "the 400-day-old day is outside a year")
	assert.Equal(t, 1, year[0].Count)

	today, err := newStreak().ContributionHeatmap(t.Context(), userID, 1)
	require.NoError(t, err)
	assert.Empty(t, today, "a one-day window is today alone, and yesterday is not today")

	contributedDaysAgo(t, userID, 0)
	today, err = newStreak().ContributionHeatmap(t.Context(), userID, 1)
	require.NoError(t, err)
	require.Len(t, today, 1, "and today is included rather than excluded by an off-by-one")
	assert.Equal(t, 1, today[0].Count)
}

// A member with no contributions gets an empty grid rather than an error: it is the ordinary state of
// every new account.
func TestANewMemberHasAnEmptyHeatmap(t *testing.T) {
	got, err := newStreak().ContributionHeatmap(t.Context(), createUser(t), 365)
	require.NoError(t, err)
	assert.Empty(t, got)
}

// todaysStamp is the database's current instant, for a fixture that has to land on its day.
func todaysStamp(t *testing.T) time.Time {
	t.Helper()
	now, err := q().DatabaseNow(t.Context())
	require.NoError(t, err)
	return now
}

func uuidNew(t *testing.T) uuid.UUID {
	t.Helper()
	return uuid.Must(uuid.NewV7())
}
