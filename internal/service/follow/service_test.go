package follow

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The rules that do not need a database: which levels exist, that a user cannot follow themselves,
// and that an unknown level is refused rather than defaulted.
//
// The Service is built with nil queries on purpose -- every case here returns before a query runs,
// so a nil handle is also a check that the validation happens FIRST. If a case ever starts
// needing the database this test panics instead of silently reaching for one.

func nilService() *Service { return New(nil, nil) }

func TestValidVisibility(t *testing.T) {
	for _, level := range []string{VisibilityPrivate, VisibilityFollowers, VisibilityPublic} {
		assert.True(t, ValidVisibility(level), "%q is a level the feed understands", level)
	}

	// The ones a caller could plausibly send. 'followers' is the stored spelling and 'FOLLOWERS'
	// is the GraphQL enum: the service takes the STORED spelling, and a resolver that passes the
	// enum straight through must fail here rather than at the CHECK constraint, where the message
	// would be a constraint name.
	for _, level := range []string{"", "FOLLOWERS", "Public", "friends", "all", "private "} {
		assert.False(t, ValidVisibility(level), "%q is not a level the feed understands", level)
	}
}

func TestSetVisibilityRefusesAnUnknownLevel(t *testing.T) {
	err := nilService().SetVisibility(t.Context(), uuid.Must(uuid.NewV4()), uuid.Must(uuid.NewV4()), "FOLLOWERS")
	require.ErrorIs(t, err, ErrUnknownVisibility,
		"a typo'd level must be refused rather than stored: silently becoming 'private' looks "+
			"like the feature is broken and silently becoming 'public' is the leak this column "+
			"exists to prevent")
}

func TestFollowRefusesSelfFollow(t *testing.T) {
	id := uuid.Must(uuid.NewV4())
	err := nilService().Follow(t.Context(), id, id)
	require.ErrorIs(t, err, ErrSelfFollow)
}

func TestVisibilityConstantsMatchTheSchema(t *testing.T) {
	// The strings the service compares against are the strings in migration 115's CHECK. Written
	// as an assertion so a rename here fails a test rather than a user's write.
	assert.Equal(t, "private", VisibilityPrivate)
	assert.Equal(t, "followers", VisibilityFollowers)
	assert.Equal(t, "public", VisibilityPublic)
}
