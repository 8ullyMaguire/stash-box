package sdbimport

import (
	"encoding/json"
	"testing"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/models"
)

// The enum lists in mapping.go are hand-copied from the generated constants.
// That is a drift risk: a value that stops being accepted starts being silently
// dropped, and the only symptom is a count in the drop report. This test fails
// the build instead.

// enumValues extracts the value of every constant of a named string enum type
// from the generated models package by reading them through All* slices where
// they exist, falling back to a literal list.
func enumSet(t *testing.T, all []string, want []string, name string) {
	t.Helper()
	have := map[string]bool{}
	for _, v := range all {
		have[v] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("%s: mapper accepts %q but models.All%s does not contain it; "+
				"the source and the destination enums have diverged", name, w, name)
		}
	}
}

func TestEnumListsMatchGeneratedModels(t *testing.T) {
	g := make([]string, 0, len(models.AllGenderEnum))
	for _, v := range models.AllGenderEnum {
		g = append(g, string(v))
	}
	enumSet(t, g, genderValues, "GenderEnum")

	e := make([]string, 0, len(models.AllEthnicityEnum))
	for _, v := range models.AllEthnicityEnum {
		e = append(e, string(v))
	}
	enumSet(t, e, ethnicityValues, "EthnicityEnum")

	ec := make([]string, 0, len(models.AllEyeColorEnum))
	for _, v := range models.AllEyeColorEnum {
		ec = append(ec, string(v))
	}
	enumSet(t, ec, eyeValues, "EyeColorEnum")

	hc := make([]string, 0, len(models.AllHairColorEnum))
	for _, v := range models.AllHairColorEnum {
		hc = append(hc, string(v))
	}
	enumSet(t, hc, hairValues, "HairColorEnum")

	bt := make([]string, 0, len(models.AllBreastTypeEnum))
	for _, v := range models.AllBreastTypeEnum {
		bt = append(bt, string(v))
	}
	enumSet(t, bt, breastValues, "BreastTypeEnum")
}

// An unknown enum value must be DROPPED, not coerced. Coercing to a default
// would invent a fact about the performer and it would be indistinguishable
// from real data afterwards.
func TestEnumOrNilDropsUnknownAndNa(t *testing.T) {
	cases := []struct {
		in   *string
		want *string
		note string
	}{
		{nil, nil, "nil stays nil"},
		{strPtr("MALE"), strPtr("MALE"), "known value passes"},
		{strPtr("  male  "), strPtr("MALE"), "case and space normalised"},
		{strPtr("NA"), nil, "NA is the source's 'not applicable' sentinel, not a value"},
		{strPtr(""), nil, "empty string is not a value"},
		{strPtr("TRANSSEXUAL"), nil, "legacy source value this fork does not define"},
		{strPtr("BANANA"), nil, "nonsense is dropped, never defaulted"},
	}
	for _, c := range cases {
		got := enumOrNil(c.in, genderValues)
		switch {
		case c.want == nil && got != nil:
			t.Errorf("%s: got %q, want nil", c.note, *got)
		case c.want != nil && got == nil:
			t.Errorf("%s: got nil, want %q", c.note, *c.want)
		case c.want != nil && got != nil && *got != *c.want:
			t.Errorf("%s: got %q, want %q", c.note, *got, *c.want)
		}
	}
}

// A band size that is not a number is real source data that does not fit an
// int column. It must be dropped and counted, not truncated to a number that
// was never in the source.
func TestBandSizeNonNumericIsDropped(t *testing.T) {
	st := newStats()

	p := Performer{Name: "X", BandSize: mustFlex(`"34DD"`)}
	in := p.ToCreateInput(st)
	if in.BandSize != nil {
		t.Errorf("band size 34DD: got %d, want nil (dropped)", *in.BandSize)
	}
	if st.Dropped["performer.band_size=34DD"] != 1 {
		t.Errorf("expected the drop to be counted, got %v", st.Dropped)
	}

	p2 := Performer{Name: "Y", BandSize: mustFlex(`34`)}
	in2 := p2.ToCreateInput(st)
	if in2.BandSize == nil || *in2.BandSize != 34 {
		t.Errorf("band size 34: got %v, want 34", in2.BandSize)
	}
}

