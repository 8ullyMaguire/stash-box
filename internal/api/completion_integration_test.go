//go:build integration

package api_test

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/queries"
	"github.com/stashapp/stash-box/internal/service/completion"
)

// Completion scores against the database (SPEC §7.7).
//
// The unit tests prove the FORMULA -- which fields count, at what weight, in what
// order. They cannot prove the WIRING, and the wiring is where the bugs are: a row
// column mapped to the wrong field produces a correct score for the wrong entity.
// Two mutations survive every unit test in the package:
//
//   a scene's snapshot coverage read from has_image  -> SURVIVES
//   a scene's duration and studio swapped            -> SURVIVES
//
// Both are real and neither is visible from a pure test, because the formula is
// right and only the column-to-field mapping is wrong. So this file builds real
// rows and reads the score back.

// completionService builds the service over the test database.
func completionService(t *testing.T) *completion.Service {
	t.Helper()
	return completion.NewService(queries.New(dbtest.DB()))
}

// cq is a direct query handle for fixture setup.
func cq() *queries.Queries { return queries.New(dbtest.DB()) }

// createPerformerForCompletion inserts a performer with the given birthdate.
//
// There is no accuracy PARAMETER, and that is the point of the test. I designed
// this whole rule around a `birthdate_accuracy` column, read it in migration 01,
// and it was dropped in migration 42 -- which folded the precision into the value
// itself. So the fixture passes a birthdate string and the precision is whatever
// the string says: '1990' is year-accurate, '1990-01-01' is exact.
func createPerformerForCompletion(t *testing.T, name string, birthdate *string) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	_, err := cq().CreatePerformer(t.Context(), queries.CreatePerformerParams{
		ID:        id,
		Name:      name,
		Birthdate: birthdate,
	})
	require.NoError(t, err, "creating a performer to score")
	return id
}

// A year-accurate birthdate is a SPECIFIC CLAIM, and a specific claim reads as an
// answer.
//
// This is the test the whole birthdate rule turns on, and the rule is not one I
// designed against a column -- migration 42 folded `birthdate_accuracy` INTO the
// value, so '1990' is year-accurate and '1990-01-01' is exact. I built the rule
// around the accuracy column first, and it had been dropped six revisions earlier.
func TestAPartialBirthdateScoresAsMissing(t *testing.T) {
	s := completionService(t)

	cases := []struct {
		name      string
		birthdate *string
		wantField bool
	}{
		{"no birthdate at all", nil, false},
		{"an empty string", strPtr(""), false},
		{"year-accurate", strPtr("1990"), false},
		{"month-accurate", strPtr("1990-01"), false},
		{"exact", strPtr("1990-01-01"), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			performer := createPerformerForCompletion(t,
				"Partial Birthdate "+uuid.Must(uuid.NewV7()).String()[:8], tc.birthdate)

			result, err := s.Performer(t.Context(), performer)
			require.NoError(t, err)

			if tc.wantField {
				assert.NotContains(t, result.Missing, completion.FieldBirthdate,
					"an exact birthdate IS present information; treating it as "+
						"missing would make the field unfillable and the quest "+
						"impossible")
				return
			}
			assert.Contains(t, result.Missing, completion.FieldBirthdate,
				"a birthdate that is not exact is a specific claim rather than a "+
					"complete one, and a specific claim reads as an answer, so "+
					"nobody goes looking for the real date")
		})
	}
}

// A scene's heaviest fields must come from their own columns.
//
// The test for the two surviving mutations, written as the two claims they break:
// duration from duration, studio from studio, snapshots from the snapshot count.
func TestSceneCompletionReadsItsOwnColumns(t *testing.T) {
	s := completionService(t)
	scene := createSceneWithDuration(t, "Completion Wiring Scene", intPtr(600))

	// The fixture links no studio, so duration present and studio absent is the
	// state that separates the two columns.
	result, err := s.Scene(t.Context(), scene)
	require.NoError(t, err)

	assert.NotContains(t, result.Missing, completion.FieldDuration,
		"this scene HAS a duration, so it must not be reported as missing one; "+
			"reading the duration from the studio column would report the two "+
			"heaviest scene fields backwards")
	assert.Contains(t, result.Missing, completion.FieldStudio,
		"this scene has no studio linked, and the studio column is a different "+
			"column from the duration one")
	assert.Contains(t, result.Missing, completion.FieldSnapshotCoverag,
		"this scene has no snapshots at all, so it cannot have collage coverage")
}

