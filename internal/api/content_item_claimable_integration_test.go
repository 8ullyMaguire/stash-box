//go:build integration

package api_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service"
	"github.com/stashapp/stash-box/internal/service/elo"
	listsvc "github.com/stashapp/stash-box/internal/service/list"
	"github.com/stashapp/stash-box/internal/service/review"
)

// Phase 3's own acceptance criterion: a content item takes tags, a review, an elo rating
// and a list entry "with no new plumbing".
//
// That claim is the whole justification for one `content_item` enum member instead of five
// entity types, so it is worth testing as a claim rather than assuming it. The plumbing it
// depends on is three CLOSED VOCABULARIES in Go, each of which refuses a type it does not
// know -- review.validate's switch, list.validEntityType, and elo's AllEntityTypes -- and a
// closed vocabulary that has not been extended does not fail loudly in a test like this one:
// it fails as a REFUSAL, which looks exactly like "the caller did something wrong".
//
// So every claim below is asserted as a PAIR: a bogus type produces the vocabulary's refusal
// (proving the refusal exists and this test would see it), and the content-item type does
// not. An assertion that only checked "no error" would pass against a vocabulary that never
// refused anything.

const bogusEntityType = "NOT_A_CONTENT_KIND"

func TestAContentItemIsClaimedLikeEveryOtherEntity(t *testing.T) {
	ctx := t.Context()
	q := contentItemQ()
	factory := service.NewFactory(testutil.DB(), nil)

	item := newContentItem(t, "text", "An item claimed like any other")

	// --- 1. tags: the existing link table, the existing statements, nothing new ---
	tag := createTagForQuest(t, "phase-three-claimable", false)
	require.NoError(t, q.LinkContentItemTag(ctx, queries.LinkContentItemTagParams{
		ContentItemID: item.ID,
		TagID:         tag,
	}))

	tags, err := q.ListContentItemTags(ctx, item.ID)
	require.NoError(t, err)
	require.Len(t, tags, 1, "the tag linked to this item, and only it")
	assert.Equal(t, tag, tags[0].ID)

	// --- 2. reviews: the validator is the thing that has to accept the new kind ---
	author := newUUID(t)
	bogus, err := factory.Review().Submit(ctx, review.SubmitInput{
		AuthorID:   author,
		EntityType: bogusEntityType,
		EntityID:   item.ID,
		Body:       "a review of nothing",
	})
	require.Error(t, err, "the review vocabulary must refuse a type it does not know")
	assert.Contains(t, err.Error(), "cannot review entity type",
		"the refusal must be the vocabulary's own, otherwise the assertion below proves nothing")
	assert.Nil(t, bogus)

	// A random author id, so the write fails at the foreign key rather than at the
	// vocabulary -- which is exactly what makes this assertion about the vocabulary: the
	// only way to see the type refusal here would be for the type to be unknown.
	_, err = factory.Review().Submit(ctx, review.SubmitInput{
		AuthorID:   author,
		EntityType: review.EntityContentItem,
		EntityID:   item.ID,
		Body:       "a review of a content item",
		Rating:     ptrInt(4),
	})
	if err != nil {
		assert.NotContains(t, err.Error(), "cannot review entity type",
			"a content item must be reviewable: the vocabulary refused the new kind")
	}

	// --- 3. lists: same shape, same pair ---
	lists := factory.List()
	_, err = lists.AddItem(ctx, newUUID(t), item.ID, newUUID(t), bogusEntityType, 0)
	require.ErrorIs(t, err, listsvc.ErrInvalidEntityType,
		"the list vocabulary must refuse a type it does not know")

	_, err = lists.AddItem(ctx, newUUID(t), item.ID, newUUID(t), listsvc.EntityContentItem, 0)
	if err != nil {
		assert.NotErrorIs(t, err, listsvc.ErrInvalidEntityType,
			"a content item must be addable to a list: the vocabulary refused the new kind")
	}

	// --- 4. elo: the vocabulary accepts the type, and says so at the boundary ---
	_, err = factory.Elo().Leaderboard(ctx, elo.EntityType(bogusEntityType), 5)
	require.ErrorIs(t, err, elo.ErrUnknownEntityType,
		"the elo vocabulary must refuse a type it does not know")

	board, err := factory.Elo().Leaderboard(ctx, elo.EntityContentItem, 5)
	require.NotErrorIs(t, err, elo.ErrUnknownEntityType,
		"a content item must be rankable: the vocabulary refused the new kind")
	if err == nil {
		assert.Empty(t, board.Entries,
			"no content item has been voted on in this database, so the board must be empty rather than inherited from another type")
	}
}

// The spellings are not unified, and this is where that is recorded rather than discovered.
//
// `reviews.entity_type` and `list_items.entity_type` hold UPPERCASE (`PERFORMER`,
// `CONTENT_ITEM`) while `elo_ratings.entity_type` holds lowercase (`performer`,
// `content_item`) and the completion vocabulary holds lowercase too. A content item
// therefore has TWO stored spellings depending on which surface wrote it. That is
// pre-existing -- every other kind has both spellings already -- and unifying it needs a
// data migration across four tables and their indexes, which is not this slice. What this
// slice must not do is make it WORSE by picking a third spelling, so the constants are
// asserted here: change one of them and this test fails, pointing at the migration the
// change would require.
func TestTheContentItemSpellingsMatchTheirOwnVocabularies(t *testing.T) {
	assert.Equal(t, "CONTENT_ITEM", review.EntityContentItem,
		"reviews stores uppercase; a lowercase constant here would silently write a second spelling")
	assert.Equal(t, "CONTENT_ITEM", listsvc.EntityContentItem,
		"list_items stores uppercase")
	assert.Equal(t, "content_item", string(elo.EntityContentItem),
		"elo_ratings stores lowercase")
}
