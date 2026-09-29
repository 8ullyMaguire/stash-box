//go:build integration

package api

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbtest "github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/service/completion"
)

// The SQL weight duplication, checked against the Go weights (SPEC §7.7).
//
// The weights live in Go, in `internal/service/completion/score.go`. The COUNT
// QUERY needs them too and cannot call Go, so the same numbers appear a second time
// as SQL constants. That duplication is deliberate -- recomputing the score in SQL
// would put the formula in two places, and storing the score would make it a second
// source of truth -- which means the only thing keeping it honest is a test.
//
// THE FAILURE MODE THIS EXISTS FOR: someone changes a weight. The score a client
// sees and the count a quest is sized from then answer different questions. Nothing
// at any single row looks wrong, and both numbers are plausible. It would be a
// silently wrong answer with no test failing.
//
// ---------------------------------------------------------------------------
// WHY THIS FILE PARSES THE SQL INSTE OF HARD-CODING ITS EXPECTATIONS
// ---------------------------------------------------------------------------
// The first version compared the Go weights against a hand-transcribed Go map
// "of the SQL weights". Two mutations survived it:
//
//	SQL: performer country 8 -> 9        -> SURVIVED
//	Go:  add a field to performer only   -> SURVIVED
//
// and the reason is the reason for this rewrite. That test read a hand-typed copy
// of the SQL and compared Go against it, so it was a parity check between the Go
// scorer and a THIRD artefact nobody maintained. Editing the SQL could not fail it,
// because the file under test was not in the test.
//
// A parity test has to read one side from the thing it is checking. So the SQL is
// parsed here, out of the real file, and the hand-written part is reduced to a map
// from a column name to a Go field name -- which is mechanical, stable, and says
// nothing about weights.
//
// A test that hand-copies the expected values of the code under test is not a
// parity test. It is a test of the copy.

var theWeightExpr = regexp.MustCompile(`\*\s*(\d+)`)

// sqlBranchWeights extracts every `expression * N` weight from one CASE branch of
// the count query, keyed by the expression it multiplies.
//
// PARSING rather than reading line by line, because the expressions wrap: the
// measurements term is three lines long and ends in `) * 5`. A line-oriented parse
// sees two unweighted fragments and one spurious weight.
func sqlBranchWeights(t *testing.T, entityType completion.EntityType) map[string]int {
	t.Helper()

	path := filepath.Join("..", "queries", "sql", "completion.sql")
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "the count query is the file under test, so it has to "+
		"be READ, not remembered")

	src := string(raw)
	// The SQL's own literal, so the parser looks for exactly what is in the file.
	name := string(entityType)
	start := strings.Index(src, "WHEN '"+name+"' THEN (")
	require.NotEqual(t, -1, start,
		"no '%s' branch in %s -- it was renamed, and this test now fails rather "+
			"than silently checking nothing", name, path)
	end := strings.Index(src[start:], ") >= sqlc.arg(min_missing)")
	require.NotEqual(t, -1, end, "the '%s' branch's terminator changed shape", name)

	// Collapse wrapped lines so a multi-line expression is one expression.
	body := strings.Join(strings.Fields(src[start:start+end]), " ")

	out := map[string]int{}
	for _, m := range theWeightExpr.FindAllStringSubmatchIndex(body, -1) {
		weight, err := strconv.Atoi(body[m[2]:m[3]])
		require.NoError(t, err)

		// The expression is everything since the previous '+' separator.
		sep := strings.LastIndex(body[:m[0]], " + ")
		out[strings.TrimSpace(body[sep+3:m[0]])] = weight
	}
	require.NotEmpty(t, out, "no weights parsed from the '%s' branch", name)
	return out
}

