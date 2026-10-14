//go:build integration

package api_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/99designs/gqlgen/client"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbtest "github.com/stashapp/stash-box/internal/database/testutil"

	"github.com/stashapp/stash-box/internal/models"
)

// Content tiers on the page path (plan phase 5, migration 120).
//
// THE ABSENCE ASSERTION IS THE SUBJECT: "a tiered scene is absent from search" is
// vacuous unless the same query returns it when the tier is lifted, so every
// absence below is preceded by the scene being COUNTED in that exact query shape.
// This repository has already had an absence assertion pass with the thing on
// screen (`docs/track/WORKLOG.md`, the duplicated nav label), and a deleted-row
// bug and a tier gate produce the same empty page.
//
// The search is by TITLE, which names one scene in a database shared with every
// other test in the package: a page-wide assertion would be an assertion about
// the other tests' fixtures.

func tierProbeSearch(t *testing.T, runner *testRunner, title string) []uuid.UUID {
	t.Helper()
	// Executed through the GraphQL CLIENT, not by calling the resolver: the paginated
	// page is produced by querySceneResolver.Scenes, and queryResolver.QueryScenes returns
	// only the filter -- so a resolver-level call would assert against an empty struct.
	//
	// Page, PerPage, Sort and Direction are spelled out: a Go struct literal does not get
	// the schema's defaults, and `page: 0` is not "unset" but an offset of minus one page.
	result, err := runner.client.queryScenes(models.SceneQueryInput{
		Title:     &title,
		Page:      1,
		PerPage:   25,
		Sort:      models.SceneSortEnumCreatedAt,
		Direction: models.SortDirectionEnumDesc,
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	ids := make([]uuid.UUID, 0, len(result.Scenes))
	for _, scene := range result.Scenes {
		ids = append(ids, scene.UUID())
	}
	return ids
}

func TestATieredSceneIsAbsentFromSearchUntilTheViewerOptsIn(t *testing.T) {
	// The admin creates the fixture and classifies the tag: creating a tag needs the
	// CREATE role, and using the most privileged account for it is deliberate -- step
	// 4 asserts that even THIS account cannot see a tiered scene it has not consented
	// to, so no fixture role can be the thing that makes the absence meaningless.
	moderator := asAdmin(t)

	title := moderator.generateSceneName()
	tagName := moderator.generateSceneName() + " tier probe"
	tag, err := moderator.createTestTag(&models.TagCreateInput{Name: tagName})
	require.NoError(t, err)

	scene, err := moderator.createTestScene(&models.SceneCreateInput{
		Title:  &title,
		TagIds: []uuid.UUID{tag.UUID()},
	})
	require.NoError(t, err)
	sceneID := scene.UUID()

	// A viewer who has consented to nothing.
	//
	// STATED, not assumed: asRead(t) hands out ONE shared user for the whole package, so a consent
	// an earlier test set is still in place when this one runs -- which is how this test passed on
	// its own and failed in the suite. Every test below sets the ceiling it means to test.
	unconsented := asRead(t)
	_, err = unconsented.resolver.Mutation().UserSetVisibleTiers(unconsented.ctx, []int{})
	require.NoError(t, err)

	// STEP 1 -- COUNT THE SUBJECT. Before the tag is classified the scene is
	// visible to this exact viewer through this exact query, so the page below can
	// only be empty because of the tier.
	before := tierProbeSearch(t, unconsented, title)
	require.Len(t, before, 1, "the unclassified scene must be visible to a viewer with no consent: "+
		"without this the absence below would be indistinguishable from a deleted row")
	assert.Equal(t, sceneID, before[0])

	// Classify the tag. A moderator action: a tier is a judgement about what a
	// viewer sees by default, and it can only remove.
	classified, err := moderator.resolver.Mutation().TagSetContentTier(moderator.ctx, models.TagContentTierInput{
		ID:            tag.UUID(),
		Tier:          2,
		RequiresOptIn: false,
	})
	require.NoError(t, err)
	require.Equal(t, 2, classified.ContentTier)
	assert.False(t, classified.RequiresOptIn)

	// STEP 2 -- THE ABSENCE.
	assert.Empty(t, tierProbeSearch(t, unconsented, title),
		"a tier-2 scene must be absent for a viewer who has opted into no tiers")

	// STEP 3 -- THE CONTROL, and the reason step 2 is about the tier: a viewer who
	// has opted in sees it through the same query.
	consented := asRead(t)
	tiers, err := consented.resolver.Mutation().UserSetVisibleTiers(consented.ctx, []int{1, 2})
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2}, tiers, "the array is stored as sent, not collapsed to its maximum")

	stored, err := consented.resolver.Query().VisibleContentTiers(consented.ctx)
	require.NoError(t, err)
	assert.Equal(t, []int{1, 2}, stored)

	after := tierProbeSearch(t, consented, title)
	require.Len(t, after, 1, "opting into tier 2 must make a tier-2 scene visible")
	assert.Equal(t, sceneID, after[0])

	// STEP 4 -- NO ROLE SUBSTITUTES FOR CONSENT. The moderator who classified the
	// tag has consented to nothing, so they cannot see what they classified. This
	// is the asymmetry the whole mechanism rests on: a tier is denylist-shaped, so
	// privilege does not open it -- only the viewer's own opt-in does.
	assert.Empty(t, tierProbeSearch(t, moderator, title),
		"the account that classified the tag must not see the scene above its own ceiling: "+
			"no role may consent on the user's behalf")
}

