package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/models"
)

// The enum mapping, both directions, without a database: the database's vocabulary is lower case and
// the API's is not, which is the mismatch that produces a silently EMPTY status if the mapping is a
// cast instead of a switch.
func TestContentSourceStatusMapping(t *testing.T) {
	resolver := &contentSourceResolver{}

	for _, tt := range []struct {
		column string
		want   models.ContentSourceStatus
	}{
		{"unknown", models.ContentSourceStatusUnknown},
		{"live", models.ContentSourceStatusLive},
		{"intermittent", models.ContentSourceStatusIntermittent},
		{"dead", models.ContentSourceStatusDead},
		{"dmca", models.ContentSourceStatusDmca},
		{"archived", models.ContentSourceStatusArchived},
	} {
		t.Run(tt.column, func(t *testing.T) {
			got, err := resolver.Status(t.Context(), &models.ContentSource{URL: "https://x.invalid/", Status: tt.column})
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.NotEmpty(t, string(got), "an empty enum value is what a cast would produce")
		})
	}
}

// A status the API has no name for FAILS the read. The alternative -- passing it through, or calling it
// UNKNOWN -- would tell a client either something it cannot switch on or something untrue: UNKNOWN means
// "nothing has checked this URL", not "this instance does not understand the answer".
func TestContentSourceStatusRefusesAStatusItCannotName(t *testing.T) {
	resolver := &contentSourceResolver{}

	for _, unknown := range []string{"", "LIVE", "provisional", "Dead"} {
		got, err := resolver.Status(t.Context(), &models.ContentSource{URL: "https://x.invalid/", Status: unknown})
		require.Error(t, err, "%q must not be answerable", unknown)
		assert.Empty(t, got)
		assert.Contains(t, err.Error(), unknown, "the error has to name the value it could not place")
	}
}
