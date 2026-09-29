package streak

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The two pure functions are tested directly, with DATES CHOSEN BY HAND and `now`
// passed in. No test here reads the wall clock: the entire logic of this package
// is a comparison against "today", so a test that used time.Now() would have an
// answer that changes at midnight and a failure that only reproduces before 09:00.
//
// The reference date is the 15th of a month in a non-DST-transitional window, and
// the location is fixed to UTC so nothing about the host machine can move a day.

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func svc() *Service { return &Service{} }

// days builds the newest-first day list the query returns.
func days(dates ...time.Time) []time.Time {
	return dates
}

// THE RULE. A streak survives being idle for today only.
//
// These are the four cases that decide whether the feature feels right or feels
// broken, and they are the reason `now` is a parameter: the difference between
// "live" and "dead" is one calendar day, and a test that could not name `now`
// could not tell them apart.
func TestAStreakSurvivesTodayButNotYesterday(t *testing.T) {
	now := date(2026, time.September, 15)

	cases := []struct {
		name string
		// last is the most recent active day.
		last time.Time
		want int
	}{
		{"active today", date(2026, time.September, 15), 3},
		{"active yesterday, not yet today", date(2026, time.September, 14), 3},
		{"last active two days ago", date(2026, time.September, 13), 0},
		{"last active a month ago", date(2026, time.August, 15), 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A three-day run ending on `last`: last-1, last-2.
			got := svc().currentStreak(days(
				tc.last, tc.last.AddDate(0, 0, -1), tc.last.AddDate(0, 0, -2),
			), now)

			assert.Equal(t, tc.want, got,
				"a streak is live on today or yesterday and broken from two days "+
					"back. Using only days[0]==today would reset it at midnight, "+
					"which is the most common way a streak feature feels punitive")
		})
	}
}

// A single day today is a 1-day streak, and a single day long ago is nothing.
func TestASingleActiveDayIsAOneDayStreakOnlyIfRecent(t *testing.T) {
	now := date(2026, time.September, 15)
	s := svc()

	assert.Equal(t, 1, s.currentStreak(days(date(2026, time.September, 15)), now))
	assert.Equal(t, 1, s.currentStreak(days(date(2026, time.September, 14)), now),
		"a lone day yesterday is still a live 1-day streak; it only dies at two days")
	assert.Equal(t, 0, s.currentStreak(days(date(2026, time.September, 13)), now))
	assert.Equal(t, 0, s.currentStreak(days(date(2026, time.July, 1)), now),
		"one day of activity in July is not a current streak in September")
}

// A gap in the middle ends the run, and the days BEFORE the gap are still the
// longest run.
func TestAGapEndsTheRunAndLongestFindsTheOtherRun(t *testing.T) {
	now := date(2026, time.September, 15)
	// Newest first: a 2-day run ending today, a gap, then a 5-day run in July.
	history := days(
		date(2026, time.September, 15),
		date(2026, time.September, 14),
		// 13 and 12 missing
		date(2026, time.July, 10),
		date(2026, time.July, 9),
		date(2026, time.July, 8),
		date(2026, time.July, 7),
		date(2026, time.July, 6),
	)
	s := svc()

	assert.Equal(t, 2, s.currentStreak(history, now),
		"the current run stops at the gap; the July run is history, not current")
	assert.Equal(t, 5, s.longestStreak(history),
		"the longest run is the one that is over, which is exactly why Longest "+
			"is stored alongside Current rather than replacing it")
	assert.Equal(t, 7, len(history))
}

// Every day, back a long way: the run must not stop at a month boundary or a
// year boundary, which is what an off-by-one in the walk would produce.
func TestALongRunCrossesMonthAndYearBoundaries(t *testing.T) {
	now := date(2026, time.September, 15)

	// 40 consecutive days ending today, built by SUBTRACTION rather than listed --
	// so the test states the invariant ("40 consecutive days") instead of 40 dates
	// that could each be wrong.
	history := make([]time.Time, 0, 40)
	for i := 0; i < 40; i++ {
		history = append(history, now.AddDate(0, 0, -i))
	}
	s := svc()

	assert.Equal(t, 40, s.currentStreak(history, now))
	assert.Equal(t, 40, s.longestStreak(history),
		"a run crossing Aug 31 -> Sep 1 and a year boundary is still one run. A "+
			"walk that compared months or truncated to a day-of-month would break "+
			"here and report a much smaller number")
}

