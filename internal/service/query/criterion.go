package query

import (
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/models"
)

// ApplyMultiIDCriterion applies multi-ID criterion (includes/includes_all/excludes)
// Modifies the query pointer in place
// tableName: the main table name (e.g., "scenes")
// joinTable: the join table name (e.g., "scene_performers")
// fkColumn: the foreign key column in the join table referencing the main table (e.g., "scene_id")
// joinField: the field in the join table to filter on (e.g., "performer_id")
func ApplyMultiIDCriterion(query *sq.SelectBuilder, tableName, joinTable, fkColumn, joinField string, criterion *models.MultiIDCriterionInput) error {
	return ApplyMultiIDCriterionExpanded(query, tableName, joinTable, fkColumn, joinField, criterion, nil)
}

// ApplyMultiIDCriterionExpanded is ApplyMultiIDCriterion for a field whose values have a
// hierarchy: `expansion` maps each REQUESTED id to the ids that satisfy it, so an item that
// carries something implying the requested value is a match.
//
// A nil `expansion` means every id is its own expansion, which is exactly what the plain
// ApplyMultiIDCriterion above asks for. The map is per requested id rather than one flat set
// because INCLUDES_ALL is a per-requested-id question: an item tagged only `cowgirl_position`
// satisfies both `woman_on_top` and `vaginal_sex` -- which it implies -- while carrying
// neither, and a flat set cannot say that.
func ApplyMultiIDCriterionExpanded(query *sq.SelectBuilder, tableName, joinTable, fkColumn, joinField string, criterion *models.MultiIDCriterionInput, expansion map[uuid.UUID][]uuid.UUID) error {
	// The join tables are unique on (fk, field), so the Having count below is a
	// count of distinct ids. A repeated id would raise the target without
	// matching another row, and the criterion would match nothing.
	values := make([]uuid.UUID, 0, len(criterion.Value))
	seen := make(map[uuid.UUID]struct{}, len(criterion.Value))
	for _, id := range criterion.Value {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		values = append(values, id)
	}

	// For a single value, "includes all" is identical to "includes" — collapse
	// here so the Includes branch handles both.
	mod := criterion.Modifier
	if mod == models.CriterionModifierIncludesAll && len(values) == 1 {
		mod = models.CriterionModifierIncludes
	}

	switch mod {
	case models.CriterionModifierIncludes:
		// Semi-join — naturally deduplicating regardless of len, no DISTINCT needed.
		// The hierarchy makes this a union: "any of these" is satisfied by anything that
		// implies any of them.
		subquery := sq.Select("1").
			From(joinTable).
			Where(sq.Eq{joinField: satisfiedBy(values, expansion)}).
			Where(sq.Expr(fmt.Sprintf("%s.%s = %s.id", joinTable, fkColumn, tableName)))
		*query = query.Where(sq.Expr("EXISTS (?)", subquery))
	case models.CriterionModifierIncludesAll:
		// One EXISTS per REQUESTED value, each over that value's expansion. It cannot be a
		// count over an expanded list: an item tagged only `cowgirl_position` satisfies both
		// `woman_on_top` and `vaginal_sex` -- which it implies -- while contributing ONE join
		// row, so "COUNT(*) = len(expanded)" would refuse it. The old counting form is kept
		// below only for the no-hierarchy case it was written for, where it is equivalent.
		if expansion == nil {
			// len > 1 only; "match all of these" has no semi-join equivalent.
			subquery := sq.Select(fkColumn).
				From(joinTable).
				Where(sq.Eq{joinField: values}).
				GroupBy(fkColumn).
				Having(sq.Eq{"COUNT(*)": len(values)})
			*query = query.JoinClause(sq.Expr(fmt.Sprintf("INNER JOIN (?) AS %s_filter ON %s.id = %s_filter.%s", joinTable, tableName, joinTable, fkColumn), subquery))

			break
		}

		for _, value := range values {
			subquery := sq.Select("1").
				From(joinTable).
				Where(sq.Eq{joinField: expansionOf(value, expansion)}).
				Where(sq.Expr(fmt.Sprintf("%s.%s = %s.id", joinTable, fkColumn, tableName)))
			// Each Where ANDs onto the previous one, which is the "all of" part.
			*query = query.Where(sq.Expr("EXISTS (?)", subquery))
		}
	case models.CriterionModifierExcludes:
		// The whole expansion is refused, not just the named tag: excluding `sex` has to
		// exclude what implies `sex`, or the hierarchy is useless for filtering.
		subquery := sq.Select("1").
			From(joinTable).
			Where(sq.Eq{joinField: satisfiedBy(values, expansion)}).
			Where(sq.Expr(fmt.Sprintf("%s.%s = %s.id", joinTable, fkColumn, tableName)))
		*query = query.Where(sq.Expr("NOT EXISTS (?)", subquery))
	default:
		return fmt.Errorf("unsupported modifier %s for %s.%s", criterion.Modifier, joinTable, joinField)
	}
	return nil
}

