//go:build integration

package api_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/models"
)

// Scene freshness: the score behind the FRESH and RISING sorts (spec section 7, migration 114).
//
// WHAT IS PROVEN HERE:
//
//  1. The DECAY IS THE SPEC'S FORMULA, checked against numbers computed by hand from
//     `1 / (1 + age_hours / 48) ^ 1.5` rather than against the SQL. Expecting 1.060660 for a
//     48-hour-old signal is what catches a wrong constant in the view: at age 0 every version of
//     the formula agrees, so a fixture of only-fresh signals proves nothing.
//  2. The three signals SUM with their specified weights (3, 3, rating - 3), and a review with no
//     rating is not a signal rather than a -3.
//  3. The 90-day window is a real boundary: a signal inside it counts, one outside it leaves the
//     scene with no score row at all.
//  4. FRESH and RISING RANK DIFFERENTLY, in the direction the spec says -- a scene with five
//     signals thirty hours ago leads on FRESH and loses on RISING to a scene with one signal two
//     hours ago. That pair is the only way to show the second sort is not a synonym for the first.
//  5. THE SORT READS THE VIEW: a new signal changes nothing until the refresh runs. That is what
//     makes "materialized, refreshed hourly" observable rather than decorative.
//
// The signals are inserted with SQL rather than through the API on purpose. Their producers
// (fingerprint submissions, list items, reviews) are other features with their own tests; the
// subject here is the score, not the way a review is written. The ordering tests read through
// `queryScenes`, so the sort itself is exercised end to end.
//
// Every query is scoped by an ID criterion on the fixture's own scenes: the package shares one
// database with the full suite, so "the scenes in this order" has to be asked about these scenes
// or it is a claim about everyone's.

type sceneFreshnessTestRunner struct {
	sceneQueryLanguageRunner
}

func createSceneFreshnessTestRunner(t *testing.T) *sceneFreshnessTestRunner {
	t.Helper()
	return &sceneFreshnessTestRunner{sceneQueryLanguageRunner: *createSceneQueryLanguageRunner(t)}
}

// db is the raw handle: aging a row is the only way to build a fixture with a known age, and the
// API deliberately does not let a caller choose when a signal happened.
func (s *sceneFreshnessTestRunner) db() *pgxpool.Pool {
	return dbtest.DB()
}

func (s *sceneFreshnessTestRunner) refresh() {
	s.t.Helper()

	err := dbtest.Factory().Scene().RefreshFreshness(context.Background())
	require.NoError(s.t, err, "refreshing the freshness view")
}

// freshScene creates a scene with NO signals.
//
// The package's scene fixture SUBMITS A FINGERPRINT as part of creating a scene, which is a real
// +3 signal, so a score of 3 belongs to every scene it makes. This slice has to control every
// signal a score is made of, so the fixture's own submission is removed and each test adds exactly
// the signals it is about.
func (s *sceneFreshnessTestRunner) freshScene() uuid.UUID {
	s.t.Helper()

	sceneID := s.sceneTagged()

	_, err := s.db().Exec(context.Background(), `DELETE FROM scene_fingerprints WHERE scene_id = $1`, sceneID)
	require.NoError(s.t, err, "clearing the scene fixture's own fingerprint submission")

	return sceneID
}

// signalFingerprint records a fingerprint submission (weight +3) aged `age`.
func (s *sceneFreshnessTestRunner) signalFingerprint(sceneID uuid.UUID, age time.Duration) {
	s.t.Helper()
	s.signalFingerprintVote(sceneID, age, 1)
}

// signalFingerprintVote records a fingerprint submission or its WITHDRAWAL (`vote` -1, worth -3)
// aged `age`.
//
// `fingerprints.hash` is a BIGINT (migration 52: a phash is a 64-bit number, which is also what
// the bktree index on it needs), so a fixture hash is an integer and not a string -- an md5-shaped
// string fails on the cast before anything about freshness is reached.
func (s *sceneFreshnessTestRunner) signalFingerprintVote(sceneID uuid.UUID, age time.Duration, vote int) {
	s.t.Helper()

	ctx := context.Background()

	var fingerprintID int
	err := s.db().QueryRow(ctx, `
		INSERT INTO fingerprints (hash, algorithm)
		VALUES ($1, 'PHASH')
		RETURNING id`, time.Now().UnixNano()+int64(vote)).Scan(&fingerprintID)
	require.NoError(s.t, err, "creating a fingerprint row")

	// `scene_fingerprints.created_at` is a `timestamp` (no zone) stamped by the database, so it is
	// aged in UTC to stay comparable with the view's `now()`.
	_, err = s.db().Exec(ctx, `
		INSERT INTO scene_fingerprints (fingerprint_id, scene_id, user_id, duration, vote, created_at)
		SELECT $1, $2, u.id, 0, $4, (now() AT TIME ZONE 'UTC') - $3::interval
		FROM users u
		ORDER BY u.id
		LIMIT 1`, fingerprintID, sceneID, age.String(), vote)
	require.NoError(s.t, err, "recording a fingerprint submission")
}

