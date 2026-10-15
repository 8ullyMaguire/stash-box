//go:build integration

package seasonal_test

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
	"github.com/stashapp/stash-box/internal/service/seasonal"
)

// Seasonal events against a real database (plan phase 6 slice 6g).
//
// The pure tests next door prove the boundaries. These prove the COUNT: which contributions fall
// inside a window, and that the number a member reads is their own.
//
// TestMain brings up the test database.
func TestMain(m *testing.M) {
	testutil.TestWithDatabase(m, nil)
}

func q() *queries.Queries {
	return queries.New(testutil.DB())
}

func createUser(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	_, err := q().CreateUser(t.Context(), queries.CreateUserParams{
		ID:           id,
		Name:         "Seasonal User " + id.String(),
		PasswordHash: "not-a-real-hash-this-test-never-logs-in",
		Email:        id.String() + "@seasonal.invalid",
	})
	require.NoError(t, err, "creating a member")
	return id
}

// declare writes an event the way an operator does: a row, no migration and no CRUD surface.
func declare(t *testing.T, slug string, starts, ends time.Time, threshold int) {
	t.Helper()
	_, err := testutil.DB().Exec(t.Context(),
		`INSERT INTO seasonal_events (id, slug, title, description, starts_at, ends_at, threshold)
		 VALUES ($1, $2, $3, '', $4, $5, $6)`,
		uuid.Must(uuid.NewV7()), slug, "Event "+slug, starts, ends, threshold)
	require.NoError(t, err, "declaring an event")
}

// contributeAt writes one contribution at an exact instant, which is the only way to place a row
// on a boundary deliberately.
func contributeAt(t *testing.T, userID uuid.UUID, at time.Time) {
	t.Helper()
	entityID := uuid.Must(uuid.NewV7())
	_, err := testutil.DB().Exec(t.Context(),
		`INSERT INTO trust_events (user_id, kind, entity_type, entity_id, delta, created_at)
		 VALUES ($1, 'edit_approved', 'performer', $2, 1, $3)`,
		userID, entityID, at)
	require.NoError(t, err)
}

func forUser(t *testing.T, userID uuid.UUID) map[string]seasonal.Progress {
	t.Helper()
	out := map[string]seasonal.Progress{}
	progress, err := seasonal.NewService(q()).For(t.Context(), userID)
	require.NoError(t, err)
	for _, p := range progress {
		out[p.Event.Slug] = p
	}
	return out
}

// THE WINDOW IS THE FEATURE: a contribution before it and a contribution after it do not count,
// and one INSIDE it does -- including one at each boundary instant.
func TestOnlyContributionsInsideTheWindowCount(t *testing.T) {
	userID := createUser(t)
	starts := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	ends := starts.AddDate(0, 0, 7)
	declare(t, "boundary-"+uuid.Must(uuid.NewV7()).String()[:8], starts, ends, 10)

	// One of each of five positions: before, at the start, inside, at the end, after.
	contributeAt(t, userID, starts.Add(-time.Hour))
	contributeAt(t, userID, starts)
	contributeAt(t, userID, starts.AddDate(0, 0, 3))
	contributeAt(t, userID, ends)
	contributeAt(t, userID, ends.Add(time.Hour))

	// The service returns every declared event, so the assertion names the one it means.
	progress, err := seasonal.NewService(q()).For(t.Context(), userID)
	require.NoError(t, err)
	var mine *seasonal.Progress
	for i := range progress {
		if progress[i].Event.StartsAt.Equal(starts) {
			mine = &progress[i]
		}
	}
	require.NotNil(t, mine, "fixture: the declared event must be in the list")
	assert.Equal(t, 3, mine.Contributions,
		"the contribution at the opening instant and the one at the closing instant both count; the two outside do not")
	// The status is deliberately NOT asserted here. This window is a fixed January so that the
	// boundary instants are exact, which puts it in the past relative to the database's today --
	// and the first version of this test asserted "active" and read "ended", which was the fixture
	// being wrong rather than the status. Status is its own test, against relative windows.
}

