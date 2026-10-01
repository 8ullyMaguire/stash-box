package sdbimport

import (
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stashapp/stash-box/internal/models"
)

func ptr[T any](v T) *T { return &v }

func flex(n int) FlexInt { return FlexInt{Value: &n} }

// --- stop condition --------------------------------------------------------

func TestStopCondition(t *testing.T) {
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	margin := time.Hour

	tests := []struct {
		name      string
		updated   time.Time
		watermark time.Time
		wantStop  bool
	}{
		{"no watermark never stops", base.Add(-72 * time.Hour), time.Time{}, false},
		{"well newer than watermark", base, base.Add(-24 * time.Hour), false},
		{"older than watermark", base.Add(-2 * time.Hour), base, true},
		// The margin cases are the point of the whole function.
		{"inside margin does not stop", base.Add(-30 * time.Minute), base, false},
		{"exactly at margin edge does not stop", base.Add(-time.Hour), base, false},
		{"just past margin stops", base.Add(-time.Hour - time.Second), base, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantStop, shouldStop(tt.updated, tt.watermark, margin))
		})
	}
}

// A run interrupted at a page boundary must resume without skipping. This is the
// scenario the margin exists for: two records sharing an `updated` value, with
// unstable relative order, would put one on the wrong side of a strict stop.
func TestStopConditionMarginCoversUnstableOrdering(t *testing.T) {
	watermark := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

	// A record exactly AT the watermark. A strict comparison might stop here and
	// never see it again if the order flips.
	atWatermark := watermark

	assert.False(t, shouldStop(atWatermark, watermark, time.Hour),
		"a record exactly at the watermark must still be processed")
	assert.True(t, shouldStop(watermark.Add(-time.Hour-time.Second), watermark, time.Hour),
		"a record a full margin older may be skipped")
}

// --- list comparison -------------------------------------------------------

func TestListEq(t *testing.T) {
	assert.True(t, listEq(nil, nil), "nil and nil are equal")
	assert.True(t, listEq(nil, []string{}), "nil and empty are equal")
	assert.False(t, listEq([]string{"a"}, nil), "different lengths differ")

	// Order must not matter: same URLs in another order is the same performer.
	assert.True(t, listEq([]string{"a", "b", "c"}, []string{"c", "a", "b"}))
	assert.False(t, listEq([]string{"a", "b"}, []string{"a", "b", "c"}))
	assert.False(t, listEq([]string{"a", "b"}, []string{"a", "x"}))
}

// --- modes -----------------------------------------------------------------

func TestParseMode(t *testing.T) {
	for _, in := range []string{"", "latest-wins", "upstream-wins", "local-wins", " local-wins "} {
		_, err := ParseMode(in)
		assert.NoError(t, err, "mode %q should parse", in)
	}

	// An unrecognised mode must NOT silently default. Defaulting to upstream-wins
	// on a typo would overwrite 110,000 curated records.
	_, err := ParseMode("upstream-winss")
	require.Error(t, err, "a typo must be an error, not a default")
}

func TestModes(t *testing.T) {
	upd := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	local := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		f    Field
		mode SyncMode
		want bool
	}{
		{"upstream-wins always writes a differing field",
			Field{Name: "country", Local: "US", Up: "CA", UpUpdated: upd}, ModeUpstream, true},
		{"local-wins preserves a set field",
			Field{Name: "country", Local: "US", Up: "CA", LocalEmpty: false}, ModeLocal, false},
		{"local-wins fills an empty field",
			Field{Name: "country", Local: "", Up: "CA", LocalEmpty: true}, ModeLocal, true},
		{"latest-wins: never written locally, upstream may write",
			Field{Name: "country", Local: "US", Up: "CA", UpUpdated: upd}, ModeLatest, true},
		{"latest-wins: local newer than upstream, keep local",
			Field{Name: "country", Local: "US", Up: "CA", UpUpdated: local, LocalWrittenAt: upd}, ModeLatest, false},
		{"latest-wins: upstream newer than local, take upstream",
			Field{Name: "country", Local: "US", Up: "CA", UpUpdated: upd, LocalWrittenAt: local}, ModeLatest, true},
		{"latest-wins: equal timestamps are not 'newer'",
			Field{Name: "country", Local: "US", Up: "CA", UpUpdated: upd, LocalWrittenAt: upd}, ModeLatest, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, shouldWrite(tt.f, tt.mode))
		})
	}
}

