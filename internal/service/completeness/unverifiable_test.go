package completeness

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service/content"
)

// Which statuses mean "the evidence is gone".
//
// The two that must NOT are the point of the test: an absent record (the ordinary case -- most
// source pages are not URLs this instance aggregates) and `unknown` (bytes nobody has looked at
// yet) are both "no evidence either way". Reporting either as a lost source would tell a curator
// their claim is unverifiable when nothing has ever checked it.
func TestUnverifiable(t *testing.T) {
	for _, tt := range []struct {
		status string
		want   bool
	}{
		{content.StatusDead, true},
		{content.StatusDMCA, true},
		{content.StatusArchived, true},

		{content.StatusLive, false},
		{content.StatusIntermittent, false},
		{content.StatusUnknown, false},
		// The zero value of the field, which is what a total with no health record carries.
		{"", false},
	} {
		t.Run(tt.status, func(t *testing.T) {
			total := Total{Source: &models.ContentSource{Status: tt.status}}
			assert.Equal(t, tt.want, total.Unverifiable())
			assert.Equal(t, tt.status, total.SourceStatus())
		})
	}

	t.Run("no record at all", func(t *testing.T) {
		var total Total
		assert.Nil(t, total.Source)
		assert.Equal(t, "", total.SourceStatus())
		assert.False(t, total.Unverifiable())
	})
}
