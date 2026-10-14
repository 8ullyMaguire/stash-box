//go:build integration

package api_test

import (
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/models"
)

// The tag explorer's facet list (docs/spec/feature-discovery.md section 6).
//
// WHAT IS PROVEN HERE:
//
//  1. A facet's count is what CLICKING it returns. The test compares the facet's count for a tag
//     against `queryScenes` filtered by that tag, on the same fixture -- because the failure this
//     guards is a number that looks plausible and disagrees with the result the reader gets.
//  2. The count walks the IMPLICATION CLOSURE. The fixture carries the general tag directly on
//     ONE scene and reaches it through a specific tag on two others, so a direct-row count would
//     report 1 where the filter returns 3. That is the whole reason this is a recursive query.
//  3. The tags the filter ALREADY applies are not offered back, and tags outside the result set
//     are not in the list at all.
//  4. `strength` is a fraction of the INSTANCE, not of the result set: a tag whose other scenes
//     are outside the result set is below 1.0, which is what makes it informative.
//
// The fixture is deliberately built so the subtle case (a tag reached only through the
// hierarchy) has a DIFFERENT count from the naive one -- `general` is on one scene directly and
// on two through `specific`, so any assertion of "1" is a bug and any assertion of "3" is the
// closure working.
type sceneFacetRunner struct {
	sceneQueryLanguageRunner
}

func createSceneFacetRunner(t *testing.T) *sceneFacetRunner {
	t.Helper()
	return &sceneFacetRunner{sceneQueryLanguageRunner: *createSceneQueryLanguageRunner(t)}
}

type tagFacetOutput struct {
	Tag      idName  `json:"tag"`
	Count    int     `json:"count"`
	Strength float64 `json:"strength"`
}

type idName struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type tagFacetsOutput struct {
	Tags        []tagFacetOutput `json:"tags"`
	TaggedCount int              `json:"tagged_count"`
}

// sceneFacets asks for the facets of a query. `limit` and `min_count` are passed as nullable so
// one call can exercise the schema's defaults and an explicit value.
func (c *graphqlClient) sceneFacets(input models.SceneQueryInput, limit *int, minCount *int) (*tagFacetsOutput, error) {
	q := `
	query SceneFacets($input: SceneQueryInput!, $limit: Int, $min_count: Int) {
		queryScenes(input: $input) {
			facets(limit: $limit, min_count: $min_count) {
				tagged_count
				tags {
					count
					strength
					tag {
						id
						name
					}
				}
			}
		}
	}`

	var resp struct {
		QueryScenes struct {
			Facets tagFacetsOutput `json:"facets"`
		} `json:"queryScenes"`
	}
	if err := c.Post(q, &resp, client.Var("input", input), client.Var("limit", limit), client.Var("min_count", minCount)); err != nil {
		return nil, err
	}

	return &resp.QueryScenes.Facets, nil
}

func (c *graphqlClient) tagSceneCount(id uuid.UUID) (int, error) {
	q := `
	query TagSceneCount($id: ID!) {
		findTag(id: $id) {
			scene_count
		}
	}`

	var resp struct {
		FindTag *struct {
			SceneCount int `json:"scene_count"`
		} `json:"findTag"`
	}
	if err := c.Post(q, &resp, client.Var("id", id)); err != nil {
		return 0, err
	}
	if resp.FindTag == nil {
		return 0, nil
	}

	return resp.FindTag.SceneCount, nil
}

// fixture builds the scene/tag graph the assertions below describe and returns everything they
// name. Named in one place so a failure message and the fixture cannot drift apart.
type facetFixture struct {
	marker    uuid.UUID
	specific  uuid.UUID
	general   uuid.UUID
	outside   uuid.UUID
	unrelated uuid.UUID
}

