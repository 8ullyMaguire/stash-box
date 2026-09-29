package completion

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The formula, tested against numbers a person worked out by hand.
//
// THE TRAP THIS FILE EXISTS TO AVOID: asserting `Score(...) == expected` where
// `expected` came from running the same function is a tautology. It passes for
// every formula, including a wrong one. So every number below is a fraction
// written out longhand from the weight table, and a change to a weight has to be
// a deliberate edit to arithmetic somebody can check.

// allPresent is every scored field for a type, for the "perfect entity" case.
func allPresent(t *testing.T, entityType EntityType) map[Field]bool {
	t.Helper()
	fields, err := Fields(entityType)
	require.NoError(t, err)
	present := map[Field]bool{}
	for _, f := range fields {
		present[f] = true
	}
	return present
}

// A performer with everything is 100. Trivially true, and the baseline every
// other case is checked against: if THIS is not 100, the denominator is wrong and
// every other number in this file is meaningless.
func TestAFullyPopulatedEntityScores100(t *testing.T) {
	for _, entityType := range AllEntityTypes {
		result, err := Score(entityType, allPresent(t, entityType))
		require.NoError(t, err, "%s", entityType)

		assert.Equal(t, 100, result.Score,
			"%s with every field present is complete; a lower number means the "+
				"denominator is counting a field nothing can satisfy", entityType)
		assert.Empty(t, result.Missing,
			"%s with everything present has nothing missing", entityType)
		assert.Equal(t, result.Total, result.Earned)
	}
}

// A performer with NOTHING is 0.
//
// The case that catches a denominator built from the wrong list, and the one that
// matters most in practice: a zero here is a performer the archive knows nothing
// about, which is the majority of an incomplete archive.
func TestAnEmptyEntityScoresZero(t *testing.T) {
	for _, entityType := range AllEntityTypes {
		result, err := Score(entityType, map[Field]bool{})
		require.NoError(t, err, "%s", entityType)

		assert.Equal(t, 0, result.Score, "%s with no fields at all is 0%%", entityType)

		// Every missing field is a REAL field for this type, and nothing outside
		// the type's list ever appears. This holds whatever the weight convention
		// is, which is the point: an earlier version of this test counted the
		// missing entries and re-encoded "the name is weight zero" here, which is
		// a hand-written copy of the thing under test -- and it was wrong for
		// tags, where the name is scored.
		assertMissingFieldsAreValidFor(t, entityType, result.Missing)
		assert.NotEmpty(t, result.Missing,
			"%s: an entity with nothing filled has gaps, or the score is 0 by "+
				"accident of an empty denominator", entityType)
	}
}

// Hand-computed: a performer with ONLY a birthdate.
//
// Performer weights: name 0, aliases 10, birthdate 15, gender 5, ethnicity 3,
// country 8, eye 2, hair 2, height 5, measurements 5, career 5, urls 10,
// image 10, details 10. Total = 90.
//
// 15/90 = 0.1666... -> 17. The +total/2 rounding in percent() is what makes this
// 17 rather than 16.
func TestPerformerWithOnlyABirthdateIsSeventeen(t *testing.T) {
	result, err := Score(EntityPerformer, map[Field]bool{
		FieldBirthdate: true,
	})
	require.NoError(t, err)

	assert.Equal(t, 90, result.Total, "the hand-summed total of the performer weights")
	assert.Equal(t, 15, result.Earned)
	assert.Equal(t, 17, result.Score, "15/90 rounds to 17")
	assert.Equal(t, []Field{
		FieldAliases, FieldGender, FieldEthnicity, FieldCountry,
		FieldEyeColor, FieldHairColor, FieldHeight, FieldMeasurements,
		FieldCareerDates, FieldURLs, FieldImage, FieldDetails,
	}, result.Missing,
		"the missing list is in WEIGHT order, not alphabetical: the most valuable "+
			"gap comes first so a quest built from it reads as a priority list")
}