func TestATierAboveTheCeilingStaysAbsent(t *testing.T) {
	moderator := asAdmin(t)

	title := moderator.generateSceneName()
	tagName := moderator.generateSceneName() + " tier 3 probe"
	tag, err := moderator.createTestTag(&models.TagCreateInput{Name: tagName})
	require.NoError(t, err)

	_, err = moderator.createTestScene(&models.SceneCreateInput{
		Title:  &title,
		TagIds: []uuid.UUID{tag.UUID()},
	})
	require.NoError(t, err)

	// A viewer who opted into tiers 1 and 2 but not 3. Consent is per tier, so a
	// ceiling of 2 must not be a key to everything -- which is what the ceiling
	// would become if the gate compared "consented at all" instead of the value.
	twoTier := asRead(t)
	_, err = twoTier.resolver.Mutation().UserSetVisibleTiers(twoTier.ctx, []int{1, 2})
	require.NoError(t, err)

	_, err = moderator.resolver.Mutation().TagSetContentTier(moderator.ctx, models.TagContentTierInput{
		ID:            tag.UUID(),
		Tier:          3,
		RequiresOptIn: true,
	})
	require.NoError(t, err)

	assert.Empty(t, tierProbeSearch(t, twoTier, title),
		"a tier-3 scene must stay absent for a viewer whose ceiling is 2")

	// And the same viewer with tier 3 added sees it, which is what makes the
	// assertion above about the ceiling rather than about a broken query.
	_, err = twoTier.resolver.Mutation().UserSetVisibleTiers(twoTier.ctx, []int{1, 2, 3})
	require.NoError(t, err)

	visible := tierProbeSearch(t, twoTier, title)
	require.Len(t, visible, 1, "opting into tier 3 must make a tier-3 scene visible")
}

// --- the similarity path: a RECOMMENDATION, which is where a tier matters most ---

type similarSceneOutput struct {
	Scene   sceneOutput `json:"scene"`
	Score   float64     `json:"score"`
	Reasons []struct {
		Kind string `json:"kind"`
	} `json:"reasons"`
}

