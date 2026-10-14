//go:build integration

package api_test

import (
	"strings"
	"testing"
	"time"

	"github.com/99designs/gqlgen/client"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/queries"
)

// Social discovery over GraphQL (feature-discovery SPEC section 10, phase 7 slice 7).
//
// The unit tests in internal/service/follow and .../activity cover what is pure: the visibility
// levels, the unpublished-list rule, the merge order. This file proves what only a database can:
// that the DEFAULT GRANT IS PRIVATE and that a row nobody has touched publishes nothing, that a
// grant reaches exactly the people it names and no one else, that the feed is ordered by the
// timestamp rather than by insertion, and that a follower cannot widen their own access.
//
// THE ABSENCE ASSERTIONS ALL COUNT THEIR SUBJECT FIRST, which is the rule this repo learned the
// expensive way: "it is not in the feed" is vacuous when the feed is empty for an unrelated
// reason. Every "this is not visible" below is preceded by a "and here it IS visible" through the
// same path, so the test can only pass if the visibility rule is what removed it.

const activityFeedQuery = `
	query($input: ActivityFeedInput) {
		activityFeed(input: $input) {
			kind
			createdAt
			actor { id name }
			scene { id title }
			listId
			listName
			rating
		}
	}
`

type activityRow struct {
	Kind string
	// A STRING, because that is what the Time scalar is on the wire: the gqlgen client decodes
	// JSON, so a time.Time field fails to unmarshal. Parsed where it is compared, following the
	// convention the identification tests set.
	CreatedAt string
	Actor     struct {
		ID   string
		Name string
	}
	Scene struct {
		ID    string
		Title string
	}
	ListID   *string
	ListName *string
	Rating   *int
}

func activityFeed(t *testing.T, runner *testRunner, input map[string]any) []activityRow {
	t.Helper()
	var resp struct {
		ActivityFeed []activityRow
	}
	runner.client.MustPost(activityFeedQuery, &resp, client.Var("input", input))
	return resp.ActivityFeed
}

// NOTE on `asNone(t)`: the role-less user is used in this file as a FOLLOW PAIR PARTNER and as a
// READER rather than as an author. The integration database is shared, so a pair or an author that
// no other case touches is the only way an assertion about a grant can be about THIS case's grant.

func activityTitles(rows []activityRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Scene.Title)
	}
	return out
}

func activityKinds(rows []activityRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Kind)
	}
	return out
}

// activityFixture makes one scene for the feed under test, with a title unique to the case so a
// row can be identified by name in the assertions.
//
// THE CASE NAME IS THE FILTER, and every assertion below runs on `rowsFor(feed, caseName)` rather
// than on the whole feed. The feed is INSTANCE-WIDE and the integration database is shared with
// every other test in this package, so "the feed has one row" is only a statement about the rows
// this case created. Filtering is not tidiness: without it these tests pass or fail according to
// which other tests ran first, which is worse than failing.
func activityFixture(t *testing.T, caseName, label string) uuid.UUID {
	t.Helper()
	return similarSceneFixture(t, "Social "+caseName+" "+label, similarSceneOptions{})
}

// rowsFor narrows a feed page to the rows one case created.
func rowsFor(rows []activityRow, caseName string) []activityRow {
	prefix := "Social " + caseName + " "
	out := make([]activityRow, 0, len(rows))
	for _, row := range rows {
		if strings.HasPrefix(row.Scene.Title, prefix) {
			out = append(out, row)
		}
	}
	return out
}

// backdate moves an activity's timestamp back, so the test can prove the ORDER is chronological
// rather than the insertion order. Without this, three rows inserted milliseconds apart are
// already in timestamp order and "the feed is chronological" would be untested -- a test that
// passes because the fixture happened to be written in order proves nothing about the sort.
func backdate(t *testing.T, table, column string, id uuid.UUID, by time.Duration) {
	t.Helper()
	ctx := t.Context()
	_, err := dbtest.DB().Exec(ctx,
		"UPDATE "+table+" SET created_at = created_at - $2::interval WHERE "+column+" = $1",
		id, by.String())
	require.NoError(t, err, "backdating %s", table)
}

