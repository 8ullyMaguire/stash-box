package content

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// The cadence, as a table. The two halves move the interval in OPPOSITE directions and both are
// policy, so every boundary is here: the base, each doubling, the cap, the halving, and the floor.
func TestCheckInterval(t *testing.T) {
	const day = 24 * time.Hour

	tests := []struct {
		name      string
		failures  int
		claimants int
		want      time.Duration
	}{
		{"a quiet reliable source is looked at daily", 0, 1, day},
		{"one failure doubles it", 1, 1, 2 * day},
		{"two failures quadruple it", 2, 1, 4 * day},
		{"four failures is the cap", 4, 1, 16 * day},
		{"and nine failures is STILL the cap", 9, 1, 16 * day},
		{"two claimants halve it", 0, 2, 12 * time.Hour},
		{"four claimants is three halvings and lands on the floor", 0, 4, MinCheckInterval},
		{"eight claimants hit the floor", 0, 8, MinCheckInterval},
		{"and a hundred claimants stay on it", 0, 100, MinCheckInterval},
		{"popularity does not outrun reliability past the floor", 3, 64, MinCheckInterval},
		{"an unclaimed URL is on the base interval, not zero and not twice it", 0, 0, day},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, CheckInterval(tt.failures, tt.claimants))
		})
	}
}

// The cap is a claim about the WORST case, so it is asserted as one: a source that keeps failing must
// still come back within a bounded time, because a source nobody looks at cannot come back at all --
// which is the failure mode this phase exists to prevent.
func TestCheckIntervalNeverExceedsTheBackoffCap(t *testing.T) {
	assert.LessOrEqual(t, CheckInterval(1000, 1), BaseCheckInterval*time.Duration(1<<MaxCheckBackoff))
}

// The resurrection cadence. Two claims: a month for a quiet source, and -- the one that is easy to get
// wrong -- that reliability is NOT fed back in. A dead source's failure count is saturated, so applying
// the sweep's backoff to it would schedule the next attempt a year out, and a source nobody asks is a
// source that never comes back.
func TestResurrectionInterval(t *testing.T) {
	assert.Equal(t, 30*24*time.Hour, ResurrectionInterval(1))
	assert.Equal(t, 30*24*time.Hour, ResurrectionInterval(0))
	assert.Equal(t, 90*time.Hour, ResurrectionInterval(8), "a popular dead source is still asked sooner")
	assert.LessOrEqual(t, ResurrectionInterval(1),
		BaseCheckInterval*time.Duration(1<<MaxCheckBackoff)*ResurrectionMultiplier,
		"the resurrection interval must stay inside the bound the backoff cap promises")
}