func (c *graphqlClient) similarScenes(sceneID uuid.UUID, limit int) ([]similarSceneOutput, error) {
	q := `
	query SimilarScenes($id: ID!, $limit: Int) {
		findScene(id: $id) {
			id
			similarScenes(limit: $limit) {
				` + makeFragment(reflect.TypeFor[similarSceneOutput]()) + `
			}
		}
	}`

	var resp struct {
		FindScene struct {
			ID            string               `json:"id"`
			SimilarScenes []similarSceneOutput `json:"similarScenes"`
		} `json:"findScene"`
	}
	if err := c.Post(q, &resp, client.Var("id", sceneID), client.Var("limit", limit)); err != nil {
		return nil, err
	}
	return resp.FindScene.SimilarScenes, nil
}

// Strings, not uuids: an assertion failure prints this list, and `[]uuid.UUID{[16]uint8{0x1, ...}}`
// in a failure message hides the one thing the message is for.
func similarSceneIDs(scenes []similarSceneOutput) []string {
	ids := make([]string, 0, len(scenes))
	for _, s := range scenes {
		ids = append(ids, s.Scene.UUID().String())
	}
	return ids
}

// A similarity list is a RECOMMENDATION: the viewer did not ask for this scene and did not know
// it existed. Offering what they opted out of is the failure the tier exists to prevent, and it
// is worse here than in a search result they typed.
func TestSimilarScenesExcludesWhatTheViewerHasNotOptedInto(t *testing.T) {
	admin := asAdmin(t)

	titleA := admin.generateSceneName()
	titleB := admin.generateSceneName()
	tagName := admin.generateSceneName() + " similarity"
	tag, err := admin.createTestTag(&models.TagCreateInput{Name: tagName})
	require.NoError(t, err)

	// Two scenes sharing one tag: that shared fact is what makes B a candidate for A at all.
	subject, err := admin.createTestScene(&models.SceneCreateInput{
		Title: &titleA, TagIds: []uuid.UUID{tag.UUID()},
	})
	require.NoError(t, err)
	candidate, err := admin.createTestScene(&models.SceneCreateInput{
		Title: &titleB, TagIds: []uuid.UUID{tag.UUID()},
	})
	require.NoError(t, err)

	// The same shared user: consent to NOTHING first, explicitly, or this test inherits whatever
	// the test before it opted into.
	viewer := asRead(t)
	_, err = viewer.resolver.Mutation().UserSetVisibleTiers(viewer.ctx, []int{})
	require.NoError(t, err)

	// COUNT THE SUBJECT FIRST: before the tag is classified, the candidate is offered. Without
	// this, an empty list below would be indistinguishable from a similarity query that never
	// worked -- and an empty candidate pool is exactly what the tier predicate produces.
	before, err := viewer.client.similarScenes(subject.UUID(), 10)
	require.NoError(t, err)
	require.NotEmpty(t, before, "the subject must have a candidate to lose")
	assert.Contains(t, similarSceneIDs(before), candidate.UUID().String())

	_, err = admin.resolver.Mutation().TagSetContentTier(admin.ctx, models.TagContentTierInput{
		ID: tag.UUID(), Tier: 2, RequiresOptIn: false,
	})
	require.NoError(t, err)

	unconsented, err := viewer.client.similarScenes(subject.UUID(), 10)
	require.NoError(t, err)
	assert.Empty(t, similarSceneIDs(unconsented), "similar-to must not offer a scene above the viewer's ceiling")

	// THE CONTROL: the same query with consent returns the candidate, so the emptiness above is
	// the tier and not the query.
	_, err = viewer.resolver.Mutation().UserSetVisibleTiers(viewer.ctx, []int{1, 2})
	require.NoError(t, err)

	after, err := viewer.client.similarScenes(subject.UUID(), 10)
	require.NoError(t, err)
	require.NotEmpty(t, after, "opting into tier 2 must restore the candidate")
	assert.Contains(t, similarSceneIDs(after), candidate.UUID().String())
}

// --- the activity feed: the third enumerating path ---