// LongestStreak walks the list NEWEST-FIRST, and the direction is the one thing
// in it that can be silently wrong: comparing each day to the wrong neighbour
// still gives the right answer for a symmetric history and the wrong one as soon
// as the runs differ in length.
func TestLongestStreakIsCorrectWhenTheOlderRunIsLonger(t *testing.T) {
	// A 1-day run today, then a 4-day run last month. Walking in the wrong
	// direction would report 1 or 2 instead of 4.
	history := days(
		date(2026, time.September, 15),
		date(2026, time.August, 20),
		date(2026, time.August, 19),
		date(2026, time.August, 18),
		date(2026, time.August, 17),
	)
	assert.Equal(t, 4, svc().longestStreak(history))
}

// One day is one day regardless of how many events it held, and that is enforced
// in SQL, not here -- but the consequence is asserted here because it is the
// difference between a streak and a score.
func TestManyEventsInOneDayAreOneDay(t *testing.T) {
	now := date(2026, time.September, 15)
	// The query already collapsed the day; the service must not re-expand it.
	single := days(now, now.AddDate(0, 0, -1))
	assert.Equal(t, 2, svc().currentStreak(single, now))
	require.Len(t, single, 2)
}

// A DST transition must not break a streak, and this is the case an
// elapsed-hours implementation fails twice a year.
func TestAStreakSurvivesADaylightSavingTransition(t *testing.T) {
	// Europe/Madrid springs forward on 2026-03-29, so that local day is 23 hours
	// long and 2026-10-25 is 25. Both are real days and both are in a streak.
	madrid, err := time.LoadLocation("Europe/Madrid")
	require.NoError(t, err)

	// Three consecutive local days, one of which is the 23-hour day.
	history := []time.Time{
		time.Date(2026, 3, 31, 12, 0, 0, 0, madrid),
		time.Date(2026, 3, 30, 12, 0, 0, 0, madrid),
		time.Date(2026, 3, 29, 12, 0, 0, 0, madrid), // 23 hours long
	}
	now := time.Date(2026, 3, 31, 23, 30, 0, 0, madrid)

	assert.Equal(t, 3, svc().currentStreak(history, now),
		"a 23-hour day is still a day. Dividing elapsed hours by 24 calls it 0 "+
			"days and erases the streak for every user in a DST timezone twice a "+
			"year, which is the kind of bug that only appears in production")
}

// An EMPTY history is a zero streak, not a panic and not an error.
func TestAnEmptyHistoryIsAZeroStreak(t *testing.T) {
	now := date(2026, time.September, 15)
	s := svc()

	assert.Equal(t, 0, s.currentStreak(nil, now))
	assert.Equal(t, 0, s.longestStreak(nil))
}

// truncateDay must keep the LOCATION, or "today" and the event days are compared
// in different frames and a midnight contribution lands on the wrong day.
func TestTruncateDayKeepsTheLocation(t *testing.T) {
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	require.NoError(t, err)

	got := truncateDay(time.Date(2026, 9, 15, 23, 59, 59, 999, tokyo))

	assert.Equal(t, tokyo, got.Location(),
		"a truncated day that moved to UTC is a different day for a user east of "+
			"Greenwich: 09:00 in Tokyo is the previous day in UTC")
	assert.Equal(t, 0, got.Hour())
	assert.Equal(t, 15, got.Day())
}

// sameDay compares the CALENDAR date, not the instant, and the two differ for any
// pair of times on the same day.
func TestSameDayComparesCalendarDatesNotInstants(t *testing.T) {
	early := date(2026, 9, 15)
	late := date(2026, 9, 15).Add(23 * time.Hour)

	assert.True(t, sameDay(early, late),
		"23:59 and 00:01 on the same day are the same DAY, which is the whole "+
			"point of comparing dates instead of subtracting instants")
	assert.False(t, sameDay(date(2026, 9, 15), date(2026, 9, 16)))
}
