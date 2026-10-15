package seasonal

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// The pure half of seasonal events (plan phase 6 slice 6g).
//
// All that is decided without a database is where an event sits relative to a moment, and the
// boundaries are the whole function: a window is closed at BOTH ends, so the instant it opens is
// active and the instant it closes still is.

var (
	starts = time.Date(2026, 12, 1, 12, 0, 0, 0, time.UTC)
	ends   = time.Date(2026, 12, 14, 0, 0, 0, 0, time.UTC)
	window = Event{Slug: "winter-sprint", Title: "Winter Sprint", StartsAt: starts, EndsAt: ends, Threshold: 10}
)

func TestStatusOfIsClosedAtBothEnds(t *testing.T) {
	tests := []struct {
		name string
		now  time.Time
		want Status
	}{
		{"a nanosecond before it opens it is upcoming", starts.Add(-time.Nanosecond), StatusUpcoming},
		{"THE INSTANT IT OPENS IT IS ACTIVE", starts, StatusActive},
		{"during the window it is active", starts.Add(72 * time.Hour), StatusActive},
		{"THE INSTANT IT CLOSES IT IS STILL ACTIVE", ends, StatusActive},
		{"a nanosecond after it closes it has ended", ends.Add(time.Nanosecond), StatusEnded},
		{"long after it has ended", ends.AddDate(0, 0, 400), StatusEnded},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, StatusOf(window, tc.now))
		})
	}
}

// The status says nothing about whether anything was ACHIEVED, which is the other half of the
// design: an event that has ended still reports the members who reached it, because nothing takes
// credit away.
func TestStatusIsIndependentOfAchievement(t *testing.T) {
	ended := StatusOf(window, ends.AddDate(0, 0, 30))
	assert.Equal(t, StatusEnded, ended)

	achieved := Progress{Event: window, Status: ended, Contributions: 12, Reached: true}
	assert.True(t, achieved.Reached, "an event being over does not undo a member's work")

	missed := Progress{Event: window, Status: ended, Contributions: 3, Reached: false}
	assert.False(t, missed.Reached, "and it does not invent credit either")
}
