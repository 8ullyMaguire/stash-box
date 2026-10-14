//go:build integration

package api_test

import (
	"strconv"
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/queries"
)

// For You over GraphQL (feature-discovery SPEC section 9, phase 7 slice 6).
//
// The unit tests in internal/service/taste prove the mix: the stride, the subset property at
// serendipity 0, the contrast at 1. This file proves what pure functions cannot reach -- that
// the affinity SQL reads the signals it claims to (fingerprint vote and its sign, list add,
// review rating and its STATUS), that the hierarchy rule of section 9 holds on real rows
// (a parent tag scores more than nothing and less than a direct hit), that the feed never
// offers back what the viewer has already engaged with, and that `query` really is the existing
// query language rather than a second implementation that would drift from it.
//
// EVERY TEST FILTERS ITS OWN POOL, and that is not decoration. The tests share one database AND
// one viewer (userDB.read), so the affinity ranking is global: an earlier test's fixtures are
// candidates for a later test's feed, and the page is the top `limit` of a pool of up to 600.
// The first version of this file asserted against the unfiltered feed and passed alone, then
// failed three of its eight cases in the full suite -- the fixtures were not in the top page,
// because other tests had given the shared viewer MORE affinity than they had. Each test therefore
// tags every scene it creates with a unique `filter` tag and passes it as `query`, so the pool is
// the fixture and the assertions are about the fixture. Tag names are single tokens because a bare
// word in the query language is a tag: a name with a space would be read as two.
//
// THE FILTER TAG IS ON EVERY FIXTURE SCENE EXCEPT THE ONE THE VIEWER SIGNALS, and that is a claim
// about the feature rather than about tidiness: affinity is computed from the tags of the scenes a
// user has engaged with, so a tag carried by a fingerprinted scene IS a signal, and a filter tag on
// the signal source gives every candidate +1.0 and appears in every contribution list. The first
// version of these tests did exactly that and asserted a weight of 1 where the feed correctly said
// 2. The filter narrows the pool; it must not also be part of the taste.
//
// (The same session found the mirror-image problem: these fixtures add thirty-odd tags whose names
// sort before `testtag...`, which pushed TestQueryTags' tags out of its unfiltered page-1 window.
// That test now looks its tags up by name.)

const forYouQuery = `
	query($input: ForYouInput) {
		forYou(input: $input) {
			scene { id title }
			affinity
			contributions { tag { id name } weight }
		}
	}
`

type forYouContribution struct {
	Tag struct {
		ID   string
		Name string
	}
	Weight float64
}

type forYouRow struct {
	Scene struct {
		ID    string
		Title string
	}
	Affinity      float64
	Contributions []forYouContribution
}

func forYou(t *testing.T, runner *testRunner, input map[string]any) []forYouRow {
	t.Helper()
	var resp struct {
		ForYou []forYouRow
	}
	runner.client.MustPost(forYouQuery, &resp, client.Var("input", input))
	return resp.ForYou
}

func forYouTitles(rows []forYouRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Scene.Title)
	}
	return out
}

// forYouRowFor returns the row for a title, and whether it was in the page at all.
func forYouRowFor(rows []forYouRow, title string) (forYouRow, bool) {
	for _, r := range rows {
		if r.Scene.Title == title {
			return r, true
		}
	}
	return forYouRow{}, false
}

func contributionNames(row forYouRow) []string {
	out := make([]string, 0, len(row.Contributions))
	for _, c := range row.Contributions {
		out = append(out, c.Tag.Name)
	}
	return out
}

// fingerprintFixture records a vote from a user on a scene.
//
// The fingerprint row is created first because scene_fingerprints references it: the hash is
// what identifies "the same file" across users, and reusing one hash for two fixtures would
// collide on the fingerprint table.
var fingerprintHash int64 = 900000

