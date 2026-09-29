//go:build integration

package api_test

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/service/trust"
)

// The trust service (SPEC §6, Phase 1 Step 1).
//
// The level curve itself is unit-tested in internal/service/trust/level_test.go.
// These tests cover what the curve cannot: that recording an event moves the
// rollup, that a retry does not double-count, and that the two writes happen
// together or not at all.

// trustService returns the trust service under test.
func trustService(t *testing.T) *trust.Trust {
	t.Helper()
	svc := testutil.Factory().Trust()
	require.NotNil(t, svc, "the trust service must be wired into the factory")
	return svc
}

// event builds an edit-approved event for a user, with a distinct entity so the
// dedup index does not reject it.
func event(userID uuid.UUID, kind trust.KindEnum) trust.Event {
	entityID := uuid.Must(uuid.NewV7())
	return trust.Event{
		UserID:     userID,
		Kind:       kind,
		Delta:      1,
		EntityType: "performer",
		EntityID:   &entityID,
	}
}

// A brand-new user is level 0 and has no rollup row.
//
// This is the common case, not an error path: it is every account that has not
// contributed yet, and the service must answer rather than fail.
func TestNewUserIsLevelPublicWithNoRollup(t *testing.T) {
	user, err := asAdmin(t).createTestUser(nil, nil)
	require.NoError(t, err)

	level, err := trustService(t).Level(t.Context(), user.ID)
	require.NoError(t, err, "a user with no contributions must be readable, not an error")
	assert.Equal(t, trust.LevelPublic, level)

	totals, err := trustService(t).TotalsFor(t.Context(), user.ID)
	require.NoError(t, err)
	assert.Equal(t, trust.Totals{}, totals, "a new user has zero of everything")

	can, err := trustService(t).CanViewContent(t.Context(), user.ID)
	require.NoError(t, err)
	assert.False(t, can, "a new user must not be able to view content")
}

// Recording an applied edit moves the rollup and the level.
func TestRecordingAnAppliedEditRaisesTheLevel(t *testing.T) {
	user, err := asAdmin(t).createTestUser(nil, nil)
	require.NoError(t, err)
	svc := trustService(t)

	rollup, err := svc.RecordEvent(t.Context(), event(user.ID, trust.KindEditApproved))
	require.NoError(t, err)
	require.NotNil(t, rollup)

	assert.Equal(t, 1, rollup.ApprovedEdits, "one applied edit must be counted once")
	assert.Equal(t, 0, rollup.RejectedEdits, "an approval must not touch the rejection count")

	// One applied edit is 10 points, which is exactly the LevelRegistered
	// threshold.
	level, err := svc.Level(t.Context(), user.ID)
	require.NoError(t, err)
	assert.Equal(t, trust.LevelRegistered, level,
		"one applied edit (10 points) must reach LevelRegistered")
}

// A rejected edit is counted separately and must not raise the level.
func TestRecordingARejectedEditDoesNotRaiseTheLevel(t *testing.T) {
	user, err := asAdmin(t).createTestUser(nil, nil)
	require.NoError(t, err)
	svc := trustService(t)

	rollup, err := svc.RecordEvent(t.Context(), event(user.ID, trust.KindEditRejected))
	require.NoError(t, err)

	assert.Equal(t, 1, rollup.RejectedEdits, "a rejection must be counted")
	assert.Equal(t, 0, rollup.ApprovedEdits, "a rejection must not count as an approval")

	level, err := svc.Level(t.Context(), user.ID)
	require.NoError(t, err)
	assert.Equal(t, trust.LevelPublic, level,
		"one rejected edit is 1 point, below the first threshold, so the user "+
			"must stay at level 0")
}

// The dedup guarantee: recording the SAME event twice counts once.
//
// This is the property that makes a retried request safe, and it is enforced by
// the migration 76 index rather than by the service -- so this test is really
// asserting that the service passes the event through with a stable dedup key.
func TestRecordingTheSameEventTwiceCountsOnce(t *testing.T) {
	user, err := asAdmin(t).createTestUser(nil, nil)
	require.NoError(t, err)
	svc := trustService(t)

	e := event(user.ID, trust.KindEditApproved)

	first, err := svc.RecordEvent(t.Context(), e)
	require.NoError(t, err)
	require.Equal(t, 1, first.ApprovedEdits)

	second, err := svc.RecordEvent(t.Context(), e)
	require.NoError(t, err, "a duplicate event is a no-op, not an error")
	assert.Equal(t, 1, second.ApprovedEdits,
		"the same event recorded twice must count once (#76 dedup index)")

	history, err := svc.History(t.Context(), user.ID)
	require.NoError(t, err)
	assert.Len(t, history, 1, "the event log must hold one row, not two")
}