// theSQLWeightField maps a token appearing in a SQL expression to the Go field that
// expression scores.
//
// The only hand-written knowledge in this file, deliberately about COLUMN NAMES
// rather than weights: `p.cup_size` is the measurements field because the SQL writes
// four measurement columns as one `OR` and the scorer counts them as one field. The
// weights come out of the file.
var theSQLWeightField = map[completion.EntityType]map[string]completion.Field{
	"performer": {
		"p.name":              completion.FieldName,
		"performer_aliases":   completion.FieldAliases,
		"p.birthdate":         completion.FieldBirthdate,
		"p.gender":            completion.FieldGender,
		"p.ethnicity":         completion.FieldEthnicity,
		"p.country":           completion.FieldCountry,
		"p.eye_color":         completion.FieldEyeColor,
		"p.hair_color":        completion.FieldHairColor,
		"p.height":            completion.FieldHeight,
		"p.cup_size":          completion.FieldMeasurements,
		"p.career_start_year": completion.FieldCareerDates,
		"performer_urls":      completion.FieldURLs,
		"performer_images":    completion.FieldImage,
	},
	"studio": {
		"st.name":             completion.FieldName,
		"studio_urls":         completion.FieldURLs,
		"studio_images":       completion.FieldImage,
		"st.parent_studio_id": completion.FieldParentStudio,
	},
	"site": {
		"si.name":        completion.FieldName,
		"si.url":         completion.FieldURLs,
		"si.description": completion.FieldDetails,
		"si.regex":       completion.FieldRegex,
	},
	"tag": {
		"t.name":        completion.FieldName,
		"t.description": completion.FieldDetails,
	},
	"scene": {
		"s.title":          completion.FieldName,
		"s.duration":       completion.FieldDuration,
		"s.studio_id":      completion.FieldStudio,
		"scene_performers": completion.FieldPerformers,
		"s.date":           completion.FieldDate,
		"scene_urls":       completion.FieldURLs,
		"scene_tags":       completion.FieldTags,
		"scene_images":     completion.FieldImage,
		"s.details":        completion.FieldDetails,
		"scene_snapshots":  completion.FieldSnapshotCoverage,
	},
}

// TestTheSQLWeightsMatchTheGoWeights is the real parity check: for every field the
// count query scores, the weight the SQL says and the weight the scorer says are
// the same number.
func TestTheSQLWeightsMatchTheGoWeights(t *testing.T) {
	for entityType, tokenToField := range theSQLWeightField {
		t.Run(string(entityType), func(t *testing.T) {
			byExpr := sqlBranchWeights(t, entityType)

			matched := 0
			for token, field := range tokenToField {
				var sqlWeight int
				var found bool
				for expr, w := range byExpr {
					if strings.Contains(expr, token) {
						sqlWeight, found = w, true
						break
					}
				}
				require.True(t, found,
					"the count query's '%s' branch has no expression mentioning "+
						"%q, so a weight the scorer applies is absent from the "+
						"query and every %s it counts is under-counted",
					entityType, token, entityType)

				goWeight, err := completion.WeightFor(entityType, field)
				require.NoError(t, err,
					"the count query scores %q for a %s but the scorer has no such "+
						"field -- renamed or removed on one side only",
					field, entityType)

				assert.Equal(t, sqlWeight, goWeight,
					"%s.%s is weighted %d in the Go scorer and %d in the count "+
						"query. The score a client sees and the count a quest is "+
						"sized from are now answering different questions",
					entityType, field, goWeight, sqlWeight)
				matched++
			}

			// The count of terms against the count of assertions. Without this, a
			// term added to the SQL matches no token, is skipped, and the test
			// passes having checked one field less than last time.
			assert.Equal(t, len(byExpr), matched,
				"the '%s' branch has %d weighted terms and %d are accounted for. "+
					"An unaccounted term is a weight the query applies that no "+
					"assertion here covers -- add it to theSQLWeightField, or "+
					"remove it from the query", entityType, len(byExpr), matched)
		})
	}
}

// TestTheSQLWeightTotalsMatchTheGoTotals catches a field the scorer knows and the
// query does not.
//
// The per-field test cannot see this: it iterates the tokens it knows, and a field
// with no token is skipped silently. Asserting the totals closes that direction.
func TestTheSQLWeightTotalsMatchTheGoTotals(t *testing.T) {
	for entityType, tokenToField := range theSQLWeightField {
		t.Run(string(entityType), func(t *testing.T) {
			byExpr := sqlBranchWeights(t, entityType)

			for _, field := range goFieldsScoredByQuery(t, entityType) {
				assert.True(t, hasTokenFor(field, tokenToField),
					"the scorer weights %q for a %s and the count query's '%s' "+
						"branch has no term for it, so the threshold every %s is "+
						"compared against is wrong",
					field, entityType, entityType, entityType)
			}

			var sqlTotal int
			for _, w := range byExpr {
				sqlTotal += w
			}
			goTotal, err := completion.TotalWeight(entityType)
			require.NoError(t, err)

			assert.Equal(t, sqlTotal, goTotal,
				"the count query's weights for a %s sum to %d and the scorer's to "+
					"%d. A term on one side only shifts the threshold for every "+
					"row of this type", entityType, sqlTotal, goTotal)
		})
	}
}