// followAs follows someone through the real mutation, which is the path a client takes.
func followAs(t *testing.T, runner *testRunner, followed uuid.UUID) {
	t.Helper()
	var resp struct {
		FollowUser bool
	}
	runner.client.MustPost(`mutation($input: FollowInput!) { followUser(input: $input) }`,
		&resp, client.Var("input", map[string]any{"userId": followed.String()}))
	require.True(t, resp.FollowUser, "following must succeed")
}

// grantAs sets one follower's visibility on the CURRENT user's activity, through the mutation, as
// the author.
func grantAs(t *testing.T, runner *testRunner, follower uuid.UUID, visibility string) {
	t.Helper()
	var resp struct {
		SetActivityVisibility bool
	}
	runner.client.MustPost(
		`mutation($input: ActivityVisibilityInput!) { setActivityVisibility(input: $input) }`,
		&resp,
		client.Var("input", map[string]any{"followerId": follower.String(), "visibility": visibility}))
	require.True(t, resp.SetActivityVisibility, "granting visibility must succeed")
}

// THE WHOLE FEATURE IN ONE CASE: a followed actor's activity appears, newest first, with the
// actor, the scene and the kind -- and the order is the TIMESTAMP, not the insertion order.
func TestActivityFeedShowsFollowedActorsNewestFirstByTimestamp(t *testing.T) {
	asAdmin(t)
	viewer := asRead(t)
	actor := asModerate(t)

	first := activityFixture(t, "Order", "First")
	second := activityFixture(t, "Order", "Second")
	third := activityFixture(t, "Order", "Third")

	// Three kinds, one per scene, inserted in fixture order.
	fingerprintFixture(t, userDB.moderate.ID, first, 1)
	fingerprintFixture(t, userDB.moderate.ID, second, 1)
	fingerprintFixture(t, userDB.moderate.ID, third, 1)

	// The subject, counted BEFORE the assertions about it: without this the rest could pass on an
	// empty feed.
	followAs(t, viewer, userDB.moderate.ID)
	grantAs(t, actor, userDB.read.ID, "FOLLOWERS")
	published := rowsFor(activityFeed(t, viewer, map[string]any{"limit": 50}), "Order")
	require.Len(t, published, 3, "the grant publishes all three to this follower: %v", activityTitles(published))

	// Now move the FIRST-inserted activity into the past, and require the feed to follow the
	// timestamps rather than the insertion order. The gaps are wide (30 and 90 minutes) so the
	// expected order cannot depend on the milliseconds between fixture inserts.
	backdate(t, "scene_fingerprints", "scene_id", second, 30*time.Minute)
	backdate(t, "scene_fingerprints", "scene_id", first, 90*time.Minute)

	rows := rowsFor(activityFeed(t, viewer, map[string]any{"limit": 50}), "Order")
	require.Len(t, rows, 3)
	assert.Equal(t, "Social Order Third", rows[0].Scene.Title,
		"the newest timestamp leads, whatever order the rows were written in")
	assert.Equal(t, "Social Order Second", rows[1].Scene.Title)
	assert.Equal(t, "Social Order First", rows[2].Scene.Title,
		"a row whose timestamp is older comes last even though it was inserted first")

	// And the field the order is on is descending, pairwise: the property, not one example. The
	// timestamps are parsed, because a string comparison of two RFC3339 stamps is only correct
	// while the layout stays fixed.
	for i := 1; i < len(rows); i++ {
		prev, err := time.Parse(time.RFC3339, rows[i-1].CreatedAt)
		require.NoError(t, err, "createdAt must be a parseable timestamp")
		cur, err := time.Parse(time.RFC3339, rows[i].CreatedAt)
		require.NoError(t, err, "createdAt must be a parseable timestamp")
		assert.False(t, cur.After(prev), "row %d is newer than the row before it", i)
	}

	// The actor is named, and it is the followed user rather than the viewer.
	assert.Equal(t, userDB.moderate.ID.String(), rows[0].Actor.ID)
	assert.NotEqual(t, userDB.read.ID.String(), rows[0].Actor.ID)
}

