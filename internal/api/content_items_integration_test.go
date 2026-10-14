//go:build integration

package api_test

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/queries"
)

// The content-item data layer (aggregator phase 3, migration 117).
//
// These go through `queries.New(dbtest.DB())` -- the generated statements, not a service -- for
// the reason this repository's migration rule exists: a constraint is only observable at the
// database, and a service-level test cannot tell "the constraint refused this" from "the service
// never asked". The statements ARE the interface the phase's service will use, so proving them
// here is proving the layer the next slice builds on.
//
// What this does NOT cover, and where it is covered instead: the Go vocabularies that enumerate
// entity types (completion's weights, EloEntityType, the review validator). Those are closed
// lists in code, so the compiler and `completion_weight_parity_test.go` force each one when the
// entity type is exposed -- which is slice 3b. Adding the TABLE first is deliberate: the
// polymorphism decision is the thing this slice is proving, and the measurement behind it is
// that no migration enumerates entity kinds (108's own check already accepts 'content_item').

func contentItemQ() *queries.Queries { return queries.New(dbtest.DB()) }

func newContentItem(t *testing.T, kind, title string) queries.ContentItem {
	t.Helper()
	id, err := uuid.NewV4()
	require.NoError(t, err)
	item, err := contentItemQ().CreateContentItem(t.Context(), queries.CreateContentItemParams{
		ID:    id,
		Kind:  kind,
		Title: title,
	})
	require.NoError(t, err)
	require.Equal(t, id, item.ID)
	return item
}

func TestAContentItemRoundTripsThroughItsDataLayer(t *testing.T) {
	q := contentItemQ()
	item := newContentItem(t, "text", "A text item")

	// Read back by id.
	got, err := q.GetContentItem(t.Context(), item.ID)
	require.NoError(t, err)
	assert.Equal(t, "A text item", got.Title)
	assert.Equal(t, "text", got.Kind)
	assert.False(t, got.Deleted)

	// It appears in the newest-first list, and filtering by kind finds it while another kind
	// does not.
	list, err := q.ListContentItems(t.Context(), queries.ListContentItemsParams{
		Kind: strPtr("text"), RowLimit: 50,
	})
	require.NoError(t, err)
	assert.True(t, containsContentItem(list, item.ID))

	other, err := q.ListContentItems(t.Context(), queries.ListContentItemsParams{
		Kind: strPtr("comic"), RowLimit: 50,
	})
	require.NoError(t, err)
	assert.False(t, containsContentItem(other, item.ID),
		"a kind filter that returns everything is not a filter")

	// A chapter and a media variant, which is what makes the kind more than a label.
	chID, err := uuid.NewV4()
	require.NoError(t, err)
	chapter, err := q.CreateContentItemChapter(t.Context(), queries.CreateContentItemChapterParams{
		ID: chID, ContentItemID: item.ID, Number: 1, Title: strPtr("One"),
	})
	require.NoError(t, err)
	assert.Equal(t, 1, chapter.Number)

	mediaID, err := uuid.NewV4()
	require.NoError(t, err)
	_, err = q.CreateContentItemMedia(t.Context(), queries.CreateContentItemMediaParams{
		ID: mediaID, ContentItemID: item.ID, Url: "https://example.invalid/item.pdf", Kind: "original",
	})
	require.NoError(t, err)

	chapters, err := q.ListContentItemChapters(t.Context(), item.ID)
	require.NoError(t, err)
	assert.Len(t, chapters, 1)
	media, err := q.ListContentItemMedia(t.Context(), item.ID)
	require.NoError(t, err)
	assert.Len(t, media, 1)
}

func TestAContentItemTakesTagsInBothDirections(t *testing.T) {
	q := contentItemQ()
	item := newContentItem(t, "comic", "A tagged comic")
	tagID := createTagForQuest(t, "content-item-tag", false)

	require.NoError(t, q.LinkContentItemTag(t.Context(), queries.LinkContentItemTagParams{
		ContentItemID: item.ID, TagID: tagID,
	}))

	tags, err := q.ListContentItemTags(t.Context(), item.ID)
	require.NoError(t, err)
	require.Len(t, tags, 1)
	assert.Equal(t, tagID, tags[0].ID)

	// The other direction: the tag page asks which items carry it. This is the direction the
	// unique index does NOT cover, which is why it has its own index and its own assertion.
	items, err := q.ListContentItemsForTag(t.Context(), queries.ListContentItemsForTagParams{
		TagID: tagID, RowLimit: 50,
	})
	require.NoError(t, err)
	assert.True(t, containsContentItem(items, item.ID),
		"a tag link that only reads one way makes the tag page a dead end")

	// Linking twice is not an error and does not duplicate: the tag service retries.
	require.NoError(t, q.LinkContentItemTag(t.Context(), queries.LinkContentItemTagParams{
		ContentItemID: item.ID, TagID: tagID,
	}))
	tags, err = q.ListContentItemTags(t.Context(), item.ID)
	require.NoError(t, err)
	assert.Len(t, tags, 1)

	require.NoError(t, q.UnlinkContentItemTag(t.Context(), queries.UnlinkContentItemTagParams{
		ContentItemID: item.ID, TagID: tagID,
	}))
	tags, err = q.ListContentItemTags(t.Context(), item.ID)
	require.NoError(t, err)
	assert.Empty(t, tags)
}

