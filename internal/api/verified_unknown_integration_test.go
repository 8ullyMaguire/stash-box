//go:build integration

package api_test

import (
	"encoding/json"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	testutil "github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/verification"
)

// SPEC §7.24.1 end to end: an assertion submitted on a SCENE EDIT reaches
// field_verification_states, and the reason's suppresses_gap decides whether the gap is closed.
//
// WHY THIS TEST EXISTS AT ALL. Every half of this feature already existed and nothing joined them:
// the GraphQL input type, the edit-data model, the XP rule (unit-tested on its own, with 7/7
// mutations killed), the queries, and the storage. What did not exist was any proof that an
// assertion submitted through the API reaches the table -- and the model field exists whether or not
// a client can ever populate it, so the unit tests could not tell the difference. This is the test
// that fails if any LINK is missing.
//
// It also pins the entity_type SPELLING. Migration 96 put no CHECK on that column and this schema
// spells the same idea two ways ('SCENE' in edits.target_type, 'scene' in content_sources.entity_type),
// so a value in the wrong case would write successfully and then match nothing at read time -- an
// assertion that silently suppresses no gap, in a feature whose purpose is suppressing gaps.
func TestVerifiedUnknownReachesTheStoreAndSuppressesTheGap(t *testing.T) {
	admin := asAdmin(t)

	// THE SCENE IS CREATED THROUGH AN EDIT, not through the fixture helper, and that is load-bearing
	// rather than tidy: createTestScene writes the row directly, so the scene has NO create edit and
	// the §7.24.1 rule has no previous editor to compare against -- which made an earlier version of
	// this test assert a rule that could not fire. Measured, after the lookup returned no rows and
	// the question was whether the join or the fixture was wrong: the scene's only history entry was
	// the asserting edit itself.
	createEdit, err := admin.createTestSceneEdit(models.OperationEnumCreate,
		&models.SceneEditDetailsInput{Title: pStr(admin.generateSceneName())}, nil)
	require.NoError(t, err)
	applyEdit(t, admin, createEdit)
	sceneID := sceneIDOfEdit(t, admin, createEdit.ID)

	// One assertion that SUPPRESSES the gap, and one that must not.
	edit, err := admin.createTestSceneEdit(models.OperationEnumModify,
		&models.SceneEditDetailsInput{
			VerifiedUnknowns: []models.VerifiedUnknownInput{
				{Field: "date", ReasonCode: "not_publicly_knowable"},
				{Field: "details", ReasonCode: "not_yet_looked"},
			},
		},
		&models.EditInput{Operation: models.OperationEnumModify, ID: &sceneID})
	require.NoError(t, err)
	require.NotNil(t, edit)

	// The assertions are written when the edit is APPLIED, not when it is submitted: a claim that
	// has not been accepted is not yet a fact about the archive.
	if !edit.Applied {
		applied, err := admin.resolver.Mutation().ApproveEdit(admin.ctx,
			models.ApproveEditInput{ID: edit.ID})
		require.NoError(t, err)
		if !applied.Applied {
			// This funnel records a FAILED apply as an edit comment carrying the error text, which is
			// the most direct evidence available for why the assertions are not in the store.
			var text string
			_ = testutil.DB().QueryRow(admin.ctx,
				`SELECT text FROM edit_comments WHERE edit_id = $1 ORDER BY created_at DESC LIMIT 1`,
				edit.ID).Scan(&text)
			t.Fatalf("the edit did not apply. applied=%+v; last comment: %s", applied, text)
		}
	}

	// BISECT: is the assertion in the STORED edit data? The edit is written to jsonb and re-read
	// when it is applied, so a field that survives the API but not the round trip reaches the apply
	// path empty and is written nowhere.
	var stored []byte
	require.NoError(t, testutil.DB().QueryRow(admin.ctx,
		`SELECT data FROM edits WHERE id = $1`, edit.ID).Scan(&stored))
	var probe struct {
		New *struct {
			VerifiedUnknowns []models.VerifiedUnknownInput `json:"verified_unknowns"`
		} `json:"new_data"`
	}
	require.NoError(t, json.Unmarshal(stored, &probe))
	require.NotNil(t, probe.New, "the stored edit has no new_data")
	storedCount := len(probe.New.VerifiedUnknowns)
	t.Logf("stored verified_unknowns: %d", storedCount)
	require.Equal(t, 2, storedCount,
		"the assertions did not survive the edit-data round trip, so the apply path never saw them")

	// The row exists, with the entity type spelled the way the edit machinery spells it.
	rows, err := queries.New(testutil.DB()).GetFieldVerificationStates(admin.ctx,
		queries.GetFieldVerificationStatesParams{
			EntityType: verification.EntityScene,
			EntityID:   sceneID,
		})
	require.NoError(t, err)
	require.Len(t, rows, 2, "an assertion submitted through the API did not reach the store")

	byField := make(map[string]queries.GetFieldVerificationStatesRow, len(rows))
	for _, r := range rows {
		byField[r.Field] = r
	}

	assert.Equal(t, verification.EntityScene, byField["date"].EntityType)
	assert.Equal(t, "not_publicly_knowable", byField["date"].ReasonCode)
	assert.True(t, byField["date"].SuppressesGap, "the registry lost suppresses_gap for a suppressing code")
	// The consensus trail is reachable from the assertion, which is what 96's nullable edit_id is
	// FOR and why this path fills it in.
	require.True(t, byField["date"].EditID.Valid)
	assert.Equal(t, edit.ID, byField["date"].EditID.UUID)

	assert.False(t, byField["details"].SuppressesGap, "not_yet_looked became a gap-suppressing reason")

	// And the ruling the whole feature rests on: one reason closes the gap and the other leaves it
	// actionable, which is what stops an unanswerable question from being closed by assertion.
	suppressing, err := testutil.Factory().Verification().SuppressingFields(admin.ctx,
		verification.EntityScene, sceneID)
	require.NoError(t, err)
	assert.Equal(t, []string{"date"}, suppressing,
		"the suppressing set must hold the not_publicly_knowable field and NOT the not_yet_looked one")

	// GATE 3, THROUGH THE REAL APPLY PATH: no trust for asserting on an entity you just edited.
	//
	// This admin created the scene and then asserted on it, so the assertion is self-affirming and
	// the funnel must withhold the trust event. The event is keyed by the edit, so zero rows for
	// THIS edit is the rule firing -- not merely the absence of a write somewhere.
	// ISOLATE THE LOOKUP the rule depends on: the assertion is by the admin, and the admin is also
	// the author of the scene's create edit, so the rule's "previous editor" must be the admin.
	previous, err := queries.New(testutil.DB()).GetPreviousSceneEditAuthor(admin.ctx, edit.ID)
	require.NoError(t, err)
	require.True(t, previous.Valid, "the rule found no previous editor, so it could never detect self-affirmation")
	assert.Equal(t, edit.UserID.UUID, previous.UUID,
		"the previous editor of the scene is not the user who asserted on it, so the rule cannot fire")

	assert.Zero(t, trustEventsFor(t, admin, edit.ID),
		"a self-affirming verified-unknown earned trust, which is the farm SPEC §7.24.1 names")

	// THE POSITIVE CONTROL. Without it this test would pass just as happily if the funnel never
	// recorded trust for anything, so the assertion below is what makes the one above evidence.
	modifier := asModify(t)
	otherCreate, err := modifier.createTestSceneEdit(models.OperationEnumCreate,
		&models.SceneEditDetailsInput{Title: pStr(modifier.generateSceneName())}, nil)
	require.NoError(t, err)
	applyEdit(t, admin, otherCreate)
	otherSceneID := sceneIDOfEdit(t, modifier, otherCreate.ID)

	otherEdit, err := admin.createTestSceneEdit(models.OperationEnumModify,
		&models.SceneEditDetailsInput{
			VerifiedUnknowns: []models.VerifiedUnknownInput{
				{Field: "date", ReasonCode: "not_publicly_knowable"},
			},
		},
		&models.EditInput{Operation: models.OperationEnumModify, ID: &otherSceneID})
	require.NoError(t, err)
	if !otherEdit.Applied {
		applied, err := admin.resolver.Mutation().ApproveEdit(admin.ctx,
			models.ApproveEditInput{ID: otherEdit.ID})
		require.NoError(t, err)
		require.True(t, applied.Applied, "the control edit did not apply, so the control proves nothing")
	}
	assert.Equal(t, 1, trustEventsFor(t, admin, otherEdit.ID),
		"an assertion about another user's entity earned nothing, so nobody can ever earn trust for one")
}