// Hand-computed: a scene with duration, studio and performers.
//
// Scene weights: name 0, duration 20, studio 15, performers 20, date 5, urls 18,
// tags 7, image 5, details 10, snapshots 10. Total = 110.
//
// 55/110 = 0.5 -> 50 exactly. The exact case, chosen because a rounding bug shows
// up here as 49 or 51 and a weighting bug shows up as a number nowhere near 50.
//
// The 110 is worth a note, because it is a COINCIDENCE that it did not change. The
// list used to have separate `site 10` and `urls 8` fields, which is 18, and
// collapsing them into one `urls 18` leaves the total identical. So this test kept
// passing through a change to the list that should have been visible here -- the
// assertion is true and the arithmetic above it is now correct, but for a week the
// test could not have told anyone the list had changed. A test that passes across a
// change to the thing it describes is passing for the wrong reason, and the fix is
// to check the list, not just the total.
func TestSceneWithItsIdentifyingFieldsIsFifty(t *testing.T) {
	result, err := Score(EntityScene, map[Field]bool{
		FieldDuration:   true,
		FieldStudio:     true,
		FieldPerformers: true,
	})
	require.NoError(t, err)

	assert.Equal(t, 110, result.Total, "the hand-summed total of the scene weights")
	assert.Equal(t, 55, result.Earned)
	assert.Equal(t, 50, result.Score, "55/110 is exactly half")
}

// THE case §7.7's own example makes: "link 10 unlinked scenes". A scene with
// performers but no studio cannot be traced to a producer, and that gap is worth
// more than a missing tag.
func TestAStudioIsWorthMoreThanATagOnAScene(t *testing.T) {
	withStudio, err := Score(EntityScene, map[Field]bool{FieldStudio: true})
	require.NoError(t, err)
	withTag, err := Score(EntityScene, map[Field]bool{FieldTags: true})
	require.NoError(t, err)

	assert.Greater(t, withStudio.Score, withTag.Score,
		"a scene with no studio cannot be traced to a producer, while a scene with "+
			"no tag can still be found and curated; scoring them equally would "+
			"rank an untagged scene alongside an untraceable one and send curators "+
			"at the wrong gap")
}

// Snapshot coverage is scored as a whole, not per frame. A scene with 3 of the 12
// frames a collage needs is not meaningfully more complete than one with none.
func TestSnapshotCoverageIsOneFieldNotAPerFrameScore(t *testing.T) {
	fields := mustFields(t, EntityScene)

	count := 0
	for _, f := range fields {
		if f == FieldSnapshotCoverag {
			count++
		}
	}
	assert.Equal(t, 1, count,
		"snapshot coverage is a single scored field; a per-frame score would make "+
			"a barely-started collage look nearly finished, which would put those "+
			"scenes at the BOTTOM of a quest queue when they are the ones most "+
			"needing snapshots")
}

// An unmentioned field counts as ABSENT, not as present.
//
// This is the default that matters: a caller that forgets to check a field must
// produce an entity that looks incomplete, because the cost of each false "present"
// is a curator sent to fix something already fixed.
func TestAnUnmentionedFieldIsMissing(t *testing.T) {
	// Explicitly false, and absent entirely, must behave the same way.
	explicitlyFalse, err := Score(EntityPerformer, map[Field]bool{
		FieldBirthdate: false,
	})
	require.NoError(t, err)
	notMentioned, err := Score(EntityPerformer, map[Field]bool{})
	require.NoError(t, err)

	assert.Equal(t, explicitlyFalse.Missing, notMentioned.Missing,
		"a field the caller did not mention is missing, and defaulting it to "+
			"present would make an incomplete caller report a complete entity")
	assert.Equal(t, explicitlyFalse.Score, notMentioned.Score)
}

