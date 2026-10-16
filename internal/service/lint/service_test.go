package lint

import (
	"context"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/queries"
)

// The parts of the lint service that need no database: which registry rows are refused, what a
// candidate carries, and WHICH QUESTION IS ASKED. The SQL's own meaning (are these really the
// performers that share a URL?) is in the integration test, because a fake that returns rows cannot
// tell you whether the join is right.
//
// The fake embeds queries.Querier so that only the methods this package calls exist. A full stand-in
// would be hundreds of methods of noise, and every one of them a place to drift from the real
// interface -- which is why the embedding idiom is worth the one sharp edge: a call to a method the
// test forgot to implement panics on a nil interface instead of silently returning zero values.

type fakeQueries struct {
	queries.Querier

	defs       []queries.LintQuestDefinition
	inserted   []queries.InsertBuiltinLintQuestDefinitionParams
	insertErr  map[string]error
	sceneRows  []queries.LintDuplicateSceneURLsRow
	perfRows   []queries.LintDuplicatePerformerURLsRow
	aliasRows  []queries.LintPerformerAliasCollisionsRow
	dateRows   []queries.LintImpossibleSceneDatesRow
	imageRows  []queries.LintLowResolutionPerformerImagesRow
	lastScene  queries.LintDuplicateSceneURLsParams
	lastPerf   queries.LintDuplicatePerformerURLsParams
	lastAlias  queries.LintPerformerAliasCollisionsParams
	lastDate   queries.LintImpossibleSceneDatesParams
	lastImage  queries.LintLowResolutionPerformerImagesParams
	sceneCalls int
}

func (f *fakeQueries) ListLintQuestDefinitions(context.Context) ([]queries.LintQuestDefinition, error) {
	return f.defs, nil
}

func (f *fakeQueries) ListEnabledLintQuestDefinitions(context.Context) ([]queries.LintQuestDefinition, error) {
	var out []queries.LintQuestDefinition
	for _, d := range f.defs {
		if d.Enabled {
			out = append(out, d)
		}
	}
	return out, nil
}

func (f *fakeQueries) GetLintQuestDefinition(_ context.Context, slug string) (queries.LintQuestDefinition, error) {
	for _, d := range f.defs {
		if d.Slug == slug {
			return d, nil
		}
	}
	return queries.LintQuestDefinition{}, pgx.ErrNoRows
}

func (f *fakeQueries) InsertBuiltinLintQuestDefinition(_ context.Context, arg queries.InsertBuiltinLintQuestDefinitionParams) (queries.LintQuestDefinition, error) {
	if err, ok := f.insertErr[arg.Slug]; ok {
		return queries.LintQuestDefinition{}, err
	}
	f.inserted = append(f.inserted, arg)
	return queries.LintQuestDefinition{Slug: arg.Slug, Description: arg.Description, BountyPoints: arg.BountyPoints, Enabled: true, IsBuiltin: true}, nil
}

func (f *fakeQueries) LintDuplicateSceneURLs(_ context.Context, arg queries.LintDuplicateSceneURLsParams) ([]queries.LintDuplicateSceneURLsRow, error) {
	f.sceneCalls++
	f.lastScene = arg
	return f.sceneRows, nil
}

func (f *fakeQueries) LintDuplicatePerformerURLs(_ context.Context, arg queries.LintDuplicatePerformerURLsParams) ([]queries.LintDuplicatePerformerURLsRow, error) {
	f.lastPerf = arg
	return f.perfRows, nil
}

func (f *fakeQueries) LintPerformerAliasCollisions(_ context.Context, arg queries.LintPerformerAliasCollisionsParams) ([]queries.LintPerformerAliasCollisionsRow, error) {
	f.lastAlias = arg
	return f.aliasRows, nil
}

func (f *fakeQueries) LintImpossibleSceneDates(_ context.Context, arg queries.LintImpossibleSceneDatesParams) ([]queries.LintImpossibleSceneDatesRow, error) {
	f.lastDate = arg
	return f.dateRows, nil
}

func (f *fakeQueries) LintLowResolutionPerformerImages(_ context.Context, arg queries.LintLowResolutionPerformerImagesParams) ([]queries.LintLowResolutionPerformerImagesRow, error) {
	f.lastImage = arg
	return f.imageRows, nil
}

// A redisabled detector's row is history; an enabled one with no implementation is a broken
// instance. This is migration 98's stated contract ("the registry refuses it at startup") and the
// test says which of the two directions is which.
func TestValidateRefusesAnEnabledSlugThisBuildDoesNotImplement(t *testing.T) {
	f := &fakeQueries{defs: []queries.LintQuestDefinition{
		{Slug: "duplicate_scene_url", Enabled: true},
		{Slug: "a_detector_from_a_future_release", Enabled: true},
	}}
	err := NewService(f).Validate(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnknownDetector)
	// Named, not summarised: an operator has to be able to find the row.
	assert.Contains(t, err.Error(), "a_detector_from_a_future_release")
	// And the message says what this build DOES implement, so the fix does not require reading code.
	assert.Contains(t, err.Error(), "duplicate_scene_url")
}

