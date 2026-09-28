//go:build integration

package api_test

import (
	"strings"
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/models/validator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTagEditRejectsOverlongName is the end-to-end regression test for issue
// #660, "Limit Field Lengths on Form Fields".
//
// The reported failure: a form accepts a value longer than the database
// column, the edit passes review, and the write only fails later with
// `pq: value too long for type character varying(255)` — raised by the cron
// sweep that applies edits, not by the contributor who typed the value. By then
// the edit is closed and the votes are wasted.
//
// The unit tests in internal/models/field_length_test.go cover the validation
// itself. This one exists to prove the validation is WIRED to the service
// layer: `TagEditFromDiffChecked` could be left uncalled and every unit test
// would still pass while the API kept accepting overlong input. That is exactly
// the "guarded line is DEAD" failure the mutation check below is designed to
// catch.
func TestTagEditRejectsOverlongName(t *testing.T) {
	pt := createTagEditTestRunner(t)

	overlong := strings.Repeat("x", validator.MaxStringLength+1)
	require.Greater(t, len([]rune(overlong)), validator.MaxStringLength,
		"precondition: the test value must exceed the column limit")

	err := pt.client.Post(`
		mutation TagEdit($input: TagEditInput!) {
			tagEdit(input: $input) { id }
		}`,
		&struct {
			TagEdit *struct{ ID string } `json:"tagEdit"`
		}{},
		client.Var("input", map[string]any{
			"edit":    map[string]any{"operation": "CREATE"},
			"details": map[string]any{"name": overlong},
		}))

	require.Error(t, err, "an overlong tag name must be rejected at edit creation")
	assert.Contains(t, err.Error(), "name",
		"the error should name the offending field so the form can show it")
	assert.Contains(t, err.Error(), "255",
		"the error should state the limit rather than leaving the contributor guessing")
}

// TestTagEditAcceptsNameAtLimit is the boundary guard. A fix that used `>=`
// instead of `>` would reject exactly-255 names, and only this test would catch
// it.
func TestTagEditAcceptsNameAtLimit(t *testing.T) {
	pt := createTagEditTestRunner(t)

	atLimit := strings.Repeat("y", validator.MaxStringLength)
	edit, err := pt.createTestTagEdit(models.OperationEnumCreate,
		&models.TagEditDetailsInput{Name: &atLimit}, nil)

	require.NoError(t, err,
		"a name of exactly the column width must be accepted")
	require.NotNil(t, edit)
}

// TestTagEditRejectsOverlongDescription covers the second varchar(255) column
// on tags, which shares the helper but not the field name.
func TestTagEditRejectsOverlongDescription(t *testing.T) {
	pt := createTagEditTestRunner(t)

	name := "short-name"
	overlong := strings.Repeat("z", validator.MaxStringLength+1)

	err := pt.client.Post(`
		mutation TagEdit($input: TagEditInput!) {
			tagEdit(input: $input) { id }
		}`,
		&struct {
			TagEdit *struct{ ID string } `json:"tagEdit"`
		}{},
		client.Var("input", map[string]any{
			"edit":    map[string]any{"operation": "CREATE"},
			"details": map[string]any{"name": name, "description": overlong},
		}))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "description")
}
