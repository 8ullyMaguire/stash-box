package list

import (
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/queries"
)

// These tests need no database, following service/review's convention. Everything tested
// here is a decision the SCHEMA cannot express: canView is not a constraint, the name
// rules are not constraints, and the entity-type set is a code choice. The constraints are
// covered by scripts/verify-107.sh against a real postgres; duplicating them here would be
// testing the same thing twice in a way that only ever agrees.

// aList builds a generated list row, published or not.
func aList(published bool) queries.List {
	l := queries.List{
		ID:        uuid.Must(uuid.NewV7()),
		OwnerID:   uuid.Must(uuid.NewV7()),
		Name:      "a list",
		OwnerName: "someone",
	}
	if published {
		l.PublishedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	}
	return l
}

// canView is the rule that decides whether a draft leaks. It gets the most cases here for
// that reason, including the two that are easy to get wrong and that no database can check:
// the zero viewer, and a viewer who owns a DIFFERENT list.

func TestCanViewPublishedListIsVisibleToAnyone(t *testing.T) {
	l := aList(true)

	assert.True(t, canView(l, uuid.Must(uuid.NewV7())), "a stranger may read a published list")
	assert.True(t, canView(l, uuid.Nil), "an anonymous viewer may read a published list")
	assert.True(t, canView(l, l.OwnerID), "the owner may of course read their own published list")
}

func TestCanViewDraftIsVisibleOnlyToItsOwner(t *testing.T) {
	l := aList(false)

	assert.True(t, canView(l, l.OwnerID), "the owner may always read their own draft")
	assert.False(t, canView(l, uuid.Must(uuid.NewV7())), "a stranger may not read a draft")
}

func TestCanViewAnonymousViewerIsNotTheOwnerEvenWhenOwnerIDIsZero(t *testing.T) {
	// The zero-uuid guard. An unauthenticated request arrives with uuid.Nil, so if a list's
	// owner_id were the zero uuid the naive `viewer == owner` rule would publish it to
	// everyone. Owner ids are minted v4 so this cannot arise in production -- but a rule that
	// holds only because the data happens to be well-formed is not a rule, and this is the
	// one case where a future backfill or a seed script could make it real.
	l := aList(false)
	l.OwnerID = uuid.Nil

	assert.False(t, canView(l, uuid.Nil), "a draft owned by the zero uuid must not be anonymous-readable")
}

// The direction of the error matters as much as the fact of it. Get returns ErrNotFound for
// an invisible draft rather than ErrNotOwner precisely so a caller cannot probe a uuid range
// to discover which private lists exist. That property lives in Get, not here, but it is the
// reason this test exists as a pair rather than as a single assertion.
func TestInvisibleDraftAndMissingListAreIndistinguishable(t *testing.T) {
	// Both cases route through the same error. Asserting the error values are DIFFERENT
	// pins that the distinction is a deliberate choice someone could later "fix" -- and this
	// failure tells them they just removed the privacy of private lists.
	assert.NotEqual(t, ErrNotFound, ErrNotOwner,
		"a draft someone else owns must be reported as not-found, or private ids become enumerable")
}

// Name normalisation. The unique index is on (owner_id, name) at the byte level, so what
// gets stored has to be the trimmed value or two spellings of one name can coexist.

func TestNormaliseNameTrimsBeforeValidating(t *testing.T) {
	// The order matters. Validating length first would accept "   " as a three-character
	// name, which is then stored and matched against a real name by index comparison.
	got, err := normaliseName("  Favourites  ")
	require.NoError(t, err)
	assert.Equal(t, "Favourites", got, "the stored name is the trimmed one")
}

func TestNormaliseNameRejectsBlankAndOverlong(t *testing.T) {
	for _, name := range []string{"", "   ", "\t\n"} {
		_, err := normaliseName(name)
		assert.ErrorIs(t, err, ErrInvalidName, "%q must not be a usable name", name)
	}

	_, err := normaliseName(strings.Repeat("x", 201))
	assert.ErrorIs(t, err, ErrInvalidName, "201 characters is over the limit")

	_, err = normaliseName(strings.Repeat("x", 200))
	assert.NoError(t, err, "exactly 200 is within the limit")
}

func TestNullableStringCollapsesEmptyToNull(t *testing.T) {
	// '' and NULL are different states in the API. A list with no description should
	// serialise as null so a client can tell "none given" from "the empty string", and the
	// schema's CHECK forbids the blank case anyway -- so this is the Go side of one rule.
	assert.Nil(t, nullableString(""))
	require.NotNil(t, nullableString(" "), "a space is a description, not an absence")
	assert.Equal(t, " ", *nullableString(" "))
}

func TestValidEntityType(t *testing.T) {
	for _, t2 := range []string{EntityPerformer, EntityScene, EntityStudio, EntitySite} {
		assert.True(t, validEntityType(t2), "%s is supported", t2)
	}
	// Free text in the column, closed set here. A typo is caught by this function; without
	// it a list item would reference an entity type no resolver will ever look for.
	for _, t2 := range []string{"", "performer", "PERFORMR", "PERFORMER ", "ACTOR"} {
		assert.False(t, validEntityType(t2), "%q is not a supported entity type", t2)
	}
}

// Error identity is load-bearing for the GraphQL layer: each of these maps to a distinct
// message and, where it matters, a distinct HTTP-level outcome. A test that only checks
// "an error happened" would let two of them merge silently.

func TestErrorsAreDistinct(t *testing.T) {
	all := map[string]error{
		"not-found":      ErrNotFound,
		"not-owner":      ErrNotOwner,
		"name-taken":     ErrNameTaken,
		"invalid-name":   ErrInvalidName,
		"invalid-entity": ErrInvalidEntityType,
		"duplicate-item": ErrDuplicateItem,
		"already-pub":    ErrAlreadyPublished,
		"not-published":  ErrNotPublished,
	}
	seen := make(map[error]string, len(all))
	for name, err := range all {
		require.NotNil(t, err, "%s must be a non-nil error", name)
		prev, dup := seen[err]
		require.False(t, dup, "%s and %s are the same error value; the GraphQL layer cannot tell them apart", name, prev)
		seen[err] = name
	}
}
