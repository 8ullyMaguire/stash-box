//go:build integration

package api_test

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/api"
	"github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service"
)

// Chapters and media on a content item (aggregator phase 3, slice 3d).
//
// Both lists are per-item, which is the property that needs a fixture the query must
// EXCLUDE. Asserting "the three chapters I made came back" passes against a statement
// with no `WHERE content_item_id` at all, as long as the database holds no other
// chapters -- and the integration database holds thousands. So every test here creates a
// SECOND item with its own chapter/media row and asserts the first item never returns it.

func ptrString(s string) *string { return &s }
func ptrInt(i int) *int          { return &i }

func chapterMediaResolvers(t *testing.T) *api.Resolver {
	t.Helper()
	return api.NewResolver(*service.NewFactory(testutil.DB(), nil))
}

func TestContentItemChaptersAreReadInReadingOrderAndScopedToTheItem(t *testing.T) {
	resolvers := chapterMediaResolvers(t)
	ctx := t.Context()
	q := contentItemQ()

	item := newContentItem(t, "comic", "A serial")
	other := newContentItem(t, "comic", "A different serial")

	// Inserted 3, 1, 2: insertion order and reading order disagree, so a statement that
	// returns them in the order the planner happened to produce them cannot pass.
	for _, number := range []int{3, 1, 2} {
		_, err := q.CreateContentItemChapter(ctx, queries.CreateContentItemChapterParams{
			ID:            newUUID(t),
			ContentItemID: item.ID,
			Number:        number,
			Title:         ptrString("Chapter " + strconv.Itoa(number)),
			PageCount:     ptrInt(20),
		})
		require.NoError(t, err)
	}

	// The excluded fixture. Without it, "the chapters came back" is satisfied by a query
	// that ignores the item entirely.
	_, err := q.CreateContentItemChapter(ctx, queries.CreateContentItemChapterParams{
		ID:            newUUID(t),
		ContentItemID: other.ID,
		Number:        1,
		Title:         ptrString("Another item's first chapter"),
	})
	require.NoError(t, err)

	got, err := resolvers.ContentItem().Chapters(ctx, &models.ContentItem{ID: item.ID})
	require.NoError(t, err)
	require.Len(t, got, 3,
		"exactly this item's chapters: more means the item filter is not applied, fewer means the list is capped")
	for index, chapter := range got {
		assert.Equal(t, index+1, chapter.Number,
			"chapters must arrive in reading order, whatever order they were created in")
	}
	for _, chapter := range got {
		require.NotNil(t, chapter.Title)
		assert.NotEqual(t, "Another item's first chapter", *chapter.Title,
			"another item's chapter came back from a per-item list")
	}

	// An item with no chapters answers with an empty list rather than an error: a comic
	// nobody has broken up yet is normal.
	empty := newContentItem(t, "comic", "A serial nobody has broken up")
	none, err := resolvers.ContentItem().Chapters(ctx, &models.ContentItem{ID: empty.ID})
	require.NoError(t, err)
	assert.Empty(t, none)
}

func TestContentItemMediaPutsTheItemItselfFirst(t *testing.T) {
	resolvers := chapterMediaResolvers(t)
	ctx := t.Context()
	q := contentItemQ()

	item := newContentItem(t, "image", "An item with renditions")
	other := newContentItem(t, "image", "Another item with renditions")

	// Created in the order alphabetical sorting would return: archive, preview, original.
	// Alphabetical puts `archive` first, which is a rendition of the item rather than the
	// item, so the assertion below distinguishes the ranked order from the accidental one.
	for _, media := range []struct {
		url  string
		kind string
	}{
		{"https://example.invalid/z-archive.bin", "archive"},
		{"https://example.invalid/m-preview.jpg", "preview"},
		{"https://example.invalid/a-original.jpg", "original"},
	} {
		_, err := q.CreateContentItemMedia(ctx, queries.CreateContentItemMediaParams{
			ID:            newUUID(t),
			ContentItemID: item.ID,
			Url:           media.url,
			Kind:          media.kind,
			Width:         ptrInt(1200),
			Height:        ptrInt(900),
		})
		require.NoError(t, err)
	}

	_, err := q.CreateContentItemMedia(ctx, queries.CreateContentItemMediaParams{
		ID:            newUUID(t),
		ContentItemID: other.ID,
		Url:           "https://example.invalid/another-item.jpg",
		Kind:          "original",
	})
	require.NoError(t, err)

	got, err := resolvers.ContentItem().Media(ctx, &models.ContentItem{ID: item.ID})
	require.NoError(t, err)
	require.Len(t, got, 3, "exactly this item's URLs")

	kinds := make([]string, 0, len(got))
	urls := make([]string, 0, len(got))
	for _, media := range got {
		kinds = append(kinds, string(media.Kind))
		urls = append(urls, media.URL)
		assert.NotEqual(t, "https://example.invalid/another-item.jpg", media.URL,
			"another item's URL came back from a per-item list")
	}
	assert.Equal(t, []string{"original", "preview", "archive"}, kinds,
		"the item itself first, then its renditions: alphabetical order would put archive first")
	assert.Equal(t, []string{
		"https://example.invalid/a-original.jpg",
		"https://example.invalid/m-preview.jpg",
		"https://example.invalid/z-archive.bin",
	}, urls)

	// The dimensions travel: a client deciding whether to fetch a preview needs to know.
	assert.Equal(t, 1200, *got[0].Width)
	assert.Equal(t, 900, *got[0].Height)

	empty := newContentItem(t, "image", "An item with no URL yet")
	none, err := resolvers.ContentItem().Media(ctx, &models.ContentItem{ID: empty.ID})
	require.NoError(t, err)
	assert.Empty(t, none, "no URL yet is a real answer, not an error")
}