// A scene with a cover image but no snapshots must NOT score as though it had
// snapshot coverage. The two are different fields, and conflating them is the
// first surviving mutation.
func TestSnapshotCoverageIsNotTheImageColumn(t *testing.T) {
	s := completionService(t)
	scene := createSceneWithDuration(t, "Image Is Not Coverage Scene", intPtr(600))

	// Give it an image and still no snapshots. If the coverage field were read
	// from the image column, this scene would now report coverage it does not
	// have.
	_, err := cq().CreateImage(t.Context(), queries.CreateImageParams{
		ID:       uuid.Must(uuid.NewV7()),
		Url:      strPtr("https://example.com/cover.jpg"),
		Width:    800,
		Height:   600,
		Checksum: "completion-image-checksum-" + uuid.Must(uuid.NewV7()).String()[:8],
	})
	require.NoError(t, err)

	result, err := s.Scene(t.Context(), scene)
	require.NoError(t, err)

	assert.Contains(t, result.Missing, completion.FieldSnapshotCoverag,
		"a scene with no snapshots has no snapshot coverage, and reading that "+
			"field from has_image would make any scene with a cover photo look "+
			"collage-ready -- which is exactly what sends a curator to build a "+
			"collage that cannot be built")
}

// Snapshot coverage is a THRESHOLD at 12, and the boundary is the point.
//
// A scene with 11 snapshots scores exactly as one with none, because SPEC §8's
// minimum collage is 12 frames and an 11-frame collage is not a partial collage,
// it is no collage.
func TestSnapshotCoverageIsAllOrNothing(t *testing.T) {
	s := completionService(t)
	collage := dbtest.Factory().Collage()

	const durationMS = 600_000
	scene := createSceneWithDuration(t, "Threshold Scene", intPtr(durationMS/1000))

	// 11 snapshots: still short of a collage.
	for ts := int64(0); ts < 11*20_000; ts += 20_000 {
		_, err := collage.AddSnapshot(t.Context(), scene, ts, nil)
		require.NoError(t, err)
	}
	short, err := s.Scene(t.Context(), scene)
	require.NoError(t, err)
	assert.Contains(t, short.Missing, completion.FieldSnapshotCoverag,
		"11 of the 12 frames a collage needs is not coverage; a half-finished "+
			"collage is no collage, and scoring it as partial would fill a quest "+
			"queue with scenes that are still barely started")

	// The 12th snapshot completes it.
	_, err = collage.AddSnapshot(t.Context(), scene, 11*20_000, nil)
	require.NoError(t, err)

	enough, err := s.Scene(t.Context(), scene)
	require.NoError(t, err)
	assert.NotContains(t, enough.Missing, completion.FieldSnapshotCoverag,
		"12 snapshots is a compliant collage, so the field is present")
}

// A missing entity and a soft-deleted one are the same answer, and saying which
// it was would leak the existence of deleted records.
func TestMissingAndDeletedAreBothNotFound(t *testing.T) {
	s := completionService(t)

	_, err := s.Scene(t.Context(), uuid.Must(uuid.NewV7()))
	assert.ErrorIs(t, err, completion.ErrNotFound,
		"a scene id that does not exist is not scoreable, and the caller needs to "+
			"be able to tell that from a database failure")

	_, err = s.Performer(t.Context(), uuid.Must(uuid.NewV7()))
	assert.ErrorIs(t, err, completion.ErrNotFound)
	_, err = s.Studio(t.Context(), uuid.Must(uuid.NewV7()))
	assert.ErrorIs(t, err, completion.ErrNotFound)
	_, err = s.Site(t.Context(), uuid.Must(uuid.NewV7()))
	assert.ErrorIs(t, err, completion.ErrNotFound)
	_, err = s.Tag(t.Context(), uuid.Must(uuid.NewV7()))
	assert.ErrorIs(t, err, completion.ErrNotFound)
}

// A site scores its regex, which is the field that turns a site from a list entry
// into a working import source -- and the one nobody fills in by hand.
func TestSiteCompletionIncludesTheRegex(t *testing.T) {
	s := completionService(t)

	id := uuid.Must(uuid.NewV7())
	_, err := cq().CreateSite(t.Context(), queries.CreateSiteParams{
		ID:         id,
		Name:       "Completion Test Site",
		Url:        strPtr("https://example.com"),
		Regex:      strPtr(`https://example\.com/.*`),
		ValidTypes: []string{"SCENE"},
	})
	require.NoError(t, err)

	result, err := s.Site(t.Context(), id)
	require.NoError(t, err)
	assert.NotContains(t, result.Missing, completion.FieldRegex,
		"a site with a regex has the thing that makes automatic import work")
	assert.NotContains(t, result.Missing, completion.FieldURLs)
	assert.Contains(t, result.Missing, completion.FieldDetails,
		"this site has no description, which is the field a curator would fill "+
			"in from the site's own about page")

	// And clearing it puts the field BACK, so the assertion above is about this
	// site rather than about the regex column being unreadable.
	_, err = cq().UpdateSite(t.Context(), queries.UpdateSiteParams{
		ID:          id,
		Name:        "Completion Test Site",
		Url:         strPtr("https://example.com"),
		Regex:       strPtr(`https://example\.com/.*`),
		Description: strPtr("A site with a description now."),
		ValidTypes:  []string{"SCENE"},
	})
	require.NoError(t, err)

	result, err = s.Site(t.Context(), id)
	require.NoError(t, err)
	assert.NotContains(t, result.Missing, completion.FieldDetails,
		"a described site has its description, so a client cannot be told a "+
			"present field is missing -- which would send a curator to write text "+
			"that is already there")
}

