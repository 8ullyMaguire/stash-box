package completion

import (
	"fmt"
	"sort"
)

// SPEC §7.24.8: the edit form shows "this edit takes the scene from 62% to 71%", then suggests
// the next-highest-value missing field for that entity.
//
// # This is a READ, not a second score
//
// §7.24.8 is explicit: "a read of §7.7's existing completion score plus a ranking of missing
// fields by marginal gain -- not a second, competing definition of completion." So nothing here
// computes a percentage or holds a weight. Every number comes from Score, WeightFor and
// TotalWeight, and the ranking is a sort over WeightFor's answers.
//
// That is worth stating because the tempting implementation is to keep a parallel "priority"
// list here -- a field marked important for the suggestion and weighted differently for the
// score. The two would agree until the first weight was tuned, and then the edit form would
// promise a gain the completion bar does not deliver.
//
// # Why the sort is not just Result.Missing
//
// Result.Missing is in WEIGHT-LIST DECLARATION ORDER, because it is built by one pass over the
// spec. For performers that happens to be roughly by importance, so the first entry looks
// right; for a type whose list is not ordered that way, "the first missing field" is an
// accident of where somebody typed the line. So the ranking sorts by weight explicitly.
//
// Ties break on the field name, not on map or slice iteration order. Without that the
// suggestion flickers between two equally valuable fields on every render, which reads as a
// bug in the UI and is impossible to reproduce from a bug report.

// NextField returns the highest-weight field that is missing, in descending weight order.
//
// The whole slice is returned rather than just the first element, because the edit form shows a
// suggestion and a curator who dismisses it needs the next one -- and because a caller
// rendering a "next up" list should not have to re-sort.
//
// Returns nil (not an error) when the entity is complete: "nothing to suggest" is a normal
// answer to this question. An unknown entity type IS an error, because "I do not know this
// type" and "this entity is complete" must not look the same at the call site -- the first is a
// bug and the second is the normal end state.
func NextField(entityType EntityType, present map[Field]bool) ([]Field, error) {
	fields, err := Fields(entityType)
	if err != nil {
		return nil, err
	}

	type rankedField struct {
		field  Field
		weight int
	}
	var candidates []rankedField
	for _, f := range fields {
		if present[f] {
			continue
		}
		w, err := WeightFor(entityType, f)
		if err != nil {
			return nil, err
		}
		if w == 0 {
			// Always-expected fields (the name) are not counted either way, so they
			// cannot be "the next highest-value thing to fill in".
			continue
		}
		candidates = append(candidates, rankedField{field: f, weight: w})
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].weight != candidates[j].weight {
			return candidates[i].weight > candidates[j].weight
		}
		return candidates[i].field < candidates[j].field
	})

	// NIL, not an empty slice, when nothing is missing. Both are "len == 0" to a caller,
	// but a JSON-marshalled API returns `[]` for one and `null` for the other, and the edit
	// form renders "no suggestions" as a hidden section for one and an empty list for the
	// other. Found by TestNextFieldExcludesFieldsAlreadyPresent, which asserts nil.
	if len(candidates) == 0 {
		return nil, nil
	}

	out := make([]Field, len(candidates))
	for i, c := range candidates {
		out[i] = c.field
	}
	return out, nil
}

// Delta is a completion preview for one field being filled in.
type Delta struct {
	FromPercent  int
	ToPercent    int
	DeltaPercent int
	// Next is what to suggest after this edit: the highest-weight field still
	// missing once this one is filled. Nil when nothing else is missing.
	Next []Field
}

// Preview answers §7.24.8's "this edit takes the scene from 62% to 71%".
//
// Both percentages come from Score, so the preview and the completion bar cannot disagree --
// which is the whole point of §7.24.8 refusing a second definition of completion.
//
// An unknown FIELD for this entity type is an error rather than a zero delta. A caller passing
// FieldRegex for a scene has a bug, and returning "0% gain" would let it render a preview
// instead of surfacing it. The converse -- a field that is already present -- is NOT an error
// and previews as a genuine zero, because a curator re-saving an unchanged field is ordinary
// and telling them nothing is correct.
func Preview(entityType EntityType, present map[Field]bool, filling Field) (*Delta, error) {
	if !filling.ValidFor(entityType) {
		return nil, fmt.Errorf("%w: field %q is not scored for %q", ErrUnknownField, filling, entityType)
	}

	before, err := Score(entityType, present)
	if err != nil {
		return nil, err
	}

	after := make(map[Field]bool, len(present)+1)
	for k, v := range present {
		after[k] = v
	}
	after[filling] = true

	afterResult, err := Score(entityType, after)
	if err != nil {
		return nil, err
	}

	next, err := NextField(entityType, after)
	if err != nil {
		return nil, err
	}

	return &Delta{
		FromPercent:  before.Score,
		ToPercent:    afterResult.Score,
		DeltaPercent: afterResult.Score - before.Score,
		Next:         next,
	}, nil
}
