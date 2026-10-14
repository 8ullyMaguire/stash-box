package search

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/models"
)

// Resolver turns the NAMES in an expression into ids. It is an interface so that the language
// stays testable without a database, and so that the one place a name becomes an id is explicit
// (spec section 3.1). A name that is not found is (uuid.Nil, false, nil) -- not an error: a
// filter naming a tag nobody has is a legitimate query with an empty answer.
type Resolver interface {
	TagID(ctx context.Context, name string) (uuid.UUID, bool, error)
	PerformerID(ctx context.Context, name string) (uuid.UUID, bool, error)
	StudioID(ctx context.Context, name string) (uuid.UUID, bool, error)
}

// DiagnosticCode classifies what Lower could not apply, so a caller can style or filter it
// without parsing English.
type DiagnosticCode string

const (
	DiagUnknownName       DiagnosticCode = "unknown_name"
	DiagUnknownFieldValue DiagnosticCode = "unknown_field_value"
	DiagNotYet            DiagnosticCode = "not_yet"
	DiagUnsupported       DiagnosticCode = "unsupported"
	DiagParseError        DiagnosticCode = "parse_error"
)

// DiagnosticFromParseError turns a parse failure into the same Diagnostic shape a lowering
// failure takes, so a client renders ONE list of reasons rather than two kinds of failure with
// different shapes. The term is not always known (an unterminated quote is about the whole
// remainder), and the offset is what a client highlights with.
func DiagnosticFromParseError(e *ParseError) Diagnostic {
	msg := e.Msg
	if e.Suggestion != "" {
		msg += fmt.Sprintf(" (did you mean %q?)", e.Suggestion)
	}

	return Diagnostic{Code: DiagParseError, Term: e.Term, Offset: e.Offset, Message: msg}
}

// Diagnostic is a term that could not be applied, with where it is. It is not an error: the
// caller shows it next to the result set, which is what makes a mistyped field recoverable
// instead of mysterious.
type Diagnostic struct {
	Code    DiagnosticCode
	Term    string
	Offset  int
	Message string
}

// Query is a lowered expression: the filter the existing query builders already understand, plus
// the diagnostics for what it could not apply.
//
// NoMatch says the answer is EMPTY BY CONSTRUCTION: some term asked for something that could not
// be applied faithfully, so the query as written has no answers. The alternative -- dropping the
// term -- returns a larger set than the person asked for, presented as if it were the answer. A
// wrong list is worse than an empty one with a reason next to it.
type Query struct {
	Filter      models.SceneQueryInput
	Diagnostics []Diagnostic
	NoMatch     bool
}

// contentKinds is every content kind the programme will carry, and which of them exist today.
// Naming the ones phase 3 adds is what lets the diagnostic say "not yet" instead of "unknown".
var contentKinds = map[string]bool{
	"scene": true, "image": false, "gallery": false, "text": false, "audio": false, "comic": false,
}

