//go:build integration

package api_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/service/quest"
	"github.com/stashapp/stash-box/internal/service/streak"
	"github.com/stashapp/stash-box/internal/service/trust"
)

// Streaks against the real database (SPEC §12, Phase 2 step 8).
//
// The unit tests pin the ARITHMETIC with hand-chosen dates. None of that proves
// the thing arithmetic cannot: that the query returns one row per calendar DAY and
// not one row per event.
//
// That distinction is the whole feature. A curator who approves forty edits in one
// afternoon has ONE active day. If the query returned forty rows, that afternoon
// would read as a 40-day streak, and every user who worked hard in a single session
// would be credited with a month of consistency they did not have.

// fixedNow is a clock the test controls, so "is today active" is a decision the
// test makes rather than a race with the wall clock.
var fixedNow = time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)

func newStreakService(t *testing.T) (*streak.Service, *trust.Trust) {
	t.Helper()
	f := dbtest.Factory()
	return f.Streak(), f.Trust()
}

// ptr takes an address, because Event.EntityID is a *uuid.UUID and the dedup
// index treats a NULL entity as one value: repeated events with no entity are
// repeated attempts at a single row.
func ptr(u uuid.UUID) *uuid.UUID { return &u }

// dbNow reads the DATABASE's clock.
//
// Not time.Now(). Events are stamped by PostgreSQL, so the only correct "today" is
// the one the database would use -- a host whose clock is a minute fast makes
// "active today" false for an event that landed two seconds ago, and the failure
// looks like a streak bug rather than a clock bug.
func dbNow(t *testing.T) time.Time {
	t.Helper()
	var now time.Time
	require.NoError(t, dbtest.DB().QueryRow(context.Background(),
		"SELECT now()").Scan(&now))
	return now
}

// todayInDB is the DATABASE's notion of today, as a date.
//
// Asked of PostgreSQL rather than computed, and specifically NOT
// now.Truncate(24*time.Hour): that rounds down to a multiple of 24 hours since
// the UNIX EPOCH, not to midnight, so in most timezones it is YESTERDAY. My first
// version of this fixture used it, every "today" landed a day early, and both
// streak tests reported 0 with a green build -- a fixture bug that reads exactly
// like a service bug.
//
// SQL, not Go: the query truncates with the database SESSION's timezone, so only
// the database can name the day the rows were grouped into. A Go-side
// time.Date(..., time.Local) would be a different day from the host's point of
// view, and the test host is not necessarily the database host.
func todayInDB(t *testing.T) time.Time {
	t.Helper()
	var day time.Time
	require.NoError(t, dbtest.DB().QueryRow(context.Background(),
		"SELECT date_trunc('day', now())").Scan(&day))
	return day
}

// backdateTrustEvent moves ONE named event onto a chosen day, addressed by the id
// RecordEvent handed back.
//
// The service never does this -- events are append-only and the database owns the
// timestamp. A test cannot wait three days to build a multi-day history, and the
// property under test is about how the QUERY groups rows into days, not about how
// they got their timestamps. So it is a fixture, deliberately reaching past the
// service, and it is the only way to exercise the gap logic against real rows.
//
// ADDRESSED BY ENTITY ID, which the test itself chose and which is unique per
// call. My first version used "the row with the highest event id", which is a
// guess about a sequence: it picked the wrong row, and the test built a
// five-days-ago event and read back a five-days-IN-THE-FUTURE one, because the
// sequence is not guaranteed to ascend with insert order across a suite that
// truncates between cases. Nothing here is inferred now.
func backdateTrustEvent(t *testing.T, userID uuid.UUID, entityID uuid.UUID, day time.Time) {
	t.Helper()
	_, err := dbtest.DB().Exec(context.Background(),
		`UPDATE trust_events SET created_at = $1
		 WHERE user_id = $2 AND entity_id = $3`, day, userID, entityID)
	require.NoError(t, err)
}