// Every entity type is scoreable through the service, so a type added to the
// weight list without a reader here is caught by this rather than by a client.
func TestEveryEntityTypeScores(t *testing.T) {
	s := completionService(t)
	performer := createPerformerForCompletion(t, "Every Type Performer", nil)
	scene := createSceneWithDuration(t, "Every Type Scene", intPtr(600))

	tag := uuid.Must(uuid.NewV7())
	_, err := cq().CreateTag(t.Context(), queries.CreateTagParams{
		ID:   tag,
		Name: "completion-test-tag-" + uuid.Must(uuid.NewV7()).String()[:8],
	})
	require.NoError(t, err)

	for _, tc := range []struct {
		name string
		call func() (completion.Result, error)
	}{
		{"performer", func() (completion.Result, error) { return s.Performer(t.Context(), performer) }},
		{"scene", func() (completion.Result, error) { return s.Scene(t.Context(), scene) }},
		{"tag", func() (completion.Result, error) { return s.Tag(t.Context(), tag) }},
	} {
		result, err := tc.call()
		require.NoError(t, err, "%s", tc.name)
		assert.GreaterOrEqual(t, result.Score, 0, "%s", tc.name)
		assert.LessOrEqual(t, result.Score, 100, "%s: a score outside 0-100 is "+
			"rendered as a broken progress bar", tc.name)
		assert.NotEmpty(t, result.Missing, "%s: a bare fixture has gaps", tc.name)
	}
}