type activityItemOutput struct {
	Kind  string `json:"kind"`
	Actor struct {
		ID string `json:"id"`
	} `json:"actor"`
	Scene struct {
		ID string `json:"id"`
	} `json:"scene"`
	ListID *string `json:"listId"`
}

func (c *graphqlClient) activityFeed(limit int) ([]activityItemOutput, error) {
	q := `
	query Feed($limit: Int) {
		activityFeed(input: {limit: $limit}) {
			` + makeFragment(reflect.TypeFor[activityItemOutput]()) + `
		}
	}`

	var resp struct {
		ActivityFeed []activityItemOutput `json:"activityFeed"`
	}
	if err := c.Post(q, &resp, client.Var("limit", limit)); err != nil {
		return nil, err
	}
	return resp.ActivityFeed, nil
}

// followSomeoneAndSignalTheirs makes the feed non-empty: a user the viewer follows gets a relative
// grant to their activity (PRIVATE is the default, so a follow alone shows nothing), and that user
// submits a fingerprint on `sceneID`.
//
// The actor is chosen in SQL rather than from a runner, because the actor has to be a DIFFERENT user
// from the viewer and the runners in this package are shared, so "not me" is the only property that
// matters.
func (r *testRunner) followSomeoneAndSignalTheirs(t *testing.T, viewerID, sceneID uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	var actorID uuid.UUID
	err := dbtest.DB().QueryRow(ctx, `
		INSERT INTO user_follows (follower_id, followed_id, activity_visibility)
		SELECT $1, u.id, 'public' FROM users u WHERE u.id <> $1 ORDER BY u.id LIMIT 1
		RETURNING followed_id`, viewerID).Scan(&actorID)
	require.NoError(t, err, "following a user, with a grant that makes their activity visible")

	// The feed's FINGERPRINT arm reads `scene_fingerprints` (an FK to `fingerprints`), which is why
	// the hash row exists: it is not decoration, and an insert without it fails on the constraint
	// before any tier is reached.
	var fingerprintID int
	err = dbtest.DB().QueryRow(ctx, `INSERT INTO fingerprints (hash, algorithm) VALUES ($1, 'PHASH') RETURNING id`,
		time.Now().UnixNano()).Scan(&fingerprintID)
	require.NoError(t, err, "creating the fingerprint row the submission points at")

	_, err = dbtest.DB().Exec(ctx, `
		INSERT INTO scene_fingerprints (fingerprint_id, scene_id, user_id, duration, vote)
		VALUES ($1, $2, $3, 0, 1)`, fingerprintID, sceneID, actorID)
	require.NoError(t, err, "recording the followed user's submission")
}

// The feed is the third path that ENUMERATES scenes for a viewer -- after search and similarity.
// Its filter is the one written in Go, so this is the test that proves the decision and the lookup
// actually meet: the pure table above cannot show that the wrapper is called at all.
func TestTheActivityFeedDropsWhatTheViewerHasNotOptedInto(t *testing.T) {
	admin := asAdmin(t)
	viewer := asRead(t)

	_, err := viewer.resolver.Mutation().UserSetVisibleTiers(viewer.ctx, []int{})
	require.NoError(t, err)

	title := admin.generateSceneName()
	tagName := admin.generateSceneName() + " feed tier"
	tag, err := admin.createTestTag(&models.TagCreateInput{Name: tagName})
	require.NoError(t, err)
	scene, err := admin.createTestScene(&models.SceneCreateInput{
		Title: &title, TagIds: []uuid.UUID{tag.UUID()},
	})
	require.NoError(t, err)

	viewer.followSomeoneAndSignalTheirs(t, userDB.read.ID, scene.UUID())

	// COUNT THE SUBJECT FIRST. The feed is empty for three uninteresting reasons -- the viewer
	// follows nobody, the grant is private, the scene is not published -- and every one of them
	// would make the absence below pass for a reason that is not the tier.
	before, err := viewer.client.activityFeed(20)
	require.NoError(t, err)
	require.Contains(t, feedSceneIDs(before), scene.UUID().String(),
		"the followed user's activity must be in the feed before anything is claimed about the tier")

	_, err = admin.resolver.Mutation().TagSetContentTier(admin.ctx, models.TagContentTierInput{
		ID: tag.UUID(), Tier: 2, RequiresOptIn: false,
	})
	require.NoError(t, err)

	after, err := viewer.client.activityFeed(20)
	require.NoError(t, err)
	assert.NotContains(t, feedSceneIDs(after), scene.UUID().String(),
		"the feed must not show a scene above the viewer's ceiling")

	// THE CONTROL: the same call with consent returns the row again.
	_, err = viewer.resolver.Mutation().UserSetVisibleTiers(viewer.ctx, []int{1, 2})
	require.NoError(t, err)

	consented, err := viewer.client.activityFeed(20)
	require.NoError(t, err)
	assert.Contains(t, feedSceneIDs(consented), scene.UUID().String(),
		"opting into tier 2 must put the row back, so the absence above is the tier and not the feed")
}

