package search

import (
	"context"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/models"
)

// The fake resolver is the point of the Resolver interface: the language's own behaviour is
// testable without a database, and the tests below are about what the language DOES with a name
// -- resolve it, refuse it, or report it -- rather than about how a name is looked up.
type fakeResolver struct {
	tags       map[string]uuid.UUID
	performers map[string]uuid.UUID
	studios    map[string]uuid.UUID
}

func (f fakeResolver) TagID(_ context.Context, name string) (uuid.UUID, bool, error) {
	id, ok := f.tags[name]

	return id, ok, nil
}

func (f fakeResolver) PerformerID(_ context.Context, name string) (uuid.UUID, bool, error) {
	id, ok := f.performers[name]

	return id, ok, nil
}

func (f fakeResolver) StudioID(_ context.Context, name string) (uuid.UUID, bool, error) {
	id, ok := f.studios[name]

	return id, ok, nil
}

func testID(n byte) uuid.UUID {
	var id uuid.UUID
	for i := range id {
		id[i] = n
	}

	return id
}

func testResolver() fakeResolver {
	return fakeResolver{
		tags: map[string]uuid.UUID{
			"anal":  testID(1),
			"rough": testID(2),
			"pov":   testID(3),
		},
		performers: map[string]uuid.UUID{"ana": testID(4), "bea": testID(6)},
		studios:    map[string]uuid.UUID{"acme": testID(5)},
	}
}

func lowerText(t *testing.T, text string, base models.SceneQueryInput) Query {
	t.Helper()

	expr, err := Parse(text)
	require.NoError(t, err)

	out, err := Lower(context.Background(), expr, base, testResolver())
	require.NoError(t, err)

	return out
}

func TestTagsIncludeAndExclude(t *testing.T) {
	out := lowerText(t, "anal -rough", models.SceneQueryInput{})

	require.NotNil(t, out.Filter.Tags)
	assert.Equal(t, models.CriterionModifierIncludes, out.Filter.Tags.Modifier)
	assert.Equal(t, []uuid.UUID{testID(1)}, out.Filter.Tags.Value)

	assert.Equal(t, []uuid.UUID{testID(2)}, out.ExcludeTags)
	assert.Empty(t, out.Diagnostics)
	assert.False(t, out.NoMatch)
}

func TestAnUnknownNameMakesTheQueryEmptyAndSaysWhy(t *testing.T) {
	out := lowerText(t, "anal nosuchtag", models.SceneQueryInput{})

	assert.True(t, out.NoMatch, "dropping the unknown tag would return MORE than was asked for")
	require.Len(t, out.Diagnostics, 1)
	assert.Equal(t, DiagUnknownName, out.Diagnostics[0].Code)
	assert.Equal(t, "unknown tag \"nosuchtag\"", out.Diagnostics[0].Message)
	assert.Equal(t, 5, out.Diagnostics[0].Offset)
	assert.Equal(t, "nosuchtag", out.Diagnostics[0].Term)
}

func TestAnUnknownExcludedNameIsAlsoReported(t *testing.T) {
	out := lowerText(t, "anal -nosuchtag", models.SceneQueryInput{})

	assert.True(t, out.NoMatch)
	require.Len(t, out.Diagnostics, 1)
	assert.Contains(t, out.Diagnostics[0].Message, "exclusion cannot be applied")
}

func TestTextTermsAreJoinedBecauseThereIsOneField(t *testing.T) {
	out := lowerText(t, `text:hotel text:"room service"`, models.SceneQueryInput{})

	require.NotNil(t, out.Filter.Text)
	assert.Equal(t, "hotel room service", *out.Filter.Text)
	assert.Empty(t, out.Diagnostics)
}

func TestAnExcludedPerformerAloneIsExclusion(t *testing.T) {
	out := lowerText(t, "-performer:ana", models.SceneQueryInput{})

	require.NotNil(t, out.Filter.Performers)
	assert.Equal(t, models.CriterionModifierExcludes, out.Filter.Performers.Modifier)
	assert.Empty(t, out.Diagnostics)
	assert.False(t, out.NoMatch)
}

// The mixed case is the one the generated input cannot hold, and this test is the record of
// that: the include is applied, the exclude is refused, and the answer is empty rather than
// larger than asked for.
func TestIncludingAndExcludingTheSameFieldIsRefusedNotDropped(t *testing.T) {
	out := lowerText(t, "performer:ana -performer:bea", models.SceneQueryInput{})

	assert.True(t, out.NoMatch)
	require.NotNil(t, out.Filter.Performers)
	assert.Equal(t, models.CriterionModifierIncludes, out.Filter.Performers.Modifier)
	assert.Equal(t, []uuid.UUID{testID(4)}, out.Filter.Performers.Value)

	require.Len(t, out.Diagnostics, 1)
	assert.Equal(t, DiagUnsupported, out.Diagnostics[0].Code)
	assert.Contains(t, out.Diagnostics[0].Message, "cannot yet")

	// An unresolved exclude is refused for its own reason, and it is the only reason: the mixed
	// case never arises, because there is no exclusion to apply. Reported once, not twice.
	unknown := lowerText(t, "performer:ana -performer:nosuch", models.SceneQueryInput{})
	assert.True(t, unknown.NoMatch)
	require.Len(t, unknown.Diagnostics, 1)
	assert.Equal(t, DiagUnknownName, unknown.Diagnostics[0].Code)
}

