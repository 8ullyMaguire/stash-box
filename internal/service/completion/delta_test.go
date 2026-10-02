package completion

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SPEC §7.24.8: the edit form shows "this edit takes the scene from 62% to 71%", then suggests
// the next-highest-value missing field for that entity.
//
// The ranking is a read of §7.7's existing completion score plus a marginal-gain ordering — NOT
// a second, competing definition of completion. The test that enforces that is the one that
// matters most in this file: it compares the ranking against the weights Score already uses, so
// a second opinion cannot creep in.

// The ranking must be exactly Score's weights, in descending order. Not a similar order, not a
// re-derived one: the same numbers.
func TestNextFieldRanksByTheSameWeightsScoreUses(t *testing.T) {
	for _, entityType := range AllEntityTypes {
		t.Run(string(entityType), func(t *testing.T) {
			fields, err := Fields(entityType)
			require.NoError(t, err)

			// Nothing present: every field is a candidate, so the ranking is a total order
			// over the whole weight list.
			got, err := NextField(entityType, map[Field]bool{})
			require.NoError(t, err)

			// Derive the expectation from the scorer, independently of the ranking code.
			var want []Field
			for _, f := range fields {
				w, err := WeightFor(entityType, f)
				require.NoError(t, err)
				if w == 0 {
					continue // a zero weight cannot be the "next highest value"
				}
				want = append(want, f)
			}
			sort.SliceStable(want, func(i, j int) bool {
				wi, _ := WeightFor(entityType, want[i])
				wj, _ := WeightFor(entityType, want[j])
				if wi != wj {
					return wi > wj
				}
				// A tie must break DETERMINISTICALLY, or the suggestion flickers
				// between two equally-valuable fields on every render.
				return want[i] < want[j]
			})

			assert.Equal(t, want, got,
				"§7.24.8 forbids a second definition of completion: the ranking must be "+
					"Score's own weights, not a re-derivation of them")
		})
	}
}

// The single most important property: a field the scorer does not weight can never be
// suggested. §7.24.1's write path already refused to author a quest against an unreachable gap
// for the same reason -- suggesting work nobody can complete is worse than suggesting nothing.
func TestNextFieldNeverSuggestsAFieldScoreDoesNotWeight(t *testing.T) {
	for _, entityType := range AllEntityTypes {
		fields, err := Fields(entityType)
		require.NoError(t, err)
		valid := map[Field]bool{}
		for _, f := range fields {
			valid[f] = true
		}

		got, err := NextField(entityType, map[Field]bool{})
		require.NoError(t, err)
		for _, f := range got {
			assert.True(t, valid[f] && f.ValidFor(entityType),
				"%s suggested field %q, which Score does not weight for %s -- a curator "+
					"could never complete it", entityType, f, entityType)
		}
	}
}

// The suggestion must EXCLUDE what is already there. Ranking a present field first is the
// obvious implementation bug: the ranking is computed from the weight list, and the weight list
// does not know what the caller already has.
func TestNextFieldExcludesFieldsAlreadyPresent(t *testing.T) {
	for _, entityType := range AllEntityTypes {
		fields, err := Fields(entityType)
		require.NoError(t, err)

		// Present everything except one, and ask for that one back.
		var target Field
		for _, f := range fields {
			w, err := WeightFor(entityType, f)
			require.NoError(t, err)
			if w > 0 {
				target = f
				break
			}
		}
		require.NotEmpty(t, target, "every entity type has at least one weighted field")

		present := map[Field]bool{}
		for _, f := range fields {
			present[f] = true
		}
		present[target] = true // nothing is missing

		got, err := NextField(entityType, present)
		require.NoError(t, err)
		assert.Nil(t, got,
			"%s suggested %q when every weighted field was already present", entityType, target)

		delete(present, target)
		got, err = NextField(entityType, present)
		require.NoError(t, err)
		assert.Equal(t, []Field{target}, got,
			"%s did not suggest the one missing field", entityType)
	}
}

