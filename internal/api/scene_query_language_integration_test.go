//go:build integration

package api_test

import (
	"fmt"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/models"
)

// The search language reaches the query surface through `SceneQueryInput.query`, and the two
// tests here exist because a language that agrees with ITSELF and not with the query builder is
// the failure mode that matters: the parser is unit-tested in internal/service/search, so what
// is proven HERE is that the text a reader types and the filter a client could have built by
// hand return the same scenes, against a real database, through GraphQL.
//
// The fixture deliberately carries only the SPECIFIC tag, so a filter that ignores the tag
// hierarchy would return nothing and the equality below could not hold by accident. Same
// reasoning as tag_ontology_integration_test.go, whose runner this reuses for its fixtures.

type sceneQueryLanguageRunner struct {
	tagOntologyTestRunner
}

func createSceneQueryLanguageRunner(t *testing.T) *sceneQueryLanguageRunner {
	return &sceneQueryLanguageRunner{tagOntologyTestRunner: *createTagOntologyTestRunner(t)}
}

// name is the tag's NAME as the database holds it: the language resolves names, so a test that
// invented one would be testing its own fixture.
func (s *sceneQueryLanguageRunner) name(tagID uuid.UUID) string {
	return s.tag(tagID).Name
}

// searchInput runs a filter and returns which of `candidates` it found. Page, Sort and Direction
// are not optional (the input's non-null defaults), and the ID criterion is what makes an
// absence assertion mean something -- without it, "not in the results" is also true of a query
// that has stopped working.
func (s *sceneQueryLanguageRunner) searchInput(input models.SceneQueryInput, candidates []uuid.UUID) map[uuid.UUID]bool {
	input.Page = 1
	input.PerPage = 50
	input.Sort = models.SceneSortEnumTitle
	input.Direction = models.SortDirectionEnumAsc
	input.ID = &models.MultiIDCriterionInput{
		Modifier: models.CriterionModifierIncludes,
		Value:    candidates,
	}

	result, err := s.client.queryScenes(input)
	assert.NoError(s.t, err)
	if result == nil {
		s.t.Fatal("the scene query returned nothing")
	}

	found := make(map[uuid.UUID]bool, len(result.Scenes))
	for _, scene := range result.Scenes {
		id, err := uuid.FromString(scene.ID)
		assert.NoError(s.t, err)
		found[id] = true
	}

	return found
}

func TestTheQueryLanguageReturnsWhatTheFilterItLowersIntoReturns(t *testing.T) {
	s := createSceneQueryLanguageRunner(t)

	general := s.named("general")
	specific := s.named("specific")
	other := s.named("other")

	// `specific` implies `general`: the fixture carries ONLY the specific tag, so the language's
	// bare word has to expand through the hierarchy to find it.
	s.mustSetImplications(specific, []uuid.UUID{general}, nil)

	implied := s.sceneTagged(specific)
	excluded := s.sceneTagged(general, other)
	untagged := s.sceneTagged()

	s.visible(implied, excluded, untagged)

	query := fmt.Sprintf("%s -%s", s.name(general), s.name(other))

	result, err := s.client.queryScenes(models.SceneQueryInput{
		Page:      1,
		PerPage:   50,
		Sort:      models.SceneSortEnumTitle,
		Direction: models.SortDirectionEnumAsc,
		ID: &models.MultiIDCriterionInput{
			Modifier: models.CriterionModifierIncludes,
			Value:    []uuid.UUID{implied, excluded, untagged},
		},
		Query: &query,
	})
	require.NoError(t, err)
	require.NotNil(t, result, "the language query returned nothing at all")

	assert.Empty(t, result.Diagnostics, "a query that means what it says has nothing to report")

	viaLanguage := make(map[uuid.UUID]bool, len(result.Scenes))
	for _, scene := range result.Scenes {
		id, err := uuid.FromString(scene.ID)
		require.NoError(t, err)
		viaLanguage[id] = true
	}

	// The same statement, written as the filter a client could have built by hand. It is the
	// comparison and not the fixture that is the point: two implementations of "tag A and not
	// tag B" that disagree would each look right on their own.
	viaFilter := s.searchInput(models.SceneQueryInput{
		Tags: &models.MultiIDCriterionInput{
			Modifier: models.CriterionModifierIncludes,
			Value:    []uuid.UUID{general},
		},
		ExcludeTags: &models.MultiIDCriterionInput{
			Modifier: models.CriterionModifierExcludes,
			Value:    []uuid.UUID{other},
		},
	}, []uuid.UUID{implied, excluded, untagged})

	assert.Equal(t, viaFilter, viaLanguage, "the language and the filter it lowers into must agree")

	// And what the answer IS, so a shared mistake in both is still visible.
	assert.True(t, viaLanguage[implied], "the specific tag implies the general one, so it must be found")
	assert.False(t, viaLanguage[excluded], "the exclusion must remove the scene carrying the excluded tag")
	assert.False(t, viaLanguage[untagged], "a scene carrying neither tag is not a match for an include")
}