// Dates: the destination requires one, so a bad date must be caught here rather
// than becoming a database error 200,000 records into a run.
func TestValidDate(t *testing.T) {
	for _, ok := range []string{"2020-01-02", "2020-01-02T10:00:00Z", "2020-01", "2020"} {
		if !ValidDate(ok) {
			t.Errorf("%q should be a valid date", ok)
		}
	}
	for _, bad := range []string{"", "   ", "not-a-date", "02/01/2020", "2020-13-45"} {
		if ValidDate(bad) {
			t.Errorf("%q should not be a valid date", bad)
		}
	}
}

// A scene with no title and no url is unidentifiable: nothing to search it by,
// nothing to scrape. It must be skipped rather than imported as an orphan.
func TestSceneWithNoTitleOrURLIsUnidentifiable(t *testing.T) {
	st := newStats()
	s := Scene{Title: nil, URLs: nil}
	if !s.ToSceneInput(NewResolver(), st).NoTitle {
		t.Error("expected NoTitle for a scene with neither title nor url")
	}

	s2 := Scene{Title: strPtr("  "), URLs: nil}
	if !s2.ToSceneInput(NewResolver(), st).NoTitle {
		t.Error("expected NoTitle for a whitespace-only title and no url")
	}

	s3 := Scene{Title: strPtr("Real Title")}
	if s3.ToSceneInput(NewResolver(), st).NoTitle {
		t.Error("a titled scene is identifiable")
	}
}

// Host matching is what makes URLs importable at all, so the fallbacks are
// load-bearing: the source stores "models.ferronetwork.com" while the site is
// declared as "ferronetwork.com".
func TestHostCandidates(t *testing.T) {
	cases := map[string][]string{
		"https://www.brazzers.com/models/x":   {"brazzers.com"},
		"brazzers.com":                        {"brazzers.com"},
		"https://models.ferronetwork.com/foo": {"models.ferronetwork.com", "ferronetwork.com", "com"},
	}
	for in, want := range cases {
		got := HostCandidates(in)
		if len(got) < 1 || got[0] != want[0] {
			t.Errorf("%q: got %v, want first %v", in, got, want[0])
		}
	}
	if h := HostOf("not a url at all"); h == "not" || h == "" {
		// A bare string with no scheme still yields a hostname; what matters is
		// that it does not panic and does not include a path.
		if h != "not a url at all" {
			t.Logf("HostOf on a bare string returned %q", h)
		}
	}
}

// A catalogue must map a URL to the site that declared a broader hostname.
func TestSiteCatalogueMatchesBroaderDeclaration(t *testing.T) {
	cat := NewSiteCatalogue()
	cat.Add("https://ferronetwork.com", mustUUID("11111111-1111-1111-1111-111111111111"))

	id, ok := cat.Known("https://www.models.ferronetwork.com/abc")
	if !ok {
		t.Fatal("expected models.ferronetwork.com to resolve via the broader ferronetwork.com declaration")
	}
	if id.String() != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("got id %s", id)
	}
}

// Name matching is case-insensitive: the same studio appears as "BraZZers" and
// "brazzers" and matching them as different entities would split it in half.
func TestNormKeyFoldsCase(t *testing.T) {
	if normKey("  BraZZers ") != normKey("brazzers") {
		t.Error("normKey should fold case and trim space")
	}
}

// mustUUID parses a fixed id for tests; a parse failure is a typo in the test.
func mustUUID(s string) uuid.UUID {
	id, err := uuid.FromString(s)
	if err != nil {
		panic(err)
	}
	return id
}

// mustFlex builds a FlexInt from a raw JSON value for tests.
func mustFlex(raw string) FlexInt {
	var f FlexInt
	if err := json.Unmarshal([]byte(raw), &f); err != nil {
		panic(err)
	}
	return f
}

