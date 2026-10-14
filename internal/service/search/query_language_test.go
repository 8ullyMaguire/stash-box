package search

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The table is the contract, and the round trip is asserted on every row rather than on one:
// a language that parses more than it can render is a language whose saved searches cannot be
// re-derived, and the failure would show up only for the one term nobody tested.
func TestEachTermKindParsesAndRendersBack(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{"a bare word is a tag", "anal", "anal"},
		{"the explicit form is the same query", "tag:anal", "anal"},
		{"exclusion", "-rough", "-rough"},
		{"exclusion, spelled out", "-tag:rough", "-rough"},
		{"a quoted tag name", `"hotel room"`, `"hotel room"`},
		{"free text", `text:"hotel room"`, `text:"hotel room"`},
		{"a single free-text word", "text:hotel", "text:hotel"},
		{"a performer", "performer:ana", "performer:ana"},
		{"a studio", "studio:acme", "studio:acme"},
		{"the studio tree", "parent_studio:acme", "parent_studio:acme"},
		{"a content kind", "type:scene", "type:scene"},
		{"a range", "duration:60..600", "duration:60..600"},
		{"a range with one side", "duration:..600", "duration:..600"},
		{"a comparison", "duration:>300", "duration:>300"},
		{"a date", "date:2020-01-01", "date:2020-01-01"},
		{"a sort", "sort:date", "sort:date"},
		{"a descending sort, which is what the negation means here", "-sort:date", "-sort:date"},
		{"several terms", "anal -rough sort:-date", "anal -rough -sort:date"},
		{"whitespace is not significant", "  anal \t pov \n", "anal pov"},
		{"an empty query is an empty expression", "   ", ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			expr, err := Parse(c.text)
			assert.NoError(t, err)
			if expr == nil {
				return
			}

			rendered := Render(expr)
			assert.Equal(t, c.want, rendered, "canonical form")

			// The round trip: the canonical form parses to the same query, and renders the
			// same way again. A stable fixed point is what a saved search depends on.
			again, err := Parse(rendered)
			assert.NoError(t, err)
			assert.True(t, Equal(expr, again), "the canonical form must parse to the same query")
			if again != nil {
				assert.Equal(t, rendered, Render(again), "rendering must be idempotent")
			}

			// Source is kept verbatim: a caller shows the person what they typed.
			assert.Equal(t, c.text, expr.Source)
		})
	}
}

func TestTermShapes(t *testing.T) {
	expr, err := Parse(`duration:60..600 -duration:>300 tag:"a b" text:"hotel room" sort:-date`)
	assert.NoError(t, err)

	if assert.Len(t, expr.Terms, 5) {
		rangeTerm := expr.Terms[0]
		assert.Equal(t, FieldDuration, rangeTerm.Field)
		assert.False(t, rangeTerm.Negated)
		if assert.NotNil(t, rangeTerm.Range) {
			assert.Equal(t, "60", rangeTerm.Range.From)
			assert.Equal(t, "600", rangeTerm.Range.To)
		}
		assert.Equal(t, OpEq, rangeTerm.Op)

		cmp := expr.Terms[1]
		assert.True(t, cmp.Negated)
		assert.Equal(t, OpGt, cmp.Op)
		assert.Equal(t, "300", cmp.Word)
		assert.Nil(t, cmp.Range)

		quotedTag := expr.Terms[2]
		assert.Equal(t, FieldTag, quotedTag.Field)
		assert.True(t, quotedTag.Quoted)
		assert.Equal(t, "a b", quotedTag.Word)

		quotedText := expr.Terms[3]
		assert.Equal(t, FieldText, quotedText.Field)
		assert.Equal(t, "hotel room", quotedText.Word)

		sortTerm := expr.Terms[4]
		assert.Equal(t, FieldSort, sortTerm.Field)
		assert.True(t, sortTerm.Negated, "the negation of a sort means descending")
	}
}

// The offsets are the point of these cases: a diagnostic that says which term is wrong but not
// where it is makes a long query uneditable, and an offset is the one part of the message that
// cannot be reconstructed from the rest.
func TestErrorsNameTheirOffset(t *testing.T) {
	cases := []struct {
		name       string
		text       string
		offset     int
		want       string
		suggestion string
	}{
		{"a misspelled field", "duratoin:60", 0, `unknown field "duratoin"`, "duration"},
		{"a misspelled field, later in the query", "anal pov stodio:acme", 9, `unknown field "stodio"`, "studio"},
		{"an empty value", "duration:", 9, "needs a value", ""},
		{"an empty quoted value is a value", `tag:""`, 0, "", ""},
		{"an unterminated quote", `"unterminated`, 0, "unterminated quote", ""},
		{"a lone dash", "anal -", 5, "a lone '-' is not a term", ""},
		{"a range with no sides", "duration:..", 9, "a range needs at least one side", ""},
		{"a comparison with no value", "duration:>", 9, "needs a value", ""},
		{"a quoted value with a tail", `tag:"a"b`, 4, "a quoted value must be the whole value", ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			expr, err := Parse(c.text)
			if c.want == "" {
				assert.NoError(t, err, "this one is legal")
				assert.NotNil(t, expr)

				return
			}

			assert.Nil(t, expr)

			var parseErr *ParseError
			if !assert.ErrorAs(t, err, &parseErr) {
				return
			}
			assert.Equal(t, c.offset, parseErr.Offset, "offset")
			assert.Contains(t, parseErr.Error(), c.want)
			if c.suggestion != "" {
				assert.Contains(t, parseErr.Error(), c.suggestion)
			}
		})
	}
}

func TestAnUnknownFieldFarFromEveryKnownOneGetsNoSuggestion(t *testing.T) {
	_, err := Parse("zzzzzzzzz:1")
	assert.Error(t, err)

	var parseErr *ParseError
	if assert.ErrorAs(t, err, &parseErr) {
		assert.Empty(t, parseErr.Suggestion, "a suggestion that is not close is noise")
	}
}

// Two spellings that a person writes for the same statement must be the same query, or the
// order of a saved search depends on which one they happened to type.
func TestBothSpellingsOfADescendingSortAreTheSameQuery(t *testing.T) {
	byValue, err := Parse("sort:-date")
	assert.NoError(t, err)

	byTerm, err := Parse("-sort:date")
	assert.NoError(t, err)

	assert.Equal(t, "-sort:date", Render(byValue), "the canonical form is the term form")
	assert.Equal(t, "-sort:date", Render(byTerm))
	assert.True(t, Equal(byValue, byTerm))

	if assert.Len(t, byValue.Terms, 1) {
		assert.True(t, byValue.Terms[0].Negated)
		assert.Equal(t, "date", byValue.Terms[0].Word, "the dash is the negation, not the value")
	}
}

// Render must quote anything that would not survive a round trip bare, and the empty quoted
// value is the case that proves it: rendering it unquoted would produce `tag:` which does not
// parse.
func TestRenderQuotesWhatWouldNotSurviveBare(t *testing.T) {
	expr, err := Parse(`tag:"" "a:b" "a b"`)
	assert.NoError(t, err)

	assert.Equal(t, `"" "a:b" "a b"`, Render(expr))
}
