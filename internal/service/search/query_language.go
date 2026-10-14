// Package search is the query language of the discovery surface: the text a person types,
// parsed into an expression, then LOWERED into the filter structs every query builder in this
// repo already understands.
//
// WHY A LANGUAGE AND NOT MORE FIELDS. NexusXXX section 8.2 asks for "boolean tag matching
// against the ontology" and section 8.4 for a filterable trending feed; the codebase already
// has the filters and already applies the tag hierarchy to them (phase 4). What it does not
// have is a way for a PERSON to say what they mean in one line and for a saved search to store
// it. So this package owns exactly two things: the grammar, and the translation.
//
// WHAT THIS PACKAGE DELIBERATELY DOES NOT DO. It never touches a database. A tag term is a
// NAME, because that is what a person types and what a link can carry; turning a name into an
// id is Lower's caller's job, through the Resolver interface. That is also why a filter naming
// a tag that does not exist is not a parse error: whether the name exists is not a property of
// the text. See docs/spec/feature-discovery.md section 3.1.
//
// THE ROUND TRIP IS THE CONTRACT. Render(Parse(t)) is the canonical form of t, and parsing the
// canonical form again yields the same expression. A saved search stores the canonical text and
// re-derives the filter every time it runs, so a parser that could not render what it parsed
// would make a saved search a string nobody could re-derive
// (docs/spec/feature-discovery.md section 3.2).
package search

import (
	"fmt"
	"sort"
	"strings"
)

// Field is a term's field name. A bare word is FieldTag: the draft's own examples are
// `anal -rough` and `anal pov`, so a bare word is a TAG, not free text (spec section 4.1).
type Field string

const (
	FieldTag          Field = "tag"
	FieldText         Field = "text"
	FieldPerformer    Field = "performer"
	FieldStudio       Field = "studio"
	FieldParentStudio Field = "parent_studio"
	FieldType         Field = "type"
	FieldDuration     Field = "duration"
	FieldDate         Field = "date"
	FieldScore        Field = "score"
	FieldSort         Field = "sort"
	FieldSimilarTo    Field = "similar_to"
	FieldPlaylist     Field = "playlist"
)

// knownFields is every field the grammar accepts, and the list the suggestion for a misspelled
// field is drawn from. A field that is KNOWN but whose lowering is not built yet is a
// diagnostic from Lower, not a parse error: the grammar is the draft's, the filter structs
// catch up slice by slice (spec section 4).
var knownFields = []Field{
	FieldTag, FieldText, FieldPerformer, FieldStudio, FieldParentStudio, FieldType,
	FieldDuration, FieldDate, FieldScore, FieldSort, FieldSimilarTo, FieldPlaylist,
}

func (f Field) known() bool {
	for _, k := range knownFields {
		if f == k {
			return true
		}
	}

	return false
}

// Op is a comparison on a scalar term. OpEq is the default, and for a range term (`60..600`) Op
// is OpEq and Range is set.
type Op uint8

const (
	OpEq Op = iota
	OpGt
	OpGte
	OpLt
	OpLte
)

func (o Op) String() string {
	switch o {
	case OpGt:
		return ">"
	case OpGte:
		return ">="
	case OpLt:
		return "<"
	case OpLte:
		return "<="
	}

	return ""
}

// Range is a `from..to` value. Either side may be empty (`..600`, `60..`), and the two sides
// are kept as TEXT because they are dates or durations depending on the field, and it is the
// lowering that knows which.
type Range struct {
	From string
	To   string
}

func (r Range) String() string { return r.From + ".." + r.To }

// Term is one whitespace-separated term. Offset and ValueOffset are byte offsets into the
// source text and exist so an error can point at the thing that is wrong: a diagnostic that
// says which term is unknown but not where it is makes a long query uneditable.
type Term struct {
	Field       Field
	Negated     bool
	Offset      int
	ValueOffset int

	// Word is the value for a word or quoted term, already unescaped of its quotes.
	Word   string
	Quoted bool

	// Range is set for `a..b`; Op is set for `>n`, `>=n`, `<n`, `<=n`. They are mutually
	// exclusive, and Op is OpEq when neither is present.
	Range *Range
	Op    Op
}

// IsTag reports whether the term names a tag: a bare word is FieldTag, so this is the common
// case rather than a special one.
func (t Term) IsTag() bool { return t.Field == FieldTag }

