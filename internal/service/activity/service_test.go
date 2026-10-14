package activity

import (
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/models"
)

// The merge's rules, without a database. The queries decide WHO is visible; everything after that
// is here, and these tests hold the two claims a reader of the feed depends on: the order is the
// timestamp, and it is the same order for every reader of the same rows (a tie cannot be broken
// differently on the next page).

func item(actor uuid.UUID, kind Kind, at time.Time, scene uuid.UUID) Item {
	return Item{Kind: kind, CreatedAt: at, ActorID: actor, ActorName: "actor", Scene: &models.Scene{ID: scene}}
}

var (
	viewer   = uuid.Must(uuid.NewV4())
	otherA   = uuid.Must(uuid.NewV4())
	otherB   = uuid.Must(uuid.NewV4())
	sceneIDA = uuid.Must(uuid.NewV4())
	sceneIDB = uuid.Must(uuid.NewV4())
	sceneIDC = uuid.Must(uuid.NewV4())
)

func sceneIDs(items []Item) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(items))
	for _, it := range items {
		out = append(out, it.Scene.ID)
	}
	return out
}

func TestMergeOrdersByTimestampDescending(t *testing.T) {
	now := time.Now()
	older := now.Add(-2 * time.Hour)
	newest := now.Add(time.Hour)

	// Deliberately not in order, and with the middle row arriving from a different arm.
	merged := mergeItems([]Item{
		item(otherA, KindListAdd, older, sceneIDA),
		item(otherA, KindFingerprint, now, sceneIDB),
		item(otherB, KindReview, newest, sceneIDC),
	}, viewer, 10)

	assert.Equal(t, []uuid.UUID{sceneIDC, sceneIDB, sceneIDA}, sceneIDs(merged),
		"the newest activity leads: the feed is the timestamp, not the order the arms returned")
}

func TestMergeDropsTheViewersOwnActivity(t *testing.T) {
	now := time.Now()
	merged := mergeItems([]Item{
		item(viewer, KindFingerprint, now, sceneIDA),
		item(otherA, KindFingerprint, now.Add(-time.Minute), sceneIDB),
	}, viewer, 10)

	require.Len(t, merged, 1, "the viewer's own row is not 'someone you follow did something'")
	assert.Equal(t, sceneIDB, merged[0].Scene.ID)
}

func TestMergeTieBreakIsStableAndSelfConsistent(t *testing.T) {
	// SAME INSTANT, three rows. The tiebreak is arbitrary by necessity; what is not arbitrary is
	// that it does not vary between reads, because a row that moves between pages is a row a
	// reader sees twice or not at all.
	same := time.Now()
	rows := []Item{
		item(otherA, KindReview, same, sceneIDB),
		item(otherA, KindFingerprint, same, sceneIDA),
		item(otherA, KindFingerprint, same, sceneIDC),
	}

	first := mergeItems(append([]Item{}, rows...), viewer, 10)
	second := mergeItems([]Item{rows[1], rows[2], rows[0]}, viewer, 10)

	assert.Equal(t, sceneIDs(first), sceneIDs(second),
		"the same rows in a different arrival order must merge to the same feed order")

	// Kind is the first tiebreak (fingerprint < list add < review), so the review is last and the
	// two fingerprints come first. WHICH of the two leads is the scene id, ascending -- asserted
	// against the ids themselves rather than a fixed pair, because these ids are random per run
	// and an expected list here would be a coin flip.
	require.Len(t, first, 3)
	assert.Equal(t, KindReview, first[2].Kind, "the review sorts after the fingerprints")
	assert.Equal(t, KindFingerprint, first[0].Kind)
	assert.Equal(t, KindFingerprint, first[1].Kind)
	assert.Less(t, first[0].Scene.ID.String(), first[1].Scene.ID.String(),
		"ties are broken by scene id ascending: arbitrary, and the same answer every read")
}

func TestMergeCutsToThePage(t *testing.T) {
	now := time.Now()
	rows := []Item{
		item(otherA, KindFingerprint, now, sceneIDA),
		item(otherA, KindFingerprint, now.Add(-time.Minute), sceneIDB),
		item(otherA, KindFingerprint, now.Add(-2*time.Minute), sceneIDC),
	}

	merged := mergeItems(append([]Item{}, rows...), viewer, 2)
	require.Len(t, merged, 2, "the page is the size the caller asked for")
	assert.Equal(t, []uuid.UUID{sceneIDA, sceneIDB}, sceneIDs(merged), "and it is the NEWEST two")

	// The limit is applied after the viewer's own rows are dropped, so a page is not silently
	// short when the viewer's own activity would have filled it.
	mixed := append([]Item{item(viewer, KindFingerprint, now.Add(time.Hour), sceneIDC)}, rows...)
	afterFilter := mergeItems(mixed, viewer, 2)
	require.Len(t, afterFilter, 2, "the viewer's own row must not consume a slot on the page")
	assert.Equal(t, []uuid.UUID{sceneIDA, sceneIDB}, sceneIDs(afterFilter))
}

func TestHideUnpublishedList(t *testing.T) {
	// The named rule: the activity is "someone filed this scene", and WHICH list they filed it
	// into is theirs until they publish it.
	assert.True(t, HideUnpublishedList(false), "an unpublished list is not named in a feed")
	assert.False(t, HideUnpublishedList(true), "a published list is")
}

func TestFeedInputLimitIsBounded(t *testing.T) {
	// The clamping happens in Feed, which needs a database, so the constants are what is asserted
	// here: a page has a default and a ceiling, and the ceiling exists so one request cannot ask
	// the instance for its whole feed history.
	assert.Positive(t, defaultLimit)
	assert.Greater(t, maxLimit, defaultLimit)
}

// The content-tier filter (phase 5, migration 120). A table, because the mistake this guards
// against is a comparison: greater-than written the other way round lets every tiered item
// through and hides every untiered one, and both of those look like something else.
func TestTheTierFilterKeepsWhatIsAtOrBelowTheCeiling(t *testing.T) {
	below, at, above, untagged := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())

	item := func(id uuid.UUID) Item {
		return Item{Kind: KindFingerprint, Scene: &models.Scene{ID: id}}
	}
	items := []Item{item(below), item(at), item(above), item(untagged)}
	tiers := map[uuid.UUID]int16{below: 1, at: 2, above: 3}
	// `untagged` is deliberately absent from the map: the view LEFT JOINs, but a scene the
	// loader returned without a row here has no tags, and the map's zero value has to mean
	// "tier 0" rather than "keep" or "drop" by accident.

	kept := filterAboveCeiling(items, tiers, 2)

	require.Len(t, kept, 3, "an item ABOVE the ceiling is the one thing dropped")
	ids := make([]uuid.UUID, 0, len(kept))
	for _, k := range kept {
		ids = append(ids, k.Scene.ID)
	}
	assert.Contains(t, ids, below, "below the ceiling")
	assert.Contains(t, ids, at, "AT the ceiling: the comparison is strict")
	assert.Contains(t, ids, untagged, "no tier row means tier 0, which is inside every ceiling")
	assert.NotContains(t, ids, above, "above the ceiling")
}

func TestTheTierFilterIsNotAnItemFilter(t *testing.T) {
	// An item with no scene is kept whatever the ceiling: this function is about tiers, and
	// "has no scene" is not evidence about one. Dropping it here would be a feed silently
	// losing rows to a rule that does not mention them.
	kept := filterAboveCeiling([]Item{{Kind: KindReview}}, map[uuid.UUID]int16{}, 0)
	assert.Len(t, kept, 1)
}