func TestParentStudioResolvesToAStudioId(t *testing.T) {
	out := lowerText(t, "parent_studio:acme", models.SceneQueryInput{})

	require.NotNil(t, out.Filter.ParentStudio, "the builder walks the studio tree from an id")
	assert.Equal(t, testID(5).String(), *out.Filter.ParentStudio)
	assert.Empty(t, out.Diagnostics)
}

func TestContentTypesThatDoNotExistYetAreDiagnosed(t *testing.T) {
	out := lowerText(t, "type:image", models.SceneQueryInput{})

	assert.True(t, out.NoMatch)
	require.Len(t, out.Diagnostics, 1)
	assert.Equal(t, DiagNotYet, out.Diagnostics[0].Code)

	unknown := lowerText(t, "type:banana", models.SceneQueryInput{})
	assert.True(t, unknown.NoMatch)
	require.Len(t, unknown.Diagnostics, 1)
	assert.Equal(t, DiagUnknownFieldValue, unknown.Diagnostics[0].Code)
	assert.Contains(t, unknown.Diagnostics[0].Message, "unknown content type")
}

func TestDateComparisonsMapToTheCriteriaModifiers(t *testing.T) {
	gt := lowerText(t, "date:>2020-01-01", models.SceneQueryInput{})
	require.NotNil(t, gt.Filter.Date)
	assert.Equal(t, "2020-01-01", gt.Filter.Date.Value)
	assert.Equal(t, models.CriterionModifierGreaterThan, gt.Filter.Date.Modifier)
	assert.Empty(t, gt.Diagnostics)

	eq := lowerText(t, "date:2020-01-01", models.SceneQueryInput{})
	require.NotNil(t, eq.Filter.Date)
	assert.Equal(t, models.CriterionModifierEquals, eq.Filter.Date.Modifier)

	// There is no >= arm in the builder, and using > would drop the boundary date.
	gte := lowerText(t, "date:>=2020-01-01", models.SceneQueryInput{})
	assert.True(t, gte.NoMatch)
	require.Len(t, gte.Diagnostics, 1)
	assert.Equal(t, DiagNotYet, gte.Diagnostics[0].Code)
}

// A term that cannot be applied is never quietly dropped, and the terms that CAN be applied
// still are: the filter is built, the answer is empty, and the reason is available.
func TestNotYetTermsAreDiagnosedAndTheRestStillLowers(t *testing.T) {
	out := lowerText(t, "anal duration:60..600", models.SceneQueryInput{})

	assert.True(t, out.NoMatch)
	require.Len(t, out.Diagnostics, 1)
	assert.Equal(t, DiagNotYet, out.Diagnostics[0].Code)
	assert.Equal(t, "duration:60..600", out.Diagnostics[0].Term)

	require.NotNil(t, out.Filter.Tags, "the tag term is still applied")
	assert.Equal(t, []uuid.UUID{testID(1)}, out.Filter.Tags.Value)
}

func TestSortSetsDirectionAndTheNegationMeansDescending(t *testing.T) {
	desc := lowerText(t, "-sort:date", models.SceneQueryInput{})
	assert.Equal(t, models.SceneSortEnumDate, desc.Filter.Sort)
	assert.Equal(t, models.SortDirectionEnumDesc, desc.Filter.Direction)
	assert.Empty(t, desc.Diagnostics, "a descending sort is not an exclusion")

	asc := lowerText(t, "sort:trending", models.SceneQueryInput{})
	assert.Equal(t, models.SceneSortEnumTrending, asc.Filter.Sort)
	assert.Equal(t, models.SortDirectionEnumAsc, asc.Filter.Direction)

	unknown := lowerText(t, "sort:banana", models.SceneQueryInput{})
	assert.True(t, unknown.NoMatch)
	require.Len(t, unknown.Diagnostics, 1)
	assert.Equal(t, DiagUnknownFieldValue, unknown.Diagnostics[0].Code)
}

// Paging and defaults are the caller's, and the language replaces only the fields it mentions:
// a person typing a query expects their query, not their query ANDed with the previous page.
func TestBaseIsKeptExceptWhereTheLanguageSpeaks(t *testing.T) {
	baseTags := &models.MultiIDCriterionInput{
		Modifier: models.CriterionModifierIncludes,
		Value:    []uuid.UUID{testID(9)},
	}
	base := models.SceneQueryInput{Page: 3, PerPage: 10, Tags: baseTags}

	out := lowerText(t, "anal", base)

	assert.Equal(t, 3, out.Filter.Page)
	assert.Equal(t, 10, out.Filter.PerPage)
	require.NotNil(t, out.Filter.Tags)
	assert.Equal(t, []uuid.UUID{testID(1)}, out.Filter.Tags.Value, "the query's tag replaces the base's")

	untouched := lowerText(t, "anal", base)
	assert.Equal(t, 3, untouched.Filter.Page, "a query with no paging terms keeps the base's paging")
}

func TestAnEmptyExpressionIsTheBaseFilter(t *testing.T) {
	base := models.SceneQueryInput{Page: 2, PerPage: 5}

	out, err := Lower(context.Background(), nil, base, testResolver())
	require.NoError(t, err)

	assert.Equal(t, base, out.Filter)
	assert.False(t, out.NoMatch)
	assert.Empty(t, out.Diagnostics)
}