// AN EMPTY REGISTRY IS NOT A CLEAN ARCHIVE, and this test is why the service refuses one. The rows are
// written by the instance's startup, so a registry with nothing in it means the seeding never ran -- and
// a board drawn from it has no rows on it at all, which is exactly what a board looks like when every
// detector has run and found nothing. The integration harness found this the honest way: it builds an
// in-process server around the resolvers, never runs `cmd/stash-box`'s startup, and every board test in
// it "passed" against a board that had never run a detector.
func TestValidateRefusesAnEmptyRegistry(t *testing.T) {
	err := NewService(&fakeQueries{}).Validate(context.Background())
	require.Error(t, err, "an empty registry must not be able to serve a clean-looking board")
	assert.ErrorIs(t, err, ErrUnknownDetector)
	assert.Contains(t, err.Error(), "empty")
	// And it says what a build DOES implement, because the fix might be "seed this instance" or might be
	// "you are running a binary that ships no detectors", and the message has to serve both.
	assert.Contains(t, err.Error(), "duplicate_performer_url")
}

// THE BOARD ITSELF HAS TO REFUSE, and this test exists because the live walk found that it did not.
// The startup check protects the instance; the board is reached by other callers (the CLI, and the
// frontend when it lands), and a skipped row is invisible there -- the board simply comes back with one
// fewer detector, which reads exactly like a detector that found nothing. Found by planting an enabled
// unknown row in the deployed registry and watching `-validate` refuse while the board returned five
// detectors instead of six.
func TestTheBoardRefusesARegistryThisBuildDoesNotImplement(t *testing.T) {
	f := &fakeQueries{defs: []queries.LintQuestDefinition{
		{Slug: "duplicate_scene_url", Enabled: true},
		{Slug: "a_detector_from_a_future_release", Enabled: true},
	}}
	reports, err := NewService(f).Board(context.Background(), 5)
	require.Error(t, err, "a board must not render from a registry it cannot honour")
	assert.ErrorIs(t, err, ErrUnknownDetector)
	assert.Empty(t, reports, "and it must not return the detectors it DID manage to run, either")
}

func TestValidateAllowsADisabledSlugThisBuildDoesNotImplement(t *testing.T) {
	f := &fakeQueries{defs: []queries.LintQuestDefinition{
		{Slug: "a_detector_that_was_retired", Enabled: false},
	}}
	require.NoError(t, NewService(f).Validate(context.Background()),
		"a retired detector's row is history; refusing to start over it makes a rollback impossible")
}

// Every builtin gets a row, and the second start inserts NONE -- which is the property that matters,
// because a seeding pass that re-inserted would fight an operator's `enabled = false`.
func TestEnsureBuiltinsSeedsOnceAndReportsWhatItInserted(t *testing.T) {
	f := &fakeQueries{}
	svc := NewService(f)

	inserted, err := svc.EnsureBuiltins(context.Background())
	require.NoError(t, err)
	assert.Equal(t, len(Builtins()), inserted)
	assert.Len(t, f.inserted, len(Builtins()))
	for _, arg := range f.inserted {
		assert.NotEmpty(t, arg.Description, "a detector a curator cannot read is one they will not work")
	}

	// The second start: ON CONFLICT DO NOTHING answers with no row, which must read as "already
	// seeded" rather than as a failure.
	f.insertErr = map[string]error{}
	for _, d := range Builtins() {
		f.insertErr[d.Slug] = pgx.ErrNoRows
	}
	inserted, err = svc.EnsureBuiltins(context.Background())
	require.NoError(t, err, "a second start must not fail on rows that are already there")
	assert.Zero(t, inserted)
}

// A real error is NOT swallowed as "already seeded": the distinction between "inserted nothing" and
// "the database refused" is the whole reason errNoRow exists.
func TestEnsureBuiltinsReportsARealInsertFailure(t *testing.T) {
	f := &fakeQueries{insertErr: map[string]error{"duplicate_scene_url": pgx.ErrTxClosed}}
	_, err := NewService(f).EnsureBuiltins(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate_scene_url")
}

// THE IMPORT PASS ASKS THE DATABASE, not a filtered slice of the answer. If the filter were applied in
// Go over a bounded result, an archive with more findings than the limit would report an import as
// clean -- and this test is the one that notices, by checking what the query was ASKED.
func TestValidateEntitiesPushesTheEntityFilterIntoTheQuery(t *testing.T) {
	sceneID := uuid.Must(uuid.NewV7())
	f := &fakeQueries{}
	svc := NewService(f)

	_, err := svc.ValidateEntities(context.Background(), []EntityRef{
		{EntityType: "scene", EntityID: sceneID},
	})
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{sceneID}, f.lastScene.EntityIds,
		"the ids must reach the query; filtering in Go cannot answer an exact question")
}