// An unknown type is an error naming the type, not a zero score. A silent 0 for an
// unknown type would render as "this entity is completely undocumented" rather
// than "this code does not know how to score it".
func TestAnUnknownTypeIsRefusedByName(t *testing.T) {
	_, err := Score(EntityType("galaxy"), map[Field]bool{FieldName: true})

	require.ErrorIs(t, err, ErrUnknownEntityType)
	assert.Contains(t, err.Error(), "galaxy",
		"the error must name what it was given, or a caller cannot tell a typo "+
			"from a type added in a later version")

	_, err = TotalWeight(EntityType("galaxy"))
	assert.ErrorIs(t, err, ErrUnknownEntityType)
	_, err = Fields(EntityType("galaxy"))
	assert.ErrorIs(t, err, ErrUnknownEntityType)
}

// TotalWeight and Fields must agree with each other and with Score, because a
// caller rendering "30 of 100" from one and the score from the other is showing
// two numbers computed from two lists.
func TestTotalWeightAndFieldsDescribeTheSameScore(t *testing.T) {
	for _, entityType := range AllEntityTypes {
		total, err := TotalWeight(entityType)
		require.NoError(t, err)
		fields, err := Fields(entityType)
		require.NoError(t, err)

		result, err := Score(entityType, map[Field]bool{})
		require.NoError(t, err)

		assert.Equal(t, total, result.Total,
			"%s: TotalWeight and Score must agree", entityType)
		assert.Greater(t, total, 0,
			"%s: a type with no weight at all would divide by zero", entityType)
		assert.NotEmpty(t, fields)
		assertMissingFieldsAreValidFor(t, entityType, result.Missing)
	}
}

// ValidFor is what stops a caller naming a field the type does not score. If it
// diverged from the weight list, a quest could be phrased for a field that
// contributes nothing to the score it is chasing.
func TestValidForMatchesTheWeightList(t *testing.T) {
	for _, entityType := range AllEntityTypes {
		fields, err := Fields(entityType)
		require.NoError(t, err)
		for _, f := range fields {
			assert.True(t, f.ValidFor(entityType),
				"%s is in %s's list so it must be valid for it", f, entityType)
		}
	}

	// Cross-type: a scene field is not a studio field.
	assert.False(t, FieldPerformers.ValidFor(EntityStudio),
		"a scene has performers and a studio does not; a quest naming performers "+
			"for a studio would be phrased for a field that cannot be filled")
	assert.False(t, FieldDuration.ValidFor(EntityPerformer))
	assert.False(t, FieldPerformers.ValidFor(EntityType("galaxy")),
		"an unknown type validates nothing, rather than everything")
}

// The score is deterministic: the same input gives the same number every time.
//
// Stated as a test because a completion score that differed between two readers
// of the same row would be a bug report nobody could reproduce, and the natural
// way to cause it is to start ranging over a map somewhere in the formula.
func TestScoringIsDeterministic(t *testing.T) {
	present := map[Field]bool{
		FieldBirthdate: true,
		FieldCountry:   true,
		FieldURLs:      true,
		FieldImage:     true,
		FieldDetails:   true,
	}

	first, err := Score(EntityPerformer, present)
	require.NoError(t, err)
	for range 20 {
		again, err := Score(EntityPerformer, present)
		require.NoError(t, err)

		assert.Equal(t, first.Score, again.Score)
		assert.Equal(t, first.Missing, again.Missing,
			"the missing list order is part of the contract, not incidental")
	}
}

// Rounding is half-up, and the boundary is pinned.
//
// (earned*100 + total/2) / total rounds .5 up. A scene at exactly half is 50 (see
// the scene test); the point here is that the SAME rule gives 17 for 15/90 and
// would give 16 for a truncating implementation.
func TestRoundingIsHalfUp(t *testing.T) {
	// 15/90 = 16.67 -> 17. A truncating implementation gives 16, so this single
	// assertion separates the two.
	result, err := Score(EntityPerformer, map[Field]bool{FieldBirthdate: true})
	require.NoError(t, err)
	assert.Equal(t, 17, result.Score, "16.67 must round UP to 17, not truncate to 16")
}

func mustFields(t *testing.T, entityType EntityType) []Field {
	t.Helper()
	fields, err := Fields(entityType)
	require.NoError(t, err)
	return fields
}