// --- the diff --------------------------------------------------------------

func localPerf(name string) LocalPerformer {
	return LocalPerformer{
		Performer: models.Performer{
			ID:     uuid.Must(uuid.NewV4()),
			Name:   name,
			Gender: (*models.GenderEnum)(ptr(models.GenderEnumFemale)),
		},
		Aliases: []string{"JD"},
		URLs:    []string{"https://example.com/jd"},
	}
}

func TestDiffNoChanges(t *testing.T) {
	up := Performer{
		ID: "src-1", Name: "Jane Doe",
		Gender:  ptr("FEMALE"),
		Aliases: []string{"JD"},
		URLs: []struct {
			URL string `json:"url"`
		}{{URL: "https://example.com/jd"}},
	}
	loc := localPerf("Jane Doe")

	assert.Empty(t, diffPerformerFields(up, loc, nil),
		"identical records must produce no changes")
}

func TestDiffDetectsFieldChange(t *testing.T) {
	up := Performer{
		ID: "src-1", Name: "Jane Doe",
		Gender:    ptr("FEMALE"),
		Country:   ptr("CA"), // local has none
		BirthDate: ptr("1990-01-02"),
		Height:    flex(180),
		Aliases:   []string{"JD"},
	}
	loc := localPerf("Jane Doe")
	loc.Height = ptr(170)

	changed := diffPerformerFields(up, loc, nil)
	names := map[string]bool{}
	for _, f := range changed {
		names[f.Name] = true
	}
	assert.True(t, names["country"], "a newly-populated upstream field is a change")
	assert.True(t, names["height"], "a changed numeric field is a change")
	assert.False(t, names["name"], "an unchanged name is not a change")
	assert.False(t, names["gender"], "an unchanged enum is not a change")
}

func TestDiffNameIsCaseInsensitive(t *testing.T) {
	// A case-only difference is the same name. Rewriting it would churn every
	// record on every run.
	up := Performer{ID: "s", Name: "jane doe", Gender: ptr("FEMALE"), Aliases: []string{"JD"}}
	loc := localPerf("Jane Doe")
	loc.URLs = nil
	up.URLs = nil

	for _, f := range diffPerformerFields(up, loc, nil) {
		assert.NotEqual(t, "name", f.Name, "case-only name difference must not be a change")
	}
}

func TestDiffListOrderDoesNotMatter(t *testing.T) {
	up := Performer{
		ID: "s", Name: "Jane Doe", Gender: ptr("FEMALE"),
		Aliases: []string{"b", "a"},
		URLs: []struct {
			URL string `json:"url"`
		}{{URL: "https://z"}, {URL: "https://y"}},
	}
	loc := localPerf("Jane Doe")
	loc.Aliases = []string{"a", "b"}
	loc.URLs = []string{"https://y", "https://z"}

	assert.Empty(t, diffPerformerFields(up, loc, nil),
		"reordered lists are not a change")
}

// The failure mode that would destroy data: a sparse update that clears every
// field the caller did not set. The sync must always send a COMPLETE input.
func TestUpdatePreservesUnsetFields(t *testing.T) {
	// The destination has a height a curator entered.
	loc := localPerf("Jane Doe")
	loc.Height = ptr(170)
	loc.Country = ptr("US")

	// The source knows nothing about height and has a different country.
	up := Performer{ID: "s", Name: "Jane Doe", Gender: ptr("FEMALE"), Country: ptr("CA"), Aliases: []string{"JD"}}

	changed := diffPerformerFields(up, loc, nil)
	fields := map[string]bool{}
	for _, f := range changed {
		fields[f.Name] = true
	}

	assert.False(t, fields["height"],
		"height is absent upstream and must NOT be reported as a change to clear")
	assert.True(t, fields["country"], "country differs and is a real change")

	// And the plan must only ever carry fields the source actually spoke about.
	plan := planUpdate("s", changed, ModeUpstream)
	for _, c := range plan {
		assert.NotEqual(t, "height", c.Field,
			"an update plan must never contain a field the source did not set")
	}
}

// --- the plan --------------------------------------------------------------

