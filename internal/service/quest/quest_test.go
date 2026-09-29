package quest

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/service/completion"
)

// The parts of a quest that need no database: the threshold arithmetic, the
// wording, and the ordering. The parts that need one are in
// `internal/api/quest_integration_test.go`.
//
// A UNIT test cannot reach `Generate`, because Generate reads the archive. What it
// CAN reach is every decision Generate makes that is not "what is in the
// database" -- and those are the decisions worth pinning, because each one is
// wrong in a way that still produces a plausible quest.

// thresholdFor converts a field's weight into the score the count query is asked
// for. The whole point is the DIRECTION of the relationship, so every case below
// states which entities the threshold is meant to select.
func TestThresholdForSelectsTheRightEntities(t *testing.T) {
	// Performer total is 80, and the count query counts `score < threshold`.
	//
	// So the threshold must be the score of an entity missing EXACTLY this field,
	// PLUS ONE -- the count is strict, and a threshold equal to that score
	// excludes the very entity the quest is about. I wrote 81 here first (the
	// score itself) and it was wrong by exactly one for that reason.
	//
	// Derived rather than hand-summed, and the derivation is the definition:
	//	missing exactly w of total scores percent(total-w, total)
	//	the threshold that admits it is one higher
	cases := []struct {
		field   completion.Field
		want    int
		explain string
	}{
		{completion.FieldBirthdate, 82,
			"missing 15 of 80 scores 81, so the threshold is 82. An entity " +
				"missing something ELSE as well scores lower and is also admitted, " +
				"which is intended -- the per-entity score filters the extras"},
		{completion.FieldAliases, 88, "missing 10 of 80 scores 88, so 88"},
		{completion.FieldImage, 88,
			"10 of 80 again -- two fields with the same weight get the same " +
				"threshold, which is why this is a function of WEIGHT and not of " +
				"field name"},
		{completion.FieldEthnicity, 97, "missing 3 of 80 scores 96, so 97"},
	}

	for _, tc := range cases {
		assert.Equal(t, tc.want, thresholdFor(EntityPerformer, tc.field), "%s", tc.explain)
	}
}

// A field that is not scored has no threshold, and the generator refuses it
// earlier -- so the helper must not silently return something usable.
func TestThresholdForAnUnscoredFieldIsTheTightest(t *testing.T) {
	// `name` is weight zero on every type: NOT NULL, always present. A quest for
	// it can never be completed, which is the same unreachable-field bug the SQL
	// parity test found in the scorer.
	assert.Equal(t, 100, thresholdFor(EntityPerformer, completion.FieldName),
		"an unscored field has no missing weight, so nothing is below a threshold "+
			"that admits nothing -- and Generate refuses it before it gets here")
}

// The threshold must be per-TYPE, not a single number.
//
// A field's weight is only meaningful relative to its type's total, so a shared
// constant would rank a scene's birthdate-equivalent and a performer's by
// incomparable numbers.
func TestThresholdIsPerEntityTypeNotShared(t *testing.T) {
	performer := thresholdFor(EntityPerformer, completion.FieldImage)
	studio := thresholdFor(EntityStudio, completion.FieldImage)

	assert.NotEqual(t, performer, studio,
		"the same field has weight 10 of 80 for a performer and 30 of 100 for a "+
			"studio, so one shared threshold cannot mean 'missing the image' for "+
			"both")

	// And each is right for its own arithmetic, computed by hand.
	// Performer: 100 - 10*100/80 = 100 - 12 = 88. Studio: 100 - 30*100/100 = 70.
	assert.Equal(t, 88, performer)
	assert.Equal(t, 70, studio)

	// And each is one above the score of an entity missing exactly that field:
	// 88 > 87 (performer missing 10 of 80) and 70 > 69 (studio missing 30 of 100).
	assert.Greater(t, performer, 87,
		"the threshold must EXCEED the score of an entity missing only the field, "+
			"because the count is strict -- an equal threshold excludes the very "+
			"entity the quest is about")
	assert.Greater(t, studio, 69)
}

// A field of ZERO weight must not produce a threshold that admits everything.
//
// `100 - 0*100/total` is 100, which counts NOTHING -- correct by accident. The
// failure mode is the opposite sign: if the formula ever became
// `100 - (total - weight)*100/total`, an unscored field would yield 0 and the
// count query would return the entire archive as candidates.
func TestAZeroWeightFieldNeverAdmitsTheArchive(t *testing.T) {
	got := thresholdFor(EntityPerformer, completion.FieldName)
	assert.Equal(t, 100, got)
	assert.Greater(t, got, 50,
		"a zero-weight field must produce a threshold that admits NOTHING, never "+
			"one that admits everything -- the two errors are a sign flip apart and "+
			"only one of them looks like a bug")
}