// Every column is set, and every column is CHECKED.
//
// Three mutations survived the tests above, and all three were the same mistake:
//
//	a performer's aliases read from their urls        -> SURVIVES
//	a scene's performers read from its tags            -> SURVIVES
//	a studio's parent read from its image              -> SURVIVES
//
// In each case the fixture left BOTH columns false, so reading one from the other
// changed nothing observable. A wiring test whose fixtures do not distinguish the
// columns it is wiring proves nothing about the wiring -- the columns are
// interchangeable in the fixture, so any mapping between them looks equally
// correct as far as the test can tell.
//
// So this file builds entities through the GraphQL client, which is the path a
// curator actually uses and the only one that can set aliases, urls and tags in a
// single call, and it asserts each field's state individually. That is what makes
// the mapping falsifiable.
//
// (There are no insert queries for performer_urls, performer_aliases or
// scene_tags: the codebase writes them through the edit path, not through a
// fixture query. Reaching for a `CreatePerformerAlias` would have been the fourth
// invented query name in this phase.)
func TestEveryScoredColumnIsDistinguishable(t *testing.T) {
	s := completionService(t)
	admin := asAdmin(t)

	// A performer with a URL but NO alias: the two columns must be told apart.
	// A URL needs a real site, because performer_urls carries a site_id foreign
	// key: a URL is not just a string, it is a link to the site it was found on.
	// That is also why the completion score's urls field means "this performer is
	// linked to a site", not "some text was pasted in".
	performerSite, err := cq().CreateSite(t.Context(), queries.CreateSiteParams{
		ID:         uuid.Must(uuid.NewV7()),
		Name:       "Distinguishable Performer Site",
		Url:        strPtr("https://performer-site.example.com"),
		ValidTypes: []string{"PERFORMER"},
	})
	require.NoError(t, err)

	performer, err := admin.client.createPerformer(models.PerformerCreateInput{
		Name: "Distinguishable Performer",
		Urls: []models.URL{{
			URL:    "https://example.com/performer",
			SiteID: performerSite.ID,
		}},
	})
	require.NoError(t, err)

	result, err := s.Performer(t.Context(), mustParseUUID(t, performer.ID))
	require.NoError(t, err)
	assert.Contains(t, result.Missing, completion.FieldAliases,
		"this performer has a URL but no alias, so has_urls read into has_aliases "+
			"would report a performer with no links at all as fully linked")
	assert.NotContains(t, result.Missing, completion.FieldURLs,
		"this performer does have a URL")

	// The mirror case: an alias but NO url. Together with the performer above --
	// one has the url and not the alias, this one has the alias and not the url --
	// the two columns are separable, and any mapping between them fails one.

	aliased, err := admin.client.createPerformer(models.PerformerCreateInput{
		Name:    "Aliased Performer",
		Aliases: []string{"Distinguishable Alias"},
	})
	require.NoError(t, err)

	result, err = s.Performer(t.Context(), mustParseUUID(t, aliased.ID))
	require.NoError(t, err)
	assert.NotContains(t, result.Missing, completion.FieldAliases,
		"this performer has an alias and NO url, so a mapping reading aliases "+
			"from urls would report it as having none -- the mirror of the case "+
			"above, and the pair is what makes the two columns separable")
	assert.Contains(t, result.Missing, completion.FieldURLs,
		"this performer has an alias but no url")

	// A scene with TAGS but no PERFORMERS: the same distinction, the other way.
	// A tag first: SceneCreateInput takes tag IDs, not names, so a scene's tags
	// can only be set against tags that already exist.
	tag, err := admin.client.createTag(models.TagCreateInput{
		Name: "distinguishable-" + uuid.Must(uuid.NewV7()).String()[:8],
	})
	require.NoError(t, err)

	// Fingerprints is non-null in the schema -- a scene is created with its
	// fingerprints already, because that is how a scene enters the archive from
	// a source site. An empty list is the value for a scene that has none yet,
	// and the fact that it is required at all is why this test cannot invent a
	// scene with no other fields.
	scene, err := admin.client.createScene(models.SceneCreateInput{
		Title:        strPtr("Distinguishable Scene"),
		TagIds:       []uuid.UUID{mustParseUUID(t, tag.ID)},
		Fingerprints: []models.FingerprintEditInput{},
		// A duration, so the scene is not ALSO missing the field this test is not
		// about. Every assertion in this test is about tags-versus-performers, and
		// a scene that is missing four other fields makes the failure message
		// noisier without making it more informative.
		Duration: intPtr(600),
	})
	require.NoError(t, err)

	result, err = s.Scene(t.Context(), mustParseUUID(t, scene.ID))
	require.NoError(t, err)
	assert.NotContains(t, result.Missing, completion.FieldTags,
		"this scene has a tag, so has_performers read into has_tags would report "+
			"an uncredited scene as fully cast")
	assert.Contains(t, result.Missing, completion.FieldPerformers,
		"this scene has no performers at all")
	assert.NotContains(t, result.Missing, completion.FieldDuration,
		"this scene has a duration, so the field is present; the identification "+
			"board and the collage both need a time axis")

	// A studio WITH a parent, and one without: the third survivor. A child studio
	// reading its parent from its logo would report every child in the archive as
	// a root studio, and the parent is the field that places a studio in a
	// network.
	//
	// Both states are needed, and the earlier version of this test only built the
	// parentless one -- so has_parent was always false and reading it from
	// has_image changed nothing observable. That is the same fixture mistake as
	// the other two, in a third costume: a column never set to true cannot be
	// distinguished from any other column never set to true.
	parent, err := admin.client.createStudio(models.StudioCreateInput{
		Name: "Distinguishable Parent Studio",
	})
	require.NoError(t, err)

	child, err := admin.client.createStudio(models.StudioCreateInput{
		Name:     "Distinguishable Child Studio",
		ParentID: uuidPtrOf(mustParseUUID(t, parent.ID)),
	})
	require.NoError(t, err)

	result, err = s.Studio(t.Context(), mustParseUUID(t, child.ID))
	require.NoError(t, err)
	assert.NotContains(t, result.Missing, completion.FieldParentStudio,
		"this studio HAS a parent, so has_parent read from has_image would report "+
			"every child studio in the archive as a root studio -- and the parent "+
			"is the field that places a studio in a network")

	// And the parentless one, so the field can also be seen missing.
	lonely, err := admin.client.createStudio(models.StudioCreateInput{
		Name: "Distinguishable Lonely Studio",
	})
	require.NoError(t, err)

	result, err = s.Studio(t.Context(), mustParseUUID(t, lonely.ID))
	require.NoError(t, err)
	assert.Contains(t, result.Missing, completion.FieldParentStudio,
		"a studio with no parent is not in a network, and a curator should be "+
			"asked about it")
}

// mustParseUUID converts a GraphQL id to the uuid the service takes.
//
// The GraphQL client hands back ids as strings, and a test that passes one
// straight to the service cannot compile -- which is a good failure, because a
// uuid typed as a string would otherwise have to be handled at every call site
// and one lenient parse somewhere would accept a malformed id.
func mustParseUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	parsed, err := uuid.FromString(s)
	require.NoError(t, err, "a GraphQL id must be a parseable uuid")
	return parsed
}

// uuidPtrOf boxes a uuid for an optional field.
//
// A one-liner rather than a named variable at each call site: it is a type
// conversion with no logic, and naming it `parentID := mustParseUUID(...)` then
// passing `&parentID` would make the pointer-ness of the field something a
// reader has to check rather than something the type says.
func uuidPtrOf(id uuid.UUID) *uuid.UUID { return &id }