func feedSceneIDs(items []activityItemOutput) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.Scene.ID)
	}
	return ids
}

// --- For You: the fourth path, covered by construction and until now unasserted ---

type forYouSceneOutput struct {
	Scene struct {
		ID string `json:"id"`
	} `json:"scene"`
	Affinity float64 `json:"affinity"`
}

func (c *graphqlClient) forYou(limit int, serendipity float64) ([]forYouSceneOutput, error) {
	q := `
	query ForYou($limit: Int, $serendipity: Float) {
		forYou(input: {limit: $limit, serendipity: $serendipity}) {
			` + makeFragment(reflect.TypeFor[forYouSceneOutput]()) + `
		}
	}`

	var resp struct {
		ForYou []forYouSceneOutput `json:"forYou"`
	}
	if err := c.Post(q, &resp, client.Var("limit", limit), client.Var("serendipity", serendipity)); err != nil {
		return nil, err
	}
	return resp.ForYou, nil
}

func forYouSceneIDs(scenes []forYouSceneOutput) []string {
	ids := make([]string, 0, len(scenes))
	for _, s := range scenes {
		ids = append(ids, s.Scene.ID)
	}
	return ids
}

// `forYou` reaches scenes through `scenes.Query`, the tier-filtered statement -- slice 5a's note
// claiming otherwise was wrong, and "covered by construction" is a claim without a test. This is
// that test.
//
// `serendipity: 0`, deliberately: it makes the page a pure affinity ranking, so a scene that is
// absent cannot be absent because it lost a coin toss. A shuffled page would make this assertion
// pass or fail depending on the seed.
func TestForYouDoesNotRecommendWhatTheViewerHasNotOptedInto(t *testing.T) {
	admin := asAdmin(t)

	// MODIFY, because filing a scene into a list is what creates the affinity signal and
	// `listAddItem` requires it. The shared user, so the ceiling is stated rather than assumed.
	viewer := asModify(t)
	_, err := viewer.resolver.Mutation().UserSetVisibleTiers(viewer.ctx, []int{})
	require.NoError(t, err)

	tagName := admin.generateSceneName() + " for-you tier"
	tag, err := admin.createTestTag(&models.TagCreateInput{Name: tagName})
	require.NoError(t, err)

	signalTitle := admin.generateSceneName()
	signal, err := admin.createTestScene(&models.SceneCreateInput{
		Title: &signalTitle, TagIds: []uuid.UUID{tag.UUID()},
	})
	require.NoError(t, err)

	recommendedTitle := admin.generateSceneName()
	recommended, err := admin.createTestScene(&models.SceneCreateInput{
		Title: &recommendedTitle, TagIds: []uuid.UUID{tag.UUID()},
	})
	require.NoError(t, err)

	// The signal: the viewer files the first scene into their own list. Affinity is DERIVED from
	// this at read time, so nothing has to be recomputed for it to count.
	list, err := viewer.resolver.Mutation().ListCreate(viewer.ctx, models.ListCreateInput{Name: "for you tier"})
	require.NoError(t, err)
	_, err = viewer.resolver.Mutation().ListAddItem(viewer.ctx, models.ListItemInput{
		ListID: list.ID, EntityType: models.ListEntityTypeEnumScene, EntityID: signal.UUID(),
	})
	require.NoError(t, err)

	// COUNT THE SUBJECT FIRST: with the tag unclassified, the scene sharing it is recommended.
	// Without this, an empty page below would be indistinguishable from a signal that never
	// registered -- which is the more likely reason for an empty For You page.
	before, err := viewer.client.forYou(20, 0)
	require.NoError(t, err)
	require.Contains(t, forYouSceneIDs(before), recommended.UUID().String(),
		"affinity from the viewer's own list must surface the scene sharing the tag")

	_, err = admin.resolver.Mutation().TagSetContentTier(admin.ctx, models.TagContentTierInput{
		ID: tag.UUID(), Tier: 2, RequiresOptIn: false,
	})
	require.NoError(t, err)

	after, err := viewer.client.forYou(20, 0)
	require.NoError(t, err)
	assert.NotContains(t, forYouSceneIDs(after), recommended.UUID().String(),
		"For You must not recommend a scene above the viewer's ceiling")

	// THE CONTROL.
	_, err = viewer.resolver.Mutation().UserSetVisibleTiers(viewer.ctx, []int{1, 2})
	require.NoError(t, err)

	consented, err := viewer.client.forYou(20, 0)
	require.NoError(t, err)
	assert.Contains(t, forYouSceneIDs(consented), recommended.UUID().String(),
		"opting into tier 2 must restore the recommendation")
}