func fingerprintFixture(t *testing.T, userID, sceneID uuid.UUID, vote int16) {
	t.Helper()
	fingerprintHash++
	fp, err := q().CreateFingerprint(t.Context(), queries.CreateFingerprintParams{
		Hash:      fingerprintHash,
		Algorithm: "md5",
	})
	require.NoError(t, err, "creating the fingerprint")
	require.NoError(t, q().CreateOrReplaceFingerprint(t.Context(), queries.CreateOrReplaceFingerprintParams{
		FingerprintID: int(fp.ID),
		SceneID:       sceneID,
		UserID:        userID,
		Duration:      600,
		Vote:          vote,
	}), "recording the vote")
}

// forYouIntPtr is a local helper: the package already has an intPtr in another test file.
func forYouIntPtr(i int) *int { return &i }

// reviewFixture records a review. `rating` is nullable, and a null rating is the case that
// must contribute nothing rather than an invented 3.
func reviewFixture(t *testing.T, authorID, sceneID uuid.UUID, rating *int, status string) {
	t.Helper()
	_, err := q().CreateReview(t.Context(), queries.CreateReviewParams{
		ID:         uuid.Must(uuid.NewV7()),
		AuthorID:   authorID,
		EntityType: "scene",
		EntityID:   sceneID,
		Rating:     rating,
		Body:       "a review from the For You fixture",
		Verified:   false,
		Status:     status,
	})
	require.NoError(t, err, "creating a review")
}

// The name carries a counter because `lists_unique_name_per_owner` is a real constraint: two
// fixtures for the same viewer with the same name collide, and the second one is this test
// failing for a reason that has nothing to do with the feed.
var listFixtureCount int

func listFixture(t *testing.T, ownerID, sceneID uuid.UUID) {
	t.Helper()
	listFixtureCount++
	listID := uuid.Must(uuid.NewV7())
	_, err := q().CreateList(t.Context(), queries.CreateListParams{
		ID:        listID,
		OwnerID:   ownerID,
		Name:      "For You Fixture List " + strconv.Itoa(listFixtureCount),
		OwnerName: "for-you-fixture",
	})
	require.NoError(t, err, "creating the list")
	entityType := "scene"
	_, err = q().AddListItem(t.Context(), queries.AddListItemParams{
		ListID:     listID,
		EntityType: &entityType,
		EntityID:   uuid.NullUUID{UUID: sceneID, Valid: true},
		Position:   0,
	})
	require.NoError(t, err, "adding the list item")
}

// A signal on a scene raises the affinity of the OTHER scenes carrying what that scene is
// tagged with, and the feed says which tag did it.
func TestForYouRanksByTheViewersSignalsAndNamesThem(t *testing.T) {
	asAdmin(t)
	read := asRead(t)
	viewer := userDB.read.ID

	filter := similarTagFixture(t, "foryou_ranks")
	likedTag := similarTagFixture(t, "foryou_ranks_signal")
	otherTag := similarTagFixture(t, "foryou_ranks_other")

	source := similarSceneFixture(t, "For You Signal Source", similarSceneOptions{tags: []uuid.UUID{likedTag}})
	similarSceneFixture(t, "For You Expected First", similarSceneOptions{tags: []uuid.UUID{filter, likedTag}})
	similarSceneFixture(t, "For You Expected Unranked", similarSceneOptions{tags: []uuid.UUID{filter, otherTag}})

	fingerprintFixture(t, viewer, source, 1)

	rows := forYou(t, read, map[string]any{"limit": 50, "query": "foryou_ranks"})

	first, ok := forYouRowFor(rows, "For You Expected First")
	require.True(t, ok, "a scene tagged like the one the viewer own must be in the feed: %v", forYouTitles(rows))
	assert.Equal(t, 1.0, first.Affinity, "a fingerprint vote is the whole of the weight (SPEC 9)")
	assert.Equal(t, []string{"foryou_ranks_signal"}, contributionNames(first),
		"the affinity has to name the tag it came from: it is the answer to why am I seeing this")

	unranked, ok := forYouRowFor(rows, "For You Expected Unranked")
	require.True(t, ok, "a scene with nothing in common is still a scene the viewer may see")
	assert.Equal(t, 0.0, unranked.Affinity)
	assert.Empty(t, contributionNames(unranked), "no affinity means no reason, not a zero-weight reason")

	// The DEFAULT page size, with no limit in the input at all, must also work: the schema's
	// default is only applied when the argument is absent, which is the path a real client takes.
	defaultRows := forYou(t, read, map[string]any{"query": "foryou_ranks"})
	require.NotEmpty(t, defaultRows, "the absence of a limit must not mean the absence of a feed")
}