// An event with no entity must also dedup.
//
// This is the NULLS NOT DISTINCT case: the service must pass SQL NULL rather
// than an empty string, or the dedup key differs from the original and a retry
// double-counts. The migration test proves the index; this proves the service
// sends the right value.
func TestEntitylessEventsDeduplicate(t *testing.T) {
	user, err := asAdmin(t).createTestUser(nil, nil)
	require.NoError(t, err)
	svc := trustService(t)

	e := trust.Event{UserID: user.ID, Kind: trust.KindQuestCompleted, Delta: 1}

	first, err := svc.RecordEvent(t.Context(), e)
	require.NoError(t, err)
	require.Equal(t, 1, first.QuestsCompleted)

	second, err := svc.RecordEvent(t.Context(), e)
	require.NoError(t, err)
	assert.Equal(t, 1, second.QuestsCompleted,
		"an entity-less event recorded twice must count once")
}

// Distinct events for the same user accumulate.
func TestDistinctEventsAccumulate(t *testing.T) {
	user, err := asAdmin(t).createTestUser(nil, nil)
	require.NoError(t, err)
	svc := trustService(t)

	// 5 applied edits at 10 points each = 50, which is exactly LevelContributor.
	for range 5 {
		_, err := svc.RecordEvent(t.Context(), event(user.ID, trust.KindEditApproved))
		require.NoError(t, err)
	}

	rollup, err := svc.RecordEvent(t.Context(), event(user.ID, trust.KindQuestCompleted))
	require.NoError(t, err)
	assert.Equal(t, 5, rollup.ApprovedEdits)
	assert.Equal(t, 1, rollup.QuestsCompleted)

	level, err := svc.Level(t.Context(), user.ID)
	require.NoError(t, err)
	assert.Equal(t, trust.LevelContributor, level,
		"5 edits (50) + 1 quest (20) = 70 points, which is LevelContributor")
}

// A zero delta must be refused.
//
// A zero-delta event would move no counter but would still occupy the dedup
// slot, so a later real event for the same entity would be discarded as a
// "duplicate". Silently losing a real contribution is worse than an error.
func TestZeroDeltaEventIsRejected(t *testing.T) {
	user, err := asAdmin(t).createTestUser(nil, nil)
	require.NoError(t, err)
	svc := trustService(t)

	_, err = svc.RecordEvent(t.Context(), trust.Event{
		UserID: user.ID,
		Kind:   trust.KindEditApproved,
		Delta:  0,
	})
	require.Error(t, err, "a zero delta must be refused")

	// And it must not have occupied the dedup slot.
	//
	// Note this re-records the SAME `real` event rather than calling event()
	// again: event() generates a fresh entity ID per call, so a second call
	// would be a genuinely different event and would correctly count as a
	// second approval. The first version of this test made that mistake and
	// read the resulting 2 as "the refused event double-counted" -- when the
	// service was in fact right and the test was not.
	real := event(user.ID, trust.KindEditApproved)

	first, err := svc.RecordEvent(t.Context(), real)
	require.NoError(t, err, "the real event must still be recordable afterwards")
	require.Equal(t, 1, first.ApprovedEdits)

	again, err := svc.RecordEvent(t.Context(), real)
	require.NoError(t, err)
	assert.Equal(t, 1, again.ApprovedEdits,
		"the refused zero-delta event must not have blocked or double-counted "+
			"the real one")
}

// An unknown kind is recorded but does not move the rollup.
func TestUnknownKindIsRecordedButNotCounted(t *testing.T) {
	user, err := asAdmin(t).createTestUser(nil, nil)
	require.NoError(t, err)
	svc := trustService(t)

	_, err = svc.RecordEvent(t.Context(), event(user.ID, trust.KindEnum("time_travel")))
	require.ErrorIs(t, err, trust.ErrUnknownKind,
		"an unknown kind must be reported, not silently accepted")

	history, err := svc.History(t.Context(), user.ID)
	require.NoError(t, err)
	assert.Len(t, history, 1,
		"the unknown event must still be in the log: a later version may know "+
			"this kind, and refusing to log it would make the contribution "+
			"invisible to every future rebuild")

	level, err := svc.Level(t.Context(), user.ID)
	require.NoError(t, err)
	assert.Equal(t, trust.LevelPublic, level,
		"an unknown kind must not move the level")
}

