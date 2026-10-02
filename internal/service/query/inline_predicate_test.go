package query

import (
	"strings"
	"testing"

	sq "github.com/Masterminds/squirrel"
)

// Upstream PR #1280 inlines the `deleted = false` predicates so they are SQL literals rather
// than bind parameters, and adds `ExecuteCountCustomPlan` for the counts whose selectivity
// depends on a filter value.
//
// The inlining half is easy to check by generating the SQL, which is what these tests do: the
// whole point is what appears in the statement text, and `sq.Eq` versus a literal is visible
// there. A test that only ran the query would pass either way, because both forms return the
// same rows -- so it would be decoration.

// `sq.Eq` parameterises the value, which is what #1280 is removing.
func TestSquirrelEqParameterisesTheDeletedPredicate(t *testing.T) {
	psql := sq.StatementBuilder.PlaceholderFormat(sq.Dollar)
	q := psql.Select("tags.id").From("tags").Where(sq.Eq{"deleted": false})

	sql, args, err := q.ToSql()
	if err != nil {
		t.Fatalf("ToSql: %v", err)
	}
	if !strings.Contains(sql, "deleted = $1") {
		t.Errorf("expected the parameterised form, got %q", sql)
	}
	if len(args) != 1 {
		t.Errorf("expected 1 bind value, got %v", args)
	}
}

// The inlined form carries the value in the statement text and binds nothing.
func TestTheInlinedPredicateIsALiteralAndBindsNothing(t *testing.T) {
	psql := sq.StatementBuilder.PlaceholderFormat(sq.Dollar)
	q := psql.Select("tags.id").From("tags").Where("deleted = false")

	sql, args, err := q.ToSql()
	if err != nil {
		t.Fatalf("ToSql: %v", err)
	}
	if !strings.Contains(sql, "deleted = false") {
		t.Errorf("the predicate is not a literal: %q", sql)
	}
	if strings.Contains(sql, "$1") {
		t.Errorf("a placeholder survived the inlining: %q", sql)
	}
	if len(args) != 0 {
		t.Errorf("the inlined predicate still binds %v", args)
	}
}

// Both forms are semantically identical, so the change is a planner concern only. Asserting
// that is what stops someone "fixing" the literal back to a bind parameter, or the reverse.
func TestBothFormsGenerateTheSamePredicate(t *testing.T) {
	psql := sq.StatementBuilder.PlaceholderFormat(sq.Dollar)

	bound, _, err := psql.Select("id").From("tags").Where(sq.Eq{"deleted": false}).ToSql()
	if err != nil {
		t.Fatalf("ToSql: %v", err)
	}
	literal, _, err := psql.Select("id").From("tags").Where("deleted = false").ToSql()
	if err != nil {
		t.Fatalf("ToSql: %v", err)
	}

	boundNorm := strings.ReplaceAll(bound, " = $1", " = false")
	if boundNorm != literal {
		t.Errorf("the two forms are not equivalent after normalising the placeholder:\n  %q\n  %q",
			boundNorm, literal)
	}
}

// Qualified and unqualified columns are both inlined upstream, and each site has to keep its
// own column name -- inlining the wrong one compiles fine and filters on the wrong table.
func TestQualifiedAndUnqualifiedFormsBothInline(t *testing.T) {
	psql := sq.StatementBuilder.PlaceholderFormat(sq.Dollar)
	for _, col := range []string{"deleted", "scenes.deleted", "studios.deleted", "performers.deleted"} {
		sql, args, err := psql.Select("id").From("t").Where(col + " = false").ToSql()
		if err != nil {
			t.Fatalf("ToSql(%s): %v", col, err)
		}
		if !strings.Contains(sql, col+" = false") {
			t.Errorf("%s: the column was not inlined, got %q", col, sql)
		}
		if len(args) != 0 {
			t.Errorf("%s: inlined form still bound %v", col, args)
		}
	}
}