// applyEdit approves an edit if it has not applied already, and fails the test if it still has not.
//
// The approver is an admin: a create edit by a modify-role user is not immediate, and an edit that
// never applies exercises no write path at all.
func applyEdit(t *testing.T, approver *testRunner, edit *models.Edit) {
	t.Helper()
	if edit.Applied {
		return
	}
	applied, err := approver.resolver.Mutation().ApproveEdit(approver.ctx,
		models.ApproveEditInput{ID: edit.ID})
	require.NoError(t, err)
	if !applied.Applied {
		var text string
		_ = testutil.DB().QueryRow(approver.ctx,
			`SELECT text FROM edit_comments WHERE edit_id = $1 ORDER BY created_at DESC LIMIT 1`,
			edit.ID).Scan(&text)
		t.Fatalf("edit %s did not apply: %s", edit.ID, text)
	}
}

// sceneIDOfEdit resolves the scene an edit targets. scene_edits carries (edit_id, scene_id), and
// edits carries no target id of its own (migration 118).
func sceneIDOfEdit(t *testing.T, r *testRunner, editID uuid.UUID) uuid.UUID {
	t.Helper()
	var sceneID uuid.UUID
	require.NoError(t, testutil.DB().QueryRow(r.ctx,
		`SELECT scene_id FROM scene_edits WHERE edit_id = $1`, editID).Scan(&sceneID))
	return sceneID
}

// trustEventsFor counts the trust events recorded for one edit. The funnel keys every event it
// writes by (entity_type, entity_id) = ("edit", the edit's id), so this is the exact row the
// §7.24.1 rule decides whether to write.
func trustEventsFor(t *testing.T, r *testRunner, editID uuid.UUID) int {
	t.Helper()
	var n int
	require.NoError(t, testutil.DB().QueryRow(r.ctx,
		`SELECT count(*) FROM trust_events WHERE entity_type = 'edit' AND entity_id = $1`,
		editID).Scan(&n))
	return n
}