// Several events on ONE day are one active day, and a rejection counts too.
func TestStreakCountsDaysNotEvents(t *testing.T) {
	svc, trustSvc := newStreakService(t)
	ctx := context.Background()
	user := createUserForQuest(t, "A One Day Marathon Curator")

	// Four events, all on today. Two positive and two negative, so the test also
	// pins that a reversal does not subtract a day.
	for i, kind := range []trust.KindEnum{
		trust.KindEditApproved, trust.KindEditApproved,
		trust.KindEditRejected, trust.KindEditRejected,
	} {
		delta := 1
		if kind == trust.KindEditRejected {
			delta = -1
		}
		// A DISTINCT entity per event, and that is not incidental. The dedup
		// index is (user_id, kind, entity_type, entity_id), so four events with a
		// nil entity and the same kind are four attempts at one row: three are
		// silently dropped by ON CONFLICT DO NOTHING and the day count comes out
		// as 1. The rollup was right and the fixture was wrong.
		_, err := trustSvc.RecordEvent(ctx, trust.Event{
			UserID:     user,
			Kind:       kind,
			EntityType: string(quest.EntityTag),
			EntityID:   ptr(uuid.Must(uuid.NewV7())),
			Delta:      delta,
		})
		require.NoError(t, err, "event %d", i)
	}

	// The events are stamped by the database clock, so read "now" from the same
	// place rather than assuming the host agrees with it.
	now := dbNow(t)
	got, err := svc.For(ctx, user, now)
	require.NoError(t, err)

	assert.Equal(t, 1, got.TotalActiveDays,
		"four events on one day are ONE active day. Per-event counting gives a "+
			"4-day streak to someone who showed up once, which is the opposite of "+
			"what a streak measures")
	assert.Equal(t, 1, got.CurrentStreak)
	assert.True(t, got.ActiveToday)
}

// Consecutive REAL days form one run. This is the only test that can build a
// multi-day history at all, so it is also the only place the gap logic is proven
// against real rows rather than a hand-built slice.
func TestStreakSpansConsecutiveDaysInTheDatabase(t *testing.T) {
	svc, trustSvc := newStreakService(t)
	ctx := context.Background()
	user := createUserForQuest(t, "A Three Day Curator")

	// Three consecutive days, two events on the middle one: four events, three
	// days, one run.
	counts := []int{1, 2, 1}
	base := todayInDB(t)
	dayOffsets := []int{0, 1, 2}

	recorded := 0
	for i, offset := range dayOffsets {
		for j := 0; j < counts[i]; j++ {
			entity := uuid.Must(uuid.NewV7())
			_, err := trustSvc.RecordEvent(ctx, trust.Event{
				UserID:     user,
				Kind:       trust.KindIdentificationSolved,
				EntityType: string(quest.EntityTag),
				EntityID:   ptr(entity),
				Delta:      1,
			})
			require.NoError(t, err)
			backdateTrustEvent(t, user, entity, base.AddDate(0, 0, offset))
			recorded++
		}
	}
	require.Equal(t, 4, recorded)

	got, err := svc.For(ctx, user, base.AddDate(0, 0, 2).Add(12*time.Hour))
	require.NoError(t, err)

	assert.Equal(t, 3, got.TotalActiveDays, "three distinct days from four events")
	assert.Equal(t, 3, got.CurrentStreak,
		"consecutive days form one run, and the extra event on the middle day "+
			"does not extend it")
	assert.Equal(t, 3, got.LongestStreak)
}