// --- the trending path: the guard in slice 5a, until now unexercised ---

// signalFor records a fingerprint submission by the user at `userOffset` in the users table, aged
// `age`.
//
// THE RANKING IS A COUNT OF DISTINCT USERS, NOT A DECAYED SCORE: `scene_popularity_trending`
// (migration 65) is `COUNT(DISTINCT user_id) ... WHERE created_at >= now()::DATE - 7` -- the
// freshness decay of migration 114 is a DIFFERENT view. So a fixture that wants one scene above
// another gives it MORE USERS inside the seven-day window, and an aged signal only decides whether
// the scene appears in the view at all. (A scene with no counts in that window has no row, which is
// how a first attempt at this fixture produced an empty page for a reason that was not the tier.)
//
// The distinct offset also matters because two signals from one user count once: a tie is real, and
// `ORDER BY trending_count DESC, scene_id DESC` then breaks it by uuid, which is random per run.
func (r *testRunner) signalFor(t *testing.T, sceneID uuid.UUID, userOffset int, age time.Duration) {
	t.Helper()
	ctx := context.Background()

	var fingerprintID int
	err := dbtest.DB().QueryRow(ctx,
		`INSERT INTO fingerprints (hash, algorithm) VALUES ($1, 'PHASH') RETURNING id`,
		time.Now().UnixNano()+int64(userOffset)).Scan(&fingerprintID)
	require.NoError(t, err, "creating the fingerprint row the submission points at")

	_, err = dbtest.DB().Exec(ctx, `
		INSERT INTO scene_fingerprints (fingerprint_id, scene_id, user_id, duration, vote, created_at)
		SELECT $1, $2, u.id, 0, 1, (now() AT TIME ZONE 'UTC') - $4::interval
		FROM (SELECT id FROM users ORDER BY id OFFSET $3 LIMIT 1) u`,
		fingerprintID, sceneID, userOffset, age.String())
	require.NoError(t, err, "recording a submission aged by the fixture")
}

