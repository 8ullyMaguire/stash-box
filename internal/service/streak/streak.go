// Package streak derives a user's activity days and current streak.
//
// Nothing here is stored, for the same reason badges are not stored: a stored
// streak can disagree with the log that justifies it, and nothing reports the
// disagreement. But the reason is STRONGER for a streak, and worth stating
// separately, because a counter does not decay on its own and a streak must.
//
// A badge is a fact about the past that stays true. A streak is a claim about the
// PRESENT -- "this person has contributed on each of the last N days" -- and it is
// false the moment they do not. Stored, that decay has to be written: a nightly job,
// or a decrementing read, or an expiry timestamp, each of which is a place for the
// number to be wrong. The worst version is a streak that survives until a job runs,
// so a user who quit a month ago is still shown a 40-day streak today, and the
// decay is the visible part of the feature.
//
// Derived, "current streak" is a FUNCTION of today and the log. Yesterday's answer
// is a different answer, computed the same way, and there is no state to be stale.
package streak

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/queries"
)

// Streak is a user's activity summary.
//
// The two numbers answer different questions and only one of them decays, which is
// the whole reason this is a struct rather than a single int: "active 300 days"
// is a lifetime fact and "40-day streak" is a statement about this week, and
// showing the streak alone hides a user who was active for a year and has not
// returned.
type Streak struct {
	// CurrentStreak is consecutive active days ending today or yesterday.
	CurrentStreak int
	// LongestStreak is the longest run of consecutive days ever recorded. Derived
	// on every read, which is a full scan of the history, and honestly the price
	// of not storing it -- see LongestStreak's own note.
	LongestStreak int
	// TotalActiveDays is every distinct day with any contribution, ever. Does not
	// decay.
	TotalActiveDays int
	// ActiveToday says whether today is an active day, which is NOT the same as
	// CurrentStreak > 0. Reported separately because a user whose streak is alive
	// on yesterday's activity has not contributed today, and a UI that shows a
	// live streak next to "today" needs to tell those two states apart to avoid
	// implying a day that has not happened yet.
	ActiveToday bool
	// LastActiveDay is the most recent day with a contribution, or nil if never.
	// Exposed because "your last contribution was 14 days ago" is the honest
	// version of a reset, and a user who has lost a streak deserves to be told
	// when it ended rather than just watching it go to zero.
	LastActiveDay *time.Time
}

// Service computes streaks from the event log.
type Service struct {
	queries *queries.Queries
}

// NewService builds a streak service.
func NewService(q *queries.Queries) *Service {
	return &Service{queries: q}
}

// For computes a user's streak.
//
// `now` is a parameter rather than a call to time.Now() inside, and the reason is
// that the boundary -- "is today active" -- is the entire logic of this package. A
// clock read inside the function cannot be pinned, so the one case most worth
// testing is untestable without waiting until midnight. Passed in, that case is a
// struct literal.
//
// The caller's clock is used, and it is the SERVER's clock, not the user's. A
// streak is an instance-level fact about participation; if each user measured it
// against their own timezone, two users active at the same moment would show
// different streaks, and the leaderboard would be comparing two different units.
// The query truncates to the session timezone for the same reason -- they must
// agree -- and both come from the database session, so they cannot drift apart.
func (s *Service) For(ctx context.Context, userID uuid.UUID, now time.Time) (Streak, error) {
	rows, err := s.queries.ListUserActivityDays(ctx, userID)
	if err != nil {
		return Streak{}, err
	}

	// No events: a user who has never contributed has no streak, no last day, and
	// zero total days. Returning a zero Streak is the right answer, NOT an error --
	// a brand-new user asking about their streak is the most ordinary request
	// there is, and an error page for it is a bug.
	if len(rows) == 0 {
		return Streak{}, nil
	}

	// Days arrive newest-first from the query, which is what the walk below wants:
	// it stops at the first gap, so it never has to look past the break. A
	// longest-streak scan still needs all of them, and they are already loaded.
	days := make([]time.Time, 0, len(rows))
	for _, r := range rows {
		// No null check: the column is NOT NULL and the value is a truncated
		// timestamp, so the sqlc type is time.Time rather than a pgtype wrapper
		// with a Valid flag. The wrapper only existed while the query ended in
		// ::timestamptz.
		days = append(days, r)
	}
	if len(days) == 0 {
		return Streak{}, nil
	}

	today := truncateDay(now)

	out := Streak{
		TotalActiveDays: len(days),
		ActiveToday:     sameDay(days[0], today),
		LastActiveDay:   &days[0],
	}
	out.CurrentStreak = s.currentStreak(days, today)
	out.LongestStreak = s.longestStreak(days)
	return out, nil
}

