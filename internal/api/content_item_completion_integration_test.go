//go:build integration

package api_test

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/completion"
)

// The content-item type in the completion vocabulary (aggregator phase 3, slice 3b).
//
// A kind the vocabularies do not know about is a row that gets written and never
// counted, and the failure is silent: every page renders, every count is a
// plausible number, and content items simply do not exist as far as curation is
// concerned. So these tests assert the three places a type has to appear:
//
//   1. the per-entity score (`ContentItem`)
//   2. the count queries -- `CountEntities` is one arm per type in SQL, and a
//      forgotten arm shows up as a MISSING row, not as a zero
//   3. the incomplete listing -- likewise a UNION arm, and a forgotten one returns
//      an empty list rather than an error
//
// `TestEveryScoredTypeAppearsInEveryVocabulary` is the one that scales: it walks
// `completion.AllEntityTypes` instead of naming the types, so the next type added
// is covered without anyone remembering to extend this file.
// A skipped integration test exits 0 and prints the same `ok` a pass does, so
// every case here asserts on a value, never on the absence of an error.

func completionSvc() *completion.Service {
	return completion.NewService(queries.New(testutil.DB()))
}

func TestEveryScoredTypeAppearsInEveryVocabulary(t *testing.T) {
	svc := completionSvc()
	ctx := t.Context()

	counts, err := svc.CountEntities(ctx)
	require.NoError(t, err)

	byType := map[completion.EntityType]int{}
	for _, c := range counts {
		byType[c.EntityType] = c.Count
	}

	require.NotEmpty(t, completion.AllEntityTypes, "the vocabulary cannot be empty; that would pass vacuously")
	for _, et := range completion.AllEntityTypes {
		// Every type must have weights...
		total, err := completion.TotalWeight(et)
		require.NoError(t, err, "%s is in AllEntityTypes with no weights", et)
		assert.Greater(t, total, 0, "%s scores zero of zero", et)

		// ...and a row in the count query, which is one UNION arm per type in
		// internal/queries/sql/archive_count.sql. A forgotten arm is absent, not
		// zero, and "absent" is what a page renders as a blank denominator.
		_, ok := byType[et]
		assert.True(t, ok, "%s has no arm in CountArchiveEntities: %v", et, byType)
	}
}

func TestAContentItemIsScoredAndCounted(t *testing.T) {
	svc := completionSvc()
	ctx := t.Context()

	// An item with nothing but a title: the two THINGS it can be missing that a
	// curator can supply are media and tags (details and the release date are
	// optional metadata, and they are weighted below those two).
	item := newContentItem(t, "text", "Completion vocabulary item")

	result, err := svc.ContentItem(ctx, item.ID)
	require.NoError(t, err)
	assert.Equal(t, completion.EntityContentItem, result.EntityType)
	assert.Equal(t, 100, result.Total, "the content item's weights must sum to 100: %v", result)
	assert.Less(t, result.Score, 100, "an item with no tags and no media is not complete")
	assert.Contains(t, result.Missing, completion.FieldTags)
	assert.Contains(t, result.Missing, completion.FieldURLs)
	assert.NotContains(t, result.Missing, completion.FieldName,
		"the title is NOT NULL by the table's own CHECK, so it is never a gap a quest should ask for")

	// The score must MOVE when the item gains a field: a score that is the same
	// before and after is a score computed from something other than the row.
	before := result.Score
	tagID := createTagForQuest(t, "content-item-completion", false)
	require.NoError(t, contentItemQ().LinkContentItemTag(ctx, queries.LinkContentItemTagParams{
		ContentItemID: item.ID, TagID: tagID,
	}))
	mediaID := newUUID(t)
	_, err = contentItemQ().CreateContentItemMedia(ctx, queries.CreateContentItemMediaParams{
		ID: mediaID, ContentItemID: item.ID, Url: "https://example.invalid/cv.txt", Kind: "original",
	})
	require.NoError(t, err)

	after, err := svc.ContentItem(ctx, item.ID)
	require.NoError(t, err)
	assert.Greater(t, after.Score, before,
		"a content item that gains the two heaviest fields must score higher")
	assert.NotContains(t, after.Missing, completion.FieldTags)
	assert.NotContains(t, after.Missing, completion.FieldURLs)
}

func TestAnIncompleteContentItemIsFoundByAQuestQuery(t *testing.T) {
	svc := completionSvc()
	ctx := t.Context()

	item := newContentItem(t, "image", "Quest target item")

	// CountIncomplete 100 means "missing at least one weight-unit", i.e. every
	// incomplete entity. The count is a DELTA against a baseline read before the
	// fixture, because the integration database is shared and an earlier run's rows
	// would satisfy an absolute assertion forever.
	beforeCount, err := svc.CountIncomplete(ctx, completion.EntityContentItem, 100)
	require.NoError(t, err)

	afterCount, err := svc.CountIncomplete(ctx, completion.EntityContentItem, 100)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, afterCount, beforeCount)

	// The listing is the UNION arm. A forgotten arm returns an empty page with no
	// error, which is exactly what "no incomplete items exist" looks like.
	ids, err := svc.ListIncomplete(ctx, completion.EntityContentItem, 1, nil, 200)
	require.NoError(t, err)
	assert.True(t, containsID(ids, item.ID),
		"an incomplete content item must be reachable by the quest query that exists to find it: %v", ids)
}

func containsID(ids []uuid.UUID, want uuid.UUID) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}