func (s *sceneFacetRunner) facetFixture() facetFixture {
	s.t.Helper()

	f := facetFixture{
		marker:    s.named("marker"),
		specific:  s.named("specific"),
		general:   s.named("general"),
		outside:   s.named("outside-tag"),
		unrelated: s.named("unrelated"),
	}

	// `specific` implies `general` (specific -> general, migration 111). The fixture then
	// carries ONLY the specific tag on two scenes, so a direct-row count reports 1 for `general`
	// while the filter returns 3.
	s.mustSetImplications(f.specific, []uuid.UUID{f.general}, nil)

	// In the result set: two scenes reaching `general` through `specific`, one carrying
	// `general` directly.
	inResult := []uuid.UUID{
		s.sceneTagged(f.marker, f.specific),
		s.sceneTagged(f.marker, f.specific),
		s.sceneTagged(f.marker, f.general),
	}

	// OUTSIDE the result set, so `general`'s instance count (4) is larger than its facet count
	// (3) and its strength is below 1.
	outsideResult := []uuid.UUID{
		s.sceneTagged(f.general),
		s.sceneTagged(f.unrelated),
	}

	// The absence assertions below come after a COUNT. "The unrelated tag is not offered as a
	// facet" is vacuously true of a facet list that is empty for another reason, and true of a
	// tag whose scene was never created -- so the fixture is shown to exist first.
	s.visible(append(append([]uuid.UUID{}, inResult...), outsideResult...)...)

	return f
}

func (s *sceneFacetRunner) markerInput(marker uuid.UUID) models.SceneQueryInput {
	return models.SceneQueryInput{
		Page:      1,
		PerPage:   50,
		Sort:      models.SceneSortEnumTitle,
		Direction: models.SortDirectionEnumAsc,
		Tags: &models.MultiIDCriterionInput{
			Modifier: models.CriterionModifierIncludes,
			Value:    []uuid.UUID{marker},
		},
	}
}

func (s *sceneFacetRunner) facetFor(facets *tagFacetsOutput, id uuid.UUID) (tagFacetOutput, bool) {
	for _, facet := range facets.Tags {
		if facet.Tag.ID == id.String() {
			return facet, true
		}
	}

	return tagFacetOutput{}, false
}

// A facet's count is what clicking it returns, and it is reached through the hierarchy.
func TestSceneFacetCountsMatchWhatTheFacetWouldReturn(t *testing.T) {
	s := createSceneFacetRunner(t)
	f := s.facetFixture()

	facets, err := s.client.sceneFacets(s.markerInput(f.marker), nil, nil)
	require.NoError(t, err)
	require.NotEmpty(t, facets.Tags, "the facet list must not be empty before anything is claimed about it")

	general, ok := s.facetFor(facets, f.general)
	require.True(t, ok, "a tag the result set reaches through the hierarchy must be offered")
	assert.Equal(t, 3, general.Count,
		"the general tag is on one scene directly and on two through the specific tag")

	specific, ok := s.facetFor(facets, f.specific)
	require.True(t, ok)
	assert.Equal(t, 2, specific.Count)

	// The comparison that makes the number trustworthy: the reader's filter PLUS the facet's tag.
	//
	// INCLUDES_ALL, not INCLUDES. A facet is applied ON TOP of what the reader already has, and
	// INCLUDES is a union -- `{marker, general}` with INCLUDES matches scenes carrying EITHER, so
	// it would count the one scene outside the result set too (4) and "disagree" with a facet
	// that is perfectly right. The modifier is the difference between "everything with this tag"
	// and "this tag among these results", and only the second is what a facet offers.
	viaFilter, err := s.client.queryScenes(func() models.SceneQueryInput {
		input := s.markerInput(f.marker)
		input.Tags = &models.MultiIDCriterionInput{
			Modifier: models.CriterionModifierIncludesAll,
			Value:    []uuid.UUID{f.marker, f.general},
		}
		return input
	}())
	require.NoError(t, err)
	assert.Equal(t, viaFilter.Count, general.Count,
		"a facet that disagrees with the result the reader gets by clicking it is the bug this guards")
}

