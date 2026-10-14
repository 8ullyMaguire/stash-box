package streak

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// The pure half of the contribution heatmap (plan phase 6 slice 6e).
//
// The counts come from a query and are proven against a database next door. What is decided here is the
// WINDOW: how long it is, and which day it starts on -- which is the part with an off-by-one in it.

// The clamp, including the case that matters on the wire: an absent argument is not a request for an
// empty grid.
func TestTheHeatmapWindowIsClamped(tt *testing.T) {
	tests := []struct {
		name string
		days int
		want int
	}{
		{"an omitted window is a year", 0, HeatmapDefaultDays},
		{"a negative window is an omitted one", -30, HeatmapDefaultDays},
		{"a single day is honoured", 1, 1},
		{"the default is honoured exactly", HeatmapDefaultDays, HeatmapDefaultDays},
		{"the maximum is honoured", HeatmapMaxDays, HeatmapMaxDays},
		{"a leap year fits", 366, 366},
		{"ten years is clamped, not refused", 3650, HeatmapMaxDays},
	}
	for _, tc := range tests {
		tt.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, clampHeatmapDays(tc.days))
		})
	}
}

// THE WINDOW INCLUDES TODAY, which is the off-by-one: a window of 1 is today alone, and a window of N
// starts N-1 days back, not N.
func TestTheHeatmapWindowIncludesToday(t *testing.T) {
	// Midday so the truncation has something to do, and a fixed zone so the assertion is about the
	// arithmetic rather than about where the test machine is.
	zone := time.FixedZone("DB", 2*60*60)
	noon := time.Date(2026, 6, 15, 12, 34, 56, 0, zone)

	start := heatmapSince(noon, 1)
	assert.Equal(t, time.Date(2026, 6, 15, 0, 0, 0, 0, zone), start, "a one-day window starts today")

	start = heatmapSince(noon, 7)
	assert.Equal(t, time.Date(2026, 6, 9, 0, 0, 0, 0, zone), start,
		"a week ends on today: seven days wide, inclusive of both ends")

	// A month boundary, because the arithmetic is on the DATE and a division by 24 hours would be wrong
	// across a daylight-saving change.
	start = heatmapSince(time.Date(2026, 3, 1, 9, 0, 0, 0, zone), 2)
	assert.Equal(t, time.Date(2026, 2, 28, 0, 0, 0, 0, zone), start)

	// THE ZONE IS THE DATABASE'S, AND STAYING IN IT IS THE POINT: the caller passes a day already
	// truncated in that frame, and re-zoning it here would move the boundary -- the same class of bug
	// the activity-days query documents at length.
	assert.Equal(t, zone, start.Location(), "the window stays in the frame it was given")
}