// assertMissingFieldsAreValidFor checks every missing field belongs to the type.
//
// Subset, not equality: the assertion that matters is that a caller can render
// each missing field as a quest without hitting a field the entity does not have.
// Whether a zero-weight field appears in the list is a scoring convention, not
// something a caller can observe incorrectly.
func assertMissingFieldsAreValidFor(t *testing.T, entityType EntityType, missing []Field) {
	t.Helper()
	for _, f := range missing {
		assert.True(t, f.ValidFor(entityType),
			"%s is not a field of a %s, so a quest naming it would ask a curator "+
				"to fill in something the entity does not have", f, entityType)
	}
}

// A rounding mutation SURVIVES, and the reason is worth writing down rather than
// papering over.
//
// Removing the half-up term -- `(earned*100 + total/2) / total` -- is caught, by
// the 15/90 case. Subtracting one more, `(earned*100 + total/2 - 1) / total`, is
// NOT caught, and the same is true of other perturbations near a boundary: the
// per-type totals (90, 110, 60, 90, 100) are all even, so `total/2` lands exactly
// on the midpoint for every one of them, and a +/-1 there moves nothing for any
// earned value.
//
// Two ways to close it, and the honest reason neither is in this file:
//
//   - Assert a score for EVERY (earned, total) pair, which means asserting 90x90
//     numbers computed by hand. That is not a better test; it is a second,
//     unreadable copy of the formula, and the trap this file exists to avoid is
//     exactly that kind of copy.
//   - Change the rounding to use a divisor the tests do not divide evenly, which
//     would mean changing a WEIGHT to make a test pass. The weights are the
//     product decision, not the test's to move.
//
// So the coverage claim is precise: the FORMULA (which fields count, in what
// order, at what weight) is mutation-checked and the rounding is checked at its
// own boundary. A one-unit perturbation in the rounding's tie-breaker is
// unobservable through this interface, and that is a property of the interface
// rather than a gap someone should go fix.
func TestTheRoundingSurvivorIsNamed(t *testing.T) {
	// The claim the comment above makes is about the NUMBERS, so it is asserted
	// rather than left in prose: if a weight ever changes and a total becomes
	// odd, the rounding becomes observable through this interface and the
	// comment above needs rewriting.
	for _, entityType := range AllEntityTypes {
		total, err := TotalWeight(entityType)
		require.NoError(t, err)
		assert.Zero(t, total%2,
			"%s: an even total is why a one-unit rounding perturbation is "+
				"invisible; an odd total would make it testable", entityType)
	}
}

// The scene's field list, asserted as a set.
//
// Added because a hand-written total did NOT change when the list did -- see the
// note above -- so the total is not sufficient evidence that the list is what this
// file says it is. A scene's score is a claim about what a curator can fix, and the
// claim is wrong in a way a number cannot show if the number happens to be stable.
//
// This is a hand-written copy of the list, which is normally the trap. It is
// written here DELIBERATELY and for a reason that makes it load-bearing: the
// alternative was a test that passed across a real change to the scoring model,
// which is strictly worse. The list is short, it changes rarely, and the failure
// mode of this test going stale is a failing test rather than a silently wrong
// score.
func TestSceneFieldsAreTheOnesTheSchemaSupports(t *testing.T) {
	got := mustFields(t, EntityScene)

	assert.Equal(t, []Field{
		FieldName,
		FieldDuration,
		FieldStudio,
		FieldPerformers,
		FieldDate,
		// ONE urls field, not a separate site and urls pair. scene_urls is
		// (scene_id, site_id, url), so a linked url IS a site link; scoring both
		// would credit a single URL twice and inflate every scene's score by a
		// field nobody can fill on its own.
		FieldURLs,
		FieldTags,
		FieldImage,
		FieldDetails,
		FieldSnapshotCoverag,
	}, got,
		"the scene's scored fields are a claim about what a curator can fix, and "+
			"a field the schema has no way to fill makes that claim false. The "+
			"weight TOTAL stayed 110 across the site/urls collapse, so the total "+
			"alone could not catch it -- this can")
}
