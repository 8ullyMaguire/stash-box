package leaderboard

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// The pure half of the leaderboard (plan phase 6 slice 6f).
//
// The ranking comes from a query and is proven against a database next door. What is decided here is the
// shape of a request: the two clamps, and the day the window starts on.

func TestTheBoardShapeIsClamped(t *testing.T) {
	t.Run("the window", func(t *testing.T) {
		assert.Equal(t, DefaultDays, clampDays(0), "an omitted window is a month")
		assert.Equal(t, DefaultDays, clampDays(-7), "a negative window is an omitted one")
		assert.Equal(t, 1, clampDays(1))
		assert.Equal(t, MaxDays, clampDays(MaxDays))
		assert.Equal(t, MaxDays, clampDays(3650), "ten years is clamped, not refused")
	})

	t.Run("the page size", func(t *testing.T) {
		assert.Equal(t, DefaultLimit, clampLimit(0), "an omitted limit is a page")
		assert.Equal(t, DefaultLimit, clampLimit(-25))
		assert.Equal(t, 1, clampLimit(1))
		assert.Equal(t, MaxLimit, clampLimit(MaxLimit))
		assert.Equal(t, MaxLimit, clampLimit(100000),
			"a board is a page, not an export: the per-row user lookup is why this is bounded")
	})
}

// THE WINDOW INCLUDES TODAY, which is the same off-by-one the heatmap carries: a one-day window is today
// alone, so a board that says "today" is not empty on the day it is asked.
//
// This helper is a deliberate second copy of the heatmap's, in another package, and the duplication is
// recorded rather than hidden: both copies have their own test and both are three lines of arithmetic
// whose rule is written down in each. A shared helper would be better and is a refactor for another day.
func TestTheBoardWindowIncludesToday(t *testing.T) {
	zone := time.FixedZone("DB", 2*60*60)
	noon := time.Date(2026, 6, 15, 12, 34, 56, 0, zone)

	assert.Equal(t, time.Date(2026, 6, 15, 0, 0, 0, 0, zone), sinceFor(noon, 1), "a one-day board is today")
	assert.Equal(t, time.Date(2026, 6, 9, 0, 0, 0, 0, zone), sinceFor(noon, 7), "a week ends on today")

	// A month boundary: the arithmetic is on the DATE, not on a division by 24 hours.
	assert.Equal(t, time.Date(2026, 2, 28, 0, 0, 0, 0, zone), sinceFor(time.Date(2026, 3, 1, 9, 0, 0, 0, zone), 2))

	start := sinceFor(noon, 30)
	assert.Equal(t, zone, start.Location(), "and it stays in the frame it was given")
}