// signalListAdd records a scene being added to a list (weight 3) aged `age`.
func (s *sceneFreshnessTestRunner) signalListAdd(sceneID uuid.UUID, age time.Duration) {
	s.t.Helper()

	ctx := context.Background()

	var listID uuid.UUID
	err := s.db().QueryRow(ctx, `
		INSERT INTO lists (owner_id, name, owner_name, published_at, published_by)
		SELECT u.id, $1, u.name, now(), u.id
		FROM users u
		ORDER BY u.id
		LIMIT 1
		RETURNING id`, s.generateTagName()).Scan(&listID)
	require.NoError(s.t, err, "creating a list")

	_, err = s.db().Exec(ctx, `
		INSERT INTO list_items (list_id, entity_type, entity_id, position, created_at)
		VALUES ($1, 'scene', $2, 1, now() - $3::interval)`, listID, sceneID, age.String())
	require.NoError(s.t, err, "adding the scene to a list")
}

// signalReview records a review (weight `rating - 3`, or nothing at all when rating is nil, since
// a NULL rating is permitted precisely for reviews that are not a 1-to-5 quantity) aged `age`.
func (s *sceneFreshnessTestRunner) signalReview(sceneID uuid.UUID, rating *int, age time.Duration) {
	s.t.Helper()

	_, err := s.db().Exec(context.Background(), `
		INSERT INTO reviews (id, author_id, entity_type, entity_id, rating, body, created_at)
		SELECT gen_random_uuid(), u.id, 'scene', $1, $2, 'a fixture review', now() - $3::interval
		FROM users u
		ORDER BY u.id
		LIMIT 1`, sceneID, rating, age.String())
	require.NoError(s.t, err, "recording a review")
}

// score reads a scene's freshness score, or nil when the view holds no row for it -- a different
// fact from a score of zero, and the distinction the window and null-rating cases turn on.
func (s *sceneFreshnessTestRunner) score(sceneID uuid.UUID) *float64 {
	s.t.Helper()

	var score *float64
	err := s.db().QueryRow(context.Background(),
		`SELECT score FROM scene_freshness WHERE scene_id = $1`, sceneID).Scan(&score)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	require.NoError(s.t, err, "reading a freshness score")

	return score
}

// order runs the query with the given sort scoped to `candidates` and returns the ids in the
// order the sort produced. Page, sort and direction are not optional (the input's non-null
// defaults), and the ID criterion is what makes an ORDER assertion about these scenes rather than
// about the whole database.
func (s *sceneFreshnessTestRunner) order(sort models.SceneSortEnum, candidates []uuid.UUID) []uuid.UUID {
	s.t.Helper()

	input := models.SceneQueryInput{
		Page:      1,
		PerPage:   50,
		Sort:      sort,
		Direction: models.SortDirectionEnumDesc,
		ID: &models.MultiIDCriterionInput{
			Modifier: models.CriterionModifierIncludes,
			Value:    candidates,
		},
	}

	result, err := s.client.queryScenes(input)
	require.NoError(s.t, err)

	require.Len(s.t, result.Scenes, len(candidates),
		"the query must return every fixture scene before it can say anything about their order")

	ids := make([]uuid.UUID, 0, len(result.Scenes))
	for _, scene := range result.Scenes {
		id, err := uuid.FromString(scene.ID)
		require.NoError(s.t, err)
		ids = append(ids, id)
	}

	return ids
}