// THE DEFAULT: following someone grants nothing, so the feed stays empty. This is the assertion
// the whole visibility model exists for, and it is paired with the publish that fills it.
func TestActivityFeedIsEmptyUntilTheAuthorPublishes(t *testing.T) {
	asAdmin(t)
	viewer := asRead(t)
	actor := asEdit(t)

	// THE AUTHOR IS `edit`, and that is load-bearing: the feed is instance-wide and the database
	// is shared, so an author whom ANOTHER case publishes to a named follower (or to everyone)
	// arrives in this case's feed already visible. Each absence case in this file owns a distinct
	// (author -> viewer) pair for exactly that reason.
	scene := activityFixture(t, "Default", "Fingerprint")
	fingerprintFixture(t, userDB.edit.ID, scene, 1)

	followAs(t, viewer, userDB.edit.ID)

	// A row nobody has touched: 'private'.
	assert.Empty(t, rowsFor(activityFeed(t, viewer, map[string]any{"limit": 50}), "Default"),
		"following someone must not publish their activity")

	// THE SUBJECT, COUNTED: the same activity IS in the feed once the author says so, through the
	// mutation a real author would use. Without this the assertion above would pass just as well
	// if the fingerprint had never been written.
	grantAs(t, actor, userDB.read.ID, "FOLLOWERS")
	after := rowsFor(activityFeed(t, viewer, map[string]any{"limit": 50}), "Default")
	require.Len(t, after, 1, "the grant is what makes it visible, so the subject existed all along")
	assert.Equal(t, scene.String(), after[0].Scene.ID)
	assert.Equal(t, "FINGERPRINT", after[0].Kind)
}

// A grant named for one follower reaches that follower and nobody else.
func TestFollowersVisibilityReachesTheNamedFollowerAndNoOneElse(t *testing.T) {
	asAdmin(t)
	viewer := asRead(t)
	// The author is `modify` and the bystander `moderate`: neither is connected to the other by
	// any case in this file, so "the bystander cannot see it" is about the grant under test.
	actor := asModify(t)
	bystander := asModerate(t)

	scene := activityFixture(t, "Named", "Fingerprint")
	fingerprintFixture(t, userDB.modify.ID, scene, 1)

	followAs(t, viewer, userDB.modify.ID)
	grantAs(t, actor, userDB.read.ID, "FOLLOWERS")

	// The subject, counted: the named follower sees it.
	require.Len(t, rowsFor(activityFeed(t, viewer, map[string]any{"limit": 50}), "Named"), 1,
		"the follower the grant names must see the activity")

	// And the bystander, who follows nobody, must not. 'followers' is per follow, not per user.
	assert.Empty(t, rowsFor(activityFeed(t, bystander, map[string]any{"limit": 50}), "Named"),
		"a per-follow grant must not reach a user that follow does not name")
}

// A public grant broadcasts: it reaches readers who follow nobody at all.
func TestPublicGrantReachesReadersWhoFollowNobody(t *testing.T) {
	asAdmin(t)
	viewer := asRead(t)
	// The admin authors this case's activity and `modify` is the stranger. The admin is used
	// everywhere as the fixture-creator runner and nowhere else as an activity author, so the
	// broadcast below is the only thing that can publish these rows.
	actor := asAdmin(t)
	stranger := asModify(t)

	scene := activityFixture(t, "Public", "Fingerprint")
	fingerprintFixture(t, userDB.admin.ID, scene, 1)

	// The stranger follows nobody, and sees nothing yet: the subject counted before the grant.
	assert.Empty(t, rowsFor(activityFeed(t, stranger, map[string]any{"limit": 50}), "Public"))

	followAs(t, viewer, userDB.admin.ID)
	grantAs(t, actor, userDB.read.ID, "PUBLIC")

	// The named follower sees it...
	require.Len(t, rowsFor(activityFeed(t, viewer, map[string]any{"limit": 50}), "Public"), 1)
	// ...and so does the stranger, which is the whole difference between public and followers.
	require.Len(t, rowsFor(activityFeed(t, stranger, map[string]any{"limit": 50}), "Public"), 1,
		"public is a level about the ACTIVITY: it reaches readers the grant does not name")
}