// The facet list is about the result set: the filter's own tag is not offered back, and a tag
// only outside the set is not in the list.
func TestFacetsAreAboutTheResultSet(t *testing.T) {
	s := createSceneFacetRunner(t)
	f := s.facetFixture()

	facets, err := s.client.sceneFacets(s.markerInput(f.marker), nil, nil)
	require.NoError(t, err)
	require.NotEmpty(t, facets.Tags)

	_, offered := s.facetFor(facets, f.marker)
	assert.False(t, offered, "the tag the filter already applies is not a suggestion")

	_, offered = s.facetFor(facets, f.unrelated)
	assert.False(t, offered, "a tag no scene in the result set carries must not be offered")

	assert.Equal(t, 3, facets.TaggedCount, "all three result scenes carry at least one tag")
}

// Strength is a fraction of the instance, so a tag whose other scenes are outside the result set
// scores below 1 -- and a tag that is entirely inside it scores exactly 1.
func TestFacetStrengthIsAFractionOfTheInstance(t *testing.T) {
	s := createSceneFacetRunner(t)
	f := s.facetFixture()

	facets, err := s.client.sceneFacets(s.markerInput(f.marker), nil, nil)
	require.NoError(t, err)

	general, ok := s.facetFor(facets, f.general)
	require.True(t, ok)
	assert.InDelta(t, 0.75, general.Strength, 0.001, "3 of the instance's 4 scenes carrying it are in the result set")

	specific, ok := s.facetFor(facets, f.specific)
	require.True(t, ok)
	assert.InDelta(t, 1.0, specific.Strength, 0.001, "every scene carrying the specific tag is in the result set")

	for _, facet := range facets.Tags {
		assert.Greater(t, facet.Strength, 0.0, "a facet with no scenes behind it should not be listed")
		assert.LessOrEqual(t, facet.Strength, 1.0, "a result set cannot hold more than the instance has")
	}
}

// `limit` and `min_count` reach the query.
func TestFacetLimitsApply(t *testing.T) {
	s := createSceneFacetRunner(t)
	f := s.facetFixture()

	one := 1
	limited, err := s.client.sceneFacets(s.markerInput(f.marker), &one, nil)
	require.NoError(t, err)
	require.Len(t, limited.Tags, 1, "the limit must reach the statement, not be applied to a full list afterwards")
	assert.Equal(t, f.general.String(), limited.Tags[0].Tag.ID, "the single row is the most-used tag")

	// min_count, with the limit left alone so the assertion is about the floor and not about how
	// many rows fit.
	three := 3
	minimum, err := s.client.sceneFacets(s.markerInput(f.marker), nil, &three)
	require.NoError(t, err)
	require.NotEmpty(t, minimum.Tags)

	for _, facet := range minimum.Tags {
		assert.GreaterOrEqual(t, facet.Count, 3, "min_count is a floor on the count, not on the list length")
	}

	if _, ok := s.facetFor(minimum, f.specific); ok {
		t.Error("a tag below min_count must not be listed")
	}
}

// `Tag.scene_count` is the same question the facet asks, for one tag: what a filter on it
// returns, through the implications below it.
func TestTagSceneCountWalksTheImplications(t *testing.T) {
	s := createSceneFacetRunner(t)
	f := s.facetFixture()

	general, err := s.client.tagSceneCount(f.general)
	require.NoError(t, err)
	assert.Equal(t, 4, general, "one scene carries it directly, two reach it through the specific tag, one is outside the result set")

	specific, err := s.client.tagSceneCount(f.specific)
	require.NoError(t, err)
	assert.Equal(t, 2, specific)

	// A tag nothing carries is zero and not an error: it is the state every new tag starts in.
	empty, err := s.client.tagSceneCount(s.named("empty"))
	require.NoError(t, err)
	assert.Zero(t, empty)
}