// The decay curve: 3 at age 0, 1.060660 at 48 hours, 0.314270 at a week.
func TestFreshnessDecaysWithAge(t *testing.T) {
	s := createSceneFreshnessTestRunner(t)

	fresh := s.freshScene()
	day := s.freshScene()
	week := s.freshScene()

	s.signalFingerprint(fresh, 0)
	s.signalFingerprint(day, 48*time.Hour)
	s.signalFingerprint(week, 168*time.Hour)

	s.refresh()

	// 3 / (1 + 0/48)^1.5 = 3
	require.NotNil(t, s.score(fresh), "a signal submitted now must have a score")
	assert.InDelta(t, 3.0, *s.score(fresh), 0.001)

	// 3 / (1 + 48/48)^1.5 = 3 / 2^1.5 = 3 / 2.828427 = 1.060660
	assert.InDelta(t, 1.060660, *s.score(day), 0.001)

	// 3 / (1 + 168/48)^1.5 = 3 / 4.5^1.5 = 3 / 9.545941 = 0.314270
	assert.InDelta(t, 0.314270, *s.score(week), 0.001)

	// And the sort agrees with the scores, newest first.
	assert.Equal(t, []uuid.UUID{fresh, day, week}, s.order(models.SceneSortEnumFresh, []uuid.UUID{week, fresh, day}))
}

// The three signals sum with their weights, and a review nobody rated is not a signal.
func TestFreshnessSumsItsSignals(t *testing.T) {
	s := createSceneFreshnessTestRunner(t)

	scene := s.freshScene()
	s.signalFingerprint(scene, 0)        // 3
	s.signalListAdd(scene, 48*time.Hour) // 3 / 2^1.5 = 1.060660
	five := 5
	s.signalReview(scene, &five, 48*time.Hour) // 2 / 2^1.5 = 0.707107

	unrated := s.freshScene()
	s.signalReview(unrated, nil, 0)

	s.refresh()

	// 3 + 1.060660 + 0.707107 = 4.767767
	assert.InDelta(t, 4.767767, *s.score(scene), 0.001,
		"a fingerprint is worth 3, a list add 3 and a 5-star review 2, and they sum")

	assert.Nil(t, s.score(unrated),
		"a review with no rating contributes nothing: it is not a signal, and it is not a -3")
}

// A withdrawal is a NEGATIVE SIGNAL, not an absence: the `vote` column records it, so it is worth
// -3 and the score can go below zero. A scene whose only claim was withdrawn ranks below a scene
// nobody has touched -- a disputed fingerprint does not promote anything.
func TestAWithdrawnFingerprintIsANegativeSignal(t *testing.T) {
	s := createSceneFreshnessTestRunner(t)

	contested := s.freshScene()
	s.signalFingerprint(contested, 0)         // +3
	s.signalFingerprintVote(contested, 0, -1) // -3

	withdrawn := s.freshScene()
	s.signalFingerprintVote(withdrawn, 0, -1)

	untouched := s.freshScene()

	s.refresh()

	// 3 - 3 = 0, and the row EXISTS: a scene whose signals cancel is not a scene without signals.
	require.NotNil(t, s.score(contested))
	assert.InDelta(t, 0.0, *s.score(contested), 0.001)

	require.NotNil(t, s.score(withdrawn))
	assert.InDelta(t, -3.0, *s.score(withdrawn), 0.001)

	assert.Equal(t, []uuid.UUID{untouched, withdrawn}, s.order(models.SceneSortEnumFresh, []uuid.UUID{withdrawn, untouched}),
		"a scene with a withdrawn claim sorts below one with no signals at all")
}

// The 90-day window is a boundary, not a comment on one.
func TestFreshnessWindowExcludesOlderSignals(t *testing.T) {
	s := createSceneFreshnessTestRunner(t)

	inside := s.freshScene()
	outside := s.freshScene()

	s.signalFingerprint(inside, 83*24*time.Hour)
	s.signalFingerprint(outside, 125*24*time.Hour)

	s.refresh()

	// 3 / (1 + 1992/48)^1.5 = 3 / 42.5^1.5 = 3 / 277.062 = 0.010828
	require.NotNil(t, s.score(inside))
	assert.InDelta(t, 0.010828, *s.score(inside), 0.0005)

	assert.Nil(t, s.score(outside),
		"a signal older than the window leaves no score row: out there the decay is 0.3% of one signal")
}