// ApplyEnumCriterion applies a criterion whose value is a nullable enum column
// (eye_color, hair_color, breast_type). Those are nullable varchar in the schema,
// so IS NULL / NOT NULL are meaningful in their own right -- a performer with no
// recorded eye color must be reachable by filtering for exactly that.
//
// Generic over the enum so eye/hair/breast share one implementation and cannot
// drift apart.
func ApplyEnumCriterion[T ~string](query sq.SelectBuilder, field string, value *T, modifier models.CriterionModifier) sq.SelectBuilder {
	switch modifier {
	case models.CriterionModifierEquals:
		if value == nil {
			return query.Where(field + " IS NULL")
		}
		return query.Where(sq.Eq{field: string(*value)})
	case models.CriterionModifierNotEquals:
		if value == nil {
			return query.Where(field + " IS NOT NULL")
		}
		// A performer with no recorded value is not a non-match on "not this
		// value" -- NULL means unknown, not "something else".
		return query.Where(sq.And{
			sq.NotEq{field: string(*value)},
			sq.Expr(field + " IS NOT NULL"),
		})
	case models.CriterionModifierIsNull:
		return query.Where(field + " IS NULL")
	case models.CriterionModifierNotNull:
		return query.Where(field + " IS NOT NULL")
	default:
		return query
	}
}

// ApplyBodyModificationCriterion filters on a body modification (tattoo or
// piercing) recorded for the performer.
//
// Tattoos and piercings are relational tables keyed (performer_id, location),
// not jsonb columns on the performer, so this is an EXISTS semi-join. That is
// also what makes "location AND description match the same row" fall out for
// free: a performer with a left-shoulder tattoo and a separate wing tattoo does
// not match a query asking for both, because no single row satisfies both
// predicates.
func ApplyBodyModificationCriterion(query sq.SelectBuilder, table, fkColumn string, criterion *models.BodyModificationCriterionInput) sq.SelectBuilder {
	if criterion == nil {
		return query
	}

	// A criterion with neither field set would match every performer that has
	// any modification at all, which is almost never what the caller meant.
	if criterion.Location == nil && criterion.Description == nil {
		return query
	}

	mod := criterion.Modifier
	negate := false

	switch mod {
	case models.CriterionModifierEquals, models.CriterionModifierIncludes:
		// handled below
	case models.CriterionModifierNotEquals:
		negate = true
		mod = models.CriterionModifierEquals
	case models.CriterionModifierIsNull:
		// "no modification at all"
		return query.Where(sq.Expr(
			"NOT EXISTS (SELECT 1 FROM " + table + " WHERE " + table + "." + fkColumn + " = performers.id)"))
	case models.CriterionModifierNotNull:
		return query.Where(sq.Expr(
			"EXISTS (SELECT 1 FROM " + table + " WHERE " + table + "." + fkColumn + " = performers.id)"))
	default:
		return query
	}

	subquery := sq.Select("1").
		From(table).
		Where(sq.Expr(table + "." + fkColumn + " = performers.id"))

	if criterion.Location != nil {
		switch mod {
		case models.CriterionModifierEquals:
			subquery = subquery.Where(sq.Eq{table + ".location": *criterion.Location})
		case models.CriterionModifierIncludes:
			subquery = subquery.Where(sq.ILike{table + ".location": "%" + *criterion.Location + "%"})
		}
	}

	if criterion.Description != nil {
		switch mod {
		case models.CriterionModifierEquals:
			subquery = subquery.Where(sq.Eq{table + ".description": *criterion.Description})
		case models.CriterionModifierIncludes:
			subquery = subquery.Where(sq.ILike{table + ".description": "%" + *criterion.Description + "%"})
		}
	}

	sql, args, err := subquery.ToSql()
	if err != nil {
		// Built from a fixed set of shapes, so this cannot fail in practice;
		// degrade to no filter rather than emitting a broken query.
		return query
	}

	exists := "EXISTS (" + sql + ")"
	if negate {
		exists = "NOT " + exists
	}
	return query.Where(sq.Expr(exists, args...))
}