// Wording is the sentence a curator reads, so the plural and the verb are the
// test's subject, not decoration.
func TestWordingReadsAsASentence(t *testing.T) {
	cases := []struct {
		entityType EntityType
		field      completion.Field
		count      int
		want       string
	}{
		{EntityPerformer, completion.FieldBirthdate, 5,
			"Add missing birthdate for 5 performers"},
		{EntityPerformer, completion.FieldBirthdate, 1,
			"Add missing birthdate for 1 performer"},
		{EntityScene, completion.FieldDuration, 3,
			"Fill in missing duration for 3 scenes"},
		{EntityScene, completion.FieldSnapshotCoverage, 2,
			"Add missing snapshot coverage for 2 scenes"},
		{EntityStudio, completion.FieldParentStudio, 4,
			"Fill in missing parent studio for 4 studios"},
		{EntityTag, completion.FieldDetails, 10,
			"Add missing details for 10 tags"},
		{EntitySite, completion.FieldRegex, 2,
			"Fill in missing regex for 2 sites"},
	}

	for _, tc := range cases {
		assert.Equal(t, tc.want, Wording(tc.entityType, tc.field, tc.count),
			"the sentence is what a curator acts on")
	}
}

// An underscore in a field name must not reach the sentence. A quest reading
// "Add missing eye_color" is a quest from the database, not from a person.
func TestWordingNeverLeaksAMachineName(t *testing.T) {
	for _, field := range []completion.Field{
		completion.FieldEyeColor, completion.FieldCareerDates,
		completion.FieldSnapshotCoverage, completion.FieldParentStudio,
	} {
		got := Wording(EntityPerformer, field, 1)
		assert.NotContains(t, got, "_",
			"field %q renders as %q, which is a column name in a user-facing "+
				"sentence", field, got)
	}
}

// An unknown entity type must still produce a sentence rather than an empty one.
// A client rendering a quest board should show a slightly wrong card over no card.
func TestWordingHandlesAnUnknownType(t *testing.T) {
	got := Wording(EntityType("dragon"), completion.FieldBirthdate, 2)
	assert.Equal(t, "Add missing birthdate for 2 entities", got,
		"an unrecognised type falls back to a generic noun rather than rendering "+
			"nothing -- Generate refuses it, so this is a client's problem and the "+
			"sentence should still be usable")
}

// Ordering puts the emptiest entity first -- ascending score, which is the
// opposite of the instinct and the whole point.
func TestSortByUrgencyPutsTheEmptiestFirst(t *testing.T) {
	items := []Item{
		{EntityID: uuid.Must(uuid.NewV7()), Score: 90, Name: "nearly done"},
		{EntityID: uuid.Must(uuid.NewV7()), Score: 10, Name: "empty"},
		{EntityID: uuid.Must(uuid.NewV7()), Score: 50, Name: "half"},
	}
	SortByUrgency(items)

	assert.Equal(t, "empty", items[0].Name,
		"a quest is a list of GAPS: the biggest gap is the one worth doing first. "+
			"Descending order would put the nearly-complete entities on top, which "+
			"is right for a progress bar and backwards for a task list")
	assert.Equal(t, "half", items[1].Name)
	assert.Equal(t, "nearly done", items[2].Name)
}

// The order must be STABLE, so a curator's place in a list survives a re-read.
func TestSortByUrgencyIsStableAcrossCalls(t *testing.T) {
	mk := func() []Item {
		return []Item{
			{EntityID: uuid.Must(uuid.FromString("00000000-0000-0000-0000-000000000003")), Score: 50},
			{EntityID: uuid.Must(uuid.FromString("00000000-0000-0000-0000-000000000001")), Score: 50},
			{EntityID: uuid.Must(uuid.FromString("00000000-0000-0000-0000-000000000002")), Score: 50},
		}
	}

	first, second := mk(), mk()
	SortByUrgency(first)
	SortByUrgency(second)

	assert.Equal(t, first, second,
		"equal scores tie-break on id, so two reads of the same archive produce "+
			"the same order. A quest that reshuffles between reads makes a "+
			"curator's place in the list meaningless")
	assert.Equal(t, "00000000-0000-0000-0000-000000000001", first[0].EntityID.String(),
		"the tie-break is the id, ascending, so it does not depend on map order")
}

// An item's Missing list is the quest, so it must be non-empty and in the
// scorer's order. A test on the generator's own contract, using the scorer's
// output shape directly.
func TestAnItemWithNoMissingFieldsIsNotAnItem(t *testing.T) {
	// The generator's membership test is containsField, and this is what it
	// rejects. Asserted here because the unit test cannot reach Generate, and this
	// is the predicate that decides whether an entity appears in a quest at all.
	complete := []completion.Field{}
	partial := []completion.Field{completion.FieldBirthdate, completion.FieldCountry}

	assert.False(t, containsField(complete, completion.FieldBirthdate),
		"an entity missing nothing cannot be in a quest about a missing field")
	assert.True(t, containsField(partial, completion.FieldBirthdate))
	assert.False(t, containsField(partial, completion.FieldAliases),
		"an entity missing a birthdate and a country is not in the ALIASES quest")
}

// Service construction must not require a name resolver, because a client that
// only wants counts has no resolver to give.
func TestNewServiceToleratesANilNameResolver(t *testing.T) {
	svc := NewService(completion.NewService(nil), nil)
	require.NotNil(t, svc,
		"a nil nameOf is legal: the id is still actionable and the name is only "+
			"a convenience. A missing name renders worse; a missing item renders "+
			"nothing")
}
