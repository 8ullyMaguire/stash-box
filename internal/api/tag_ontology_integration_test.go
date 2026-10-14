//go:build integration

package api_test

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/stashapp/stash-box/internal/models"
)

// The tag ontology, end to end: an implication is submitted as a tag edit, applied, and then
// CHANGES WHAT A SEARCH RETURNS.
//
// Two rules the tests below follow, both learned from a failure:
//
//  1. Every query is scoped to the scenes THIS test created, by ID. The suite shares one
//     database with every other test in the package: run alone the table holds a handful of
//     scenes, but in the package's full run it holds hundreds, and a criterion that matches
//     most of them (EXCLUDES does) pushes the fixture off page 1. That failure reads as "the
//     search is broken" and is not.
//  2. The negative assertion comes FIRST, and its subject is counted. "Filtering by the
//     general tag finds the scene" passes just as well when the criterion ignores the
//     hierarchy and the fixture happens to carry both tags -- so these scenes carry ONLY the
//     specific tag, the search runs once before the implication exists, and `visible` asserts
//     the fixture is in the pool before anything is claimed about the filter.

type tagOntologyTestRunner struct {
	testRunner
}

func createTagOntologyTestRunner(t *testing.T) *tagOntologyTestRunner {
	return &tagOntologyTestRunner{testRunner: *asAdmin(t)}
}

// named creates a tag whose name ends in the given role, so a failure message says which one.
func (s *tagOntologyTestRunner) named(role string) uuid.UUID {
	tag, err := s.createTestTag(&models.TagCreateInput{Name: s.generateTagName() + "-" + role})
	assert.NoError(s.t, err)
	if tag == nil {
		s.t.Fatal("could not create the " + role + " tag")
	}

	return tag.UUID()
}

func (s *tagOntologyTestRunner) sceneTagged(tags ...uuid.UUID) uuid.UUID {
	title := s.generateSceneName()
	scene, err := s.createTestScene(&models.SceneCreateInput{
		Title:        &title,
		Date:         "2020-03-02",
		Fingerprints: []models.FingerprintEditInput{s.generateSceneFingerprint(nil)},
		TagIds:       tags,
	})
	assert.NoError(s.t, err)
	if scene == nil {
		s.t.Fatal("could not create the scene")
	}

	id, err := uuid.FromString(scene.ID)
	assert.NoError(s.t, err)

	return id
}

// setImplications submits and approves a modify edit carrying the implication change: the
// only way the ontology changes, because tag data is shared data and the edit system is this
// instance's proposal-discussion-vote machine.
func (s *tagOntologyTestRunner) setImplications(tagID uuid.UUID, added, removed []uuid.UUID) (*models.Edit, error) {
	editInput := models.EditInput{
		Operation: models.OperationEnumModify,
		ID:        &tagID,
	}
	details := models.TagEditDetailsInput{
		AddedImplications:   added,
		RemovedImplications: removed,
	}

	edit, err := s.createTestTagEdit(models.OperationEnumModify, &details, &editInput)
	if err != nil {
		return nil, err
	}

	return s.approveEdit(edit.ID)
}

func (s *tagOntologyTestRunner) mustSetImplications(tagID uuid.UUID, added, removed []uuid.UUID) {
	applied, err := s.setImplications(tagID, added, removed)
	assert.NoError(s.t, err)
	if applied != nil {
		// The edit applied: an approved tag edit is immediately accepted for an admin, which
		// is what makes the assertions below about the TAG rather than about the queue.
		s.verifyEditApplication(true, applied)
	}
}

func (s *tagOntologyTestRunner) tag(tagID uuid.UUID) *models.Tag {
	tag, err := s.resolver.Query().FindTag(s.ctx, &tagID, nil)
	assert.NoError(s.t, err)
	if tag == nil {
		s.t.Fatal("tag not found: " + tagID.String())
	}

	return tag
}

