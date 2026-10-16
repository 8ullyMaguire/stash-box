package sdbimport

import (
	"encoding/json"
	"testing"
	"time"
)

// A DIFF THAT NEVER CONVERGES IS A DIFF THAT CANNOT BE TRUSTED.
//
// Both tests are about one defect found against the live source: a difference the destination cannot write was
// planned anyway, applied as a deliberate no-op, and re-detected on the next run. The report then said
// "updated 49" for 49 records where nothing had changed and nothing would -- measured twice in a row, before
// and after a real run.

// URLs cannot be written: a URL carries a SiteID, which is a uuid this code cannot invent, so the import path
// loses the URL and records the loss. Planning it as a change made every run rewrite the same performers.
func TestAURLDifferenceIsCountedNotPlanned(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

	// Built through JSON because the source type's URL list is an anonymous struct -- the field's real shape
	// matters here, and this is how a record actually arrives.
	var up Performer
	if err := json.Unmarshal([]byte(`{"id":"src","name":"Jane Doe","urls":[{"url":"https://example.test/jane"}]}`), &up); err != nil {
		t.Fatal(err)
	}
	up.Updated = now
	if len(up.URLStrings()) != 1 {
		t.Fatalf("the fixture did not build a URL: %v", up.URLStrings())
	}
	loc := localPerf("Jane Doe")
	loc.URLs = nil

	rep := &SyncReport{}
	lookup := func(Performer) (*LocalPerformer, bool) { return &loc, true }
	w := SyncWalk{Mode: ModeUpstream, Watermark: now.Add(-48 * time.Hour), Margin: time.Hour, DryRun: true}
	if _, err := w.Step([]Performer{up}, rep, lookup); err != nil {
		t.Fatal(err)
	}

	if len(rep.Changes) != 0 {
		t.Errorf("a URL difference was planned as a change: %+v -- it cannot be written, so planning it means "+
			"the same record is 'updated' on every run forever", rep.Changes)
	}
	if rep.NotApplied != 1 {
		t.Errorf("NotApplied = %d, want 1: the difference must be COUNTED, not silently dropped -- 'no URL "+
			"changed' must stay distinguishable from 'nobody looked'", rep.NotApplied)
	}
	if rep.Updated != 0 {
		t.Errorf("Updated = %d, want 0: nothing was planned, so nothing is updated", rep.Updated)
	}
}

// The general rule the URL case is one instance of.
func TestADifferenceTheDestinationCannotWriteIsNeverPlanned(t *testing.T) {
	fields := []Field{
		{Name: "name", Local: "old", Up: "new"},
		{Name: "urls", Local: "", Up: "https://example.test/jane", notWritable: true},
	}
	plan, notApplied := planUpdate("s", fields, ModeUpstream)

	if len(plan) != 1 || plan[0].Field != "name" {
		t.Errorf("plan = %+v, want only the writable field", plan)
	}
	if notApplied != 1 {
		t.Errorf("notApplied = %d, want 1", notApplied)
	}
}

// An enum whose upstream value is the source's "not applicable" sentinel has no representation here. enumOrNil at
// the write refuses to invent a fact; asking the same question one step earlier keeps the diff from reporting
// that invented fact as a change it will never make.
func TestAnEnumValueWithNoRepresentationIsNotAChange(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	loc := localPerf("Jane Doe")

	for _, sentinel := range []string{"NA", "UNKNOWN", "", "NOT_A_REAL_ENUM"} {
		up := Performer{ID: "src", Name: "Jane Doe", BreastType: ptr(sentinel), Updated: now}
		rep := &SyncReport{}
		lookup := func(Performer) (*LocalPerformer, bool) { return &loc, true }
		w := SyncWalk{Mode: ModeUpstream, Watermark: now.Add(-48 * time.Hour), Margin: time.Hour, DryRun: true}
		if _, err := w.Step([]Performer{up}, rep, lookup); err != nil {
			t.Fatal(err)
		}
		if len(rep.Changes) != 0 {
			t.Errorf("breast_type %q was planned as a change: %+v -- the write refuses that value, so the plan "+
				"is a promise the writer will not keep", sentinel, rep.Changes)
		}
	}

	// And a value the destination DOES define is still a change, so this is a rule about unrepresentable values
	// rather than a rule that turns the field off.
	up := Performer{ID: "src", Name: "Jane Doe", BreastType: ptr("FAKE"), Updated: now}
	rep := &SyncReport{}
	lookup := func(Performer) (*LocalPerformer, bool) { return &loc, true }
	w := SyncWalk{Mode: ModeUpstream, Watermark: now.Add(-48 * time.Hour), Margin: time.Hour, DryRun: true}
	if _, err := w.Step([]Performer{up}, rep, lookup); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range rep.Changes {
		if c.Field == "breast_type" {
			found = true
		}
	}
	if !found {
		t.Errorf("a representable breast_type change was not planned: %+v", rep.Changes)
	}
}