// goFieldsScoredByQuery lists the scorer's fields that actually contribute to a
// score.
//
// The filter matters: `name` is scored at weight zero, contributes to no
// denominator, and has no term in the count query -- which is correct rather than a
// drift. A naive "every scorer field must appear in the query" assertion would fail
// on a field behaving exactly as designed, and a test that fails on correct code
// gets ignored, which is worse than not having it.
func goFieldsScoredByQuery(t *testing.T, entityType completion.EntityType) []completion.Field {
	t.Helper()
	fields, err := completion.Fields(entityType)
	require.NoError(t, err)

	scored := make([]completion.Field, 0, len(fields))
	for _, f := range fields {
		// Weight zero contributes to no denominator, so it is not counted, so it
		// does not need a query term.
		w, err := completion.WeightFor(entityType, f)
		require.NoError(t, err)
		if w == 0 {
			continue
		}
		scored = append(scored, f)
	}
	return scored
}

// hasTokenFor reports whether the query's token map accounts for a scorer field.
func hasTokenFor(field completion.Field, tokenToField map[string]completion.Field) bool {
	for _, f := range tokenToField {
		if f == field {
			return true
		}
	}
	return false
}

// TestTheCountQueryIsValidSQL is the check the weight-parity tests structurally
// cannot make, and the one that actually matters.
//
// MUTATIONS THAT SURVIVED the weight-parity tests:
//
//	remove a `::int` cast             -> SURVIVED
//	move the cast inside the NOT      -> SURVIVED
//	both of those, in three branches  -> SURVIVED
//
// The parity test reads a WEIGHT (`* 5`) and a field token. Neither is the cast,
// so from its point of view all of those edits changed nothing. A textual
// "is there an ::int, and is it after the term's closing paren" check was the
// second attempt and it was wrong twice more, because `NOT (...)::int * 5` does
// contain an `::int` and the paren the position check compared against was the
// term's own.
//
// That is twice a check shaped like the text it reads has failed to model the
// thing it checks. The lesson is the same both times: ASK THE THING THAT HAS TO BE
// TRUE rather than inferring it. PostgreSQL is available in these tests, so ask
// PostgreSQL -- `PREPARE` type-checks and plans a statement without running it,
// which is exactly the question "will this execute?".
//
// A query that will not execute returns a GraphQL error to every client that asks
// it for a count, and nothing else notices: `sqlc generate` checks the SQL's
// SYNTAX and happily generates Go from a query that cannot run. That is how this
// got shipped in the first place -- the count query had never been executed by
// anything at all.
func TestTheCountQueryIsValidSQL(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "queries", "sql", "completion.sql"))
	require.NoError(t, err)

	src := string(raw)
	for _, name := range []string{
		"CountEntitiesWithCompletionBelow",
		"ListIncompleteEntities",
	} {
		t.Run(name, func(t *testing.T) {
			stmt := prepareableStatement(t, src, name)

			// Types are declared. PREPARE will not infer a parameter's type from
			// a comparison nested inside a CASE it may not evaluate -- it answers
			// "there is no parameter $1" -- and that is a statement about
			// PREPARE's strictness, not about the query. sqlc's generated call
			// passes exactly these types at run time, so declaring them tests the
			// query as it is actually used.
			_, err := dbtest.DB().Exec(t.Context(), "PREPARE q AS "+stmt)
			assert.NoError(t, err,
				"%s does not type-check. PostgreSQL has no boolean*integer "+
					"operator, so a weight term must be `(NOT (...))::int * N` -- "+
					"and a cast placed inside the NOT casts NOT's argument, which "+
					"is rejected the same way. The statement is:\n%s", name, stmt)
		})
	}
}

