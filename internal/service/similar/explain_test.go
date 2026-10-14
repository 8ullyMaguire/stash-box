package similar

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The scorer, isolated from the database.
//
// This is where the WEIGHTS are decidable. The integration test proves the queries find the
// evidence; this file proves what the evidence is worth, which is a different claim and the
// one a reader of a recommendation actually cares about. Both matter: a query that finds
// nothing and a score that ignores what it found look identical from outside.

func tag(name string) uuid.UUID {
	// Named tags need stable ids that differ: derived from the name so a test can build a
	// map without a registry, and stable so two runs agree.
	var u uuid.UUID
	copy(u[:], name)
	return u
}

func TestScoreWeighsEachKindOfEvidence(t *testing.T) {
	ev := evidence{
		tags: map[uuid.UUID]tagEvidence{
			tag("Same"):     {tagID: tag("Same"), tagName: "Same", depth: 0},
			tag("Narrower"): {tagID: tag("Narrower"), tagName: "Narrower", rootName: "Root", direction: "down", depth: 1},
			tag("Broader"):  {tagID: tag("Broader"), tagName: "Broader", rootName: "Root", direction: "up", depth: 1},
		},
		performers:    map[uuid.UUID]string{tag("perf"): "Ana"},
		sameStudio:    true,
		studioName:    "Studio A",
		durationDelta: intPtrFixture(0),
	}

	reasons, got := score(ev, 3, 1, true, true)

	// The subject offers 3 tags (9.0) + 1 performer (5.0) + a studio (2.0) + a duration
	// (2.0) = 18.0. The candidate earns 3.0 + 1.5 + 1.0 + 5.0 + 2.0 + 2.0 = 14.5.
	require.Len(t, reasons, 6)
	assert.InDelta(t, 14.5/18.0, got, 1e-9,
		"the score is what the candidate earned over what the subject offers")

	byKind := make(map[ReasonKind]Reason, len(reasons))
	for _, r := range reasons {
		byKind[r.Kind] = r
	}

	assert.InDelta(t, 3.0, byKind[ReasonSameTag].Weight, 1e-9, "the same tag is the fact itself")
	assert.Equal(t, "Same", byKind[ReasonSameTag].Label)
	assert.Empty(t, byKind[ReasonSameTag].Via, "an exact match has nothing in between to name")

	assert.InDelta(t, 1.5, byKind[ReasonNarrowerTag].Weight, 1e-9,
		"a specific instance of a subject claim is strong but half an exact match: it is a claim in one direction")
	assert.Equal(t, "Root", byKind[ReasonNarrowerTag].Via,
		"the reason must name the subject's tag, or a reader cannot check the implication")

	assert.InDelta(t, 1.0, byKind[ReasonBroaderTag].Weight, 1e-9,
		"a family relative is the weakest tag evidence and is named rather than hidden")

	assert.InDelta(t, 5.0, byKind[ReasonSharedPerformer].Weight, 1e-9,
		"a person in common is the strongest fact the archive records")
	assert.Equal(t, "Ana", byKind[ReasonSharedPerformer].Label)

	assert.InDelta(t, 2.0, byKind[ReasonSameStudio].Weight, 1e-9)
	assert.Equal(t, "Studio A", byKind[ReasonSameStudio].Label)

	assert.InDelta(t, 2.0, byKind[ReasonSimilarDuration].Weight, 1e-9,
		"identical runtimes earn the whole term")
}

func TestScoreOrdersReasonsStrongestKindFirst(t *testing.T) {
	ev := evidence{
		tags: map[uuid.UUID]tagEvidence{
			tag("Zeta broad"):  {tagName: "Zeta broad", rootName: "R", direction: "up", depth: 1},
			tag("Alpha same"):  {tagName: "Alpha same", depth: 0},
			tag("Beta narrow"): {tagName: "Beta narrow", rootName: "R", direction: "down", depth: 1},
		},
		performers: map[uuid.UUID]string{tag("p1"): "Zoe", tag("p2"): "Ada"},
	}

	reasons, _ := score(ev, 3, 2, false, false)

	kinds := make([]ReasonKind, 0, len(reasons))
	for _, r := range reasons {
		kinds = append(kinds, r.Kind)
	}
	assert.Equal(t, []ReasonKind{
		ReasonSameTag,
		ReasonNarrowerTag,
		ReasonBroaderTag,
		ReasonSharedPerformer,
		ReasonSharedPerformer,
	}, kinds, "exact, then an instance, then a relative, then the people")

	// Two performers, both named, in name order: an unordered reason list makes two
	// identical responses differ.
	assert.Equal(t, "Ada", reasons[3].Label)
	assert.Equal(t, "Zoe", reasons[4].Label)
}

func TestScoreTreatsAnUnknownDurationAsNoEvidence(t *testing.T) {
	ev := evidence{tags: map[uuid.UUID]tagEvidence{tag("T"): {tagName: "T"}}}

	// The subject HAS a duration, the pair's delta is unknown: the term is simply absent.
	reasons, withUnknown := score(ev, 1, 0, false, true)
	for _, r := range reasons {
		assert.NotEqual(t, ReasonSimilarDuration, r.Kind,
			"an unknown runtime is not evidence of a similar one")
	}
	assert.InDelta(t, 3.0/5.0, withUnknown, 1e-9)

	// And the subject having NO duration does not lower the bar either: the term is not in
	// the denominator it cannot be earned against.
	_, noDurationOffered := score(ev, 1, 0, false, false)
	assert.InDelta(t, 3.0/3.0, noDurationOffered, 1e-9,
		"a scene with no runtime cannot offer a runtime term")
}