// Expr is a parsed query. Source is kept verbatim so a caller can show the user what they
// typed, which is NOT the same as Render's output (Render canonicalises).
type Expr struct {
	Source string
	Terms  []Term
}

// ParseError names the offset of the thing that is wrong. An unknown field or an empty value
// is an ERROR and not a silently dropped clause: a filter nobody applied is a wrong answer
// presented as a right one (spec section 4.2, 4.3).
type ParseError struct {
	Offset     int
	Msg        string
	Suggestion string

	// Term is the offending term as written. Parse fills it in for every error its readers
	// return, because the term's TEXT is known where it is read and nowhere else; an error
	// raised by Parse itself already has the whole input for it.
	Term string
}

func (e *ParseError) Error() string {
	out := fmt.Sprintf("at offset %d: %s", e.Offset, e.Msg)
	if e.Suggestion != "" {
		out += fmt.Sprintf(" (did you mean %q?)", e.Suggestion)
	}

	return out
}

func isSpace(c byte) bool { return c == ' ' || c == '	' || c == '\n' || c == '\r' }

// Parse reads a query. It is pure: no database, no clock, no ids.
func Parse(text string) (*Expr, error) {
	expr := &Expr{Source: text}

	for i := 0; i < len(text); {
		for i < len(text) && isSpace(text[i]) {
			i++
		}
		if i >= len(text) {
			break
		}

		start := i
		negated := false
		if text[i] == '-' {
			negated = true
			i++
			if i >= len(text) || isSpace(text[i]) {
				return nil, &ParseError{Offset: start, Msg: "a lone '-' is not a term", Term: "-"}
			}
		}

		raw, next, err := readTerm(text, i)
		if err != nil {
			return nil, withTerm(err, text[i:])
		}
		i = next

		term, err := parseTerm(raw, start)
		if err != nil {
			return nil, withTerm(err, raw)
		}
		term.Negated = negated || term.Negated
		expr.Terms = append(expr.Terms, term)
	}

	return expr, nil
}

// withTerm names the term an error is about. The readers raise errors from inside a term, where
// the surrounding text is out of scope, so the caller that DOES have it fills it in rather than
// every reader repeating it.
func withTerm(err error, term string) error {
	if parseErr, ok := err.(*ParseError); ok && parseErr.Term == "" {
		parseErr.Term = term
	}

	return err
}

// readTerm reads one term: up to the next whitespace that is not inside quotes. Quoted runs are
// skipped whole, so `tag:"a b"` is one term and `"a b" -c` is two.
func readTerm(text string, i int) (string, int, error) {
	j := i
	for j < len(text) {
		switch {
		case text[j] == '"':
			k := j + 1
			for k < len(text) && text[k] != '"' {
				k++
			}
			if k >= len(text) {
				return "", 0, &ParseError{Offset: j, Msg: "unterminated quote"}
			}
			j = k + 1
		case isSpace(text[j]):
			return text[i:j], j, nil
		default:
			j++
		}
	}

	return text[i:j], j, nil
}