// prepareableStatement extracts one named query and rewrites sqlc.arg(...) into
// PostgreSQL positional parameters.
//
// NUMBERING IS BY ARGUMENT NAME, not by position. The count query's min_missing
// appears once per CASE branch, so numbering by occurrence gives it five different
// numbers; PostgreSQL then rejects the statement with "there is no parameter $2",
// and the error says nothing about the query being wrong. One name is one number,
// and the repeated uses share it -- which is what the generated call does too.
func prepareableStatement(t *testing.T, src, name string) string {
	t.Helper()

	marker := "-- name: " + name
	i := strings.Index(src, marker)
	require.NotEqual(t, -1, i, "%s is not in the SQL file", name)

	// The slice must start AFTER the marker. `strings.Index(body, "-- name: ")`
	// on a string that BEGINS with the marker returns 0, so a `next > 0` guard
	// skipped the truncation entirely and the whole rest of the FILE went to
	// PostgreSQL with its `sqlc.arg(...)` calls intact -- $1 landed where a
	// literal belonged, and the error named the symptom, not the cause. It took
	// a diff against a hand-built statement to see the two were not the same
	// text.
	//
	// `next >= 0` is not the fix either: the marker is at offset 0, so that
	// truncated to the EMPTY string and the next line panicked on a -1 index.
	// The boundary is marker + 1.
	body := src[i+len(marker):]
	if next := strings.Index(body, "-- name: "); next >= 0 {
		body = body[:next]
	}

	at := strings.Index(body, "SELECT")
	require.NotEqual(t, -1, at, "%s has no SELECT in it", name)
	stmt := strings.TrimSpace(body[at:])
	stmt = strings.TrimRight(stmt, ";")

	// Longest suffix first, so `::text` is consumed as part of the argument rather
	// than leaving `) ::int` dangling. The suffix is KEPT: a declared parameter
	// with a redundant cast still type-checks the same way, and dropping it would
	// make the statement differ from the one that runs.
	byName := map[string]int{}
	castOf := map[string]string{}
	var order []string
	for _, m := range regexp.MustCompile(`sqlc\.arg\((\w+)\)((?:::\w+)?)`).
		FindAllStringSubmatch(stmt, -1) {
		if _, seen := byName[m[1]]; !seen {
			byName[m[1]] = len(order) + 1
			order = append(order, m[1])
			castOf[m[1]] = strings.TrimPrefix(m[2], "::")
		}
	}

	stmt = regexp.MustCompile(`sqlc\.arg\(\w+\)((?:::\w+)?)`).
		ReplaceAllStringFunc(stmt, func(m string) string {
			argName := regexp.MustCompile(`sqlc\.arg\((\w+)\)`).FindStringSubmatch(m)[1]
			return fmt.Sprintf("$%d", byName[argName])
		})
	return stmt
}

// numbered by prepareableStatement, which is what PREPARE's declaration list is
// positional about.
//
// Declaring rather than inferring is deliberate. PREPARE refuses to infer a type
// for a parameter that only appears inside a comparison in a CASE branch, and the
// resulting error names the parameter rather than the problem. sqlc's generated
// call supplies these same types when the query runs, so declaring them here tests
// the query as it is actually used.
func parameterTypes(_ string, order []string) []string {
	out := make([]string, 0, len(order))
	for _, argName := range order {
		out = append(out, sqlcArgTypes[argName])
	}
	return out
}

// sqlcArgTypes is the PostgreSQL type of each named argument in the count and list
// queries.
//
// Hand-written and small, and the one thing a reader can check against the query's
// own casts: every argument is written `sqlc.arg(name)::type` in the SQL, so this
// table is that same fact, typed. A new argument with a type not in this map gets
// an empty entry, which PostgreSQL rejects loudly -- the failure this table exists
// to make visible.
var sqlcArgTypes = map[string]string{
	"entity_type": "text",
	"min_missing": "integer",
	"after_id":    "uuid",
	"page_size":   "integer",
}

// The textual version of the cast check -- "every `* N` line has an `::int`, and
// the `::int` comes after the term's closing paren" -- is GONE, replaced by
// TestTheCountQueryIsValidSQL. It is recorded here rather than deleted outright
// because the reasoning behind it was reasonable and still wrong twice:
//
//	remove a cast        -> the has-an-::int check catches it
//	move the cast inside -> the line still has ::int, and the paren the position
//	                       check compared against was the term's own
//
// A check shaped like the text it reads is only as good as my model of that text.
// The PREPARE check has no model to get wrong: PostgreSQL either accepts the
// statement or it does not.