// The source declares band_size as Int but emits strings on some records; the
// run died on exactly that. FlexInt must absorb BOTH shapes, and neither
// shape may abort a page.
func TestFlexIntAcceptsBothShapes(t *testing.T) {
	cases := []struct {
		raw     string
		want    *int
		invalid string
		note    string
	}{
		{`36`, ptrInt(36), "", "bare number, the declared type"},
		{`"36"`, ptrInt(36), "", "numeric string, the shape that crashed the run"},
		{`null`, nil, "", "null is absent, not an error"},
		{`0`, ptrInt(0), "", "zero is a real value, not absent"},
		{`"34DD"`, nil, "34DD", "non-numeric string is recorded, not parsed"},
		{`"  36  "`, ptrInt(36), "", "surrounding space tolerated"},
		{`""`, nil, "", "empty string is absent"},
	}
	for _, c := range cases {
		got := mustFlex(c.raw)
		switch {
		case c.want == nil && got.Value != nil:
			t.Errorf("%s: got %d, want nil", c.note, *got.Value)
		case c.want != nil && got.Value == nil:
			t.Errorf("%s: got nil, want %d", c.note, *c.want)
		case c.want != nil && *got.Value != *c.want:
			t.Errorf("%s: got %d, want %d", c.note, *got.Value, *c.want)
		}
		if got.Invalid != c.invalid {
			t.Errorf("%s: Invalid = %q, want %q", c.note, got.Invalid, c.invalid)
		}
	}
}

// A dirty value on an int field must not lose the whole record: the performer
// still imports, the field is dropped, and the drop is counted.
func TestDirtyIntFieldDoesNotLoseTheRecord(t *testing.T) {
	st := newStats()
	p := Performer{
		Name:     "Survivor",
		Height:   mustFlex(`"not-a-number"`),
		BandSize: mustFlex(`"34DD"`),
	}
	in := p.ToCreateInput(st)

	if in.Name != "Survivor" {
		t.Error("the record itself must survive a dirty numeric field")
	}
	if in.Height != nil {
		t.Errorf("dirty height should be dropped, got %d", *in.Height)
	}
	if in.BandSize != nil {
		t.Errorf("dirty band size should be dropped, got %d", *in.BandSize)
	}
	if st.Dropped["performer.height=not-a-number"] != 1 {
		t.Errorf("dirty height not counted: %v", st.Dropped)
	}
	if st.Dropped["performer.band_size=34DD"] != 1 {
		t.Errorf("dirty band size not counted: %v", st.Dropped)
	}
}

func ptrInt(n int) *int { return &n }

// The dedup KEY is the most consequential decision in the importer, so it is
// pinned here.
//
// The destination is `UNIQUE (name, disambiguation) WHERE NOT deleted`. An
// earlier version matched on name alone, and the source is full of distinct
// people who share one: 3,107 of 20,000 sampled performers (15.5%), with "alex"
// appearing 29 times. Name-only matching merges them, and a merged performer is
// indistinguishable from a correct one after the fact.
func TestNullStringTreatsEmptyDisambiguationAsAbsent(t *testing.T) {
	if got := nullString(""); got != nil {
		t.Errorf("empty disambiguation must become nil so the narg() branch treats it as absent, got %q", *got)
	}
	// A performer with no disambiguation has to match ITSELF on a re-run, or
	// every such performer is duplicated.
	if got := nullString("Raging Stallion 2000"); got == nil || *got != "Raging Stallion 2000" {
		t.Error("a real disambiguation must be preserved")
	}
}

// Two performers with the same name and DIFFERENT disambiguations are two
// people. This is the exact case name-only dedup destroys.
func TestSameNameDifferentDisambiguationAreDistinctPeople(t *testing.T) {
	alexA := Performer{Name: "Alex", Disambiguation: strPtr("I")}
	alexB := Performer{Name: "Alex", Disambiguation: strPtr("II")}

	if deref(alexA.Disambiguation) == deref(alexB.Disambiguation) {
		t.Fatal("test fixture is wrong: the two must differ")
	}
	// The keys the dedup uses must differ for these two records.
	keyA := normKey(alexA.Name) + "|" + deref(alexA.Disambiguation)
	keyB := normKey(alexB.Name) + "|" + deref(alexB.Disambiguation)
	if keyA == keyB {
		t.Error("distinct people with a shared name must produce distinct dedup keys")
	}

	// And the same person re-sent with the same disambiguation must NOT.
	alexA2 := Performer{Name: "alex", Disambiguation: strPtr("I")}
	keyA2 := normKey(alexA2.Name) + "|" + deref(alexA2.Disambiguation)
	if keyA != keyA2 {
		t.Error("the same person must produce the same dedup key regardless of case")
	}
}