// parseTerm splits a read term into its field and value at the first colon that is not inside
// quotes, then reads the value's shape.
func parseTerm(raw string, offset int) (Term, error) {
	term := Term{Field: FieldTag, Offset: offset, ValueOffset: offset}

	colon := -1
	inQuotes := false
	for k := 0; k < len(raw) && colon < 0; k++ {
		switch raw[k] {
		case '"':
			inQuotes = !inQuotes
		case ':':
			if !inQuotes {
				colon = k
			}
		}
	}

	value := raw
	if colon >= 0 {
		field := Field(raw[:colon])
		if !field.known() {
			return Term{}, &ParseError{
				Offset:     offset,
				Msg:        fmt.Sprintf("unknown field %q", string(field)),
				Suggestion: nearestField(string(field)),
			}
		}
		term.Field = field
		value = raw[colon+1:]
		term.ValueOffset = offset + colon + 1
	}

	// A sort's leading dash is the same statement as the term's: `sort:-date` and `-sort:date`
	// both mean "newest first", and a person writes both. The kind of value where a leading
	// dash could mean anything else (a negative number) puts the dash after its operator, so
	// this is unambiguous for exactly the field that has an order to negate. The canonical form
	// is the term form, which is what Render emits.
	if term.Field == FieldSort && strings.HasPrefix(value, "-") {
		term.Negated = true
		value = value[1:]
		if value == "" {
			return Term{}, &ParseError{
				Offset: term.ValueOffset,
				Msg:    fmt.Sprintf("%s: needs a value", term.Field),
			}
		}
	}

	if value == "" {
		return Term{}, &ParseError{
			Offset: term.ValueOffset,
			Msg:    fmt.Sprintf("%s: needs a value", term.Field),
		}
	}

	// A quoted value: the whole value must be the quoted run, so `"a"b` is refused rather than
	// read as one of the two things it might have meant.
	if value[0] == '"' {
		if len(value) < 2 || value[len(value)-1] != '"' || strings.ContainsAny(value[1:len(value)-1], `"`) {
			return Term{}, &ParseError{Offset: term.ValueOffset, Msg: "a quoted value must be the whole value"}
		}
		term.Word = value[1 : len(value)-1]
		term.Quoted = true

		return term, nil
	}

	// A comparison. `>=` before `>` so the longer operator wins.
	for _, c := range []struct {
		prefix string
		op     Op
	}{{">=", OpGte}, {"<=", OpLte}, {">", OpGt}, {"<", OpLt}} {
		if strings.HasPrefix(value, c.prefix) {
			rest := value[len(c.prefix):]
			if rest == "" {
				return Term{}, &ParseError{
					Offset: term.ValueOffset,
					Msg:    fmt.Sprintf("%s:%s needs a value", term.Field, c.prefix),
				}
			}
			term.Op = c.op
			term.Word = rest

			return term, nil
		}
	}

	// A range. Both sides empty (`..`) is refused: it looks like a filter and is not one.
	if at := strings.Index(value, ".."); at >= 0 {
		from, to := value[:at], value[at+2:]
		if from == "" && to == "" {
			return Term{}, &ParseError{Offset: term.ValueOffset, Msg: "a range needs at least one side"}
		}
		term.Range = &Range{From: from, To: to}

		return term, nil
	}

	term.Word = value

	return term, nil
}

// Render is the canonical text of an expression: what a saved search stores and what a UI shows
// in place of what the user typed. It is the inverse of Parse in the only sense that matters --
// parsing its output yields the same expression.
func Render(expr *Expr) string {
	if expr == nil {
		return ""
	}

	parts := make([]string, 0, len(expr.Terms))
	for _, t := range expr.Terms {
		var b strings.Builder
		if t.Negated {
			b.WriteString("-")
		}
		// A tag term is the bare form: `tag:anal` and `anal` are the same query, and the
		// canonical one is the one a person typed.
		if !t.IsTag() {
			b.WriteString(string(t.Field))
			b.WriteString(":")
		}
		switch {
		case t.Range != nil:
			b.WriteString(t.Range.String())
		case t.Op != OpEq:
			b.WriteString(t.Op.String())
			b.WriteString(quoteValue(t.Word))
		default:
			b.WriteString(quoteValue(t.Word))
		}
		parts = append(parts, b.String())
	}

	return strings.Join(parts, " ")
}

// quoteValue quotes a value that would not survive a round trip bare: an empty one, one with
// whitespace, one with a colon (which a reader would take for a field), or one with a quote.
func quoteValue(v string) string {
	if v == "" || strings.ContainsAny(v, " \t\n\r:\"") {
		return `"` + v + `"`
	}

	return v
}

// Equal compares two expressions for the same QUERY by comparing their canonical forms, so
// `tag:anal` equals `anal` -- they are the same query, and Source (what the user happened to
// type) is not part of it. It is what the round-trip test asserts with.
func Equal(a, b *Expr) bool {
	if a == nil || b == nil {
		return a == b
	}

	return Render(a) == Render(b)
}

// nearestField is the suggestion for an unknown field: the closest known name within a third of
// its length. A suggestion that is further away than that is noise, so none is offered.
func nearestField(name string) string {
	best, bestDist := "", -1
	candidates := make([]string, 0, len(knownFields))
	for _, f := range knownFields {
		candidates = append(candidates, string(f))
	}
	sort.Strings(candidates)

	for _, f := range candidates {
		d := editDistance(name, f)
		if bestDist < 0 || d < bestDist {
			best, bestDist = f, d
		}
	}
	if bestDist <= 0 || bestDist > len(name)/3+1 {
		return ""
	}

	return best
}

// editDistance is Levenshtein, iterative, two rows. Small enough to keep here rather than
// taking a dependency for one suggestion.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, min(cur[j-1]+1, prev[j-1]+cost))
		}
		prev, cur = cur, prev
	}

	return prev[len(b)]
}

func min(a, b int) int {
	if a < b {
		return a
	}

	return b
}
