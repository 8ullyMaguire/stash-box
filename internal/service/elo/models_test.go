package elo

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
)

// The pure parts of the service: the validation and key-scheme rules that decide
// what gets stored, tested without a database.
//
// The database-backed behaviour -- that a vote moves both ratings inside one
// transaction -- is in the integration test, because a unit test with a mocked
// queries package would only prove that the mock was called, which is the test
// that passes forever regardless of whether the service works.

func TestEntityTypeValidRejectsUnknownTypes(t *testing.T) {
	for _, valid := range AllEntityTypes {
		assert.True(t, valid.Valid(), "%q is in AllEntityTypes so it must be valid", valid)
	}

	// The interesting case is a type that LOOKS plausible. An empty string is the
	// one that would actually reach here from a missing GraphQL argument, and a
	// typo like "performers" is the one a careless migration would write.
	for _, bogus := range []EntityType{"", "performers", "Performer", "PERFORMER", "instance "} {
		assert.False(t, bogus.Valid(),
			"%q must be rejected: a vote naming an unknown type would be recorded "+
				"and then silently fail to move any rating", bogus)
	}
}

// Every type in AllEntityTypes must have a distinct taste prefix, or two kinds
// of entity would share a key space and a performer's votes would bleed into the
// studio preferences of the same uuid.
func TestEveryEntityTypeHasADistinctTastePrefix(t *testing.T) {
	seen := map[string]EntityType{}
	for _, kind := range AllEntityTypes {
		prefix := prefixFor(kind)
		other, dup := seen[prefix]
		assert.False(t, dup,
			"%q and %q both use taste prefix %q, so their preferences would share "+
				"a key space", kind, other, prefix)
		seen[prefix] = kind
	}
}

// The key must be parseable back to its parts, because a recommender will need to
// turn "performer:<uuid>" into a lookup. Encoding without a documented separator
// and no way back is a schema that cannot be queried.
func TestTasteKeyRoundTrips(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	key := tasteKey(EntityPerformer, id)

	assert.Equal(t, TasteKeyPerformer+":"+id.String(), key)

	prefix, entityID, ok := splitTasteKey(key)
	assert.True(t, ok, "a key this service wrote must be splittable")
	assert.Equal(t, TasteKeyPerformer, prefix)
	assert.Equal(t, id, entityID)
}

func TestSplitTasteKeyRejectsMalformedKeys(t *testing.T) {
	for _, bad := range []string{"", "performer", "performer:", ":" + uuid.Must(uuid.NewV7()).String(), "notauuid:x"} {
		_, _, ok := splitTasteKey(bad)
		assert.False(t, ok, "%q must not parse as a taste key", bad)
	}
}

// PickedSide must reject anything but 0 and 1, because it is cast straight into a
// smallint column. A value of 7 would store fine and then mean nothing, and the
// display-order contract -- which slot did the user see first -- is the only
// defence against position bias being measurable.
func TestPickedSideValidRejectsOutOfRange(t *testing.T) {
	assert.True(t, PickedLeft.Valid())
	assert.True(t, PickedRight.Valid())
	for _, bad := range []PickedSide{-1, 2, 7, 1000} {
		assert.False(t, bad.Valid(), "picked side %d must be rejected", bad)
	}
}