// A term nobody can apply empties the answer instead of being dropped, and says which term it
// was and where -- the rule the language is built around (docs/spec/feature-discovery.md, rule 9).
func TestAnUnapplicableTermEmptiesTheResultAndIsReported(t *testing.T) {
	s := createSceneQueryLanguageRunner(t)

	general := s.named("general")
	scene := s.sceneTagged(general)
	s.visible(scene)

	cases := []struct {
		why  string
		text string
		code string
		term string
	}{
		{
			why:  "a field whose data does not exist yet",
			text: s.name(general) + " score:>10",
			code: "not_yet",
			term: "score:>10",
		},
		{
			why:  "a term nobody wrote",
			text: s.name(general) + " duratoin:60",
			code: "parse_error",
			term: "duratoin:60",
		},
		{
			why:  "a tag that does not exist, which is a database question and not a parse one",
			text: s.name(general) + " nosuchtagsanywhere",
			code: "unknown_name",
			term: "nosuchtagsanywhere",
		},
	}

	for _, c := range cases {
		t.Run(c.why, func(t *testing.T) {
			query := c.text
			result, err := s.client.queryScenes(models.SceneQueryInput{
				Page:      1,
				PerPage:   50,
				Sort:      models.SceneSortEnumTitle,
				Direction: models.SortDirectionEnumAsc,
				ID: &models.MultiIDCriterionInput{
					Modifier: models.CriterionModifierIncludes,
					Value:    []uuid.UUID{scene},
				},
				Query: &query,
			})
			require.NoError(t, err)
			require.NotNil(t, result)

			// The absence first, then its subject: the scene IS visible to this same query when
			// the language says nothing, so "no results" is a statement about the term and not
			// about a query that never worked.
			assert.Empty(t, result.Scenes,
				"a term that could not be applied must not return the set with it dropped")
			assert.Equal(t, 0, result.Count)

			require.Len(t, result.Diagnostics, 1, "exactly one term is unapplicable here")
			assert.Equal(t, c.code, result.Diagnostics[0].Code)
			assert.Equal(t, c.term, result.Diagnostics[0].Term)
			assert.NotEmpty(t, result.Diagnostics[0].Message)
		})
	}
}

// searchLanguage runs the language and returns what it found plus what it could not apply.
func (s *sceneQueryLanguageRunner) searchLanguage(query string, candidates []uuid.UUID) (map[uuid.UUID]bool, []searchDiagnosticOutput) {
	result, err := s.client.queryScenes(models.SceneQueryInput{
		Page:      1,
		PerPage:   50,
		Sort:      models.SceneSortEnumTitle,
		Direction: models.SortDirectionEnumAsc,
		ID: &models.MultiIDCriterionInput{
			Modifier: models.CriterionModifierIncludes,
			Value:    candidates,
		},
		Query: &query,
	})
	s.t.Helper()
	require.NoError(s.t, err)
	require.NotNil(s.t, result, "the language query returned nothing at all")

	found := make(map[uuid.UUID]bool, len(result.Scenes))
	for _, scene := range result.Scenes {
		id, err := uuid.FromString(scene.ID)
		require.NoError(s.t, err)
		found[id] = true
	}

	return found, result.Diagnostics
}

