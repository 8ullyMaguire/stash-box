package invite

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// THE THREE COUNTERS ARE ROWS, AND THESE ARE THE TWO WAYS A ROW-COUNTING READER GOES WRONG
// (plan phase 11 slice 11c).
//
// The database answers a grouped count with ONE ROW PER KIND THAT HAS EVENTS. A reader that assigns
// those rows onto a struct leaves the kinds that never happened as whatever the struct started as,
// which is indistinguishable from a kind this code has never heard of -- and a member whose
// invitations have never been scanned would be told nothing rather than "0".
func TestCountsFromRowsStartsEveryKindAtZero(t *testing.T) {
	// Nothing at all: three zeroes, not three absences.
	assert.Equal(t, EventCounts{}, CountsFromRows(nil))
	assert.Equal(t, EventCounts{Scans: 0, Signups: 0, Activations: 0}, CountsFromRows([]KindCount{}))

	// One kind present is still all three answers: the other two are zero.
	one := CountsFromRows([]KindCount{{Kind: string(EventSignup), Count: 3}})
	assert.Equal(t, 3, one.Signups)
	assert.Zero(t, one.Scans, "a key that was never scanned reads 0, not nothing")
	assert.Zero(t, one.Activations, "and a signup that nobody finished is 0 activations")
}

// An unknown kind is IGNORED, not invented and not an error: the same rule the trust rollup applies
// to an event kind it does not know, and the reason `kind` is free text in the schema.
func TestCountsFromRowsIgnoresAKindItDoesNotKnow(t *testing.T) {
	counts := CountsFromRows([]KindCount{
		{Kind: string(EventScan), Count: 2},
		{Kind: "something_this_version_has_never_heard_of", Count: 99},
	})
	assert.Equal(t, 2, counts.Scans)
	assert.Equal(t, EventCounts{Scans: 2}, counts,
		"the unknown kind contributes to NOTHING rather than to the nearest counter")
}

// The three kinds all land, and they land in their OWN field: this is the assertion that would catch
// a copy-paste between the three switch arms, which is the mistake that reads as "the counters work"
// right up until a member asks which of them is which.
func TestCountsFromRowsKeepsTheThreeKindsApart(t *testing.T) {
	counts := CountsFromRows([]KindCount{
		{Kind: string(EventScan), Count: 11},
		{Kind: string(EventSignup), Count: 7},
		{Kind: string(EventActivation), Count: 3},
	})
	assert.Equal(t, EventCounts{Scans: 11, Signups: 7, Activations: 3}, counts)
	assert.NotEqual(t, counts.Scans, counts.Signups)
	assert.NotEqual(t, counts.Signups, counts.Activations)
}

// THE KIND STRINGS ARE DATA, not labels: they are written into a column and read back by every
// counter and every future reader of the table, so changing one silently orphans every row that
// carries the old spelling. They are pinned here so that change has to be deliberate.
func TestTheStoredKindStringsAreWhatTheRowsSay(t *testing.T) {
	assert.Equal(t, "scan", string(EventScan))
	assert.Equal(t, "signup", string(EventSignup))
	assert.Equal(t, "activation", string(EventActivation))

	// And the closed set is exactly these three, in the order a member's journey through an
	// invitation goes: somebody looked, somebody gave an address, somebody joined.
	assert.Equal(t, []EventKind{EventScan, EventSignup, EventActivation}, AllEventKinds())
}