// THE THRESHOLD IS INCLUSIVE: the tenth contribution reaches a threshold of ten.
func TestTheThresholdIsInclusive(t *testing.T) {
	starts := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	ends := starts.AddDate(0, 0, 30)
	slug := "threshold-" + uuid.Must(uuid.NewV7()).String()[:8]
	declare(t, slug, starts, ends, 3)

	short := createUser(t)
	contributeAt(t, short, starts.Add(time.Hour))
	contributeAt(t, short, starts.Add(2*time.Hour))
	assert.False(t, forUser(t, short)[slug].Reached, "two is not three")

	exact := createUser(t)
	for i := range 3 {
		contributeAt(t, exact, starts.Add(time.Duration(i+1)*time.Hour))
	}
	reached := forUser(t, exact)[slug]
	assert.Equal(t, 3, reached.Contributions)
	assert.True(t, reached.Reached, "three IS three: the threshold is a minimum, not an exclusive bound")
}

// THE PROGRESS IS THE CALLER'S OWN. Two members get two different numbers from the same event,
// which is what makes this surface private without a privacy rule being written for it.
func TestProgressIsPerMember(t *testing.T) {
	starts := time.Now().AddDate(0, 0, -1)
	ends := time.Now().AddDate(0, 0, 1)
	slug := "per-member-" + uuid.Must(uuid.NewV7()).String()[:8]
	declare(t, slug, starts, ends, 5)

	busy := createUser(t)
	for i := range 4 {
		contributeAt(t, busy, starts.Add(time.Duration(i+1)*time.Minute))
	}
	idle := createUser(t)

	assert.Equal(t, 4, forUser(t, busy)[slug].Contributions)
	assert.Equal(t, 0, forUser(t, idle)[slug].Contributions, "a member who did nothing sees zero, not somebody else's four")
	assert.False(t, forUser(t, idle)[slug].Reached)
}

// AN EVENT WITH NO CONTRIBUTIONS AT ALL IS ZERO AND NOT AN ERROR: the ordinary state of every member
// on the first day of an event.
func TestAnEventNobodyHasContributedToIsZero(t *testing.T) {
	slug := "quiet-" + uuid.Must(uuid.NewV7()).String()[:8]
	declare(t, slug, time.Now().AddDate(0, 0, -1), time.Now().AddDate(0, 0, 1), 1)

	got := forUser(t, createUser(t))[slug]
	assert.Equal(t, 0, got.Contributions)
	assert.False(t, got.Reached)
}

// A WINDOW THAT HAS NOT OPENED IS UPCOMING, and one that has closed is ENDED -- decided against the
// DATABASE's clock, which is where every other window in this codebase is decided.
func TestStatusComesFromTheDatabaseClock(t *testing.T) {
	future := "future-" + uuid.Must(uuid.NewV7()).String()[:8]
	declare(t, future, time.Now().AddDate(0, 0, 30), time.Now().AddDate(0, 0, 60), 1)
	past := "past-" + uuid.Must(uuid.NewV7()).String()[:8]
	declare(t, past, time.Now().AddDate(0, 0, -60), time.Now().AddDate(0, 0, -30), 1)

	got := forUser(t, createUser(t))
	assert.Equal(t, seasonal.StatusUpcoming, got[future].Status)
	assert.Equal(t, seasonal.StatusEnded, got[past].Status)
}

// THE SLUG IS THE NAME A CALLER CAN HOLD, and an unknown one is refused rather than answered with a
// zero-valued event.
func TestEventBySlug(t *testing.T) {
	starts := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	slug := "by-slug-" + uuid.Must(uuid.NewV7()).String()[:8]
	declare(t, slug, starts, starts.AddDate(0, 0, 14), 7)

	got, err := seasonal.NewService(q()).Event(t.Context(), slug)
	require.NoError(t, err)
	assert.Equal(t, 7, got.Threshold)
	assert.True(t, got.StartsAt.Equal(starts))

	_, err = seasonal.NewService(q()).Event(t.Context(), "no-such-event-"+uuid.Must(uuid.NewV7()).String()[:8])
	assert.ErrorIs(t, err, seasonal.ErrNoSuchEvent)
}

var _ = pgx.ErrNoRows
var _ = context.Background
