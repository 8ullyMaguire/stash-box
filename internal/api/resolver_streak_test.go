//go:build integration

package api_test

import (
	"context"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service/quest"
	"github.com/stashapp/stash-box/internal/service/trust"
)

type streakOutput struct {
	CurrentStreak   int     `json:"currentStreak"`
	LongestStreak   int     `json:"longestStreak"`
	TotalActiveDays int     `json:"totalActiveDays"`
	ActiveToday     bool    `json:"activeToday"`
	LastActiveDay   *string `json:"lastActiveDay"`
}

func (c *graphqlClient) userStreak() (*streakOutput, error) {
	q := `query UserStreak { userStreak {
		currentStreak
		longestStreak
		totalActiveDays
		activeToday
		lastActiveDay
	} }`

	var resp struct {
		UserStreak streakOutput `json:"userStreak"`
	}
	if err := c.Post(q, &resp); err != nil {
		return nil, err
	}
	return &resp.UserStreak, nil
}

// recordStreakEvent writes one trust event, which is what makes a day active.
//
// It goes through the trust service rather than inserting a row directly, so the
// dedup index sees the same thing production does. That matters: the index is
// (user_id, kind, entity_type, entity_id) with ON CONFLICT DO NOTHING, so two
// events of the same kind with a nil entity are ONE row and the day count comes
// out wrong. The existing service test hit exactly that and the symptom was a
// correct rollup reporting a broken fixture.
func recordStreakEvent(t *testing.T, userID uuid.UUID, kind trust.KindEnum) {
	t.Helper()
	_, err := dbtest.Factory().Trust().RecordEvent(context.Background(), trust.Event{
		UserID:     userID,
		Kind:       kind,
		EntityType: string(quest.EntityTag),
		EntityID:   ptr(uuid.Must(uuid.NewV7())),
		Delta:      1,
	})
	require.NoError(t, err)
}

// The streak arithmetic is already covered against the SERVICE in
// streak_integration_test.go. What was missing, and what this file covers, is
// the layer above it: the schema type, the resolver binding, and the generated
// interface. A derived, mutation-tested service with no GraphQL binding is
// invisible to every client, and not one of the service tests would notice that.
//
// So every assertion here goes through the real GraphQL client. A test calling
// the resolver directly would pass with the schema type missing, the @hasRole
// directive absent, or the field never generated -- exactly the three things
// that were missing.

// A user with no contribution history is the ordinary first-run state, and it is
// the first request a brand-new account makes. An error here would be the bug.
//
// This test creates its OWN user rather than using asAdmin, and the reason is
// worth recording: asAdmin is a single shared admin account that every test in
// the package drives. Other tests approve edits as it, which writes trust_events
// for it, so by the time this test runs the admin has a real streak and expects
// 0. That is a shared-fixture problem, not a bug in the resolver -- but it fails
// only in the full suite, because running this file alone gives the admin no
// history. A test that passes alone and fails in the suite is a fixture bug, and
// the suite is where it is found.
func TestUserStreakResolvesZeroForANewUser(t *testing.T) {
	fresh, err := asAdmin(t).createTestUser(nil, []models.RoleEnum{models.RoleEnumRead})
	require.NoError(t, err)

	runner := createTestRunner(t, fresh, []models.RoleEnum{models.RoleEnumRead})

	got, err := runner.client.userStreak()
	require.NoError(t, err, "a brand-new user asking for their streak must not error")

	assert.Equal(t, 0, got.CurrentStreak)
	assert.False(t, got.ActiveToday)
	assert.Equal(t, 0, got.TotalActiveDays)
	assert.Nil(t, got.LastActiveDay,
		"a user who never contributed has no last day -- not today, not the epoch")
}

// The whole point of the feature: a user who curates sees it reflected. Without
// this, the resolver could return a hardcoded zero forever and every other test
// in this file would still pass.
func TestUserStreakReflectsRealCuration(t *testing.T) {
	user, err := asAdmin(t).createTestUser(nil, []models.RoleEnum{models.RoleEnumRead})
	require.NoError(t, err)
	userID := user.ID

	var dbDay string
	require.NoError(t, dbtest.DB().QueryRow(context.Background(),
		"SELECT (now() AT TIME ZONE current_setting('TIMEZONE'))::timestamp::date::text").Scan(&dbDay))

	recordStreakEvent(t, userID, trust.KindEditApproved)

	// The client must run AS the user whose activity it is asking about.
	asUser := createTestRunner(t, user, []models.RoleEnum{models.RoleEnumRead})

	got, err := asUser.client.userStreak()
	require.NoError(t, err)

	assert.True(t, got.ActiveToday, "an approved edit seconds ago means today is active")
	assert.Equal(t, 1, got.CurrentStreak)
	assert.Equal(t, 1, got.TotalActiveDays)

	// lastActiveDay is a CALENDAR DAY, not an instant. Formatting it as UTC moves
	// midnight in a positive-offset zone onto the previous day, so the client
	// would show the day before the one the database matched the event against.
	require.NotNil(t, got.LastActiveDay)
	assert.Equal(t, dbDay, *got.LastActiveDay)
}

// The resolver reads the CALLING user's events. A resolver that ignored the user
// would return a plausible-looking streak for the wrong person, which is exactly
// the kind of bug that survives a green suite.
func TestUserStreakIsScopedToTheCaller(t *testing.T) {
	activeUser, err := asAdmin(t).createTestUser(nil, []models.RoleEnum{models.RoleEnumRead})
	require.NoError(t, err)
	idleUser, err := asAdmin(t).createTestUser(nil, []models.RoleEnum{models.RoleEnumRead})
	require.NoError(t, err)

	recordStreakEvent(t, activeUser.ID, trust.KindEditApproved)

	activeCtx := createTestRunner(t, activeUser, []models.RoleEnum{models.RoleEnumRead})
	gotActive, err := activeCtx.client.userStreak()
	require.NoError(t, err)
	assert.Equal(t, 1, gotActive.CurrentStreak, "the caller sees their own single active day")

	idleCtx := createTestRunner(t, idleUser, []models.RoleEnum{models.RoleEnumRead})
	gotIdle, err := idleCtx.client.userStreak()
	require.NoError(t, err)
	assert.Equal(t, 0, gotIdle.CurrentStreak,
		"another user's events must not leak into this user's streak")
}

// There is deliberately no `userStreak(id:)`. A streak is a private measure of
// one's own contribution, and making it addressable for arbitrary users turns a
// progress indicator into a public ranking of who has been absent -- the coercion
// surface this feature was designed to avoid.
//
// If a future change adds that argument to satisfy some caller, this fails, and
// the reason is written here rather than left to be rediscovered.
func TestUserStreakCannotBeAskedForSomebodyElse(t *testing.T) {
	user := asAdmin(t)

	err := user.client.Post(
		`query StreakOfOther { userStreak(id: "00000000-0000-0000-0000-000000000000") { currentStreak } }`,
		&struct{}{})
	require.Error(t, err,
		"userStreak must not accept an id; a streak is only ever your own")
}