// Lower merges an expression into `base` -- the filter a caller already had, with its paging and
// defaults -- and reports what it could not apply.
//
// The language WINS where it speaks: a `sort:` term overrides the base's sort, and a field the
// expression mentions replaces the base's value rather than ANDing with it, because a person who
// types a query expects their query and not their query plus the previous page's filters.
func Lower(ctx context.Context, expr *Expr, base models.SceneQueryInput, resolve Resolver) (Query, error) {
	out := Query{Filter: base}
	if expr == nil {
		return out, nil
	}

	var (
		tagIncludes       []Term
		tagExcludes       []Term
		performerIncludes []Term
		performerExcludes []Term
		studioIncludes    []Term
		studioExcludes    []Term
		parentStudios     []Term
		texts             []string
	)

	diagnose := func(code DiagnosticCode, t Term, format string, args ...any) {
		out.Diagnostics = append(out.Diagnostics, Diagnostic{
			Code:    code,
			Term:    Render(&Expr{Terms: []Term{t}}),
			Offset:  t.Offset,
			Message: fmt.Sprintf(format, args...),
		})
	}

	for _, term := range expr.Terms {
		switch term.Field {
		case FieldTag:
			if term.Negated {
				tagExcludes = append(tagExcludes, term)
			} else {
				tagIncludes = append(tagIncludes, term)
			}
		case FieldText:
			texts = append(texts, term.Word)
		case FieldPerformer:
			if term.Negated {
				performerExcludes = append(performerExcludes, term)
			} else {
				performerIncludes = append(performerIncludes, term)
			}
		case FieldStudio:
			if term.Negated {
				studioExcludes = append(studioExcludes, term)
			} else {
				studioIncludes = append(studioIncludes, term)
			}
		case FieldParentStudio:
			// A studio id, not a name: the builder walks the studio tree from it
			// (scene/query.go:94), so the name is resolved like every other name.
			parentStudios = append(parentStudios, term)
		case FieldType:
			exists, known := contentKinds[strings.ToLower(term.Word)]
			switch {
			case !known:
				diagnose(DiagUnknownFieldValue, term, "unknown content type %q (known: %s)",
					term.Word, strings.Join(sortedKeys(contentKinds), ", "))
			case !exists:
				diagnose(DiagNotYet, term,
					"content type %q is phase 3; this surface answers about scenes", term.Word)
			}
		case FieldDate:
			applyDate(&out, term, diagnose)
		case FieldDuration:
			applyDuration(&out, term, diagnose)
		case FieldSort:
			applySort(&out, term, diagnose)
		case FieldScore, FieldSimilarTo, FieldPlaylist:
			when := map[Field]string{
				FieldScore:     "phase 5 (there is no community score column yet)",
				FieldSimilarTo: "slice 5 (similarity)",
				FieldPlaylist:  "slices 2-3 (lists reach search with the tag explorer)",
			}[term.Field]
			diagnose(DiagNotYet, term, "%s is not filterable yet: %s", term.Field, when)
		default:
			// Unreachable while Parse validates the field list, and kept so that adding a
			// field to the grammar without a lowering is a visible gap rather than a term that
			// silently disappears.
			diagnose(DiagUnsupported, term, "field %q has no lowering", term.Field)
		}
	}

	tagIDs, missingTags, err := lookup(ctx, tagIncludes, resolve, Resolver.TagID)
	if err != nil {
		return out, err
	}
	excludeTagIDs, missingExcludes, err := lookup(ctx, tagExcludes, resolve, Resolver.TagID)
	if err != nil {
		return out, err
	}
	performerIDs, missingPerformers, err := lookup(ctx, performerIncludes, resolve, Resolver.PerformerID)
	if err != nil {
		return out, err
	}
	studioIDs, missingStudios, err := lookup(ctx, studioIncludes, resolve, Resolver.StudioID)
	if err != nil {
		return out, err
	}
	excludePerformerIDs, missingPerformerExcludes, err := lookup(ctx, performerExcludes, resolve, Resolver.PerformerID)
	if err != nil {
		return out, err
	}
	excludeStudioIDs, missingStudioExcludes, err := lookup(ctx, studioExcludes, resolve, Resolver.StudioID)
	if err != nil {
		return out, err
	}
	parentStudioIDs, missingParentStudios, err := lookup(ctx, parentStudios, resolve, Resolver.StudioID)
	if err != nil {
		return out, err
	}

	for _, miss := range missingTags {
		diagnose(DiagUnknownName, miss, "unknown tag %q", miss.Word)
	}
	for _, miss := range missingExcludes {
		diagnose(DiagUnknownName, miss, "unknown tag %q, so the exclusion cannot be applied", miss.Word)
	}
	for _, miss := range missingPerformers {
		diagnose(DiagUnknownName, miss, "unknown performer %q", miss.Word)
	}
	for _, miss := range missingStudios {
		diagnose(DiagUnknownName, miss, "unknown studio %q", miss.Word)
	}
	for _, miss := range missingPerformerExcludes {
		diagnose(DiagUnknownName, miss, "unknown performer %q, so the exclusion cannot be applied", miss.Word)
	}
	for _, miss := range missingStudioExcludes {
		diagnose(DiagUnknownName, miss, "unknown studio %q, so the exclusion cannot be applied", miss.Word)
	}
	if len(missingParentStudios) > 0 {
		diagnose(DiagUnknownName, missingParentStudios[0], "unknown studio %q", missingParentStudios[0].Word)
	}

	if len(tagIDs) > 0 {
		out.Filter.Tags = &models.MultiIDCriterionInput{
			Modifier: models.CriterionModifierIncludes,
			Value:    tagIDs,
		}
	}
	if len(excludeTagIDs) > 0 {
		// A criterion carries ONE modifier, which is why an exclusion needs its own field
		// (`exclude_tags`) rather than a second modifier on `Tags`: "tag A and not tag B" -- the
		// draft's own headline example -- cannot be held in one criterion. The modifier is set
		// here rather than read from the term, because the FIELD is what says it excludes.
		out.Filter.ExcludeTags = &models.MultiIDCriterionInput{
			Modifier: models.CriterionModifierExcludes,
			Value:    excludeTagIDs,
		}
	}

	if len(parentStudioIDs) > 0 {
		id := parentStudioIDs[0].String()
		out.Filter.ParentStudio = &id
	}

	// The mixed case, which the existing inputs cannot hold: one criterion, one modifier.
	if len(performerIDs) > 0 && len(excludePerformerIDs) > 0 {
		diagnose(DiagUnsupported, performerIncludes[0],
			"including and excluding performers in one query needs the criterion to carry both, which it cannot yet")
	}
	if len(studioIDs) > 0 && len(excludeStudioIDs) > 0 {
		diagnose(DiagUnsupported, studioIncludes[0],
			"including and excluding studios in one query needs the criterion to carry both, which it cannot yet")
	}
	switch {
	case len(performerIDs) > 0:
		out.Filter.Performers = &models.MultiIDCriterionInput{
			Modifier: models.CriterionModifierIncludes,
			Value:    performerIDs,
		}
	case len(excludePerformerIDs) > 0:
		out.Filter.Performers = &models.MultiIDCriterionInput{
			Modifier: models.CriterionModifierExcludes,
			Value:    excludePerformerIDs,
		}
	}
	switch {
	case len(studioIDs) > 0:
		out.Filter.Studios = &models.MultiIDCriterionInput{
			Modifier: models.CriterionModifierIncludes,
			Value:    studioIDs,
		}
	case len(excludeStudioIDs) > 0:
		out.Filter.Studios = &models.MultiIDCriterionInput{
			Modifier: models.CriterionModifierExcludes,
			Value:    excludeStudioIDs,
		}
	}

	if len(texts) > 0 {
		// One string field, so repeated `text:` terms are joined rather than one winning: the
		// person asked for all of those words, and BM25 is the thing that knows what to do with
		// them together.
		joined := strings.Join(texts, " ")
		out.Filter.Text = &joined
	}

	// Every diagnostic above means a term that could not be applied as written, so the query as
	// written has no answers. Said once, here, rather than dropping the term and answering a
	// different question.
	out.NoMatch = len(out.Diagnostics) > 0

	return out, nil
}

