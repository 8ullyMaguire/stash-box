package list

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fork's one rule that needs no database (plan phase 8 slice 8c).
//
// The cap is enforced item-by-item on the ordinary add path, and a fork is the ONE route that would
// walk past that check: it inserts every entry in a single statement, so there is no per-item moment
// at which a count could refuse. That is why the rule exists as a statement of its own rather than as
// an inline comparison, and why it has its own test.
func TestCanForkIsBoundedByTheListCap(t *testing.T) {
	assert.NoError(t, CanFork(0), "an empty list is forkable")
	assert.NoError(t, CanFork(maxItems), "a list exactly at the cap is still forkable")
	assert.NoError(t, CanFork(maxItems-1))

	err := CanFork(maxItems + 1)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrForkWouldExceedCap)
	assert.Contains(t, err.Error(), "more entries than a fork may")
}