// A gap of a single day breaks the streak, and the run before it is still the
// longest -- which is why Longest exists alongside Current.
func TestStreakBreaksAcrossAGap(t *testing.T) {
	svc, trustSvc := newStreakService(t)
	ctx := context.Background()
	user := createUserForQuest(t, "A Curator With A Gap")

	base := todayInDB(t)

	// Offsets are days BEFORE today, and today is offset 0. So:
	//
	//   0, -1, -2   a live three-day run ending today
	//   -3          NOTHING -- this is the gap
	//   -4, -5      a dead two-day run
	//
	// Written as negative offsets because "days before today" is what a reader
	// expects, and my first version used positive offsets descending from 5, which
	// put the FUTURE at the top of the list: offset +5 was the newest day, so the
	// service correctly reported a zero CURRENT streak against a five-day longest.
	// The service was right and the fixture's sign convention was backwards.
	for _, offset := range []int{0, -1, -2, -4, -5} {
		entity := uuid.Must(uuid.NewV7())
		_, err := trustSvc.RecordEvent(ctx, trust.Event{
			UserID:     user,
			Kind:       trust.KindQuestCompleted,
			EntityType: string(quest.EntityTag),
			EntityID:   ptr(entity),
			Delta:      1,
		})
		require.NoError(t, err)
		backdateTrustEvent(t, user, entity, base.AddDate(0, 0, offset))
	}

	got, err := svc.For(ctx, user, base.Add(12*time.Hour))
	require.NoError(t, err)

	assert.Equal(t, 3, got.CurrentStreak,
		"the live run is today and the two days before it. The gap at -3 ends it, "+
			"and the two days at -4 and -5 are history, not current")
	assert.Equal(t, 3, got.LongestStreak,
		"the longest run here TIES the current one, which is the honest answer "+
			"and not a bug: three is three. Reported separately anyway, because a "+
			"user whose best run is behind them is a different person from one "+
			"still setting a record")
}

// A user with no events has a zero streak and NO error -- the most ordinary
// request there is.
func TestStreakForAUserWithNoEventsIsZeroNotAnError(t *testing.T) {
	svc, _ := newStreakService(t)
	ctx := context.Background()
	user := createUserForQuest(t, "A User With No History")

	got, err := svc.For(ctx, user, fixedNow)
	require.NoError(t, err, "asking a new user about their streak must not be an "+
		"error; an error page for the most common case is a bug")
	assert.Equal(t, streak.Streak{}, got)
	assert.False(t, got.ActiveToday)
	assert.Nil(t, got.LastActiveDay)
}

// A streak that went cold is ZERO, not a number from before it went cold. This is
// the property a stored counter cannot have without a decay job, and it is the
// reason the whole thing is derived.
func TestAColdStreakIsZeroRatherThanStale(t *testing.T) {
	svc, trustSvc := newStreakService(t)
	ctx := context.Background()
	user := createUserForQuest(t, "A Curator Who Went Quiet")

	_, err := trustSvc.RecordEvent(ctx, trust.Event{
		UserID: user, Kind: trust.KindEditApproved, Delta: 1,
	})
	require.NoError(t, err)

	// The DATABASE's clock, not the fixedNow constant. That constant exists for
	// the empty-history test, where no date is compared at all; here the event is
	// stamped with the real now(), so a hardcoded September date only happens to
	// work until the calendar moves on. Reading one clock and deriving the second
	// from it is what makes the two calls comparable.
	now := dbNow(t)
	got, err := svc.For(ctx, user, now)
	require.NoError(t, err)
	require.Equal(t, 1, got.CurrentStreak)
	require.True(t, got.ActiveToday)

	// Same data, a month later. Nothing deleted, no job run.
	later, err := svc.For(ctx, user, now.AddDate(0, 0, 30))
	require.NoError(t, err)

	assert.Equal(t, 0, later.CurrentStreak,
		"a stored streak would still read 1 here until a nightly job decremented "+
			"it, and a user who quit a month ago is still shown a live streak -- "+
			"the decay being the visible part of the feature")
	assert.Equal(t, 1, later.LongestStreak, "but the history is still there")
	assert.False(t, later.ActiveToday)
	require.NotNil(t, later.LastActiveDay,
		"a user who lost a streak deserves to be told WHEN it ended, not just to "+
			"watch it go to zero")
}