func TestASoftDeletedContentItemIsHiddenButNotGone(t *testing.T) {
	q := contentItemQ()
	item := newContentItem(t, "image", "A removed image")

	require.NoError(t, q.SoftDeleteContentItem(t.Context(), item.ID))

	// The filtered read cannot see it...
	_, err := q.GetContentItem(t.Context(), item.ID)
	assert.Error(t, err, "a soft delete that one read forgets is a row on one page and not another")

	list, err := q.ListContentItems(t.Context(), queries.ListContentItemsParams{RowLimit: 200})
	require.NoError(t, err)
	assert.False(t, containsContentItem(list, item.ID))

	// ...and the audit read can, which is the whole point of not deleting the row.
	audit, err := q.GetContentItemIncludingDeleted(t.Context(), item.ID)
	require.NoError(t, err)
	assert.True(t, audit.Deleted)
}

// TestTheContentItemConstraintsRefuseRatherThanStore is the DDL half. Each case writes raw SQL
// and expects the DATABASE to refuse, because a service that validated first would make these
// pass without the constraint existing at all.
func TestTheContentItemConstraintsRefuseRatherThanStore(t *testing.T) {
	q := contentItemQ()

	t.Run("an unknown kind is refused", func(t *testing.T) {
		_, err := q.CreateContentItem(t.Context(), queries.CreateContentItemParams{
			ID: newUUID(t), Kind: "podcast", Title: "Not a kind",
		})
		require.Error(t, err, "six kinds are closed; a seventh would be a row nothing can render")
	})

	t.Run("a blank title is refused", func(t *testing.T) {
		_, err := q.CreateContentItem(t.Context(), queries.CreateContentItemParams{
			ID: newUUID(t), Kind: "text", Title: "   ",
		})
		require.Error(t, err)
	})

	t.Run("two chapters cannot share a number", func(t *testing.T) {
		item := newContentItem(t, "comic", "Chapter numbering")
		_, err := q.CreateContentItemChapter(t.Context(), queries.CreateContentItemChapterParams{
			ID: newUUID(t), ContentItemID: item.ID, Number: 1,
		})
		require.NoError(t, err)
		_, err = q.CreateContentItemChapter(t.Context(), queries.CreateContentItemChapterParams{
			ID: newUUID(t), ContentItemID: item.ID, Number: 1,
		})
		require.Error(t, err, "two chapter 4s leave a reader guessing which one they had reached")
	})

	t.Run("a zero chapter number is refused", func(t *testing.T) {
		item := newContentItem(t, "text", "Chapter zero")
		_, err := q.CreateContentItemChapter(t.Context(), queries.CreateContentItemChapterParams{
			ID: newUUID(t), ContentItemID: item.ID, Number: 0,
		})
		require.Error(t, err)
	})

	t.Run("a media variant of an unknown kind is refused", func(t *testing.T) {
		item := newContentItem(t, "image", "Media kind")
		_, err := q.CreateContentItemMedia(t.Context(), queries.CreateContentItemMediaParams{
			ID: newUUID(t), ContentItemID: item.ID, Url: "https://example.invalid/x.jpg", Kind: "thumbnail",
		})
		require.Error(t, err)
	})

	t.Run("chapters and media go when the item goes", func(t *testing.T) {
		item := newContentItem(t, "animation", "Cascade")
		_, err := q.CreateContentItemChapter(t.Context(), queries.CreateContentItemChapterParams{
			ID: newUUID(t), ContentItemID: item.ID, Number: 1,
		})
		require.NoError(t, err)

		// The hard delete is not something the API offers; this proves the FK's ON DELETE
		// CASCADE, so a purge cannot leave chapters pointing at an item that is gone.
		_, err = dbtest.DB().Exec(t.Context(), `DELETE FROM content_items WHERE id = $1`, item.ID)
		require.NoError(t, err)

		chapters, err := q.ListContentItemChapters(t.Context(), item.ID)
		require.NoError(t, err)
		assert.Empty(t, chapters)
	})
}

// newUUID is distinct from the package's mustUUID (which PARSES a string): this one makes a
// fresh id for a row the test is about to insert.
func newUUID(t *testing.T) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV4()
	require.NoError(t, err)
	return id
}

func containsContentItem(items []queries.ContentItem, id uuid.UUID) bool {
	for _, i := range items {
		if i.ID == id {
			return true
		}
	}
	return false
}