// The optimized trending path LIMITS its subquery BEFORE the filters run, so a page that has to
// exclude tiered scenes cannot take it -- slice 5a's guard adds `tierFiltered` to `hasOtherFilters`
// for exactly that reason, and this is the test that proves the guard does something. Without it the
// optimized path returns a page whose FIRST entry is then filtered away, and the reader gets a
// SHORT page (here, an empty one) rather than the next scene.
func TestTrendingDoesNotGoShortBecauseATieredSceneRanksFirst(t *testing.T) {
	admin := asAdmin(t)
	viewer := asRead(t)

	_, err := viewer.resolver.Mutation().UserSetVisibleTiers(viewer.ctx, []int{})
	require.NoError(t, err)

	tagName := admin.generateSceneName() + " trending tier"
	tag, err := admin.createTestTag(&models.TagCreateInput{Name: tagName})
	require.NoError(t, err)

	tieredTitle := admin.generateSceneName()
	tiered, err := admin.createTestScene(&models.SceneCreateInput{
		Title: &tieredTitle, TagIds: []uuid.UUID{tag.UUID()},
	})
	require.NoError(t, err)

	fillerTitle := admin.generateSceneName()
	filler, err := admin.createTestScene(&models.SceneCreateInput{Title: &fillerTitle})
	require.NoError(t, err)

	// TWO users on the tiered scene and one on the filler, all inside the seven-day window: the
	// tiered scene is first in the trending order outright, with no tie for the uuid to settle.
	viewer.signalFor(t, tiered.UUID(), 0, 0)
	viewer.signalFor(t, tiered.UUID(), 1, time.Hour)
	viewer.signalFor(t, filler.UUID(), 2, 24*time.Hour)
	require.NoError(t, dbtest.Factory().Scene().RefreshPopularityTrending(context.Background()))

	// perPage 1 and NO other filter, so this is the optimized path with the smallest window that
	// still shows the failure: one slot, taken by a scene the reader must not see.
	page := func() []string {
		got, err := viewer.client.queryScenes(models.SceneQueryInput{
			Sort: models.SceneSortEnumTrending, Direction: models.SortDirectionEnumDesc, PerPage: 1,
		})
		require.NoError(t, err)
		ids := make([]string, 0, len(got.Scenes))
		for _, s := range got.Scenes {
			ids = append(ids, s.UUID().String())
		}
		return ids
	}

	// COUNT THE SUBJECT FIRST: before the tag is classified, the tiered scene IS the first page.
	require.Equal(t, []string{tiered.UUID().String()}, page(),
		"the tiered scene must rank first before anything is claimed about the tier")

	_, err = admin.resolver.Mutation().TagSetContentTier(admin.ctx, models.TagContentTierInput{
		ID: tag.UUID(), Tier: 2, RequiresOptIn: false,
	})
	require.NoError(t, err)

	after := page()
	assert.NotContains(t, after, tiered.UUID().String(), "the tiered scene must be excluded")
	assert.NotEmpty(t, after, "and the page must fall through to the next scene rather than going short")

	// THE CONTROL: consent restores the scene to the top of the order.
	_, err = viewer.resolver.Mutation().UserSetVisibleTiers(viewer.ctx, []int{1, 2})
	require.NoError(t, err)
	assert.Equal(t, []string{tiered.UUID().String()}, page())
}

// --- the T3 cooling period (migration 121) ---