// lookup resolves terms in order and returns the ids it found together with the terms it did
// not. Keeping the TERMS rather than the names is what lets a diagnostic point at the offset of
// the term the person has to edit, and it is the bug the first cut of this file had: skipping an
// unresolved name made the ids and the names unequal in a way the caller then read as "the
// tail did not resolve".
func lookup(
	ctx context.Context, terms []Term, resolve Resolver,
	fn func(Resolver, context.Context, string) (uuid.UUID, bool, error),
) (ids []uuid.UUID, missing []Term, err error) {
	for _, term := range terms {
		id, ok, err := fn(resolve, ctx, term.Word)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			missing = append(missing, term)

			continue
		}
		ids = append(ids, id)
	}

	return ids, missing, nil
}

func applyDate(out *Query, term Term, diagnose func(DiagnosticCode, Term, string, ...any)) {
	switch {
	case term.Range != nil:
		diagnose(DiagNotYet, term,
			"a date range is not expressible yet: the criterion carries one value and a modifier")
	case term.Op == OpEq:
		out.Filter.Date = &models.DateCriterionInput{Value: term.Word, Modifier: models.CriterionModifierEquals}
	case term.Op == OpGt:
		out.Filter.Date = &models.DateCriterionInput{Value: term.Word, Modifier: models.CriterionModifierGreaterThan}
	case term.Op == OpGte:
		// The builder has no >= arm, and using > would drop the boundary date. Said out loud
		// rather than quietly answering a different question.
		diagnose(DiagNotYet, term, "the date criterion has no 'at or after' arm; write date:> instead")
	case term.Op == OpLt:
		out.Filter.Date = &models.DateCriterionInput{Value: term.Word, Modifier: models.CriterionModifierLessThan}
	case term.Op == OpLte:
		diagnose(DiagNotYet, term, "the date criterion has no 'at or before' arm; write date:< instead")
	}
}