// ApplyIDCriterion applies ID criterion for a direct column (equals, not equals, includes, excludes, is null, not null)
// Returns the modified query
func ApplyIDCriterion(query sq.SelectBuilder, field string, criterion *models.IDCriterionInput) sq.SelectBuilder {
	switch criterion.Modifier {
	case models.CriterionModifierEquals:
		if len(criterion.Value) > 0 {
			return query.Where(sq.Eq{field: criterion.Value[0]})
		}
		return query
	case models.CriterionModifierNotEquals:
		if len(criterion.Value) > 0 {
			return query.Where(sq.NotEq{field: criterion.Value[0]})
		}
		return query
	case models.CriterionModifierIncludes:
		return query.Where(sq.Eq{field: criterion.Value})
	case models.CriterionModifierExcludes:
		return query.Where(sq.Or{sq.Expr(field + " IS NULL"), sq.NotEq{field: criterion.Value}})
	case models.CriterionModifierIsNull:
		return query.Where(field + " IS NULL")
	case models.CriterionModifierNotNull:
		return query.Where(field + " IS NOT NULL")
	default:
		return query
	}
}

// ApplyIntCriterion applies integer criterion (equals, not equals, greater than, less than, is null, not null)
// Returns the modified query
func ApplyIntCriterion(query sq.SelectBuilder, field string, criterion *models.IntCriterionInput) sq.SelectBuilder {
	switch criterion.Modifier {
	case models.CriterionModifierEquals:
		return query.Where(sq.Eq{field: criterion.Value})
	case models.CriterionModifierNotEquals:
		return query.Where(sq.NotEq{field: criterion.Value})
	case models.CriterionModifierGreaterThan:
		return query.Where(sq.Gt{field: criterion.Value})
	case models.CriterionModifierLessThan:
		return query.Where(sq.Lt{field: criterion.Value})
	case models.CriterionModifierIsNull:
		return query.Where(field + " IS NULL")
	case models.CriterionModifierNotNull:
		return query.Where(field + " IS NOT NULL")
	default:
		return query
	}
}

// ApplyStringCriterion applies string criterion (equals, not equals, includes, is null, not null)
// Returns the modified query
func ApplyStringCriterion(query sq.SelectBuilder, field string, criterion *models.StringCriterionInput) sq.SelectBuilder {
	switch criterion.Modifier {
	case models.CriterionModifierEquals:
		return query.Where(sq.Eq{field: criterion.Value})
	case models.CriterionModifierNotEquals:
		return query.Where(sq.NotEq{field: criterion.Value})
	case models.CriterionModifierIncludes:
		return query.Where(sq.ILike{field: "%" + criterion.Value + "%"})
	case models.CriterionModifierIsNull:
		return query.Where(field + " IS NULL")
	case models.CriterionModifierNotNull:
		return query.Where(field + " IS NOT NULL")
	default:
		return query
	}
}

// ApplyDateCriterion applies date criterion (equals, not equals, greater than, less than, is null, not null)
// Returns the modified query
func ApplyDateCriterion(query sq.SelectBuilder, field string, criterion *models.DateCriterionInput) sq.SelectBuilder {
	switch criterion.Modifier {
	case models.CriterionModifierEquals:
		return query.Where(sq.Eq{field: criterion.Value})
	case models.CriterionModifierNotEquals:
		return query.Where(sq.NotEq{field: criterion.Value})
	case models.CriterionModifierGreaterThan:
		return query.Where(sq.Gt{field: criterion.Value})
	case models.CriterionModifierLessThan:
		return query.Where(sq.Lt{field: criterion.Value})
	case models.CriterionModifierIsNull:
		return query.Where(field + " IS NULL")
	case models.CriterionModifierNotNull:
		return query.Where(field + " IS NOT NULL")
	default:
		return query
	}
}

// expansionOf is the ids that satisfy one requested id: always at least the id itself, so a
// filter naming a value the hierarchy has never heard of behaves exactly as it did before
// the hierarchy existed.
func expansionOf(value uuid.UUID, expansion map[uuid.UUID][]uuid.UUID) []uuid.UUID {
	if ids, ok := expansion[value]; ok && len(ids) > 0 {
		return ids
	}

	return []uuid.UUID{value}
}

// satisfiedBy is the union over every requested value's expansion, or the values themselves
// when there is no hierarchy. Duplicates are dropped because the result goes straight into an
// IN clause and an id repeated there is noise rather than a criterion.
func satisfiedBy(values []uuid.UUID, expansion map[uuid.UUID][]uuid.UUID) []uuid.UUID {
	if expansion == nil {
		return values
	}

	seen := make(map[uuid.UUID]struct{}, len(values))
	out := make([]uuid.UUID, 0, len(values))
	for _, value := range values {
		for _, id := range expansionOf(value, expansion) {
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}

	return out
}