// Section 9's acceptance criterion, on real rows: propagation raises the parent's affinity but
// by less than the direct weight.
func TestForYouPropagatesToTheParentAtAFractionOfTheDirectWeight(t *testing.T) {
	asAdmin(t)
	read := asRead(t)
	viewer := userDB.read.ID

	filter := similarTagFixture(t, "foryou_ontology")
	child := similarTagFixture(t, "foryou_ontology_child")
	parent := similarTagFixture(t, "foryou_ontology_parent")
	require.NoError(t, q().CreateTagImplication(t.Context(), queries.CreateTagImplicationParams{
		TagID:        child,
		ImpliedTagID: parent,
	}), "child implies parent")

	source := similarSceneFixture(t, "For You Ontology Source", similarSceneOptions{tags: []uuid.UUID{child}})
	similarSceneFixture(t, "For You Ontology Direct", similarSceneOptions{tags: []uuid.UUID{filter, child}})
	similarSceneFixture(t, "For You Ontology Inherited", similarSceneOptions{tags: []uuid.UUID{filter, parent}})

	fingerprintFixture(t, viewer, source, 1)

	rows := forYou(t, read, map[string]any{"limit": 50, "query": "foryou_ontology"})

	direct, ok := forYouRowFor(rows, "For You Ontology Direct")
	require.True(t, ok, "the direct hit must be in the feed: %v", forYouTitles(rows))
	inherited, ok := forYouRowFor(rows, "For You Ontology Inherited")
	require.True(t, ok, "the parent-tagged scene must be in the feed: %v", forYouTitles(rows))

	assert.Equal(t, 1.0, direct.Affinity, "the direct tag carries the whole weight")
	assert.Equal(t, 0.4, inherited.Affinity,
		"a tag that IMPLIES one the viewer likes is worth the propagation fraction (0.4) of it")
	assert.Greater(t, inherited.Affinity, 0.0)
	assert.Less(t, inherited.Affinity, direct.Affinity,
		"the parent claim is weaker than the child's: a scene the viewer never signalled must not "+
			"outrank one they did")
	assert.Equal(t, []string{"foryou_ontology_parent"}, contributionNames(inherited))
}