// A merge turns the merged-away tag's id into a redirect and its NAME into an alias of the
// survivor (internal/service/edit/tag.go). A query -- a saved search, or a reader's memory of
// what the tag was called -- names the old tag, so resolution has to follow the alias; with a
// name-only lookup the term becomes unknown, the language refuses it, and the saved search
// silently empties. That is the cost the spec's section 3.2 warns about, and this is the test
// that says the alias lookup is what pays it.
func TestAQueryOverAMergedTagStillMeansSomething(t *testing.T) {
	s := createSceneQueryLanguageRunner(t)

	doomed := s.named("doomed")
	survivor := s.named("survivor")
	doomedName := s.name(doomed)

	scene := s.sceneTagged(doomed)
	s.visible(scene)

	// The control, before the merge: the query works now, so the identical text after the merge
	// is saying something about the merge and not about a query that never worked.
	foundBefore, diagnosticsBefore := s.searchLanguage(doomedName, []uuid.UUID{scene})
	assert.Empty(t, diagnosticsBefore)
	assert.True(t, foundBefore[scene], "the control: this query finds the scene before the merge")

	mergeInput := models.EditInput{
		Operation:      models.OperationEnumMerge,
		ID:             &survivor,
		MergeSourceIds: []uuid.UUID{doomed},
	}
	details := models.TagEditDetailsInput{}
	edit, err := s.createTestTagEdit(models.OperationEnumMerge, &details, &mergeInput)
	require.NoError(t, err)
	require.NotNil(t, edit, "the merge edit was not created")

	applied, err := s.approveEdit(edit.ID)
	require.NoError(t, err)
	if applied != nil {
		s.verifyEditApplication(true, applied)
	}

	foundAfter, diagnosticsAfter := s.searchLanguage(doomedName, []uuid.UUID{scene})
	assert.Empty(t, diagnosticsAfter,
		"the merged-away name must still resolve: an unknown name here means the alias lookup is gone")
	assert.True(t, foundAfter[scene],
		"a query written before a merge must still mean something after it")
}

// The duration term reaches SQL: two scenes, one longer than the threshold and one shorter, and
// a comparison that finds exactly one of them. The unit tests prove the lowering; this proves
// the criterion the lowering produces is the one the query builder applies.
func TestADurationComparisonReachesTheQuery(t *testing.T) {
	s := createSceneQueryLanguageRunner(t)

	long := 600
	short := 60
	shortTitle := s.generateSceneName()
	longTitle := s.generateSceneName()

	longScene, err := s.createTestScene(&models.SceneCreateInput{
		Title:        &longTitle,
		Date:         "2020-03-02",
		Duration:     &long,
		Fingerprints: []models.FingerprintEditInput{s.generateSceneFingerprint(nil)},
	})
	require.NoError(t, err)
	require.NotNil(t, longScene)

	shortScene, err := s.createTestScene(&models.SceneCreateInput{
		Title:        &shortTitle,
		Date:         "2020-03-02",
		Duration:     &short,
		Fingerprints: []models.FingerprintEditInput{s.generateSceneFingerprint(nil)},
	})
	require.NoError(t, err)
	require.NotNil(t, shortScene)

	longID, err := uuid.FromString(longScene.ID)
	require.NoError(t, err)
	shortID, err := uuid.FromString(shortScene.ID)
	require.NoError(t, err)

	candidates := []uuid.UUID{longID, shortID}
	s.visible(candidates...)

	query := "duration:>300"
	result, err := s.client.queryScenes(models.SceneQueryInput{
		Page:      1,
		PerPage:   50,
		Sort:      models.SceneSortEnumTitle,
		Direction: models.SortDirectionEnumAsc,
		ID: &models.MultiIDCriterionInput{
			Modifier: models.CriterionModifierIncludes,
			Value:    candidates,
		},
		Query: &query,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Empty(t, result.Diagnostics)

	found := make(map[uuid.UUID]bool, len(result.Scenes))
	for _, scene := range result.Scenes {
		id, err := uuid.FromString(scene.ID)
		require.NoError(t, err)
		found[id] = true
	}

	assert.Len(t, found, 1, "exactly one of the two scenes is longer than 300 seconds")
	assert.True(t, found[longID], "the 600-second scene is the one that matches")
	assert.False(t, found[shortID], "the 60-second scene is not")
}