// The plan's headline ordering: many old signals rank below one fresh one.
func TestAnOlderButBusierSceneSortsBelowAFreshOne(t *testing.T) {
	s := createSceneFreshnessTestRunner(t)

	busy := s.freshScene()
	fresh := s.freshScene()

	for range 10 {
		s.signalFingerprint(busy, 240*time.Hour)
	}
	s.signalFingerprint(fresh, 0)

	s.refresh()

	// Ten signals at 240h are 10 * 3 / 6^1.5 = 10 * 0.204124 = 2.041240, against 3.0 for one
	// signal submitted now. More activity, older, ranks lower -- the decay doing work rather than
	// the count.
	require.NotNil(t, s.score(busy))
	assert.InDelta(t, 2.041240, *s.score(busy), 0.001)
	assert.InDelta(t, 3.0, *s.score(fresh), 0.001)

	assert.Equal(t, []uuid.UUID{fresh, busy}, s.order(models.SceneSortEnumFresh, []uuid.UUID{busy, fresh}))
}

// RISING compares two days, so it disagrees with FRESH on the same fixture -- by design.
//
// The fixture is built so that "this day only" and "this day minus the one before" give OPPOSITE
// answers, which is what makes the second half of the comparison testable: a scene with MORE today
// but also a busy yesterday must lose to a scene with less today and nothing yesterday. A pair
// where both readings agree would pass with the subtraction deleted.
func TestRisingComparesThisDayWithTheOneBefore(t *testing.T) {
	s := createSceneFreshnessTestRunner(t)

	// dipper: two signals in the last 24h, three in the 24 before it.
	dipper := s.freshScene()
	s.signalFingerprint(dipper, 2*time.Hour)
	s.signalFingerprint(dipper, 2*time.Hour)
	for range 3 {
		s.signalFingerprint(dipper, 30*time.Hour)
	}

	// climber: one signal in the last 24h and nothing before it.
	climber := s.freshScene()
	s.signalFingerprint(climber, 2*time.Hour)

	s.refresh()

	// Each 2-hour-old signal is 3 / (1 + 2/48)^1.5 = 2.821806 and each 30-hour-old one is
	// 3 / (1 + 30/48)^1.5 = 1.448242.
	require.NotNil(t, s.score(dipper))
	require.NotNil(t, s.score(climber))
	assert.InDelta(t, 2*2.821806+3*1.448242, *s.score(dipper), 0.001) // 10.088334
	assert.InDelta(t, 2.821806, *s.score(climber), 0.001)

	// FRESH: more total decayed engagement wins, so the busier scene leads.
	assert.Equal(t, []uuid.UUID{dipper, climber}, s.order(models.SceneSortEnumFresh, []uuid.UUID{climber, dipper}),
		"by total decayed engagement the scene with five signals leads")

	// RISING: today minus the day before is +1.299 for the dipper and +2.822 for the climber, so
	// the scene with LESS total activity but a quiet yesterday leads. The two sorts disagree, and
	// the disagreement is the feature -- a scene that peaked yesterday is not rising.
	assert.Equal(t, []uuid.UUID{climber, dipper}, s.order(models.SceneSortEnumRising, []uuid.UUID{dipper, climber}),
		"by this day against the day before, the scene that only climbed leads")
}

// The sort reads the view: a signal is invisible until the refresh runs, which is what
// "materialized and refreshed hourly" means and would be false if the sort aggregated the signal
// tables for itself.
func TestFreshnessAppearsOnlyAfterTheRefresh(t *testing.T) {
	s := createSceneFreshnessTestRunner(t)

	scene := s.freshScene()

	// Refresh FIRST, so the view is current and still knows nothing about this scene -- which is
	// also the assertion that the scene fixture's own submission was really removed.
	s.refresh()
	assert.Nil(t, s.score(scene), "a scene with no signals has no score row")

	s.signalFingerprint(scene, 0)
	assert.Nil(t, s.score(scene), "the signal was recorded after the last refresh")

	s.refresh()
	require.NotNil(t, s.score(scene), "the refresh is what publishes it")
	assert.InDelta(t, 3.0, *s.score(scene), 0.001)
}

// A scene nobody has engaged with is still in a freshness feed, at the END of it. The LEFT JOIN in
// the sort is what makes that true.
func TestASceneWithNoSignalsIsLastAndNotMissing(t *testing.T) {
	s := createSceneFreshnessTestRunner(t)

	engaged := s.freshScene()
	untouched := s.freshScene()

	s.signalFingerprint(engaged, 0)
	s.refresh()

	// The ORDER assertion comes after a count of the subject: "the untouched scene is last" is
	// vacuous if the query never returned it, and that is asserted inside `order`.
	assert.Nil(t, s.score(untouched))
	assert.Equal(t, []uuid.UUID{engaged, untouched}, s.order(models.SceneSortEnumFresh, []uuid.UUID{untouched, engaged}))
}