// Every kind of signal has its own weight, and the ones with no weight contribute nothing.
func TestForYouWeighsEachKindOfSignalAndIgnoresTheRest(t *testing.T) {
	asAdmin(t)
	read := asRead(t)
	viewer := userDB.read.ID

	filter := similarTagFixture(t, "foryou_weights")
	fingerprintTag := similarTagFixture(t, "foryou_weights_fingerprint")
	listTag := similarTagFixture(t, "foryou_weights_list")
	reviewTag := similarTagFixture(t, "foryou_weights_review")
	badReviewTag := similarTagFixture(t, "foryou_weights_bad_review")
	unratedTag := similarTagFixture(t, "foryou_weights_unrated")
	removedTag := similarTagFixture(t, "foryou_weights_removed")

	fpSource := similarSceneFixture(t, "For You Weight Fingerprint Source", similarSceneOptions{tags: []uuid.UUID{fingerprintTag}})
	listSource := similarSceneFixture(t, "For You Weight List Source", similarSceneOptions{tags: []uuid.UUID{listTag}})
	reviewSource := similarSceneFixture(t, "For You Weight Review Source", similarSceneOptions{tags: []uuid.UUID{reviewTag}})
	badReviewSource := similarSceneFixture(t, "For You Weight Bad Review Source", similarSceneOptions{tags: []uuid.UUID{badReviewTag}})
	unratedSource := similarSceneFixture(t, "For You Weight Unrated Source", similarSceneOptions{tags: []uuid.UUID{unratedTag}})
	removedSource := similarSceneFixture(t, "For You Weight Removed Source", similarSceneOptions{tags: []uuid.UUID{removedTag}})

	similarSceneFixture(t, "For You Weight Fingerprint Target", similarSceneOptions{tags: []uuid.UUID{filter, fingerprintTag}})
	similarSceneFixture(t, "For You Weight List Target", similarSceneOptions{tags: []uuid.UUID{filter, listTag}})
	similarSceneFixture(t, "For You Weight Review Target", similarSceneOptions{tags: []uuid.UUID{filter, reviewTag}})
	similarSceneFixture(t, "For You Weight Bad Review Target", similarSceneOptions{tags: []uuid.UUID{filter, badReviewTag}})
	similarSceneFixture(t, "For You Weight Unrated Target", similarSceneOptions{tags: []uuid.UUID{filter, unratedTag}})
	similarSceneFixture(t, "For You Weight Removed Target", similarSceneOptions{tags: []uuid.UUID{filter, removedTag}})

	fingerprintFixture(t, viewer, fpSource, 1)
	listFixture(t, viewer, listSource)
	reviewFixture(t, viewer, reviewSource, forYouIntPtr(5), "published")
	reviewFixture(t, viewer, badReviewSource, forYouIntPtr(1), "published")
	reviewFixture(t, viewer, unratedSource, nil, "published")
	reviewFixture(t, viewer, removedSource, forYouIntPtr(5), "removed")

	rows := forYou(t, read, map[string]any{"limit": 100, "query": "foryou_weights"})

	// The weights, and they are ordered the way the spec claims: owning beats filing, and
	// filing beats an opinion.
	fp, ok := forYouRowFor(rows, "For You Weight Fingerprint Target")
	require.True(t, ok)
	assert.Equal(t, 1.0, fp.Affinity, "a fingerprint vote is +1.0")

	li, ok := forYouRowFor(rows, "For You Weight List Target")
	require.True(t, ok)
	assert.Equal(t, 0.8, li.Affinity, "filing is weaker than owning")

	rev, ok := forYouRowFor(rows, "For You Weight Review Target")
	require.True(t, ok)
	assert.Equal(t, 0.5, rev.Affinity, "five stars is +0.5: half a fingerprint, because it is an opinion")

	bad, ok := forYouRowFor(rows, "For You Weight Bad Review Target")
	require.True(t, ok)
	assert.Equal(t, -0.5, bad.Affinity,
		"one star is NEGATIVE and must be reported as such: a feed that hides the negative half is "+
			"more confident than its data")
	assert.Equal(t, []string{"foryou_weights_bad_review"}, contributionNames(bad))

	unrated, ok := forYouRowFor(rows, "For You Weight Unrated Target")
	require.True(t, ok)
	assert.Equal(t, 0.0, unrated.Affinity,
		"an unrated review contributes NOTHING: rating is nullable because a review of a studio's "+
			"ethics is not a 1-to-5 quantity, and inventing a 3 would invent a preference")

	removed, ok := forYouRowFor(rows, "For You Weight Removed Target")
	require.True(t, ok)
	assert.Equal(t, 0.0, removed.Affinity,
		"a removed review must not shape a recommendation while the reader cannot see it")
}

// A withdrawal is a signal too: -1.0, and it is what makes the feed able to say "not this".
func TestForYouReportsAWithdrawnFingerprintAsANegativeAffinity(t *testing.T) {
	asAdmin(t)
	read := asRead(t)
	viewer := userDB.read.ID

	filter := similarTagFixture(t, "foryou_withdrawn")
	tag := similarTagFixture(t, "foryou_withdrawn_signal")
	source := similarSceneFixture(t, "For You Negative Source", similarSceneOptions{tags: []uuid.UUID{tag}})
	similarSceneFixture(t, "For You Negative Target", similarSceneOptions{tags: []uuid.UUID{filter, tag}})

	fingerprintFixture(t, viewer, source, -1)

	rows := forYou(t, read, map[string]any{"limit": 100, "query": "foryou_withdrawn"})
	target, ok := forYouRowFor(rows, "For You Negative Target")
	require.True(t, ok, "a scene with negative affinity is still a scene: the feed has to return it")
	assert.Equal(t, -1.0, target.Affinity)
	require.Len(t, target.Contributions, 1)
	assert.Equal(t, -1.0, target.Contributions[0].Weight)
}