func TestScoreDecaysTheDurationTermToNothing(t *testing.T) {
	cases := []struct {
		name      string
		delta     int
		wantTerm  float64
		wantAReas bool
	}{
		{"identical", 0, 2.0, true},
		{"a quarter of the half-width apart", 450, 1.5, true},
		{"five minutes apart", 300, 2.0 * (1.0 - 300.0/1800.0), true},
		{"exactly the half-width apart", 1800, 0, false},
		{"an hour apart", 3600, 0, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := evidence{tags: map[uuid.UUID]tagEvidence{tag("T"): {tagName: "T"}}, durationDelta: &tc.delta}
			reasons, _ := score(ev, 1, 0, false, true)

			var term float64
			found := false
			for _, r := range reasons {
				if r.Kind == ReasonSimilarDuration {
					term = r.Weight
					found = true
				}
			}
			assert.Equal(t, tc.wantAReas, found,
				"a runtime this far from the subject's is not a reason to recommend anything")
			if found {
				assert.InDelta(t, tc.wantTerm, term, 1e-9)
			}
		})
	}
}

func TestScoreWithoutAnythingToOfferIsZeroNotADivisionByZero(t *testing.T) {
	// Unreachable through FindSimilarScenes, whose candidate predicate requires a shared
	// axis -- but this runs on a read path, and a panic there is an outage rather than a
	// wrong number.
	reasons, got := score(evidence{}, 0, 0, false, false)
	assert.Equal(t, 0.0, got)
	assert.Empty(t, reasons)
}

func TestTagRankOrdersTheThreeKinds(t *testing.T) {
	assert.Equal(t, 0, tagRank(tagEvidence{depth: 0, direction: "down"}),
		"an exact tag is the strongest")
	assert.Equal(t, 1, tagRank(tagEvidence{depth: 1, direction: "down"}),
		"then an instance of a subject claim")
	assert.Equal(t, 2, tagRank(tagEvidence{depth: 3, direction: "up"}),
		"then a family relative, however deep")
	// A tag matched in both directions -- impossible while the DAG refuses cycles, and the
	// `up` arm starts at depth 1 -- but the rank has to decide rather than fall through.
	// Depth zero is an exact match whichever arm produced it, so it ranks with the exact
	// tags: the id is the same tag, and the reason for arriving at it is not the claim.
	assert.Equal(t, 0, tagRank(tagEvidence{depth: 0, direction: "up"}))
}

func TestBetterExplanationPrefersTheShallowestThenTheDownwardDirection(t *testing.T) {
	exact := tagEvidence{tagName: "X", depth: 0, direction: "down"}
	instance := tagEvidence{tagName: "X", depth: 1, direction: "down", rootName: "Root"}
	relative := tagEvidence{tagName: "X", depth: 1, direction: "up", rootName: "Root"}

	assert.True(t, betterExplanation(exact, instance))
	assert.True(t, betterExplanation(instance, relative),
		"at the same depth, 'a kind of X' names something checkable in both scenes")
	assert.False(t, betterExplanation(relative, instance))
	assert.False(t, betterExplanation(exact, exact))

	// Two roots at the same depth: the name decides, so the choice is stable.
	assert.True(t, betterExplanation(
		tagEvidence{depth: 1, direction: "down", rootName: "Alpha"},
		tagEvidence{depth: 1, direction: "down", rootName: "Beta"}))
}

func TestCandidatePoolSizeNeverReturnsNothingAndIsBounded(t *testing.T) {
	assert.Equal(t, candidateFloor, candidatePoolSize(0))
	assert.Equal(t, 1*candidateSlack+candidateFloor, candidatePoolSize(1),
		"a sidebar wants the best ONE, which means seeing more than one")
	assert.Equal(t, 10*candidateSlack+candidateFloor, candidatePoolSize(10))
	assert.Equal(t, maxCandidates, candidatePoolSize(100000),
		"a pool is a closure walk per candidate; it has to have a ceiling")
}

func TestEvidenceIsEmptyOnlyWhenNothingWasObserved(t *testing.T) {
	assert.True(t, evidence{}.isEmpty())
	assert.False(t, evidence{tags: map[uuid.UUID]tagEvidence{tag("T"): {}}}.isEmpty())
	assert.False(t, evidence{performers: map[uuid.UUID]string{tag("p"): "Ana"}}.isEmpty())
	assert.False(t, evidence{sameStudio: true}.isEmpty())
	// A duration delta ALONE is not evidence: the candidate predicate never selects on
	// runtime, so a pair whose only observation is a similar length shares nothing at all.
	assert.True(t, evidence{durationDelta: intPtrFixture(0)}.isEmpty())
}

// intPtrFixture is the local pointer helper: this file is in the service's own package,
// where the api package's intPtr is not in scope.
func intPtrFixture(v int) *int { return &v }