// currentStreak counts consecutive active days back from today.
//
// THE RULE, and the part that is easy to get wrong: a streak survives being idle
// for TODAY only, and only for today. If the most recent active day is today or
// yesterday the streak is live, because a user's day is not over until it is over
// and at 09:00 yesterday's contributor has not yet broken anything. From two days
// back, it is broken -- there is a whole day in between with nothing in it, and
// pretending otherwise is how a "streak" becomes a count of total days.
//
// The alternative, using only `days[0] == today`, is defensible and wrong: it
// resets every streak at midnight, so a user who contributed yesterday and has not
// yet contributed today is told they have no streak at 09:00. That reads as
// punitive and it is the single most common way a streak feature feels broken.
func (s *Service) currentStreak(days []time.Time, today time.Time) int {
	// An empty history is a zero streak, not a panic. Reachable: `For` already
	// returns early for it, but this function is also called directly by the
	// tests, and a helper that panics on empty input is a helper whose contract
	// has to be remembered by every future caller. Cheaper to hold the invariant
	// than to document it.
	if len(days) == 0 {
		return 0
	}

	// Start from the most recent active day, and walk back. The gap check below
	// catches the case where the most recent day is too old, so there is no
	// separate "is it recent enough" test here.
	// THE RULE: a streak survives being idle for TODAY only. If the most recent
	// active day is today or yesterday the streak is live -- a user's day is not
	// over until it is over, and at 09:00 yesterday's contributor has not broken
	// anything. From two days back there is a whole day in between with nothing in
	// it, and the streak is zero.
	//
	// This check is the whole reason a streak is not just "count back from
	// days[0]": without it a user who last contributed in January reads as a
	// 1-day streak rather than none, which is worse than a wrong number because it
	// looks alive.
	//
	// Calendar comparison rather than a duration divided by 24. A DST transition
	// makes a "one day" gap 23 or 25 hours, so an elapsed-hours test is 0 days on
	// the short side and still fails the check on the long one -- and it fails it
	// in the direction that erases a real streak for every user in a timezone that
	// observes DST, twice a year. Two sameDay tests have no arithmetic to get
	// wrong.
	if !sameDay(days[0], today) && !sameDay(days[0], today.AddDate(0, 0, -1)) {
		return 0
	}

	streak := 0
	// expected is the day the current run must include, walked backwards. Starting
	// at days[0] rather than at today is what makes the yesterday-grace work: the
	// walk begins where the user last was and asks whether the path back is
	// unbroken, rather than demanding they be active today.
	expected := days[0]

	for _, d := range days {
		// A gap: this day is not the day the walk expected, so the run is over.
		// One inequality, not two -- the list is sorted newest-first, so a day that
		// is not the one we were looking for means the run broke, whichever side
		// of the gap it fell on.
		//
		// sameDay and Equal are INTERCHANGEABLE here and mutation testing proved
		// it: both operands are midnight-truncated (the query's date_trunc, and
		// `expected` built by AddDate from one of those), so there is no
		// sub-day component for Equal to disagree about. sameDay is kept because
		// the comparison is ABOUT a day, and because it keeps holding if a caller
		// ever passes an untruncated `now` -- which is exactly the mistake the
		// argument's own comment warns about.
		if !sameDay(d, expected) {
			break
		}
		streak++
		expected = d.AddDate(0, 0, -1)
	}
	return streak
}

// longestStreak is the longest run of consecutive days in the whole history.
//
// Computed on every read over every day the user has ever been active. That is a
// full scan, and for a user with three years of daily activity it is a thousand
// rows to answer a number that barely changes. The alternative is to store it, and
// a stored longest-streak is monotonic -- it can only grow -- which makes it one of
// the few counters that genuinely is safe to cache, because there is no way for it
// to become WRONG. Only a deletion of history (a GDPR erasure, a reversal that
// removes the day) can lower it, and that is rare enough to justify a recompute
// path rather than a cache-invalidation scheme.
//
// So this is left computed, and if the scan ever shows up in a profile page's
// latency the fix is a column with a recompute on erasure -- NOT a denormalised
// field updated on every event, which reintroduces exactly the drift this package
// exists to avoid.
func (s *Service) longestStreak(days []time.Time) int {
	longest, run := 0, 0
	// The list is newest-first, so the previous day in the walk is the NEXT
	// element. Walking the reversed order instead would be clearer, and the
	// direction is the one thing worth being careful about here: comparing each
	// day to days[i-1] while iterating downwards measures the gap backwards, which
	// for a "consecutive" test is the same answer but is not obvious to a reader.
	for i, d := range days {
		if i == 0 {
			run = 1
		} else {
			prev := days[i-1]
			// The list is NEWEST-first, so `d` is the OLDER day: it is one day
			// BEFORE prev, not one day after. Writing +1 here compiles, returns 1
			// for every entry, and fails every multi-day case in the suite while
			// passing every single-day one -- which is exactly the signature that
			// made it look like the walk was at fault.
			// sameDay rather than Equal, for the same reason as the current-streak
			// walk above, and here it is not even equivalent: a `prev` carrying any
			// time component would make AddDate land at that same time on the
			// previous day, and Equal would still match -- but only by luck of the
			// offsets lining up. A date comparison states what is being asked.
			if sameDay(d, prev.AddDate(0, 0, -1)) {
				run++
			} else {
				run = 1
			}
		}
		if run > longest {
			longest = run
		}
	}
	return longest
}

// truncateDay drops the time, keeping the date.
//
// Done by CONSTRUCTION rather than by formatting to a string and parsing back:
// a string round-trip goes through a format that has to be chosen, and choosing it
// is how a UTC timestamp ends up on the wrong day. Truncating in the same location
// as the input keeps "today" and the event days in one frame of reference, which is
// the property the whole package depends on.
func truncateDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// sameDay compares two times by calendar date, not by instant.
func sameDay(a, b time.Time) bool {
	return a.Year() == b.Year() && a.YearDay() == b.YearDay()
}

// ForCurrent computes the streak against the DATABASE's clock.
//
// It exists so callers do not each have to remember to fetch the right "now".
// Two of them already got it wrong: time.Now() on the Go side reports the HOST's
// calendar, and events are stamped by PostgreSQL, so a host whose clock is a
// minute fast makes "active today" false for an event that landed two seconds
// ago. That failure reads as a streak bug rather than a clock bug, which is why
// the clock read belongs in one place.
//
// For() stays the injectable form and is what the tests drive; this is the thin
// production wrapper over it, not a second implementation.
func (s *Service) ForCurrent(ctx context.Context, userID uuid.UUID) (Streak, error) {
	now, err := s.queries.DatabaseNow(ctx)
	if err != nil {
		return Streak{}, err
	}
	return s.For(ctx, userID, now)
}