func TestPlanRespectsMode(t *testing.T) {
	upd := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	fields := []Field{
		{Name: "country", Local: "US", Up: "CA", LocalWrittenAt: upd, UpUpdated: upd},
		{Name: "eye_color", Local: "", Up: "BROWN", LocalEmpty: true, UpUpdated: upd},
	}

	// local-wins writes only the empty one.
	plan := planUpdate("s", fields, ModeLocal)
	require.Len(t, plan, 1)
	assert.Equal(t, "eye_color", plan[0].Field)

	// upstream-wins writes both.
	assert.Len(t, planUpdate("s", fields, ModeUpstream), 2)

	// latest-wins: country was written locally at the same instant upstream
	// touched it, so it is not newer; eye_color has never been written here.
	plan = planUpdate("s", fields, ModeLatest)
	require.Len(t, plan, 1)
	assert.Equal(t, "eye_color", plan[0].Field)
}

// An idempotent re-run must produce nothing the second time. This is the
// property that makes a scheduled job safe to run repeatedly.
func TestIdempotent(t *testing.T) {
	// The source must actually CARRY the lists for "no change" to mean no
	// change. An upstream record with no URLs is silence, not agreement, and the
	// one-directional rule correctly declines to act on it -- see
	// TestSilentUpstreamDoesNotClear.
	up := Performer{
		ID: "s", Name: "Jane Doe", Gender: ptr("FEMALE"), Country: ptr("CA"),
		Aliases: []string{"JD"},
		URLs: []struct {
			URL string `json:"url"`
		}{{URL: "https://example.com/jd"}},
	}

	// First run: destination agrees with upstream.
	loc := localPerf("Jane Doe")
	loc.Country = ptr("CA")
	first := planUpdate("s", diffPerformerFields(up, loc, nil), ModeUpstream)
	assert.Empty(t, first, "nothing to do when the destination already matches")

	// After the write, the destination matches; a second run finds nothing.
	loc2 := localPerf("Jane Doe")
	loc2.Country = ptr("CA")
	second := planUpdate("s", diffPerformerFields(up, loc2, nil), ModeUpstream)
	assert.Empty(t, second, "a re-run after a write must find nothing to do")
}

func TestSyncReportSummary(t *testing.T) {
	r := &SyncReport{Scanned: 10, Created: 2, Updated: 3, Unchanged: 4, Skipped: 1}
	assert.Contains(t, r.summary(), "scanned 10")
	assert.Contains(t, r.summary(), "updated 3")
}

// The load-bearing safety rule, stated as its own test.
//
// "The source has no value for this field" and "this query did not ask for the
// field" are the same bytes on the wire. Reading silence as "empty" would clear a
// curator's height because upstream happened not to carry one -- silently, and
// discovered only by noticing the loss later.
func TestSilentUpstreamDoesNotClear(t *testing.T) {
	loc := localPerf("Jane Doe")
	loc.Height = ptr(170)
	loc.Country = ptr("US")
	loc.Aliases = []string{"JD"}
	loc.URLs = []string{"https://example.com/jd"}

	// Upstream reports a name and gender, and says nothing else.
	up := Performer{ID: "s", Name: "Jane Doe", Gender: ptr("FEMALE")}

	for _, f := range diffPerformerFields(up, loc, nil) {
		switch f.Name {
		case "height", "country", "aliases", "urls", "birthdate":
			t.Errorf("field %q was reported as changed, but upstream is silent about it; "+
				"silence must not clear a local value", f.Name)
		}
	}
}

// A genuine upstream CHANGE still propagates -- the rule above must not have
// quietly turned every field into a no-op.
func TestUpstreamChangeStillPropagates(t *testing.T) {
	loc := localPerf("Jane Doe")
	loc.Country = ptr("US")
	loc.Height = ptr(170)

	up := Performer{
		ID: "s", Name: "Jane Doe", Gender: ptr("FEMALE"),
		Country: ptr("CA"), Height: flex(180),
		Aliases: []string{"JD"},
		URLs: []struct {
			URL string `json:"url"`
		}{{URL: "https://example.com/jd"}},
	}

	changed := map[string]bool{}
	for _, f := range diffPerformerFields(up, loc, nil) {
		changed[f.Name] = true
	}
	assert.True(t, changed["country"], "an upstream country change must propagate")
	assert.True(t, changed["height"], "an upstream height change must propagate")
}