// Recommending a scene back to the person who already has it is the failure this filter exists
// to prevent, and it covers all three signal kinds.
func TestForYouNeverOffersBackWhatTheViewerAlreadyEngagedWith(t *testing.T) {
	asAdmin(t)
	read := asRead(t)
	viewer := userDB.read.ID

	filter := similarTagFixture(t, "foryou_engaged")
	tag := similarTagFixture(t, "foryou_engaged_signal")
	owned := similarSceneFixture(t, "For You Engaged Owned", similarSceneOptions{tags: []uuid.UUID{tag}})
	listed := similarSceneFixture(t, "For You Engaged Listed", similarSceneOptions{tags: []uuid.UUID{filter, tag}})
	reviewed := similarSceneFixture(t, "For You Engaged Reviewed", similarSceneOptions{tags: []uuid.UUID{filter, tag}})
	similarSceneFixture(t, "For You Engaged Control", similarSceneOptions{tags: []uuid.UUID{filter, tag}})

	fingerprintFixture(t, viewer, owned, 1)
	listFixture(t, viewer, listed)
	reviewFixture(t, viewer, reviewed, forYouIntPtr(4), "published")

	rows := forYou(t, read, map[string]any{"limit": 100, "query": "foryou_engaged"})
	titles := forYouTitles(rows)
	assert.NotContains(t, titles, "For You Engaged Owned", "the viewer already owns it")
	assert.NotContains(t, titles, "For You Engaged Listed", "the viewer already filed it")
	assert.NotContains(t, titles, "For You Engaged Reviewed", "the viewer already reviewed it")
	assert.Contains(t, titles, "For You Engaged Control", "and everything else is unaffected")
}

// `query` is the query language, not a second search implementation: the exclusion a search
// supports has to work here, or the feed and the search can disagree about what exists.
func TestForYouTakesTheQueryLanguageAsItsFilter(t *testing.T) {
	asAdmin(t)
	read := asRead(t)
	viewer := userDB.read.ID

	keepTag := similarTagFixture(t, "foryoufilterkeep")
	dropTag := similarTagFixture(t, "foryoufilterdrop")

	filter := similarTagFixture(t, "foryoufilter")
	source := similarSceneFixture(t, "For You Filter Source", similarSceneOptions{tags: []uuid.UUID{keepTag}})
	similarSceneFixture(t, "For You Filter Kept", similarSceneOptions{tags: []uuid.UUID{filter, keepTag}})
	similarSceneFixture(t, "For You Filter Dropped", similarSceneOptions{tags: []uuid.UUID{filter, keepTag, dropTag}})

	fingerprintFixture(t, viewer, source, 1)

	rows := forYou(t, read, map[string]any{"limit": 100, "query": "foryoufilter -foryoufilterdrop"})
	titles := forYouTitles(rows)
	assert.Contains(t, titles, "For You Filter Kept")
	assert.NotContains(t, titles, "For You Filter Dropped",
		"`-tag` is the query language's own exclusion, and the feed lowers it with the same code")
}