// A detector is not asked about an entity type it does not speak: passing performers to a scene
// detector would return nothing and read as "clean" rather than as "not asked".
func TestValidateEntitiesDoesNotAskASceneDetectorAboutPerformers(t *testing.T) {
	f := &fakeQueries{}
	svc := NewService(f)

	reports, err := svc.ValidateEntities(context.Background(), []EntityRef{
		{EntityType: "performer", EntityID: uuid.Must(uuid.NewV7())},
	})
	require.NoError(t, err)
	assert.Zero(t, f.sceneCalls, "a scene detector ran for a performer-only import")
	for _, r := range reports {
		assert.Equal(t, "performer", r.EntityType)
	}
}

// A zero and a truncated zero must not look the same. A detector that fills its limit is reporting
// "at least this many", and a board that showed exactly 5 of 4,000 without saying so would read as an
// archive in good shape.
func TestADetectorThatFillsItsLimitSaysSo(t *testing.T) {
	f := &fakeQueries{sceneRows: []queries.LintDuplicateSceneURLsRow{
		{EntityID: uuid.Must(uuid.NewV7()), Detail: "https://example.test/a"},
		{EntityID: uuid.Must(uuid.NewV7()), Detail: "https://example.test/b"},
	}}
	svc := NewService(f)

	rep, err := svc.Candidates(context.Background(), "duplicate_scene_url", 2)
	require.NoError(t, err)
	assert.Equal(t, 2, rep.Count)
	assert.NotEmpty(t, rep.LimitNotice)

	// And below the limit there is nothing to notice.
	rep, err = svc.Candidates(context.Background(), "duplicate_scene_url", 10)
	require.NoError(t, err)
	assert.Equal(t, 2, rep.Count)
	assert.Empty(t, rep.LimitNotice)
}

// A caller asking about a slug this build does not have gets a refusal that names the slug AND what
// the build does have -- the difference between a bug report and a diagnosis.
func TestCandidatesRefusesAnUnknownSlugByName(t *testing.T) {
	_, err := NewService(&fakeQueries{}).Candidates(context.Background(), "no_such_detector", 5)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnknownDetector)
	assert.Contains(t, err.Error(), "no_such_detector")
	assert.Contains(t, err.Error(), "duplicate_scene_url")
}

// The registry is authoritative for what a curator READS and what a candidate is WORTH; the build is
// authoritative for what the detector DOES. So a re-priced row keeps its price, and a row's edited
// description reaches the board.
func TestTheRegistryOverridesTheBuiltinDescriptionAndPrice(t *testing.T) {
	f := &fakeQueries{defs: []queries.LintQuestDefinition{
		{Slug: "duplicate_scene_url", Description: "our own wording", BountyPoints: 42, Enabled: true},
	}}
	rep, err := NewService(f).Candidates(context.Background(), "duplicate_scene_url", 5)
	require.NoError(t, err)
	assert.Equal(t, "our own wording", rep.Description)
	assert.Equal(t, 42, rep.BountyPoints)
}

// A caller that asks for everything must not be able to hand the database an unbounded query.
func TestTheLimitIsClampedAndZeroMeansTheDefault(t *testing.T) {
	f := &fakeQueries{}
	svc := NewService(f)

	_, err := svc.Candidates(context.Background(), "duplicate_scene_url", 0)
	require.NoError(t, err)
	assert.Equal(t, int32(DefaultLimit), f.lastScene.Limit)

	_, err = svc.Candidates(context.Background(), "duplicate_scene_url", HardLimit*10)
	require.NoError(t, err)
	assert.Equal(t, int32(HardLimit), f.lastScene.Limit)
}

// A scene joined to three too-young performers is ONE finding. Without the dedupe a board would ask
// for five and show two.
func TestTheSameFindingFromTwoRowsIsOneCandidate(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	in := []Candidate{
		{EntityID: id, Detail: "dated 1990-01-01, before Alex born 2000-01-01"},
		{EntityID: id, Detail: "dated 1990-01-01, before Alex born 2000-01-01"},
		{EntityID: id, Detail: "dated 1990-01-01, in the future"},
	}
	out := dedupe(in)
	assert.Len(t, out, 2, "the same detail twice is one finding; a different detail is a second")
}

// Every builtin is distinct, lowercase snake_case, and carries a description -- the rules migration
// 98's CHECK enforces on the ROW, checked here on the Go side so a bad slug fails in a unit test
// rather than on a live instance's startup.
func TestEveryBuiltinSlugIsAPlausibleRegistrySlug(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range Builtins() {
		assert.Regexp(t, `^[a-z][a-z0-9_]*$`, d.Slug)
		assert.LessOrEqual(t, len(d.Slug), 64)
		assert.NotEmpty(t, d.Description)
		assert.NotZero(t, d.BountyPoints)
		assert.False(t, seen[d.Slug], "duplicate slug %q", d.Slug)
		seen[d.Slug] = true
		assert.NotNil(t, d.run, "%s has no implementation", d.Slug)
		assert.Contains(t, []string{"performer", "scene", "studio", "site", "tag"}, d.EntityType)
	}
}
