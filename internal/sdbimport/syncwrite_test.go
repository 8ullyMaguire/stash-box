package sdbimport

import (
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/models"
)

func newID(t *testing.T) uuid.UUID {
	t.Helper()
	u, err := uuid.NewV4()
	require.NoError(t, err)
	return u
}

// The never-clear rule has to survive all the way to the write, not just the
// diff. sites.go's strPtr returns &"" for an empty string, which is correct
// there and destructive here: it would set a field to empty instead of leaving
// it alone. This is the test that would catch that regression.
func TestWriteNeverClearsOnEmptyUpstream(t *testing.T) {
	f := Field{Name: "country", Local: "US", Up: ""}

	in := models.PerformerUpdateInput{ID: newID(t)}
	_, apply := f.toUpdateInput()
	apply(&in)

	assert.Nil(t, in.Country,
		"an empty upstream value must leave the field UNSET, not set to empty")
}

func TestWriteSetsNonEmptyUpstream(t *testing.T) {
	f := Field{Name: "country", Local: "US", Up: "CA"}
	in := models.PerformerUpdateInput{ID: newID(t)}
	_, apply := f.toUpdateInput()
	apply(&in)

	require.NotNil(t, in.Country)
	assert.Equal(t, "CA", *in.Country)
}

// An unrecognised upstream enum must not be written. The importer counts
// dropped values for exactly this reason; casting blindly would turn a counted
// drop into an unparseable row in the column.
func TestWriteRejectsUnknownEnum(t *testing.T) {
	f := Field{Name: "gender", Local: "FEMALE", Up: "NOT_A_GENDER"}
	in := models.PerformerUpdateInput{ID: newID(t)}
	_, apply := f.toUpdateInput()
	apply(&in)
	assert.Nil(t, in.Gender, "an unknown enum must be dropped, not written")

	// The source's NA/UNKNOWN sentinels mean "not applicable" and must not
	// become a concrete value.
	for _, sentinel := range []string{"NA", "UNKNOWN", ""} {
		g := Field{Name: "gender", Up: sentinel}
		in2 := models.PerformerUpdateInput{ID: newID(t)}
		_, a2 := g.toUpdateInput()
		a2(&in2)
		assert.Nil(t, in2.Gender, "sentinel %q must not become a gender", sentinel)
	}
}

func TestWriteAcceptsKnownEnum(t *testing.T) {
	f := Field{Name: "gender", Up: "FEMALE"}
	in := models.PerformerUpdateInput{ID: newID(t)}
	_, apply := f.toUpdateInput()
	apply(&in)
	require.NotNil(t, in.Gender)
	assert.Equal(t, models.GenderEnumFemale, *in.Gender)
}

// One Update call for the whole record, not one per field. Per-field calls
// re-read and rewrite every column, so a three-field change becomes three full
// row rewrites.
func TestApplyChangesProducesOneInput(t *testing.T) {
	byName := map[string]Field{
		"country": {Name: "country", Up: "CA"},
		"height":  {Name: "height", Up: "180"},
	}
	changes := []FieldChange{
		{Field: "country", New: "CA"},
		{Field: "height", New: "180"},
	}
	id := newID(t)
	in := applyChanges(id, changes, byName)

	assert.Equal(t, id, in.ID)
	require.NotNil(t, in.Country)
	require.NotNil(t, in.Height)
	assert.Equal(t, 180, *in.Height)
}

// --- the walk --------------------------------------------------------------

type srcRec struct {
	Performer
}

func TestWalkStopsAtWatermark(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	page := []Performer{
		{ID: "a", Name: "A", Updated: now},
		{ID: "b", Name: "B", Updated: now.Add(-2 * time.Hour)},
		{ID: "c", Name: "C", Updated: now.Add(-3 * time.Hour)},
	}
	rep := &SyncReport{}
	lookup := func(string) (*LocalPerformer, bool) { return nil, false }

	w := SyncWalk{Mode: ModeUpstream, Watermark: now.Add(-time.Hour), Margin: time.Hour}
	cont, err := w.Step(page, rep, lookup)
	require.NoError(t, err)

	// The page's oldest record is 3h old, past watermark-1h, so the walk stops.
	assert.False(t, cont, "the walk must stop once the page falls outside the window")
	assert.Equal(t, now.Add(-3*time.Hour), rep.StoppedAt)
	assert.Equal(t, 3, rep.Scanned)
}

func TestWalkContinuesWhenPageIsInsideWindow(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	page := []Performer{
		{ID: "a", Name: "A", Updated: now},
		{ID: "b", Name: "B", Updated: now.Add(-10 * time.Minute)},
	}
	rep := &SyncReport{}
	lookup := func(string) (*LocalPerformer, bool) { return nil, false }

	w := SyncWalk{Mode: ModeUpstream, Watermark: now.Add(-48 * time.Hour), Margin: time.Hour}
	cont, err := w.Step(page, rep, lookup)
	require.NoError(t, err)
	assert.True(t, cont, "a page entirely inside the window must continue")
}

func TestWalkReportsChangesForKnownRecords(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	up := Performer{
		ID: "src", Name: "Jane Doe", Gender: ptr("FEMALE"),
		Country: ptr("CA"), Aliases: []string{"JD"},
		Updated: now,
	}
	loc := localPerf("Jane Doe")
	loc.Country = ptr("US")
	loc.URLs = nil

	rep := &SyncReport{}
	lookup := func(n string) (*LocalPerformer, bool) {
		if n == "Jane Doe" {
			return &loc, true
		}
		return nil, false
	}

	w := SyncWalk{Mode: ModeUpstream, Watermark: now.Add(-48 * time.Hour), Margin: time.Hour, DryRun: true}
	_, err := w.Step([]Performer{up}, rep, lookup)
	require.NoError(t, err)

	assert.Equal(t, 1, rep.Updated)
	require.NotEmpty(t, rep.Changes)
	assert.Equal(t, "country", rep.Changes[0].Field)
	assert.Equal(t, "US", rep.Changes[0].Old)
	assert.Equal(t, "CA", rep.Changes[0].New)
}

// A record we do not have is the importer's job. Counting it as "skipped" must
// not read as "we chose not to update it".
func TestWalkSkipsUnknownRecords(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	rep := &SyncReport{}
	lookup := func(string) (*LocalPerformer, bool) { return nil, false }

	w := SyncWalk{Mode: ModeUpstream, Watermark: now.Add(-48 * time.Hour), Margin: time.Hour}
	_, err := w.Step([]Performer{{ID: "x", Name: "Nobody", Updated: now}}, rep, lookup)
	require.NoError(t, err)
	assert.Equal(t, 1, rep.Skipped)
	assert.Equal(t, 0, rep.Updated)
}

// --- watermarks ------------------------------------------------------------

func TestWatermarkOnlyMovesForward(t *testing.T) {
	w := Watermarks{}
	t1 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)

	w.Set("performers", t2)
	w.Set("performers", t1) // an older value must not rewind it
	assert.Equal(t, t2, w.Get("performers"))
}

func TestWatermarkZeroMeansNoWatermark(t *testing.T) {
	var w Watermarks
	assert.True(t, w.Get("performers").IsZero())
	w.Set("performers", time.Now()) // must not panic on a nil map read path
}