// The degenerate-output test section 9 asks for, through the resolver: two contrasting settings
// of the parameter over the SAME SEED must not return near-identical pages. If serendipity did
// nothing -- or if the seed and the parameter were wired the wrong way round -- this fails.
func TestForYouSerendipityZeroIsTheAffinityOrderAndOneIsNot(t *testing.T) {
	asAdmin(t)
	read := asRead(t)
	viewer := userDB.read.ID

	filter := similarTagFixture(t, "foryou_serendipity")
	signalTag := similarTagFixture(t, "foryousignaltag")
	plainTag := similarTagFixture(t, "foryouplaintag")

	source := similarSceneFixture(t, "For You Serendipity Source", similarSceneOptions{tags: []uuid.UUID{signalTag}})
	fingerprintFixture(t, viewer, source, 1)

	// Enough of both kinds to fill a page of three either way.
	for i := 0; i < 4; i++ {
		similarSceneFixture(t, "For You Serendipity Signalled "+string(rune('A'+i)), similarSceneOptions{tags: []uuid.UUID{filter, signalTag}})
	}
	for i := 0; i < 6; i++ {
		similarSceneFixture(t, "For You Serendipity Plain "+string(rune('A'+i)), similarSceneOptions{tags: []uuid.UUID{filter, plainTag}})
	}

	serendipityInput := func(s float64, limit int) map[string]any {
		return map[string]any{"limit": limit, "serendipity": s, "seed": 7, "query": "foryou_serendipity"}
	}
	calm := forYou(t, read, serendipityInput(0, 3))
	require.Len(t, calm, 3)
	for _, row := range calm {
		assert.Greater(t, row.Affinity, 0.0,
			"serendipity 0 is the affinity order, so every row on the page is one the affinity "+
				"ranked: %v", forYouTitles(calm))
	}
	// The affinity order over this pool is deterministic among these four (all at 1.0, tied),
	// so the same call must return the same page.
	assert.Equal(t, forYouTitles(calm), forYouTitles(forYou(t, read, serendipityInput(0, 3))))

	surprising := forYou(t, read, serendipityInput(1, 3))
	require.Len(t, surprising, 3)

	shared := 0
	calmSet := map[string]struct{}{}
	for _, row := range calm {
		calmSet[row.Scene.Title] = struct{}{}
	}
	for _, row := range surprising {
		if _, ok := calmSet[row.Scene.Title]; ok {
			shared++
		}
	}
	assert.Less(t, float64(shared)/3.0, 0.5,
		"serendipity 1 must draw its page from outside the affinity order: %v vs %v",
		forYouTitles(calm), forYouTitles(surprising))
	assert.Equal(t, forYouTitles(surprising), forYouTitles(forYou(t, read, serendipityInput(1, 3))),
		"and it must be reproducible at the same seed, or the difference above proves nothing")

	// Defaults: no serendipity in the input is the documented 0.2, not 0 and not 1. The page
	// then contains at least one row from each half over a pool this size.
	defaulted := forYou(t, read, map[string]any{"limit": 5, "query": "foryou_serendipity"})
	require.Len(t, defaulted, 5)
	positivelyRanked := 0
	for _, row := range defaulted {
		if row.Affinity > 0 {
			positivelyRanked++
		}
	}
	assert.Contains(t, []int{4, 5}, positivelyRanked,
		"the default is one slot in five from outside the affinity order: %v", forYouTitles(defaulted))
}

// A viewer with no signals gets the archive's own order, not an arbitrary one, and no invented
// reasons. This is the first thing a new account sees.
func TestForYouForANewViewerIsTheArchiveOrderWithNoInventedReasons(t *testing.T) {
	asAdmin(t)
	fresh := asModerate(t)

	// The moderate user is not the one the other tests leave signals for, and the filter tag
	// exists only in this test: whatever else is in the database, this viewer has no signals on
	// anything here.
	filter := similarTagFixture(t, "foryou_new_viewer")
	similarSceneFixture(t, "For You New Viewer Scene", similarSceneOptions{tags: []uuid.UUID{filter}})

	rows := forYou(t, fresh, map[string]any{"limit": 10, "query": "foryou_new_viewer"})
	require.NotEmpty(t, rows, "a new account still sees scenes")
	for _, row := range rows {
		assert.Equal(t, 0.0, row.Affinity, "nothing is known about this viewer's taste")
		assert.Empty(t, row.Contributions, "so there is nothing to claim as a reason")
	}
}
