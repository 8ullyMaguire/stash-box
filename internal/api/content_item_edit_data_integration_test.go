//go:build integration

package api_test

import (
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/queries"
)

// The data layer the content-item edit type needs (aggregator phase 3, slice 3f).
//
// An edit stores WHAT changed as JSON, but `edits` carries a target_type and no target id
// (migration 05's shape). The target is resolved by joining the per-type join table for the
// edit's type, so a new target type without a join table is a type whose edits can never be
// linked to their entity -- the edit applies, the item exists, and `edit.target` is null
// forever, with nothing failing. Migration 118 adds the table; these tests are what prove
// the CASE has an arm and the join is scoped to the item it names.

func createContentItemEditRow(t *testing.T, q *queries.Queries, operation string) queries.Edit {
	t.Helper()
	edit, err := q.CreateEdit(t.Context(), queries.CreateEditParams{
		ID:         newUUID(t),
		UserID:     uuid.NullUUID{UUID: userDB.read.ID, Valid: true},
		TargetType: "CONTENT_ITEM",
		Operation:  operation,
		Data:       []byte("{}"),
		// CLOSED ON PURPOSE, and this is a fixture decision with a reason: the vote
		// sweeps scan every PENDING edit in the database, and a PENDING CONTENT_ITEM edit
		// is a row no processor can apply yet -- the edit TYPE is the next slice. Leaving
		// one behind makes unrelated tests fail, in packages that have nothing to do with
		// content items, with a nil pointer somewhere in the applier. A closed status keeps
		// this row out of every sweep while leaving the join and the target resolution --
		// the two things these tests are about -- exactly as they are.
		Status:  "ACCEPTED",
		Applied: true,
	})
	require.NoError(t, err)
	return edit
}

func TestAContentItemEditResolvesItsTargetAndItsHistory(t *testing.T) {
	ctx := t.Context()
	q := contentItemQ()

	item := newContentItem(t, "gallery", "An edited gallery")
	other := newContentItem(t, "gallery", "An unedited gallery")

	edit := createContentItemEditRow(t, q, "CREATE")
	require.NoError(t, q.CreateContentItemEdit(ctx, queries.CreateContentItemEditParams{
		EditID:        edit.ID,
		ContentItemID: item.ID,
	}))

	// The CASE arm. Without it this resolves to a nil id -- which the caller reads as
	// "the target was deleted", the same answer for a live item and a missing one.
	target, err := q.GetEditTargetID(ctx, edit.ID)
	require.NoError(t, err)
	assert.Equal(t, item.ID, target.ID, "the edit's target must be the item the join names")
	assert.Equal(t, "CONTENT_ITEM", target.TargetType)

	// The reverse direction, and it is scoped: the other item has never been edited, so its
	// history is empty while this one's is not. The positive half is asserted first, because
	// "no rows" is also what a query that filters on the wrong column returns.
	edits, err := q.FindContentItemEditsForItem(ctx, item.ID)
	require.NoError(t, err)
	require.Len(t, edits, 1, "the item's own edit history")
	assert.Equal(t, edit.ID, edits[0].ID)

	none, err := q.FindContentItemEditsForItem(ctx, other.ID)
	require.NoError(t, err)
	assert.Empty(t, none, "an item nobody edited must have an empty history, not this item's")

	// The foreign keys hold: a join row naming an edit that does not exist is refused rather
	// than stored, which is what keeps the history trustworthy.
	err = q.CreateContentItemEdit(ctx, queries.CreateContentItemEditParams{
		EditID:        newUUID(t),
		ContentItemID: item.ID,
	})
	require.Error(t, err, "the join must not accept an edit that does not exist")
}

func TestUpdateContentItemWritesEveryFieldAndRefusesADeletedItem(t *testing.T) {
	ctx := t.Context()
	q := contentItemQ()

	item := newContentItem(t, "text", "Before the edit")
	details := "After the edit"
	release := pgtype.Date{Time: time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC), Valid: true}

	updated, err := q.UpdateContentItem(ctx, queries.UpdateContentItemParams{
		ID:          item.ID,
		Kind:        "comic",
		Title:       "After the edit",
		Details:     &details,
		ReleaseDate: release,
	})
	require.NoError(t, err)
	assert.Equal(t, "comic", updated.Kind, "the kind is written by the update, not preserved")
	assert.Equal(t, "After the edit", updated.Title)
	require.NotNil(t, updated.Details)
	assert.Equal(t, details, *updated.Details)
	assert.True(t, updated.ReleaseDate.Valid)

	// Persisted, not just returned: the statement's RETURNING clause and its SET clause are
	// two different lists, and a row that comes back changed while the table is unchanged is
	// exactly what a mismatch between them looks like.
	read, err := q.GetContentItem(ctx, item.ID)
	require.NoError(t, err)
	assert.Equal(t, "comic", read.Kind)
	assert.Equal(t, "After the edit", read.Title)
	assert.True(t, read.ReleaseDate.Valid)

	// A soft-deleted item is not updatable: the statement's `deleted = false` predicate means
	// the update finds no row rather than resurrecting one the archive has retired.
	require.NoError(t, q.SoftDeleteContentItem(ctx, item.ID))
	_, err = q.UpdateContentItem(ctx, queries.UpdateContentItemParams{
		ID:          item.ID,
		Kind:        "text",
		Title:       "Resurrected",
		ReleaseDate: pgtype.Date{},
	})
	require.Error(t, err, "an edit applied against a deleted item must fail rather than restore it")
	assert.ErrorIs(t, err, pgx.ErrNoRows)

	after, err := q.GetContentItemIncludingDeleted(ctx, item.ID)
	require.NoError(t, err)
	assert.True(t, after.Deleted, "the item must still be deleted")
	assert.NotEqual(t, "Resurrected", after.Title,
		"the refused update must not have written the title through")
}
