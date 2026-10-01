package sdbimport

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/converter"
	"github.com/stashapp/stash-box/internal/models"
)

// This is the check the whole feature rests on, made against the REAL converter
// rather than against a test-local stand-in. Every other test in this package
// asserts on the update INPUT; this one asserts on what the converter actually
// does with it, which is the only place the never-clear rule can be violated.
//
// If someone changes UpdatePerformerFromUpdateInput to assign unconditionally,
// this fails even though every input-shape test still passes.
func TestSparseInputClearsNothingInTheRealConverter(t *testing.T) {
	stored := &models.Performer{
		ID:             newID(t),
		Name:           "Jane Doe",
		Country:        ptr("US"),
		Disambiguation: ptr("Jane (I)"),
		Height:         ptr(172),
	}

	// An update that carries ONLY a name -- the most ordinary edit there is.
	in := models.PerformerUpdateInput{ID: stored.ID, Name: ptr("Jane Doe")}
	converter.UpdatePerformerFromUpdateInput(stored, in)

	assert.Equal(t, "US", *stored.Country, "country must survive an unrelated edit")
	assert.Equal(t, "Jane (I)", *stored.Disambiguation, "disambiguation must survive")
	require.NotNil(t, stored.Height)
	assert.Equal(t, 172, *stored.Height, "height must survive")
}

// And the inverse: the field the input DOES carry must be written. A guard that
// ignores everything would pass the test above.
func TestSparseInputStillWritesTheFieldItCarries(t *testing.T) {
	stored := &models.Performer{ID: newID(t), Name: "Jane Doe", Country: ptr("US")}

	in := models.PerformerUpdateInput{ID: stored.ID, Country: ptr("CA")}
	converter.UpdatePerformerFromUpdateInput(stored, in)

	require.NotNil(t, stored.Country)
	assert.Equal(t, "CA", *stored.Country)
}

// A curator's manual edit through the UI must still be able to blank a field.
// The sync forbids clears by never SENDING an empty pointer, not by making the
// field unwritable -- and the mechanism is worth being precise about, because
// it is not what the first version of this test assumed.
//
// The converter assigns the input pointer through, so an explicit &"" yields
// &"" (a present, empty value), not NULL. Both are "cleared" as far as a reader
// is concerned, but only one is NULL in the column, and the distinction decides
// whether a later run of the sync sees the field as absent (leave it) or as
// present-but-empty (write it). That is why nonEmpty is enforced at the sender
// rather than left to the converter.
func TestConverterStillAllowsExplicitClear(t *testing.T) {
	stored := &models.Performer{ID: newID(t), Name: "Jane Doe", Country: ptr("US")}

	empty := ""
	in := models.PerformerUpdateInput{ID: stored.ID, Country: &empty}
	converter.UpdatePerformerFromUpdateInput(stored, in)

	assert.NotNil(t, stored.Country, "an explicit empty string is still written")
	assert.Equal(t, "", *stored.Country,
		"the converter stores a present-but-empty value rather than NULL")
}
