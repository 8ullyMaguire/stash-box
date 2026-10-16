//go:build integration

package api_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	testutil "github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service/completion"
)

// SPEC §7.24.1's behavioural gate, in its own words: "assert a field unknown with reason
// `not_publicly_knowable`, confirm the score does not fall and the gap leaves the missing list. Then
// assert with reason `not_yet_looked` and confirm the gap STAYS."
//
// The second half is what makes the first half mean anything. A test that only checked the gap had
// gone would pass just as happily if EVERY assertion suppressed it -- and that is precisely the
// failure mode the reason codes exist to prevent: a gap that can be closed by asserting it is a gap
// nobody has to answer, which turns the whole section into a way to clear the missing list.
func TestVerifiedUnknownSuppressesTheCompletionGap(t *testing.T) {
	admin := asAdmin(t)

	createEdit, err := admin.createTestSceneEdit(models.OperationEnumCreate,
		&models.SceneEditDetailsInput{Title: pStr(admin.generateSceneName())}, nil)
	require.NoError(t, err)
	applyEdit(t, admin, createEdit)
	sceneID := sceneIDOfEdit(t, admin, createEdit.ID)

	scorer := testutil.Factory().Completion()
	before, err := scorer.Scene(admin.ctx, sceneID)
	require.NoError(t, err)
	require.Contains(t, before.Missing, completion.FieldDate,
		"the fixture scene already has a date, so this test could not observe the gap leaving the list")
	require.Contains(t, before.Missing, completion.FieldDetails)

	// One suppressing reason and one that must not, on two different fields, in one edit.
	edit, err := admin.createTestSceneEdit(models.OperationEnumModify,
		&models.SceneEditDetailsInput{
			VerifiedUnknowns: []models.VerifiedUnknownInput{
				{Field: "date", ReasonCode: "not_publicly_knowable"},
				{Field: "details", ReasonCode: "not_yet_looked"},
			},
		},
		&models.EditInput{Operation: models.OperationEnumModify, ID: &sceneID})
	require.NoError(t, err)
	applyEdit(t, admin, edit)

	after, err := scorer.Scene(admin.ctx, sceneID)
	require.NoError(t, err)

	// The gap left the missing list, and the score did not fall.
	assert.NotContains(t, after.Missing, completion.FieldDate,
		"a not_publicly_knowable assertion did not remove the field from the missing list")
	assert.GreaterOrEqual(t, after.Score, before.Score,
		"asserting a field unanswerable LOWERED the completion score")

	// And the assertion that must NOT suppress: "the value is knowable and nobody has looked" keeps
	// the gap actionable, so the field is still missing.
	assert.Contains(t, after.Missing, completion.FieldDetails,
		"a not_yet_looked assertion closed a gap, which makes an unanswered question closable by assertion")

	// The score moved by exactly the weight of the one field that stopped being missing. Parts that
	// only compare a direction would pass if BOTH fields had suppressed, or if the score had risen
	// for some unrelated reason.
	weight, err := completion.WeightFor(completion.EntityScene, completion.FieldDate)
	require.NoError(t, err)
	assert.Equal(t, before.Score+weight, after.Score,
		"the score did not rise by exactly the weight of the field that left the missing list")
}
