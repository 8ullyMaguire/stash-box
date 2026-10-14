package completeness

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/stashapp/stash-box/internal/models"
)

func verdictOf(expected, observed int64, statuses ...string) Verdict {
	v := Verdict{Expected: expected, Observed: observed}
	for _, status := range statuses {
		v.Totals = append(v.Totals, Total{Expected: expected, Observed: observed, Source: &models.ContentSource{Status: status}})
	}
	return v
}

func statusTotal(expected, observed int64, status string) Total {
	return Total{Expected: expected, Observed: observed, Source: &models.ContentSource{Status: status}}
}

// The headline: the claim is FULLY held -- 400 of 400, so 100% is a defensible statement about the
// archive -- and the page that asserted it is gone. 100 becomes 90: true about what we hold,
// indefensible about the record, which is exactly the case the cap exists for.
func TestVerdictCapsAnUnverifiableClaim(t *testing.T) {
	dead := verdictOf(400, 400, "dead")
	assert.Equal(t, 100, dead.Percent())
	assert.True(t, dead.Unverifiable())
	assert.Equal(t, 90, dead.Score(), "capped at 100 - UnverifiablePenalty")

	// The same fraction, checkable: no cap.
	live := verdictOf(400, 400, "live")
	assert.Equal(t, 100, live.Percent())
	assert.False(t, live.Unverifiable())
	assert.Equal(t, 100, live.Score(), "a live source is not penalised")

	// One short of the claim, page alive: 99, and NOT 100 -- completeness never overstates.
	almost := verdictOf(400, 399, "live")
	assert.Equal(t, 99, almost.Percent())
	assert.Equal(t, 99, almost.Score())

	// A CEILING, not a subtraction: 25% stays 25%, because subtracting would report less completeness
	// than the archive verifiably holds.
	low := verdictOf(400, 100, "dead")
	assert.Equal(t, 25, low.Percent())
	assert.Equal(t, 25, low.Score(), "the cap is a ceiling, so it cannot pull a score DOWN")

	// The penalty is per ENTITY, not per total: two dead claims are no worse than one.
	two := Verdict{
		Expected: 800, Observed: 800,
		Totals: []Total{statusTotal(400, 400, "dead"), statusTotal(400, 400, "dead")},
	}
	assert.Equal(t, 90, two.Score(), "once per entity, not once per unverifiable total")

	// ONE lost claim is enough, however many others still stand: the entity's record is
	// compromised, not just that number.
	mixed := Verdict{
		Expected: 800, Observed: 800,
		Totals: []Total{statusTotal(400, 400, "live"), statusTotal(400, 400, "archived")},
	}
	assert.True(t, mixed.Unverifiable())
	assert.Equal(t, 90, mixed.Score())

	// Half of a claim, page gone: 50 stays 50. The cap is a ceiling, not a subtraction.
	half := verdictOf(400, 200, "dead")
	assert.Equal(t, 50, half.Score())
}

// No claim at all is not 100%. A dashboard averaging 100% for every entity nobody has claimed anything
// about would read the archive as more complete the less is known about it.
func TestVerdictWithoutAClaimIsNotComplete(t *testing.T) {
	none := Verdict{}
	assert.False(t, none.HasClaim())
	assert.Equal(t, 0, none.Percent())
	assert.Equal(t, 0, none.Score())
	assert.False(t, none.Unverifiable(), "nothing claimed is not a lost source")

	// A total that no source has been checked against is the ordinary case and does not cap anything.
	untracked := verdictOf(400, 399)
	assert.False(t, untracked.Unverifiable())
	assert.Equal(t, 99, untracked.Score())

	// A claim of one, none held: 0%, and not a cap of -10.
	empty := verdictOf(1, 0, "dead")
	assert.Equal(t, 0, empty.Percent())
	assert.Equal(t, 0, empty.Score())
}

func TestVerdictRounding(t *testing.T) {
	for _, tt := range []struct{ expected, observed, want int64 }{
		{3, 1, 33},
		{3, 2, 66}, // FLOORED. The completion scorer rounds this to 67; rounding UP is the wrong way
		{400, 200, 50},
		{400, 1, 0},     // 0.25% is 0, not 1: nothing meaningful is held
		{400, 399, 99},  // one short of the claim, page alive: 99, and not 100
		{400, 400, 100}, // only a MET claim reaches 100 -- and the cap decides whether it may be said
	} {
		v := Verdict{Expected: tt.expected, Observed: tt.observed}
		assert.Equal(t, int(tt.want), v.Percent(), "%d of %d", tt.observed, tt.expected)
	}
}

func TestVerdictNamesTheLostSource(t *testing.T) {
	v := verdictOf(400, 399, "live", "archived")
	lost, ok := v.LostSource()
	assert.True(t, ok)
	assert.Equal(t, "archived", lost.SourceStatus())

	_, ok = verdictOf(400, 399, "live", "intermittent").LostSource()
	assert.False(t, ok, "an intermittent page is flaky, not gone")
}