// applyDuration lowers a duration comparison onto scenes.duration, in SECONDS.
//
// A range is refused rather than approximated, and so is `>=`: IntCriterionInput carries ONE
// value and a modifier, so `duration:60..600` cannot be said. Answering it with `>60` would
// return everything longer than a minute while looking like it had honoured the upper bound --
// more rows than were asked for, presented as the answer.
func applyDuration(out *Query, term Term, diagnose func(DiagnosticCode, Term, string, ...any)) {
	switch {
	case term.Range != nil:
		diagnose(DiagNotYet, term,
			"a duration range is not expressible yet: the criterion carries one value, so write duration:> or duration:<")

		return
	case term.Op == OpGte, term.Op == OpLte:
		diagnose(DiagNotYet, term,
			"the duration criterion has no 'or equal' arm; write duration:> or duration:< instead")

		return
	}

	seconds, err := strconv.Atoi(term.Word)
	if err != nil {
		diagnose(DiagUnknownFieldValue, term, "duration is a number of seconds, and %q is not a number", term.Word)

		return
	}

	modifier := models.CriterionModifierEquals
	switch term.Op {
	case OpGt:
		modifier = models.CriterionModifierGreaterThan
	case OpLt:
		modifier = models.CriterionModifierLessThan
	case OpEq, OpGte, OpLte:
	}

	out.Filter.Duration = &models.IntCriterionInput{Value: seconds, Modifier: modifier}
}

var sortNames = map[string]models.SceneSortEnum{
	"title":      models.SceneSortEnumTitle,
	"date":       models.SceneSortEnumDate,
	"duration":   models.SceneSortEnumDuration,
	"trending":   models.SceneSortEnumTrending,
	"popularity": models.SceneSortEnumPopularity,
	"created_at": models.SceneSortEnumCreatedAt,
}

func applySort(out *Query, term Term, diagnose func(DiagnosticCode, Term, string, ...any)) {
	enum, ok := sortNames[strings.ToLower(term.Word)]
	if !ok {
		diagnose(DiagUnknownFieldValue, term, "unknown sort %q (known: %s)",
			term.Word, strings.Join(sortedKeys(sortNames), ", "))

		return
	}

	// The negation of a `sort:` term is DESCENDING, not exclusion: order is the only thing a
	// sort term can mean, and `-sort:date` is how a person writes "newest first".
	out.Filter.Sort = enum
	if term.Negated {
		out.Filter.Direction = models.SortDirectionEnumDesc
	} else {
		out.Filter.Direction = models.SortDirectionEnumAsc
	}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)

	return out
}