// A fully complete entity and an empty one must not get the same answer, and neither must a
// nil map: callers pass whatever they have, and `nil` is a perfectly ordinary Go value that a
// range over its keys handles correctly but a len()==0 check might not.
func TestNextFieldHandlesAnEmptyEntityAndANilMap(t *testing.T) {
	scene := EntityScene

	got, err := NextField(scene, map[Field]bool{})
	require.NoError(t, err)
	assert.NotEmpty(t, got, "an empty scene should be told what to fill in first")

	gotNil, err := NextField(scene, nil)
	require.NoError(t, err)
	assert.NotEmpty(t, gotNil,
		"a nil present-map was treated as \"nothing missing\", which is the opposite of its "+
			"meaning: nil means the caller knows nothing, so everything is a candidate")
}

// An unknown entity type is an error, not an empty suggestion. Returning nil for "I do not
// know this type" and nil for "this entity is complete" makes the two indistinguishable at
// the call site, and the caller will render an empty suggestion for a bug.
func TestNextFieldRejectsAnUnknownEntityType(t *testing.T) {
	got, err := NextField(EntityType("perfromer"), map[Field]bool{}) // the classic typo
	assert.Error(t, err,
		"a misspelled entity type was accepted, so a typo would silently produce no suggestion")
	assert.Nil(t, got)
}

// The preview itself: "this edit takes the scene from 62% to 71%". The delta must equal
// Score(after) - Score(before), computed by the same scorer the completion bar uses — not a
// re-derived percentage.
func TestCompletionDeltaIsTheScorersOwnDifference(t *testing.T) {
	scene := EntityScene

	before, err := Score(scene, map[Field]bool{FieldName: true})
	require.NoError(t, err)
	after, err := Score(scene, map[Field]bool{FieldName: true, FieldDuration: true})
	require.NoError(t, err)

	d, err := Preview(scene, map[Field]bool{FieldName: true}, FieldDuration)
	require.NoError(t, err)
	assert.Equal(t, before.Score, d.FromPercent)
	assert.Equal(t, after.Score, d.ToPercent)
	assert.Equal(t, after.Score-before.Score, d.DeltaPercent)

	// And the gain is exactly the filled field's weight, over the total — which is what
	// makes the preview predictable to a curator.
	total, err := TotalWeight(scene)
	require.NoError(t, err)
	w, err := WeightFor(scene, FieldDuration)
	require.NoError(t, err)
	assert.Equal(t, w*100/total, d.DeltaPercent,
		"filling one field of weight %d out of %d is a gain of %d%%, and the preview says %d",
		w, total, w*100/total, d.DeltaPercent)
}

// A delta of zero is a real answer (the field was already filled) and must be allowed through.
// A negative delta is NOT: §7.24.8 is a preview of an edit that ADDS a field, so a preview that
// claims an edit will reduce completion is the scorer being misused, and refusing it is better
// than showing a curator a bar moving backwards.
func TestCompletionDeltaRefusesToPreviewAnEditThatLosesCompletion(t *testing.T) {
	scene := EntityScene

	d, err := Preview(scene, map[Field]bool{}, FieldRegex)
	assert.Error(t, err,
		"`regex` is not a scene field, so this preview would have compared two unrelated "+
			"entity types and produced a meaningless delta")
	assert.Nil(t, d)
}

// Filling an ALREADY-filled field gains nothing, and saying so is the honest answer. This is
// the case a naive implementation gets wrong by assuming every field in the edit is a gain.
func TestFillingAnAlreadyPresentFieldPreviewsNoGain(t *testing.T) {
	scene := EntityScene
	d, err := Preview(scene, map[Field]bool{FieldDuration: true}, FieldDuration)
	require.NoError(t, err)
	assert.Equal(t, 0, d.DeltaPercent,
		"re-filling a field that is already set previews as a gain, which would promise a "+
			"curator points for work that changes nothing")
}