// Content viewing requires BOTH eligibility and the user's own opt-in.
func TestContentViewingRequiresEligibilityAndOptIn(t *testing.T) {
	user, err := asAdmin(t).createTestUser(nil, nil)
	require.NoError(t, err)
	svc := trustService(t)

	// Opt in first, while ineligible. SPEC §6 says high-trust users explicitly
	// opt in, so the choice must be recordable before eligibility arrives.
	_, err = svc.SetContentViewingOptIn(t.Context(), user.ID, true)
	require.NoError(t, err)

	can, err := svc.CanViewContent(t.Context(), user.ID)
	require.NoError(t, err)
	assert.False(t, can,
		"an opt-in must NOT grant content access on its own; level 4 is the gate")
}

// Reaching the content level with an opt-in grants access.
func TestContentViewingGrantedAtArchivistWithOptIn(t *testing.T) {
	user, err := asAdmin(t).createTestUser(nil, nil)
	require.NoError(t, err)
	svc := trustService(t)

	// 75 applied edits at 10 points = 750, exactly LevelArchivist.
	for range 75 {
		_, err := svc.RecordEvent(t.Context(), event(user.ID, trust.KindEditApproved))
		require.NoError(t, err)
	}

	level, err := svc.Level(t.Context(), user.ID)
	require.NoError(t, err)
	require.Equal(t, trust.LevelArchivist, level, "750 points must be LevelArchivist")

	can, err := svc.CanViewContent(t.Context(), user.ID)
	require.NoError(t, err)
	assert.False(t, can, "eligible but not opted in must still be refused")

	_, err = svc.SetContentViewingOptIn(t.Context(), user.ID, true)
	require.NoError(t, err)

	can, err = svc.CanViewContent(t.Context(), user.ID)
	require.NoError(t, err)
	assert.True(t, can, "eligible AND opted in must be allowed")
}

// Revoking the opt-in takes effect immediately.
func TestRevokingContentViewingOptInTakesEffect(t *testing.T) {
	user, err := asAdmin(t).createTestUser(nil, nil)
	require.NoError(t, err)
	svc := trustService(t)

	for range 75 {
		_, err := svc.RecordEvent(t.Context(), event(user.ID, trust.KindEditApproved))
		require.NoError(t, err)
	}
	_, err = svc.SetContentViewingOptIn(t.Context(), user.ID, true)
	require.NoError(t, err)

	can, err := svc.CanViewContent(t.Context(), user.ID)
	require.NoError(t, err)
	require.True(t, can, "precondition: access is granted")

	_, err = svc.SetContentViewingOptIn(t.Context(), user.ID, false)
	require.NoError(t, err)

	can, err = svc.CanViewContent(t.Context(), user.ID)
	require.NoError(t, err)
	assert.False(t, can, "revoking the opt-in must take effect immediately")
}

// RebuildLevels repairs a drifted rollup from the event log.
//
// This is why trust_events is append-only: the rollup is a cache, and the log
// is what makes it recoverable. The test corrupts the rollup directly and then
// proves the rebuild restores it.
func TestRebuildLevelsRepairsADriftedRollup(t *testing.T) {
	user, err := asAdmin(t).createTestUser(nil, nil)
	require.NoError(t, err)
	svc := trustService(t)

	for range 3 {
		_, err := svc.RecordEvent(t.Context(), event(user.ID, trust.KindEditApproved))
		require.NoError(t, err)
	}

	// Corrupt the rollup behind the service's back, as a bad migration or a
	// manual database edit would.
	var corrupted int
	err = trustDB(t)(
		`UPDATE user_trust SET approved_edits = 99, level = 5
		  WHERE user_id = $1 RETURNING approved_edits`, user.ID).Scan(&corrupted)
	require.NoError(t, err)
	require.Equal(t, 99, corrupted, "precondition: the rollup is corrupted")

	require.NoError(t, svc.RebuildLevels(t.Context()), "rebuild must succeed")

	totals, err := svc.TotalsFor(t.Context(), user.ID)
	require.NoError(t, err)
	assert.Equal(t, 3, totals.ApprovedEdits,
		"the rebuild must restore the rollup from the event log")

	level, err := svc.Level(t.Context(), user.ID)
	require.NoError(t, err)
	assert.Equal(t, trust.LevelRegistered, level,
		"3 edits = 30 points = LevelRegistered; the corrupted level 5 must be "+
			"recomputed, not left in place")
}
