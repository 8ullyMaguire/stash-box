//go:build integration

package api_test

import (
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/gofrs/uuid"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression tests for issue #802, "Removing category is not a valid change",
// and for the same class of defect reported as issue #9 ("Fields updated to
// NULL are ignored").
//
// WHY THIS POSTS RAW GRAPHQL INSTEAD OF CALLING THE RESOLVER
//
// A GraphQL input field carries two different absences that gqlgen flattens into
// the same nil Go pointer:
//
//	absent        — the edit does not mention the field; keep the old value
//	explicit null — the edit asks for the field to be cleared
//
// The edit diff tells them apart by reading the RAW argument map
// (pkg/utils.ArgumentsQuery, via inputArgs.Field("x").IsNull()), not the
// unmarshalled Go struct. Calling s.resolver.Mutation().TagEdit(...) directly
// therefore CANNOT express an explicit null: Arguments() finds no field context,
// IsNull() is false, and the diff reports "no changes" no matter what the Go
// struct contains.
//
// A resolver-level test of this behaviour is structurally incapable of
// passing, and asserting through it would be testing a fiction. The raw
// mutation below is the only faithful way to send an explicit null, and it is
// exactly what the React client does once TagForm sends `category_id: null`
// instead of omitting the key.
//
// The client half is covered by TagForm.test.tsx, "clears category (sends
// explicit null, not an omitted key)", which is mutation-verified: reverting
// the `?? null` makes it fail with "expected undefined to be null".

// tagEditExplicitNull posts tagEdit with the given details object passed
// through VERBATIM, so a JSON null really does become an explicit null in the
// argument map rather than being dropped by a Go struct's omitempty tag.
func tagEditExplicitNull(
	t *testing.T,
	pt *tagEditTestRunner,
	details map[string]any,
	targetID string,
) (string, error) {
	t.Helper()

	editInput := map[string]any{
		"operation": "MODIFY",
		"id":        targetID,
	}
	var resp struct {
		TagEdit *struct {
			ID       string `json:"id"`
			Applied  bool   `json:"applied"`
			Details  struct {
				Name        *string `json:"name"`
				Description *string `json:"description"`
				Category    *struct {
					ID string `json:"id"`
				} `json:"category"`
			} `json:"details"`
		} `json:"tagEdit"`
	}

	const q = `
	mutation TagEdit($input: TagEditInput!) {
		tagEdit(input: $input) {
			id
			applied
			details {
				... on TagEdit { name description category { id } }
			}
		}
	}`

	err := pt.client.Post(q, &resp, client.Var("input", map[string]any{
		"edit":    editInput,
		"details": details,
	}))
	if err != nil {
		return "", err
	}
	if resp.TagEdit == nil {
		return "", nil
	}
	return resp.TagEdit.ID, nil
}

// TestTagEditRemoveCategoryExplicitNull is the core regression test for #802.
//
// The reported failure is `Error: edit contains no changes` when a contributor
// clears a tag's category. With an explicit null the edit must be accepted.
func TestTagEditRemoveCategoryExplicitNull(t *testing.T) {
	pt := createTagEditTestRunner(t)

	category, err := pt.createTestTagCategory(nil)
	require.NoError(t, err)
	categoryID := category.ID

	created, err := pt.createTestTag(&models.TagCreateInput{
		Name:       "category-removal-tag",
		CategoryID: &categoryID,
	})
	require.NoError(t, err)
	// Precondition, or the test proves nothing: the tag must start WITH one.
	require.NotNil(t, created.Category, "precondition: tag should have a category")

	editID, err := tagEditExplicitNull(t, pt,
		map[string]any{"name": created.Name, "category_id": nil},
		created.UUID().String())
	require.NoError(t, err,
		"clearing a category must not be rejected as containing no changes")
	require.NotEmpty(t, editID, "the edit should have been created")
}

// TestTagEditOmittedFieldIsNotTreatedAsClear is the data-loss guard for the
// same code path.
//
// The tempting but wrong fix for #802 is to treat every nil field as a
// deletion. That would silently clear the category, description, studio, and
// parent of EVERY partial edit ever submitted. This test pins the correct
// three-way behaviour: an OMITTED field leaves the old value alone.
func TestTagEditOmittedFieldIsNotTreatedAsClear(t *testing.T) {
	pt := createTagEditTestRunner(t)

	category, err := pt.createTestTagCategory(nil)
	require.NoError(t, err)
	categoryID := category.ID

	description := "keep me"
	created, err := pt.createTestTag(&models.TagCreateInput{
		Name:        "partial-edit-tag",
		CategoryID:  &categoryID,
		Description: &description,
	})
	require.NoError(t, err)

	// Mention ONLY the name. category_id and description are absent entirely,
	// which must mean "leave them alone" and NOT "clear them".
	editID, err := tagEditExplicitNull(t, pt,
		map[string]any{"name": "renamed-partial-edit-tag"},
		created.UUID().String())
	require.NoError(t, err,
		"an edit that only renames should be a valid change")
	require.NotEmpty(t, editID)

	// Apply it and confirm the untouched fields survived.
	_, err = pt.approveEdit(uuid.FromStringOrNil(editID))
	require.NoError(t, err)

	id := created.UUID()
	modified, err := pt.resolver.Query().FindTag(pt.ctx, &id, nil)
	require.NoError(t, err)
	require.NotNil(t, modified)

	assert.Equal(t, "renamed-partial-edit-tag", modified.Name)
	assert.True(t, modified.CategoryID.Valid,
		"category must SURVIVE an edit that did not mention it")
	assert.Equal(t, categoryID, modified.CategoryID.UUID)
	require.NotNil(t, modified.Description)
	assert.Equal(t, description, *modified.Description,
		"description must SURVIVE an edit that did not mention it")
}
