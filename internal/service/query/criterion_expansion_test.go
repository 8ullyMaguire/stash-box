package query

import (
	"strings"
	"testing"

	sq "github.com/Masterminds/squirrel"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/stashapp/stash-box/internal/models"
)

// The expansion is a hierarchy: a filter names a tag a reader means, and an item carrying
// something that IMPLIES it is a match. What is worth testing here is the SHAPE of the
// statement each modifier produces, because the three are not variations of one query:
//
//   - "any of" becomes one EXISTS over the union of the expansions;
//   - "all of" becomes one EXISTS PER requested tag, which is the whole reason the expansion
//     is a map rather than a flat set;
//   - "none of" becomes NOT EXISTS over the same union.
//
// A test that ran the statements against a database would pass with the wrong shape for
// "all of" whenever a fixture happened to carry the tags directly, so these assert on the SQL.
// The integration test in internal/api is the other half: it asserts what the search returns.

var (
	generalA  = uuid.FromStringOrNil("00000000-0000-0000-0000-0000000000a1")
	generalB  = uuid.FromStringOrNil("00000000-0000-0000-0000-0000000000b2")
	specificF = uuid.FromStringOrNil("00000000-0000-0000-0000-0000000000c3")
)

func criterion(mod models.CriterionModifier, ids ...uuid.UUID) *models.MultiIDCriterionInput {
	return &models.MultiIDCriterionInput{Modifier: mod, Value: ids}
}

func buildSQL(t *testing.T, crit *models.MultiIDCriterionInput, expansion map[uuid.UUID][]uuid.UUID) (string, []any) {
	t.Helper()

	psql := sq.StatementBuilder.PlaceholderFormat(sq.Dollar)
	query := psql.Select("scenes.id").From("scenes")
	err := ApplyMultiIDCriterionExpanded(&query, "scenes", "scene_tags", "scene_id", "tag_id", crit, expansion)
	assert.NoError(t, err)

	sql, args, err := query.ToSql()
	assert.NoError(t, err)

	return sql, args
}

// One specific tag implying two general ones, which is the fixture the two "all of" cases turn
// on: a scene tagged only with the specific tag satisfies both general tags.
func hierarchy() map[uuid.UUID][]uuid.UUID {
	return map[uuid.UUID][]uuid.UUID{
		generalA:  {generalA, specificF},
		generalB:  {generalB, specificF},
		specificF: {specificF},
	}
}

func TestWithoutAnExpansionTheCriterionIsUnchanged(t *testing.T) {
	sql, args := buildSQL(t, criterion(models.CriterionModifierIncludes, generalA), nil)

	// The value list is exactly what was asked for: no hierarchy, no invented ids.
	assert.Contains(t, sql, "IN ($1)")
	assert.Equal(t, []any{generalA}, args)
}

func TestIncludesMatchesAnythingThatImpliesTheNamedTag(t *testing.T) {
	sql, args := buildSQL(t, criterion(models.CriterionModifierIncludes, generalA), hierarchy())

	// A semi-join over the union, so an item tagged ONLY with the specific tag matches.
	assert.Contains(t, sql, "EXISTS (")
	assert.Contains(t, sql, "IN ($1,$2)")
	assert.Equal(t, []any{generalA, specificF}, args)
	// Still one statement per criterion: the union is a value list, not a second query.
	assert.NotContains(t, sql, "UNION")
}

func TestIncludesAllAsksOncePerRequestedTag(t *testing.T) {
	sql, args := buildSQL(t, criterion(models.CriterionModifierIncludesAll, generalA, generalB), hierarchy())

	// THIS is the case a count cannot express. An item tagged only with the specific tag
	// contributes ONE join row while satisfying both requested tags, so "COUNT(*) = 2" would
	// refuse it. Hence two EXISTS, each over one requested tag's expansion.
	assert.Equal(t, 2, strings.Count(sql, "EXISTS ("), "one EXISTS per requested tag, not one over an expanded list")
	assert.NotContains(t, sql, "COUNT(*)", "the counting form cannot express 'all of' through a hierarchy")
	assert.NotContains(t, sql, "JOIN", "nor can an inner join with a HAVING count")
	assert.Equal(t, []any{generalA, specificF, generalB, specificF}, args)
}

func TestIncludesAllWithoutAnExpansionKeepsTheCountingForm(t *testing.T) {
	sql, args := buildSQL(t, criterion(models.CriterionModifierIncludesAll, generalA, generalB), nil)

	// The pre-hierarchy shape, kept for callers with no hierarchy: it is correct there, and
	// swapping it for EXISTS would be a change with no observable effect.
	assert.Contains(t, sql, "COUNT(*)")
	assert.Contains(t, sql, "INNER JOIN")
	assert.Equal(t, []any{generalA, generalB, 2}, args)
}

func TestExcludesRefusesTheWholeExpansion(t *testing.T) {
	sql, args := buildSQL(t, criterion(models.CriterionModifierExcludes, generalA), hierarchy())

	assert.Contains(t, sql, "NOT EXISTS (")
	assert.Contains(t, sql, "IN ($1,$2)")
	assert.Equal(t, []any{generalA, specificF}, args)
}

func TestAValueTheHierarchyDoesNotKnowSatisfiesItself(t *testing.T) {
	// A filter naming a tag nothing implies, and a filter naming one that does not exist at
	// all, both have to behave exactly as they did before the hierarchy existed.
	stranger := uuid.FromStringOrNil("00000000-0000-0000-0000-0000000000dd")
	assert.Equal(t, []uuid.UUID{stranger}, expansionOf(stranger, hierarchy()))

	unknown := uuid.FromStringOrNil("00000000-0000-0000-0000-0000000000ff")
	assert.Equal(t, []uuid.UUID{unknown}, expansionOf(unknown, hierarchy()))

	// And an id the map holds with an empty list is not a licence to match nothing.
	assert.Equal(t, []uuid.UUID{stranger}, expansionOf(stranger, map[uuid.UUID][]uuid.UUID{stranger: {}}))
}

func TestTheUnionDropsDuplicates(t *testing.T) {
	// Two requested tags whose expansions overlap: the shared tag has to appear once, because
	// the union goes straight into an IN clause.
	expansion := map[uuid.UUID][]uuid.UUID{
		generalA: {generalA, specificF},
		generalB: {generalB, specificF},
	}

	got := satisfiedBy([]uuid.UUID{generalA, generalB}, expansion)

	assert.Equal(t, []uuid.UUID{generalA, specificF, generalB}, got)
	assert.Equal(t, 1, strings.Count(strings.Join([]string{got[1].String()}, " "), specificF.String()))
}

func TestWithoutAnExpansionTheUnionIsTheValuesThemselves(t *testing.T) {
	values := []uuid.UUID{generalA, generalB}

	// The same slice, not a copy: the nil case is the identity, and a caller that passes nil
	// must not pay for a rebuild.
	assert.Equal(t, values, satisfiedBy(values, nil))
}