// A FOLLOWER CANNOT WIDEN THEIR OWN ACCESS. This is the rule the private default rests on, and the
// test is written as an absence with its subject counted: the follower's mutation SUCCEEDS (it
// grants their activity to someone else) while their access to the author's activity is unchanged.
func TestAFollowerCannotWidenTheirOwnAccess(t *testing.T) {
	asAdmin(t)
	// ITS OWN PAIR: `read` already holds a grant on `moderate`'s activity from other cases in
	// this file, so reusing that pair here would fail for a reason that has nothing to do with
	// this rule.
	follower := asEdit(t)
	author := asModify(t)

	scene := activityFixture(t, "Widen", "Fingerprint")
	fingerprintFixture(t, userDB.modify.ID, scene, 1)

	followAs(t, follower, userDB.modify.ID)

	// The follower's activity is real, and the author will be the one who sees it: this is the
	// subject COUNTED, before the assertion below says it is not what the follower was after.
	ownerScene := activityFixture(t, "Widen", "Own")
	fingerprintFixture(t, userDB.edit.ID, ownerScene, 1)

	// The author follows the follower, so the follower has a follow row they ARE the author of.
	followAs(t, author, userDB.edit.ID)

	// The author's grant TO the follower, read before the follower's mutation. Captured rather
	// than assumed to be the default, so this case does not depend on what other cases did to
	// the same pair.
	beforeGrant, err := q().FindFollow(t.Context(), queries.FindFollowParams{
		FollowerID: userDB.edit.ID, FollowedID: userDB.modify.ID,
	})
	require.NoError(t, err)
	// And the follower publishes their own activity to the author. Legitimate: they own that
	// decision. The claim this test refutes is that it says anything about the OTHER grant.
	grantAs(t, follower, userDB.modify.ID, "PUBLIC")

	// The row the follower changed is theirs: the author now sees the follower's fingerprint.
	require.Len(t, rowsFor(activityFeed(t, author, map[string]any{"limit": 50}), "Widen"), 1,
		"the follower's own grant took effect, so the mutation really ran")

	// And the follower's view of the author is UNCHANGED: the author never published anything.
	assert.Empty(t, rowsFor(activityFeed(t, follower, map[string]any{"limit": 50}), "Widen"),
		"setting your own visibility must not publish someone else's activity to you")

	// The two rows, read directly, so the difference is visible in the data rather than inferred
	// from two feeds: the follower's grant is public, the author's grant to the follower is still
	// the default.
	followerGrant, err := q().FindFollow(t.Context(), queries.FindFollowParams{
		FollowerID: userDB.modify.ID, FollowedID: userDB.edit.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, "public", followerGrant.ActivityVisibility)
	afterGrant, err := q().FindFollow(t.Context(), queries.FindFollowParams{
		FollowerID: userDB.edit.ID, FollowedID: userDB.modify.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, beforeGrant.ActivityVisibility, afterGrant.ActivityVisibility,
		"the follower's mutation must not reach the author's grant, whatever that grant was")
}

// The list name is reported only for a PUBLISHED list: the activity is the scene, but which list
// it went into is the owner's until they publish it.
func TestFeedReportsAListNameOnlyForPublishedLists(t *testing.T) {
	asAdmin(t)
	viewer := asRead(t)
	actor := asModerate(t)

	privateScene := activityFixture(t, "List", "Private")
	publishedScene := activityFixture(t, "List", "Published")

	listFixture(t, userDB.moderate.ID, privateScene)
	publishedList := listFixtureWithName(t, userDB.moderate.ID, publishedScene, "Social Published List")
	_, err := q().PublishList(t.Context(), queries.PublishListParams{
		ID:          publishedList,
		PublishedBy: uuid.NullUUID{UUID: userDB.moderate.ID, Valid: true},
	})
	require.NoError(t, err, "publishing the list")

	followAs(t, viewer, userDB.moderate.ID)
	grantAs(t, actor, userDB.read.ID, "FOLLOWERS")

	rows := rowsFor(activityFeed(t, viewer, map[string]any{"limit": 50}), "List")
	require.Len(t, rows, 2, "both additions are activity: %v", activityTitles(rows))

	byTitle := map[string]activityRow{}
	for _, row := range rows {
		byTitle[row.Scene.Title] = row
	}

	privateRow, ok := byTitle["Social List Private"]
	require.True(t, ok)
	assert.Nil(t, privateRow.ListName,
		"which list someone filed a scene into is theirs until they publish the list")
	assert.Nil(t, privateRow.ListID)

	publishedRow, ok := byTitle["Social List Published"]
	require.True(t, ok)
	require.NotNil(t, publishedRow.ListName, "a published list is named")
	assert.Equal(t, "Social Published List", *publishedRow.ListName)
	require.NotNil(t, publishedRow.ListID)
	assert.Equal(t, publishedList.String(), *publishedRow.ListID)
}

// Following is idempotent and grants nothing: two calls, one row, still private.
func TestFollowingIsIdempotentAndGrantsNothing(t *testing.T) {
	asAdmin(t)
	// A pair no other case follows or grants, so "still private" is about this case's follows
	// rather than about whichever case happened to run before it.
	viewer := asEdit(t)

	scene := activityFixture(t, "Idempotent", "Fingerprint")
	fingerprintFixture(t, userDB.modify.ID, scene, 1)

	followAs(t, viewer, userDB.modify.ID)
	followAs(t, viewer, userDB.modify.ID)

	rows, err := q().FindFollowers(t.Context(), userDB.modify.ID)
	require.NoError(t, err)
	count := 0
	for _, row := range rows {
		if row.UserID == userDB.edit.ID {
			count++
			assert.Equal(t, "private", row.ActivityVisibility,
				"following records who watches whom; it grants nothing")
		}
	}
	assert.Equal(t, 1, count, "a second follow is the same relationship, not a second row")
}

// Following yourself is refused, and unfollowing someone you do not follow is not an error.
func TestFollowRefusesSelfAndUnfollowIsHonest(t *testing.T) {
	asAdmin(t)
	// Again a pair of its own: an earlier case following the same target would make the first
	// unfollow below a real removal, and the assertion is about there having been nothing.
	viewer := asEdit(t)
	target := userDB.admin.ID

	var selfResp struct {
		FollowUser bool
	}
	err := viewer.client.Post(`mutation($input: FollowInput!) { followUser(input: $input) }`,
		&selfResp, client.Var("input", map[string]any{"userId": userDB.edit.ID.String()}))
	require.Error(t, err, "following yourself must fail")

	var unfollowResp struct {
		UnfollowUser bool
	}
	viewer.client.MustPost(`mutation($input: UnfollowInput!) { unfollowUser(input: $input) }`,
		&unfollowResp, client.Var("input", map[string]any{"userId": target.String()}))
	assert.False(t, unfollowResp.UnfollowUser, "there was no follow to remove, and that is not an error")

	// And a real unfollow reports true and removes the relationship: the subject, counted.
	followAs(t, viewer, target)
	viewer.client.MustPost(`mutation($input: UnfollowInput!) { unfollowUser(input: $input) }`,
		&unfollowResp, client.Var("input", map[string]any{"userId": target.String()}))
	assert.True(t, unfollowResp.UnfollowUser)
	following, err := q().FindFollowing(t.Context(), userDB.edit.ID)
	require.NoError(t, err)
	for _, row := range following {
		assert.NotEqual(t, target, row.UserID, "the relationship is gone")
	}
}

// The viewer's own activity is not in the viewer's feed, even when the viewer has published it.
func TestFeedExcludesTheViewersOwnActivity(t *testing.T) {
	asAdmin(t)
	viewer := asRead(t)

	ownScene := activityFixture(t, "Own", "Fingerprint")
	fingerprintFixture(t, userDB.read.ID, ownScene, 1)

	actor := asModerate(t)
	// The viewer publishes their own activity to everyone, which is what would otherwise make
	// them an actor in their own feed. A follow row has to exist for the grant, so the author
	// follows the viewer first.
	followAs(t, actor, userDB.read.ID)
	grantAs(t, viewer, userDB.moderate.ID, "PUBLIC")

	// Counted: another reader DOES see it, so the row is genuinely visible.
	bystander := asEdit(t)
	require.Len(t, rowsFor(activityFeed(t, bystander, map[string]any{"limit": 50}), "Own"), 1,
		"the broadcast works, so the row is visible to someone")

	assert.Empty(t, rowsFor(activityFeed(t, viewer, map[string]any{"limit": 50}), "Own"),
		"a feed of the people you follow is not a list of what you did")
}

// The feed is one kind of activity per row, with the kind reported, and a review's rating only
// when it has one.
func TestFeedReportsEachKindWithItsOwnFields(t *testing.T) {
	asAdmin(t)
	viewer := asRead(t)
	actor := asModerate(t)

	fpScene := activityFixture(t, "Kinds", "Fingerprint")
	reviewScene := activityFixture(t, "Kinds", "Review")
	unratedScene := activityFixture(t, "Kinds", "Unrated")
	withdrawnScene := activityFixture(t, "Kinds", "Withdrawn")

	fingerprintFixture(t, userDB.moderate.ID, fpScene, 1)
	reviewFixture(t, userDB.moderate.ID, reviewScene, forYouIntPtr(5), "published")
	reviewFixture(t, userDB.moderate.ID, unratedScene, nil, "published")
	// A WITHDRAWAL is not "added this scene": it is someone saying they do NOT have it.
	fingerprintFixture(t, userDB.moderate.ID, withdrawnScene, -1)

	followAs(t, viewer, userDB.moderate.ID)
	grantAs(t, actor, userDB.read.ID, "FOLLOWERS")

	rows := rowsFor(activityFeed(t, viewer, map[string]any{"limit": 50}), "Kinds")
	titles := activityTitles(rows)
	assert.NotContains(t, titles, "Social Kinds Withdrawn",
		"a withdrawn fingerprint is a signal but not the activity \"added this scene\"")

	byTitle := map[string]activityRow{}
	for _, row := range rows {
		byTitle[row.Scene.Title] = row
	}
	require.Len(t, rows, 3, "one fingerprint and two reviews: %v", activityKinds(rows))

	fp := byTitle["Social Kinds Fingerprint"]
	assert.Equal(t, "FINGERPRINT", fp.Kind)
	assert.Nil(t, fp.Rating, "a fingerprint has no rating")

	rated := byTitle["Social Kinds Review"]
	assert.Equal(t, "REVIEW", rated.Kind)
	require.NotNil(t, rated.Rating)
	assert.Equal(t, 5, *rated.Rating)

	unrated := byTitle["Social Kinds Unrated"]
	assert.Equal(t, "REVIEW", unrated.Kind)
	assert.Nil(t, unrated.Rating, "an unrated review is a complete review, not missing data")
}

// A keyset cursor pages without repeating or skipping: the second page continues after the first.
func TestFeedPagesByKeysetCursor(t *testing.T) {
	asAdmin(t)
	viewer := asRead(t)
	actor := asModerate(t)

	for i := 0; i < 4; i++ {
		scene := activityFixture(t, "Page", string(rune('A'+i)))
		fingerprintFixture(t, userDB.moderate.ID, scene, 1)
		// A is now, B an hour ago, and so on: distinct timestamps with wide gaps, so the page
		// boundary is unambiguous and the expected order cannot depend on insert milliseconds.
		backdate(t, "scene_fingerprints", "scene_id", scene, time.Duration(i)*time.Hour)
	}

	// ROWS FROM THE OTHER TWO ARMS, under a prefix of their own so they do not disturb the
	// assertion above. Without them this test exercises one arm, one arm can never fill more than
	// a page, and the merge's cut is untested: removing it left the test green, which is how this
	// was found. With all three arms populated a page really can overflow.
	listScene := activityFixture(t, "Breadth", "Listed")
	listFixture(t, userDB.moderate.ID, listScene)
	reviewScene := activityFixture(t, "Breadth", "Reviewed")
	reviewFixture(t, userDB.moderate.ID, reviewScene, forYouIntPtr(4), "published")

	followAs(t, viewer, userDB.moderate.ID)
	grantAs(t, actor, userDB.read.ID, "FOLLOWERS")

	// The case's own rows, in the order the timestamps put them: A is now and D is three hours
	// old, so the expected order cannot depend on the milliseconds between the inserts.
	full := activityFeed(t, viewer, map[string]any{"limit": 50})
	require.Equal(t, []string{"Social Page A", "Social Page B", "Social Page C", "Social Page D"},
		activityTitles(rowsFor(full, "Page")))

	// THE WALK. Two rows at a time, cursor = the last row's timestamp, until the feed runs out.
	// Walking the WHOLE feed rather than the case's four rows, because the feed is instance-wide
	// and the other cases' rows sit between these four in time.
	var (
		seen   []activityRow
		cursor *time.Time
	)
	for page := 0; page < 40; page++ {
		input := map[string]any{"limit": 2}
		if cursor != nil {
			input["before"] = *cursor
		}
		rows := activityFeed(t, viewer, input)
		if len(rows) == 0 {
			break
		}
		require.LessOrEqual(t, len(rows), 2,
			"a page is the size the caller asked for: the arms are limited and the merge cuts too")
		seen = append(seen, rows...)
		last, err := time.Parse(time.RFC3339, rows[len(rows)-1].CreatedAt)
		require.NoError(t, err)
		cursor = &last
	}

	require.GreaterOrEqual(t, len(seen), len(full),
		"the walk must reach at least as far as the single-page read")

	// NO ROW IS SHOWN TWICE, which is the property a keyset cursor exists for and an offset does
	// not have.
	seenScenes := map[string]int{}
	for _, row := range seen {
		seenScenes[row.Scene.ID]++
	}
	for scene, count := range seenScenes {
		assert.Equal(t, 1, count, "scene %s was returned by %d pages", scene, count)
	}

	// AND THE ORDER IS CONTINUOUS ACROSS PAGE BOUNDARIES: the case's four rows come out in the
	// same sequence however many pages they were split over.
	assert.Equal(t, []string{"Social Page A", "Social Page B", "Social Page C", "Social Page D"},
		activityTitles(rowsFor(seen, "Page")))

	// Strictly older each page: the cursor is a boundary, not a filter that can go backwards.
	for i := 1; i < len(seen); i++ {
		prev, err := time.Parse(time.RFC3339, seen[i-1].CreatedAt)
		require.NoError(t, err)
		cur, err := time.Parse(time.RFC3339, seen[i].CreatedAt)
		require.NoError(t, err)
		assert.False(t, cur.After(prev), "row %d is newer than the row before it", i)
	}
}

// listFixtureWithName creates a list with a name the test can assert on. listFixture above makes
// its own name unique; this one takes the name so the feed's report of it can be checked exactly.
func listFixtureWithName(t *testing.T, ownerID, sceneID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	listID := uuid.Must(uuid.NewV7())
	_, err := q().CreateList(t.Context(), queries.CreateListParams{
		ID:        listID,
		OwnerID:   ownerID,
		Name:      name,
		OwnerName: "social-fixture",
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
	return listID
}