func (s *tagOntologyTestRunner) impliedNames(tagID uuid.UUID) []string {
	implies, err := s.resolver.Tag().Implies(s.ctx, s.tag(tagID))
	assert.NoError(s.t, err)

	names := make([]string, 0, len(implies))
	for _, t := range implies {
		names = append(names, t.Name)
	}

	return names
}

// search asks the SEARCH, through GraphQL, which of `candidates` the filter returns.
//
// The ID criterion is not a convenience: it is what makes an absence assertion mean
// something. "The scene is not in the results" is vacuous if the query could never have
// returned it.
func (s *tagOntologyTestRunner) search(tags *models.MultiIDCriterionInput, candidates []uuid.UUID) map[uuid.UUID]bool {
	// Page, Sort and Direction are not optional: the input has non-null defaults on those
	// fields, so an empty SortDirectionEnum is refused by validation before the resolver ever
	// runs -- which reads as "the query returned nothing" if the error is not checked.
	result, err := s.client.queryScenes(models.SceneQueryInput{
		Page:      1,
		PerPage:   50,
		Sort:      models.SceneSortEnumTitle,
		Direction: models.SortDirectionEnumAsc,
		ID: &models.MultiIDCriterionInput{
			Modifier: models.CriterionModifierIncludes,
			Value:    candidates,
		},
		Tags: tags,
	})
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

func (s *tagOntologyTestRunner) matches(modifier models.CriterionModifier, tagIDs []uuid.UUID, candidates ...uuid.UUID) map[uuid.UUID]bool {
	return s.search(&models.MultiIDCriterionInput{Modifier: modifier, Value: tagIDs}, candidates)
}

// visible asserts the search can SEE these scenes at all, before any test asks what the tag
// filter does with them. Without this, every `False` below would also be true of a query that
// had stopped working.
func (s *tagOntologyTestRunner) visible(candidates ...uuid.UUID) {
	found := s.search(nil, candidates)
	assert.Len(s.t, found, len(candidates),
		"the fixture scenes must be visible to this query before it can say anything about the filter")
	for _, id := range candidates {
		assert.True(s.t, found[id], "the query cannot see the fixture scene %s", id)
	}
}

func TestSearchExpandsThroughTagImplications(t *testing.T) {
	s := createTagOntologyTestRunner(t)

	general := s.named("general")
	specific := s.named("specific")
	scene := s.sceneTagged(specific)

	s.visible(scene)

	// The control. Without it, a criterion that had stopped looking at tags at all would pass
	// the assertion below, and so would one whose fixture happened to carry both tags.
	before := s.matches(models.CriterionModifierIncludes, []uuid.UUID{general}, scene)
	assert.False(t, before[scene], "the scene is tagged with the specific tag only, so the general tag must not match it yet")

	versionBefore, err := s.resolver.Query().OntologyVersion(s.ctx)
	assert.NoError(t, err)

	s.mustSetImplications(specific, []uuid.UUID{general}, nil)

	// The graph has it, and it is the DIRECT relation: the general tag does not imply the
	// specific one, which is what makes the direction testable rather than symmetric.
	assert.Equal(t, []string{s.tag(general).Name}, s.impliedNames(specific))

	after := s.matches(models.CriterionModifierIncludes, []uuid.UUID{general}, scene)
	assert.True(t, after[scene], "filtering by the general tag must now find the scene tagged with the specific one")

	// And the scene was not rewritten. This is the design decision, asserted: an implied tag
	// is derived at read time, never materialised onto scene_tags, so the reader sees one tag
	// on the scene while the filter matches two.
	stored, err := s.client.findScene(scene)
	assert.NoError(t, err)
	if stored != nil {
		assert.Len(t, stored.Tags, 1, "the scene still carries exactly the tag a person put on it")
		if len(stored.Tags) == 1 {
			assert.Equal(t, specific.String(), stored.Tags[0].ID)
		}
	}

	versionAfter, err := s.resolver.Query().OntologyVersion(s.ctx)
	assert.NoError(t, err)
	assert.Greater(t, versionAfter, versionBefore, "applying the implication must move the ontology version")
}

func TestRemovingAnImplicationTakesTheExpansionWithIt(t *testing.T) {
	s := createTagOntologyTestRunner(t)

	general := s.named("general")
	specific := s.named("specific")
	scene := s.sceneTagged(specific)

	s.visible(scene)

	s.mustSetImplications(specific, []uuid.UUID{general}, nil)
	assert.True(t, s.matches(models.CriterionModifierIncludes, []uuid.UUID{general}, scene)[scene])

	// The same edit, reversed. A derived read makes this instant; a materialised one would
	// have to know which rows the rule produced, and cannot.
	s.mustSetImplications(specific, nil, []uuid.UUID{general})

	assert.Empty(t, s.impliedNames(specific))

	// The scene is still there -- asserted, not assumed, so the False below is about the
	// criterion and not about a fixture that went missing.
	s.visible(scene)
	assert.False(t, s.matches(models.CriterionModifierIncludes, []uuid.UUID{general}, scene)[scene],
		"with the implication gone, the general tag must not match the scene again")
}

func TestIncludesAllIsSatisfiedByOneTagThatImpliesBoth(t *testing.T) {
	s := createTagOntologyTestRunner(t)

	first := s.named("first")
	second := s.named("second")
	specific := s.named("specific")

	// One scene carrying ONE tag, which will imply both of the tags the filter asks for, and
	// one scene carrying nothing, which is the control.
	scene := s.sceneTagged(specific)
	other := s.sceneTagged()
	s.visible(scene, other)

	s.mustSetImplications(specific, []uuid.UUID{first, second}, nil)

	found := s.matches(models.CriterionModifierIncludesAll, []uuid.UUID{first, second}, scene, other)
	assert.True(t, found[scene],
		"a scene tagged only with a tag that implies both must satisfy 'all of' -- this is the case a COUNT over an expanded value list refuses")
	assert.False(t, found[other], "and a scene with neither tag is not swept in by the second EXISTS")
}

func TestExcludesRefusesWhatImpliesTheExcludedTag(t *testing.T) {
	s := createTagOntologyTestRunner(t)

	general := s.named("general")
	specific := s.named("specific")
	plain := s.named("plain")

	excluded := s.sceneTagged(specific)
	included := s.sceneTagged(plain)
	s.visible(excluded, included)

	s.mustSetImplications(specific, []uuid.UUID{general}, nil)

	found := s.matches(models.CriterionModifierExcludes, []uuid.UUID{general}, excluded, included)
	assert.False(t, found[excluded],
		"excluding a tag must exclude what implies it, or the hierarchy is useless for filtering")
	assert.True(t, found[included], "and must not exclude the scenes that have nothing to do with it")
}

func TestACycleIsRefusedWhenTheEditIsSubmitted(t *testing.T) {
	s := createTagOntologyTestRunner(t)

	general := s.named("general")
	specific := s.named("specific")

	s.mustSetImplications(specific, []uuid.UUID{general}, nil)

	// The reverse direction, which would close a two-node cycle. Refused at SUBMIT, so the
	// submitter is told while they can still change the edit -- the database refuses it again
	// on apply, and that is what the migration's verify script proves.
	//
	// Called through the resolver, not through createTestTagEdit: that helper reports an error
	// as a test failure, which would make this test unable to observe the refusal it is about.
	_, err := s.resolver.Mutation().TagEdit(s.ctx, models.TagEditInput{
		Edit: &models.EditInput{
			Operation: models.OperationEnumModify,
			ID:        &general,
		},
		Details: &models.TagEditDetailsInput{
			AddedImplications: []uuid.UUID{specific},
		},
	})
	assert.Error(t, err)
	if err != nil {
		assert.Contains(t, err.Error(), "cycle", "the refusal must name the reason")
	}

	// And nothing was written: the graph is still one edge in one direction.
	assert.Equal(t, []string{s.tag(general).Name}, s.impliedNames(specific))
	assert.Empty(t, s.impliedNames(general))
}

func TestAMergeMovesImplicationsRatherThanDroppingThem(t *testing.T) {
	s := createTagOntologyTestRunner(t)

	head := s.named("head")
	doomed := s.named("doomed")
	survivor := s.named("survivor")
	implied := s.named("implied")

	// Both ends of the merged tag's edges, so the merge exercises both retargets:
	//   head -> doomed   (doomed is a TAIL here and must become survivor)
	//   doomed -> implied (doomed is a HEAD here and must become survivor)
	s.mustSetImplications(head, []uuid.UUID{doomed}, nil)
	s.mustSetImplications(doomed, []uuid.UUID{implied}, nil)
	assert.Equal(t, []string{s.tag(doomed).Name}, s.impliedNames(head))
	assert.Equal(t, []string{s.tag(implied).Name}, s.impliedNames(doomed))

	// A scene carrying only the MOST SPECIFIC tag in the chain. The direction matters:
	// `head implies survivor implies implied`, so a filter on `implied` is the one that must
	// reach two steps up the graph to find it. A scene tagged at the GENERAL end would match
	// every filter and prove nothing.
	scene := s.sceneTagged(head)
	s.visible(scene)

	// The control, before the merge: filtering by `doomed` finds it (one hop), and filtering
	// by the tag `doomed` is about to be merged INTO does not, because nothing implies the
	// survivor yet. The second assertion is what makes the identical query after the merge
	// say something.
	assert.True(t, s.matches(models.CriterionModifierIncludes, []uuid.UUID{doomed}, scene)[scene],
		"before the merge, filtering by the tag the scene's tag implies must find it")
	assert.False(t, s.matches(models.CriterionModifierIncludes, []uuid.UUID{survivor}, scene)[scene],
		"and the survivor, which nothing implies yet, must not match it")

	// Merge `doomed` into `survivor`: a tag edit with the source named.
	mergeInput := models.EditInput{
		Operation:      models.OperationEnumMerge,
		ID:             &survivor,
		MergeSourceIds: []uuid.UUID{doomed},
	}
	details := models.TagEditDetailsInput{}
	edit, err := s.createTestTagEdit(models.OperationEnumMerge, &details, &mergeInput)
	assert.NoError(t, err)
	if edit == nil {
		t.Fatal("the merge edit was not created")
	}
	applied, err := s.approveEdit(edit.ID)
	assert.NoError(t, err)
	if applied != nil {
		s.verifyEditApplication(true, applied)
	}

	assert.Equal(t, []string{s.tag(implied).Name}, s.impliedNames(survivor),
		"the merged-away tag's implication must move to the survivor, not vanish with it")
	assert.Equal(t, []string{s.tag(survivor).Name}, s.impliedNames(head),
		"and the other end must move too: what used to imply the merged tag implies the survivor")

	// The same claim through the SEARCH. A merge that moved the rows but lost the transitive
	// walk passes every assertion above and fails these two.
	assert.True(t, s.matches(models.CriterionModifierIncludes, []uuid.UUID{survivor}, scene)[scene],
		"the survivor must now match the scene whose tag implies it -- one hop, through the retargeted edge")
	assert.True(t, s.matches(models.CriterionModifierIncludes, []uuid.UUID{implied}, scene)[scene],
		"and the far end of the chain must still reach it -- two hops, through the edge the merge moved")

	// Reading the merged-away tag id gives the survivor, because a merge writes a redirect.
	// Asserted rather than assumed: a reader holding the old id is the case the redirect
	// exists for, and it is also why "is the merged tag still an implication source" is not a
	// question this surface can answer.
	assert.Equal(t, survivor.String(), s.tag(doomed).ID.String(),
		"the merged-away tag resolves to the survivor")
}