// T0-T2 take effect immediately and T3 does not: consenting to the most extreme tier an instance
// offers should not be something a member can do and act on in the same minute.
//
// The cooling is tested by BACKDATING `t3_consented_at` rather than by waiting, which is the reason
// the stamp is data and not a process. The assertions, in order:
//
//  1. consenting to {1,2,3} does NOT reveal a tier-3 scene, while tier 2 is immediate;
//  2. backdating the stamp by 25 hours DOES;
//  3. RE-SENDING the same consent in a different order does NOT restart the clock;
//  4. an explicit {1,2} keeps tier 3 away (the control, so 1 is not vacuous).
func TestTierThreeCoolsBeforeItTakesEffect(t *testing.T) {
	admin := asAdmin(t)
	viewer := asRead(t)

	_, err := viewer.resolver.Mutation().UserSetVisibleTiers(viewer.ctx, []int{})
	require.NoError(t, err)

	makeTiered := func(tier int) (uuid.UUID, uuid.UUID, string) {
		tagName := admin.generateSceneName() + " cooling tier"
		tag, err := admin.createTestTag(&models.TagCreateInput{Name: tagName})
		require.NoError(t, err)
		title := admin.generateSceneName()
		scene, err := admin.createTestScene(&models.SceneCreateInput{
			Title: &title, TagIds: []uuid.UUID{tag.UUID()},
		})
		require.NoError(t, err)
		_, err = admin.resolver.Mutation().TagSetContentTier(admin.ctx, models.TagContentTierInput{
			ID: tag.UUID(), Tier: tier, RequiresOptIn: false,
		})
		require.NoError(t, err)
		return scene.UUID(), tag.UUID(), title
	}

	// Two scenes of their own, so each half of the rule is measured on a scene only that half can
	// explain: "tier 3 is not visible yet" and "tier 2 is visible already".
	_, _, tier3Title := makeTiered(3)
	tier2ID, _, tier2Title := makeTiered(2)

	found := func(title string) []string {
		got, err := viewer.client.queryScenes(models.SceneQueryInput{
			Title: &title, Sort: models.SceneSortEnumDate, Direction: models.SortDirectionEnumDesc, PerPage: 10,
		})
		require.NoError(t, err)
		ids := make([]string, 0, len(got.Scenes))
		for _, s := range got.Scenes {
			ids = append(ids, s.UUID().String())
		}
		return ids
	}

	// COUNT THE SUBJECTS FIRST: at ceiling 0, before any consent, the tier-2 scene is NOT visible
	// either -- so its presence later is the consent and not the fixture.
	require.Empty(t, found(tier2Title), "tier 2 must not be visible without consent")
	require.Empty(t, found(tier3Title), "tier 3 must not be visible without consent")

	_, err = viewer.resolver.Mutation().UserSetVisibleTiers(viewer.ctx, []int{1, 2, 3})
	require.NoError(t, err)

	assert.Equal(t, []string{tier2ID.String()}, found(tier2Title),
		"tier 2 is immediate: the cooling is for tier 3")
	assert.Empty(t, found(tier3Title),
		"consenting to tier 3 must not reveal it in the same minute")

	// 2. The cooling expires on its own, and the test does not wait for it: the stamp is data.
	_, err = dbtest.DB().Exec(context.Background(),
		`UPDATE user_trust SET t3_consented_at = now() - interval '25 hours' WHERE user_id = $1`,
		userDB.read.ID)
	require.NoError(t, err, "backdating the consent beyond the cooling period")

	visible := found(tier3Title)
	require.NotEmpty(t, visible, "after the cooling period the consent must take effect")

	// 3. THE PROPERTY A NAIVE IMPLEMENTATION FAILS: re-sending the same consent, in a different
	// order, does not reset the clock. A stamp-on-every-write version puts tier 3 away again here.
	_, err = viewer.resolver.Mutation().UserSetVisibleTiers(viewer.ctx, []int{3, 2, 1})
	require.NoError(t, err)
	assert.Equal(t, visible, found(tier3Title),
		"re-ordering the same consent is not a change of consent")

	// 4. The control: withdrawing tier 3 puts it away, and no cooling is involved in that.
	_, err = viewer.resolver.Mutation().UserSetVisibleTiers(viewer.ctx, []int{1, 2})
	require.NoError(t, err)
	assert.Empty(t, found(tier3Title), "a consent without tier 3 cannot see tier 3")
}
