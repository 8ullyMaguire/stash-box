//go:build integration

package api_test

import (
	"context"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stashapp/stash-box/internal/auth"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/trust"
	"reflect"
	"strconv"
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

	// THIS test measures the CEILING, not the cooling period, so it does not wait for one: the
	// consent is backdated past it. Without this the test asserts that consenting to tier 3 works
	// immediately -- which is exactly what the cooling period exists to prevent, and it failed the
	// first time the cooling landed. The cooling has its own test
	// (`TestTierThreeCoolsBeforeItTakesEffect`), which asserts the delay and then backdates for the
	// same reason inverted.
	_, err = dbtest.DB().Exec(context.Background(),
		`UPDATE user_trust SET t3_consented_at = now() - interval '25 hours' WHERE user_id = $1`,
		userDB.read.ID)
	require.NoError(t, err, "backdating the consent past the cooling period")

	visible := tierProbeSearch(t, twoTier, title)
	require.Len(t, visible, 1, "a tier-3 consent past its cooling period must make the scene visible")
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
	// ON CONFLICT: the follow row is unique per (follower, followed), and a second test in this
	// package following the same user would otherwise fail on the constraint rather than on anything
	// it is about. The grant is re-asserted, since another test may have narrowed it.
	err := dbtest.DB().QueryRow(ctx, `
		INSERT INTO user_follows (follower_id, followed_id, activity_visibility)
		SELECT $1, u.id, 'public' FROM users u WHERE u.id <> $1 ORDER BY u.id LIMIT 1
		ON CONFLICT (follower_id, followed_id) DO UPDATE SET activity_visibility = 'public'
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

// --- the per-entity content denylist (migration 87), which no read path consulted until now ---

// The denylist is an OPERATOR action with no mutation in this repo, so the fixture writes the row.
// Each of the three arms is denied separately: the risk this wiring carries is a predicate that
// checks two of the three tables and silently stops denying the third.
func denyTag(t *testing.T, tagID uuid.UUID) {
	t.Helper()
	_, err := dbtest.DB().Exec(context.Background(),
		`INSERT INTO content_denylist_tag (tag_id, reason) VALUES ($1, 'integration fixture')
		 ON CONFLICT (tag_id) DO NOTHING`, tagID)
	require.NoError(t, err, "denylisting a tag")

	// The denylist is instance-wide state, not per-test: a row left behind makes
	// `ContentDenylistActive` true for every later test in this package, which changes which path the
	// trending query takes. Deleting the row this fixture added leaves the instance as it was found.
	t.Cleanup(func() {
		_, err := dbtest.DB().Exec(context.Background(),
			`DELETE FROM content_denylist_tag WHERE tag_id = $1`, tagID)
		require.NoError(t, err, "removing the fixture's denylist row")
	})
}

func denyStudio(t *testing.T, studioID uuid.UUID) {
	t.Helper()
	_, err := dbtest.DB().Exec(context.Background(),
		`INSERT INTO content_denylist_studio (studio_id, reason) VALUES ($1, 'integration fixture')
		 ON CONFLICT (studio_id) DO NOTHING`, studioID)
	require.NoError(t, err, "denylisting a studio")
	t.Cleanup(func() {
		_, err := dbtest.DB().Exec(context.Background(),
			`DELETE FROM content_denylist_studio WHERE studio_id = $1`, studioID)
		require.NoError(t, err, "removing the fixture's denylist row")
	})
}

func denyPerformer(t *testing.T, performerID uuid.UUID) {
	t.Helper()
	_, err := dbtest.DB().Exec(context.Background(),
		`INSERT INTO content_denylist_performer (performer_id, reason) VALUES ($1, 'integration fixture')
		 ON CONFLICT (performer_id) DO NOTHING`, performerID)
	require.NoError(t, err, "denylisting a performer")
	t.Cleanup(func() {
		_, err := dbtest.DB().Exec(context.Background(),
			`DELETE FROM content_denylist_performer WHERE performer_id = $1`, performerID)
		require.NoError(t, err, "removing the fixture's denylist row")
	})
}

func titleSearch(t *testing.T, r *testRunner, title string) []string {
	t.Helper()
	got, err := r.client.queryScenes(models.SceneQueryInput{
		Title: &title, Sort: models.SceneSortEnumDate, Direction: models.SortDirectionEnumDesc, PerPage: 10,
	})
	require.NoError(t, err)
	ids := make([]string, 0, len(got.Scenes))
	for _, s := range got.Scenes {
		ids = append(ids, s.UUID().String())
	}
	return ids
}

// Three arms, three assertions, because a predicate that checks two of the three tables looks
// perfectly healthy from the outside. Each arm counts its subject FIRST (the scene is returned
// before it is denylisted), so an absence cannot be a query that never worked.
func TestDenylistedEntitiesAreRemovedFromSearch(t *testing.T) {
	admin := asAdmin(t)
	viewer := asRead(t)

	// --- tag ---
	tagName := admin.generateSceneName() + " denied tag"
	tag, err := admin.createTestTag(&models.TagCreateInput{Name: tagName})
	require.NoError(t, err)
	byTagTitle := admin.generateSceneName()
	byTag, err := admin.createTestScene(&models.SceneCreateInput{
		Title: &byTagTitle, TagIds: []uuid.UUID{tag.UUID()},
	})
	require.NoError(t, err)

	require.Equal(t, []string{byTag.UUID().String()}, titleSearch(t, viewer, byTagTitle),
		"the scene must be returned before the denylist is the reason it is not")
	denyTag(t, tag.UUID())
	assert.Empty(t, titleSearch(t, viewer, byTagTitle),
		"a scene whose TAG is denylisted must not be served")

	// --- studio ---
	studioName := admin.generateSceneName() + " denied studio"
	studio, err := admin.createTestStudio(&models.StudioCreateInput{Name: studioName})
	require.NoError(t, err)
	byStudioTitle := admin.generateSceneName()
	studioID := studio.UUID()
	byStudio, err := admin.createTestScene(&models.SceneCreateInput{
		Title: &byStudioTitle, StudioID: &studioID,
	})
	require.NoError(t, err)

	require.Equal(t, []string{byStudio.UUID().String()}, titleSearch(t, viewer, byStudioTitle))
	denyStudio(t, studio.UUID())
	assert.Empty(t, titleSearch(t, viewer, byStudioTitle),
		"a scene whose STUDIO is denylisted must not be served")

	// --- performer ---
	performerName := admin.generateSceneName() + " denied performer"
	performer, err := admin.createTestPerformer(&models.PerformerCreateInput{Name: performerName})
	require.NoError(t, err)
	byPerformerTitle := admin.generateSceneName()
	byPerformer, err := admin.createTestScene(&models.SceneCreateInput{
		Title:      &byPerformerTitle,
		Performers: []models.PerformerAppearanceInput{{PerformerID: performer.UUID()}},
	})
	require.NoError(t, err)

	require.Equal(t, []string{byPerformer.UUID().String()}, titleSearch(t, viewer, byPerformerTitle))
	denyPerformer(t, performer.UUID())
	assert.Empty(t, titleSearch(t, viewer, byPerformerTitle),
		"a scene whose PERFORMER is denylisted must not be served")
}

// The denylist on the other three paths. Each fixture is arranged so the DENIED tag is on the
// candidate and NOT on the subject, which is what makes these tests about the denylist rather than
// about a scene that happened to disappear.
func TestDenylistedScenesAreNotRecommended(t *testing.T) {
	admin := asAdmin(t)

	sharedName := admin.generateSceneName() + " shared"
	shared, err := admin.createTestTag(&models.TagCreateInput{Name: sharedName})
	require.NoError(t, err)
	deniedName := admin.generateSceneName() + " denied"
	deniedTag, err := admin.createTestTag(&models.TagCreateInput{Name: deniedName})
	require.NoError(t, err)

	subjectTitle := admin.generateSceneName()
	subject, err := admin.createTestScene(&models.SceneCreateInput{
		Title: &subjectTitle, TagIds: []uuid.UUID{shared.UUID()},
	})
	require.NoError(t, err)
	candidateTitle := admin.generateSceneName()
	candidate, err := admin.createTestScene(&models.SceneCreateInput{
		Title: &candidateTitle, TagIds: []uuid.UUID{shared.UUID(), deniedTag.UUID()},
	})
	require.NoError(t, err)

	// --- similarity ---
	viewer := asRead(t)
	before, err := viewer.client.similarScenes(subject.UUID(), 10)
	require.NoError(t, err)
	require.Contains(t, similarSceneIDs(before), candidate.UUID().String(),
		"the candidate must be offered before the denylist is the reason it is not")

	denyTag(t, deniedTag.UUID())
	assert.Empty(t, similarSceneIDs(func() []similarSceneOutput {
		got, err := viewer.client.similarScenes(subject.UUID(), 10)
		require.NoError(t, err)
		return got
	}()), "similar-to must not offer a denylisted scene: nobody asked for it")

	// --- For You ---
	// MODIFY for the list add that creates the affinity signal; the denied tag is again only on the
	// recommendation -- and it is a DIFFERENT tag from the one above, because that one is already
	// denylisted by the time this half runs, so reusing it would make the count-first assertion below
	// unpassable for a reason that is not this test.
	forYouDeniedName := admin.generateSceneName() + " denied for you"
	forYouDeniedTag, err := admin.createTestTag(&models.TagCreateInput{Name: forYouDeniedName})
	require.NoError(t, err)
	forYouTitle := admin.generateSceneName()
	forYouCandidate, err := admin.createTestScene(&models.SceneCreateInput{
		Title: &forYouTitle, TagIds: []uuid.UUID{shared.UUID(), forYouDeniedTag.UUID()},
	})
	require.NoError(t, err)

	signalTitle := admin.generateSceneName()
	signal, err := admin.createTestScene(&models.SceneCreateInput{
		Title: &signalTitle, TagIds: []uuid.UUID{shared.UUID()},
	})
	require.NoError(t, err)
	viewerWithLists := asModify(t)
	_, err = viewerWithLists.resolver.Mutation().UserSetVisibleTiers(viewerWithLists.ctx, []int{})
	require.NoError(t, err)
	list, err := viewerWithLists.resolver.Mutation().ListCreate(viewerWithLists.ctx, models.ListCreateInput{Name: "denylist for you"})
	require.NoError(t, err)
	_, err = viewerWithLists.resolver.Mutation().ListAddItem(viewerWithLists.ctx, models.ListItemInput{
		ListID: list.ID, EntityType: models.ListEntityTypeEnumScene, EntityID: signal.UUID(),
	})
	require.NoError(t, err)

	forYouBefore, err := viewerWithLists.client.forYou(20, 0)
	require.NoError(t, err)
	require.Contains(t, forYouSceneIDs(forYouBefore), forYouCandidate.UUID().String(),
		"the affinity from the shared tag must surface the candidate first")

	denyTag(t, forYouDeniedTag.UUID())

	assert.NotContains(t, forYouSceneIDs(func() []forYouSceneOutput {
		got, err := viewerWithLists.client.forYou(20, 0)
		require.NoError(t, err)
		return got
	}()), forYouCandidate.UUID().String(), "For You must not recommend a denylisted scene")
}

// The activity feed's denylist arm runs in Go (a batch lookup over the page), so it is the one that
// can be wired and still not fire: the tier filter and the denylist filter are separate calls.
func TestTheActivityFeedDropsDenylistedScenes(t *testing.T) {
	admin := asAdmin(t)
	viewer := asRead(t)

	_, err := viewer.resolver.Mutation().UserSetVisibleTiers(viewer.ctx, []int{})
	require.NoError(t, err)

	deniedName := admin.generateSceneName() + " feed denied"
	deniedTag, err := admin.createTestTag(&models.TagCreateInput{Name: deniedName})
	require.NoError(t, err)

	title := admin.generateSceneName()
	scene, err := admin.createTestScene(&models.SceneCreateInput{
		Title: &title, TagIds: []uuid.UUID{deniedTag.UUID()},
	})
	require.NoError(t, err)

	viewer.followSomeoneAndSignalTheirs(t, userDB.read.ID, scene.UUID())

	before, err := viewer.client.activityFeed(20)
	require.NoError(t, err)
	require.Contains(t, feedSceneIDs(before), scene.UUID().String(),
		"the followed user's activity must be in the feed before the denylist is the reason it is not")

	denyTag(t, deniedTag.UUID())

	after, err := viewer.client.activityFeed(20)
	require.NoError(t, err)
	assert.NotContains(t, feedSceneIDs(after), scene.UUID().String(),
		"the feed must not show a denylisted scene")
}

// --- warnings vs gates (phase 5 slice 5h, migration 122) ---

type contentWarningOutput struct {
	Reason string `json:"reason"`
	Source string `json:"source"`
}

// sceneWithWarnings asks for one scene and its warnings in the same request, because the point of the
// pair is that they are independent: a scene can be present with a warning, present with none, or
// absent -- and the third case must not report the second's reason.
func sceneWithWarnings(t *testing.T, r *testRunner, sceneID uuid.UUID) (bool, []contentWarningOutput) {
	t.Helper()
	q := `
	query SceneWarnings($id: ID!) {
		findScene(id: $id) {
			id
			warnings { reason source }
		}
	}`
	var resp struct {
		FindScene *struct {
			ID       string                 `json:"id"`
			Warnings []contentWarningOutput `json:"warnings"`
		} `json:"findScene"`
	}
	require.NoError(t, r.client.Post(q, &resp, client.Var("id", sceneID)))
	if resp.FindScene == nil {
		return false, nil
	}
	return true, resp.FindScene.Warnings
}

// A warn entry is an ANNOTATION, a gate entry is a DECISION, and the two must not be each other: the
// warning changes nothing about what is served, and the gate does not turn into a reader-facing note.
func TestAWarnEntryReportsWithoutRemoving(t *testing.T) {
	admin := asAdmin(t)
	viewer := asRead(t)

	_, err := viewer.resolver.Mutation().UserSetVisibleTiers(viewer.ctx, []int{})
	require.NoError(t, err)

	warnName := admin.generateSceneName() + " warned tag"
	warnTag, err := admin.createTestTag(&models.TagCreateInput{Name: warnName})
	require.NoError(t, err)
	title := admin.generateSceneName()
	scene, err := admin.createTestScene(&models.SceneCreateInput{
		Title: &title, TagIds: []uuid.UUID{warnTag.UUID()},
	})
	require.NoError(t, err)

	// COUNT THE SUBJECT FIRST, both halves of it: the scene is served, and it has nothing to report.
	require.Equal(t, []string{scene.UUID().String()}, titleSearch(t, viewer, title),
		"the scene must be served before the denylist is why it is not")
	found, warnings := sceneWithWarnings(t, viewer, scene.UUID())
	require.True(t, found)
	require.Empty(t, warnings, "nothing to report before anything is recorded")

	// A WARN row: present, and now it reports.
	_, err = dbtest.DB().Exec(context.Background(),
		`INSERT INTO content_denylist_tag (tag_id, reason, mode) VALUES ($1, 'restricted in your region', 'warn')`,
		warnTag.UUID())
	require.NoError(t, err, "warning about a tag")
	t.Cleanup(func() {
		_, err := dbtest.DB().Exec(context.Background(), `DELETE FROM content_denylist_tag WHERE tag_id = $1`, warnTag.UUID())
		require.NoError(t, err)
	})

	assert.Equal(t, []string{scene.UUID().String()}, titleSearch(t, viewer, title),
		"a WARNING must not remove the scene: it is an annotation, not a gate")
	found, warnings = sceneWithWarnings(t, viewer, scene.UUID())
	require.True(t, found)
	require.Len(t, warnings, 1, "the warning must be reported")
	assert.Equal(t, "tag", warnings[0].Source)
	assert.Equal(t, "restricted in your region", warnings[0].Reason)

	// A GATE row on a second tag of the same scene: the scene goes away, and it reports NOTHING --
	// "this content is restricted" about content the reader cannot open is itself a disclosure.
	gateName := admin.generateSceneName() + " gated tag"
	gateTag, err := admin.createTestTag(&models.TagCreateInput{Name: gateName})
	require.NoError(t, err)
	_, err = viewer.resolver.Mutation().SceneUpdate(viewer.ctx, models.SceneUpdateInput{
		ID: scene.UUID(), TagIds: []uuid.UUID{warnTag.UUID(), gateTag.UUID()},
	})
	require.NoError(t, err)
	_, err = dbtest.DB().Exec(context.Background(),
		`INSERT INTO content_denylist_tag (tag_id, reason, mode) VALUES ($1, 'not served here', 'gate')`,
		gateTag.UUID())
	require.NoError(t, err, "gating a tag")
	t.Cleanup(func() {
		_, err := dbtest.DB().Exec(context.Background(), `DELETE FROM content_denylist_tag WHERE tag_id = $1`, gateTag.UUID())
		require.NoError(t, err)
	})

	assert.Empty(t, titleSearch(t, viewer, title), "a GATE removes the scene even with a warning beside it")
	found, warnings = sceneWithWarnings(t, viewer, scene.UUID())
	if found {
		assert.Empty(t, warnings, "a scene the instance denies reports no warnings")
	}
}

// A warning is an annotation ON A VIEW. If the scene is not in the reader's view there is nothing to
// annotate, so a warn entry on a scene above the viewer's ceiling must report nothing -- and the SAME
// call with consent must report it, which is what makes the first assertion about the viewer's ceiling
// rather than about a broken query.
func TestAWarningIsNotShownForASceneTheViewerCannotSee(t *testing.T) {
	admin := asAdmin(t)
	viewer := asRead(t)

	_, err := viewer.resolver.Mutation().UserSetVisibleTiers(viewer.ctx, []int{})
	require.NoError(t, err)

	tagName := admin.generateSceneName() + " warned tier"
	tag, err := admin.createTestTag(&models.TagCreateInput{Name: tagName})
	require.NoError(t, err)
	title := admin.generateSceneName()
	scene, err := admin.createTestScene(&models.SceneCreateInput{
		Title: &title, TagIds: []uuid.UUID{tag.UUID()},
	})
	require.NoError(t, err)

	_, err = admin.resolver.Mutation().TagSetContentTier(admin.ctx, models.TagContentTierInput{
		ID: tag.UUID(), Tier: 2, RequiresOptIn: false,
	})
	require.NoError(t, err)
	_, err = dbtest.DB().Exec(context.Background(),
		`INSERT INTO content_denylist_tag (tag_id, reason, mode) VALUES ($1, 'tier two note', 'warn')`,
		tag.UUID())
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := dbtest.DB().Exec(context.Background(), `DELETE FROM content_denylist_tag WHERE tag_id = $1`, tag.UUID())
		require.NoError(t, err)
	})

	assert.Empty(t, titleSearch(t, viewer, title), "the scene is above this viewer's ceiling")

	// THE CONTROL: the same request with consent reports the warning, so the emptiness above is the
	// viewer's ceiling and not a query that never returned anything.
	_, err = viewer.resolver.Mutation().UserSetVisibleTiers(viewer.ctx, []int{1, 2})
	require.NoError(t, err)

	assert.Equal(t, []string{scene.UUID().String()}, titleSearch(t, viewer, title),
		"consent puts the scene in the view")
	found, warnings := sceneWithWarnings(t, viewer, scene.UUID())
	require.True(t, found)
	require.Len(t, warnings, 1, "and in the view, the warning is reported")
	assert.Equal(t, "tier two note", warnings[0].Reason)

	// And back below the ceiling: nothing, again.
	_, err = viewer.resolver.Mutation().UserSetVisibleTiers(viewer.ctx, []int{})
	require.NoError(t, err)
	found, warnings = sceneWithWarnings(t, viewer, scene.UUID())
	if found {
		assert.Empty(t, warnings, "out of the view, a warning is a disclosure and is withheld")
	}
}

// --- the access setup query (phase 5 slice 5i) ---

type accessSetupOutput struct {
	TermsAccepted     bool `json:"termsAccepted"`
	OptedIn           bool `json:"optedIn"`
	ContributionScore struct {
		Current  int  `json:"current"`
		Required int  `json:"required"`
		Met      bool `json:"met"`
	} `json:"contributionScore"`
	Tiers []struct {
		Tier        int     `json:"tier"`
		OptedIn     bool    `json:"optedIn"`
		AvailableAt *string `json:"availableAt"`
	} `json:"tiers"`
}

const accessSetupQuery = `
query AccessSetup {
	contentAccessSetup {
		termsAccepted
		optedIn
		contributionScore { current required met }
		tiers { tier optedIn availableAt }
	}
}`

func (r *testRunner) accessSetup(t *testing.T) accessSetupOutput {
	t.Helper()
	var resp struct {
		ContentAccessSetup accessSetupOutput `json:"contentAccessSetup"`
	}
	require.NoError(t, r.client.Post(accessSetupQuery, &resp))
	return resp.ContentAccessSetup
}

func (r *testRunner) acceptContentTerms(t *testing.T) accessSetupOutput {
	t.Helper()
	var resp struct {
		UserAcceptContentTerms accessSetupOutput `json:"userAcceptContentTerms"`
	}
	require.NoError(t, r.client.Post(`mutation { userAcceptContentTerms {
		termsAccepted optedIn contributionScore { current required met } tiers { tier optedIn availableAt } } }`, &resp))
	return resp.UserAcceptContentTerms
}

// THE DRIFT GUARD, and the reason this query exists at all: every field it reports is checked against
// the gate's OWN verdict for the same member. A wizard that drifts from the gate tells people to satisfy
// conditions that will not let them in -- and until this slice the gate had no caller, so "the wizard
// agrees with the gate" was not a proposition anyone could test.
//
// The two directions are asserted separately, because a comparison that always says "refused" would
// pass a one-sided test while telling every member they were blocked.
func TestAccessSetupAgreesWithTheGate(t *testing.T) {
	viewer := asRead(t)
	userID := auth.GetCurrentUser(viewer.ctx).ID

	// Reset the parts of the standing that other tests in this package also use.
	_, err := viewer.resolver.Mutation().UserSetVisibleTiers(viewer.ctx, []int{})
	require.NoError(t, err)

	// A distinctive contribution mix: four accepted kinds sum, and REJECTIONS ARE EXCLUDED (the elo
	// query's own comment explains why -- a binding of rejections is the count of them, not a negative
	// tally). 7 + 5 + 3 + 2 = 17 with 99 rejections: if the copy of that expression in UserAccessSetup
	// ever grew a rejection term, or lost one of the four, this number moves.
	var prev [5]int
	row := dbtest.DB().QueryRow(context.Background(),
		`SELECT approved_edits, rejected_edits, identification_solves, quests_completed, replicas_hosted
		 FROM user_trust WHERE user_id = $1`, userID)
	require.NoError(t, row.Scan(&prev[0], &prev[1], &prev[2], &prev[3], &prev[4]))
	t.Cleanup(func() {
		_, err := dbtest.DB().Exec(context.Background(),
			`UPDATE user_trust SET approved_edits = $2, rejected_edits = $3, identification_solves = $4,
			 quests_completed = $5, replicas_hosted = $6 WHERE user_id = $1`,
			userID, prev[0], prev[1], prev[2], prev[3], prev[4])
		require.NoError(t, err)
	})
	_, err = dbtest.DB().Exec(context.Background(),
		`UPDATE user_trust SET approved_edits = 7, rejected_edits = 99, identification_solves = 5,
		 quests_completed = 3, replicas_hosted = 2 WHERE user_id = $1`, userID)
	require.NoError(t, err)

	setup := viewer.accessSetup(t)

	// THE SAME EXPRESSION, READ BY THE OTHER QUERY THAT DEFINES IT (elo.sql). Two copies with an
	// assertion that they agree is one definition; two copies without is two.
	elo, err := queries.New(dbtest.DB()).GetVoterWeightInputs(viewer.ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, int(elo.ContributionScore), setup.ContributionScore.Current,
		"the setup query's contribution total must be the same expression elo.sql uses")
	assert.Equal(t, 17, setup.ContributionScore.Current,
		"seven approvals, five identifications, three quests, two replicas -- and rejections excluded")
	assert.Equal(t, int(trust.MinContribution), setup.ContributionScore.Required,
		"the required floor must be the constant the gate is handed")
	assert.True(t, setup.ContributionScore.Met, "17 is past a floor of 10")

	// Both directions of the gate, against the same numbers the query just reported.
	base := trust.AccessRequest{
		Level:             trust.LevelContentViewing,
		OptedIn:           setup.OptedIn,
		TermsAccepted:     setup.TermsAccepted,
		EntityTier:        0,
		VisibleTier:       0,
		ContributionScore: int64(setup.ContributionScore.Current),
		MinContribution:   int64(setup.ContributionScore.Required),
	}
	assert.NotContains(t, trust.EvaluateContentAccess(base).Failed, trust.ReasonContribution,
		"the query says the floor is met, so the gate must not refuse for the score")

	below := base
	below.ContributionScore = int64(setup.ContributionScore.Required) - 1
	assert.Contains(t, trust.EvaluateContentAccess(below).Failed, trust.ReasonContribution,
		"and one short of the floor the gate reports the query says it would")

	// BELOW THE FLOOR, so `met` is asserted in both directions. Without this, a `met` that was
	// hardcoded true would pass every other assertion in this test: the gate comparisons above read
	// current and required directly.
	_, err = dbtest.DB().Exec(context.Background(),
		`UPDATE user_trust SET approved_edits = 0, identification_solves = 0, quests_completed = 0,
		 replicas_hosted = 0 WHERE user_id = $1`, userID)
	require.NoError(t, err)
	empty := viewer.accessSetup(t)
	assert.Equal(t, 0, empty.ContributionScore.Current)
	assert.False(t, empty.ContributionScore.Met, "an empty record is below a floor of ten")
	belowFloor := base
	belowFloor.ContributionScore = 0
	assert.Contains(t, trust.EvaluateContentAccess(belowFloor).Failed, trust.ReasonContribution,
		"and the gate agrees that an empty record is refused")

	// THE TERMS CONDITION, both directions. This is the one the query's absence of a column used to make
	// unsatisfiable: the gate refused for terms and nothing in the instance could record acceptance.
	noTerms := base
	noTerms.TermsAccepted = false
	assert.Contains(t, trust.EvaluateContentAccess(noTerms).Failed, trust.ReasonTerms)

	accepted := viewer.acceptContentTerms(t)
	assert.True(t, accepted.TermsAccepted, "accepting the terms is reported afterwards")
	assert.Equal(t, empty.ContributionScore, accepted.ContributionScore,
		"and accepting terms changes nothing else about the standing")

	var first pgtype.Timestamptz
	require.NoError(t, dbtest.DB().QueryRow(context.Background(),
		`SELECT content_terms_accepted_at FROM user_trust WHERE user_id = $1`, userID).Scan(&first))
	require.True(t, first.Valid, "acceptance is recorded")

	// Accepting again leaves the FIRST acceptance alone: "when did you first agree" is not rewritten by
	// a member re-reading the terms, and a revision needs its own field rather than this one's history.
	viewer.acceptContentTerms(t)
	var second pgtype.Timestamptz
	require.NoError(t, dbtest.DB().QueryRow(context.Background(),
		`SELECT content_terms_accepted_at FROM user_trust WHERE user_id = $1`, userID).Scan(&second))
	assert.Equal(t, first.Time, second.Time, "the first acceptance stands")

	setup = viewer.accessSetup(t)
	assert.True(t, setup.TermsAccepted)
	withTerms := base
	withTerms.TermsAccepted = true
	assert.NotContains(t, trust.EvaluateContentAccess(withTerms).Failed, trust.ReasonTerms,
		"the query reports acceptance and the gate accepts it")
}

// The tier ladder, with the cooling period reported rather than hidden. Order-independent: it sets and
// clears the ceiling itself, because the READ user is shared with every other test in the package.
func TestTheTierLadderReportsTheCooling(t *testing.T) {
	viewer := asRead(t)
	userID := auth.GetCurrentUser(viewer.ctx).ID
	_, err := viewer.resolver.Mutation().UserSetVisibleTiers(viewer.ctx, []int{})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := viewer.resolver.Mutation().UserSetVisibleTiers(viewer.ctx, []int{})
		require.NoError(t, err)
	})

	// COUNT THE SUBJECT FIRST: a ladder of no rungs would satisfy "every rung is opted out of" and
	// "no rung is cooling" vacuously.
	setup := viewer.accessSetup(t)
	require.Len(t, setup.Tiers, trust.MaxContentTier+1, "the ladder has every tier the model defines")
	for _, tier := range setup.Tiers {
		assert.False(t, tier.OptedIn, "a cleared ceiling is opted into nothing")
		assert.Nil(t, tier.AvailableAt, "and nothing is cooling")
	}

	_, err = viewer.resolver.Mutation().UserSetVisibleTiers(viewer.ctx, []int{1, 2})
	require.NoError(t, err)
	setup = viewer.accessSetup(t)
	for _, tier := range setup.Tiers {
		// EXACT membership, not a ceiling: the ladder reports the record of consent, and tier 0 is
		// absent from it because tier 0 is always visible -- there is nothing to consent to. A test
		// asserting "tier <= 2" would pass against a query that invented a ceiling.
		assert.Equal(t, tier.Tier == 1 || tier.Tier == 2, tier.OptedIn, "tier %d", tier.Tier)
		assert.Nil(t, tier.AvailableAt, "no consent to the cooled tier, so nothing is pending")
	}

	// Opting into 3 is recorded at once and effective 24 hours later, and the ladder says WHEN rather
	// than telling the member to do what they just did.
	_, err = viewer.resolver.Mutation().UserSetVisibleTiers(viewer.ctx, []int{1, 2, 3})
	require.NoError(t, err)
	setup = viewer.accessSetup(t)
	tier3 := setup.Tiers[len(setup.Tiers)-1]
	require.Equal(t, trust.MaxContentTier, tier3.Tier)
	require.True(t, tier3.OptedIn, "the consent is recorded immediately")
	require.NotNil(t, tier3.AvailableAt, "and it is pending, so the ladder reports when it takes effect")
	availableAt, err := time.Parse(time.RFC3339, *tier3.AvailableAt)
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now().Add(trust.T3CoolingHours*time.Hour), availableAt, 5*time.Minute)

	// Backdate the consent past the period: the same field goes quiet, so it is reporting the cooling
	// and not merely echoing "there is a timestamp".
	_, err = dbtest.DB().Exec(context.Background(),
		`UPDATE user_trust SET t3_consented_at = now() - interval '25 hours' WHERE user_id = $1`, userID)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := dbtest.DB().Exec(context.Background(),
			`UPDATE user_trust SET t3_consented_at = NULL WHERE user_id = $1`, userID)
		require.NoError(t, err)
	})
	setup = viewer.accessSetup(t)
	assert.Nil(t, setup.Tiers[len(setup.Tiers)-1].AvailableAt,
		"past the period the consent is effective, so there is nothing left to wait for")
}

// --- the four direct paths (phase 5 slice 5g) ---

func findSceneExists(t *testing.T, r *testRunner, id uuid.UUID) bool {
	t.Helper()
	var resp struct {
		FindScene *struct {
			ID string `json:"id"`
		} `json:"findScene"`
	}
	require.NoError(t, r.client.Post(`query S($id: ID!) { findScene(id: $id) { id } }`, &resp, client.Var("id", id)))
	return resp.FindScene != nil
}

func findScenesIDs(t *testing.T, r *testRunner, ids []uuid.UUID) []string {
	t.Helper()
	var resp struct {
		FindScenes []*struct {
			ID string `json:"id"`
		} `json:"findScenes"`
	}
	require.NoError(t, r.client.Post(`query S($ids: [ID!]!) { findScenes(ids: $ids) { id } }`, &resp, client.Var("ids", ids)))
	var got []string
	for _, scene := range resp.FindScenes {
		if scene != nil {
			got = append(got, scene.ID)
		}
	}
	return got
}

func fingerprintSceneIDs(t *testing.T, r *testRunner, fp models.FingerprintEditInput) []string {
	t.Helper()
	var resp struct {
		FindScenesBySceneFingerprints [][]*struct {
			ID string `json:"id"`
		} `json:"findScenesBySceneFingerprints"`
	}
	// THE HASH GOES AS A HEX STRING, which is what the `FingerprintHash` scalar is: it requires a
	// string (a number is refused with "fingerprint hash must be a string") and reads it in base 16.
	// Sending the decimal rendering of 15 asked for fingerprint 21 and the lookup came back empty --
	// which read exactly like a gate that was too eager, and cost an afternoon of arithmetic theories.
	// The other tests in this package never see this because they hand the typed input to a client that
	// marshals it with the scalar's own encoder.
	require.NoError(t, r.client.Post(`
	query F($fingerprints: [[FingerprintQueryInput!]!]!) {
		findScenesBySceneFingerprints(fingerprints: $fingerprints) { id }
	}`, &resp, client.Var("fingerprints", [][]map[string]any{
		{{"hash": strconv.FormatInt(int64(fp.Hash), 16), "algorithm": string(fp.Algorithm)}},
	})))

	var got []string
	for _, group := range resp.FindScenesBySceneFingerprints {
		for _, scene := range group {
			if scene != nil {
				got = append(got, scene.ID)
			}
		}
	}
	return got
}

func changelogIDs(t *testing.T, r *testRunner, since string) []string {
	t.Helper()
	var resp struct {
		SceneChangelog []struct {
			ID string `json:"id"`
		} `json:"sceneChangelog"`
	}
	require.NoError(t, r.client.Post(`query C($since: Time!) { sceneChangelog(since: $since) { id } }`, &resp, client.Var("since", since)))

	got := make([]string, 0, len(resp.SceneChangelog))
	for _, change := range resp.SceneChangelog {
		got = append(got, change.ID)
	}
	return got
}

// THE FOUR DIRECT PATHS. Slice 5f gated the four enumerating ones; this is the other half, and the
// subject is counted on EVERY path before anything is denied -- "nothing is returned" and "the query
// was never wired" are the same observation otherwise, which is the trap that makes a gate look tested
// when it is not.
func TestADenylistedSceneReadsAsNotFound(t *testing.T) {
	admin := asAdmin(t)
	viewer := asRead(t)
	_, err := viewer.resolver.Mutation().UserSetVisibleTiers(viewer.ctx, []int{})
	require.NoError(t, err)

	tag, err := admin.createTestTag(&models.TagCreateInput{Name: admin.generateSceneName() + " denied tag"})
	require.NoError(t, err)
	title := admin.generateSceneName()
	scene, err := admin.createTestScene(&models.SceneCreateInput{
		Title:        &title,
		TagIds:       []uuid.UUID{tag.UUID()},
		Fingerprints: []models.FingerprintEditInput{admin.generateSceneFingerprint(nil)},
	})
	require.NoError(t, err)

	// THE LOOKUP USES WHAT IS STORED, read back from the row, rather than the hash this test put in
	// the create input. The harness owns how a fingerprint is written (and rewrites are its business);
	// what this test is about is whether the FINGERPRINT PATH serves a denied scene, and the path
	// matches on the stored value. Asking with the input value made the arm depend on the harness's
	// internals -- and the fixture's counter shares values with fingerprints whose scenes other tests
	// deleted, so the request could match a row the lookup then discards as deleted.
	var storedHash int64
	var storedAlgorithm string
	require.NoError(t, dbtest.DB().QueryRow(context.Background(),
		`SELECT f.hash, f.algorithm FROM scene_fingerprints sf JOIN fingerprints f ON f.id = sf.fingerprint_id
		 WHERE sf.scene_id = $1 LIMIT 1`, scene.UUID()).Scan(&storedHash, &storedAlgorithm))
	fingerprint := models.FingerprintEditInput{
		Algorithm: models.FingerprintAlgorithm(storedAlgorithm),
		Hash:      models.FingerprintHash(storedHash),
	}
	require.NoError(t, err)
	since := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)

	// The fixture's precondition, asserted rather than assumed: the lookup below can only report a
	// missing scene if the scene HAS the fingerprint. Without this, "the fingerprint path found
	// nothing" and "the fingerprint was never attached" are the same observation.
	var attached int
	require.NoError(t, dbtest.DB().QueryRow(context.Background(),
		`SELECT count(*) FROM scene_fingerprints sf JOIN fingerprints f ON f.id = sf.fingerprint_id
		 WHERE sf.scene_id = $1 AND f.hash = $2`, scene.UUID(), int64(fingerprint.Hash)).Scan(&attached))
	require.Equal(t, 1, attached, "the fixture's fingerprint must be attached to the scene")

	require.True(t, findSceneExists(t, viewer, scene.UUID()), "findScene, before")
	require.Equal(t, []string{scene.UUID().String()}, findScenesIDs(t, viewer, []uuid.UUID{scene.UUID()}), "findScenes, before")
	require.Equal(t, []string{scene.UUID().String()}, fingerprintSceneIDs(t, viewer, fingerprint), "fingerprints, before")
	require.Contains(t, changelogIDs(t, viewer, since), scene.UUID().String(), "changelog, before")

	denyTag(t, tag.UUID())

	assert.False(t, findSceneExists(t, viewer, scene.UUID()),
		"a denied scene reads as NOT FOUND, not as an error and not as 'denied'")
	assert.Empty(t, findScenesIDs(t, viewer, []uuid.UUID{scene.UUID()}),
		"findScenes(ids) must not be a bypass of findScene")
	assert.Empty(t, fingerprintSceneIDs(t, viewer, fingerprint),
		"nor the fingerprint path, which is how a client reaches a scene with a file of its own")
	assert.NotContains(t, changelogIDs(t, viewer, since), scene.UUID().String(),
		"nor the changelog, which carries the same id and update times by a slower route")

	// THE TWO THINGS THAT MUST STAY OPEN. The edit path first: if moderation of a denied scene were
	// gated too, the only way to un-denylist something would be to bypass the API.
	updated := title + " (edited while denied)"
	_, err = admin.resolver.Mutation().SceneUpdate(admin.ctx, models.SceneUpdateInput{
		ID:     scene.UUID(),
		Title:  &updated,
		TagIds: []uuid.UUID{tag.UUID()},
	})
	require.NoError(t, err, "the edit path must not be gated by the content denylist")
	assert.False(t, findSceneExists(t, viewer, scene.UUID()), "and editing it did not make it readable")

	// THE OTHER DIRECTION, and it is the mechanism an operator un-denylists with: the denylist names
	// ENTITIES, not scene ids, so the same mutation that keeps a scene denied can lift it by removing
	// the denied tag. (This is also why the edit above had to keep the tag: a nil TagIds replaces the
	// set, which would have un-denied the scene as a side effect of a title change.)
	_, err = admin.resolver.Mutation().SceneUpdate(admin.ctx, models.SceneUpdateInput{
		ID:     scene.UUID(),
		TagIds: []uuid.UUID{},
	})
	require.NoError(t, err)
	assert.True(t, findSceneExists(t, viewer, scene.UUID()),
		"removing the denied tag serves the scene again, without touching the denylist")

	// And the operator surface: an operator audits their own denylist, so the rule must still be
	// reported -- with `Enforced` true, which is now true of enumeration AND of direct access.
	var rules struct {
		AccessRules struct {
			Rules []struct {
				Name     string `json:"name"`
				Enforced bool   `json:"enforced"`
			} `json:"rules"`
		} `json:"accessRules"`
	}
	require.NoError(t, admin.client.Post(`query { accessRules { rules { name enforced } } }`, &rules))
	require.NotEmpty(t, rules.AccessRules.Rules, "the report must list the rules at all")
	var found *struct {
		Name     string `json:"name"`
		Enforced bool   `json:"enforced"`
	}
	for i := range rules.AccessRules.Rules {
		if rules.AccessRules.Rules[i].Name == "per_entity_denylist" {
			found = &rules.AccessRules.Rules[i]
		}
	}
	require.NotNil(t, found, "the per-entity denylist must be reported to an operator")
	assert.True(t, found.Enforced, "and reported as enforced, which it now is on every path")
}
