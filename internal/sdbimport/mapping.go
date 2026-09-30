package sdbimport

import (
	"strings"
	"time"

	"github.com/stashapp/stash-box/internal/models"
)

// Mapping from source records to destination create inputs.
//
// EVERY FIELD IS EXPLICIT. Nothing is copied by reflection and nothing is
// passed through on a best guess. The reason is specific: this fork's schema is
// a SUBSET of the source's, and a subset is exactly where a reflective copy
// either fails at the last record or — worse — succeeds while writing a field
// the destination does not have, producing a scene that looks imported and is
// missing its studio.
//
// Enum handling is strict. A source value that is not in the destination's
// enum set is DROPPED (the field is left nil) and counted, never coerced to a
// default and never passed through as a raw string. Two failure modes are being
// avoided: writing "TRANSSEXUAL" into a GenderEnum that has no such value, and
// quietly defaulting an unknown value to the first enum member so that a data
// problem reads as data.

// Stats counts what happened, per outcome.
//
// Reported at the end of a run because a run that "succeeded" with 400,000
// dropped enum values is not a success, and the only way to notice is to have
// counted.
type Stats struct {
	Created  int
	Skipped  int
	Failed   int
	Dropped  map[string]int
	FirstErr map[string]string
}

func newStats() *Stats {
	return &Stats{
		Dropped:  map[string]int{},
		FirstErr: map[string]string{},
	}
}

// drop records a field that could not be represented in the destination.
func (s *Stats) drop(field, value string) {
	s.Dropped[field+"="+value]++
}

func (s *Stats) fail(entity string, err error) {
	s.Failed++
	if _, seen := s.FirstErr[entity]; !seen {
		s.FirstErr[entity] = err.Error()
	}
}

// enumOrNil maps a source enum string onto a destination enum.
//
// Returns nil for empty, for the source's "NA" sentinel, and for any value the
// destination does not define. NA in particular must not become a real value:
// the source uses it to mean "not applicable", and mapping it to a concrete
// member would invent a fact about the performer.
func enumOrNil(raw *string, allowed []string) *string {
	if raw == nil {
		return nil
	}
	v := strings.ToUpper(strings.TrimSpace(*raw))
	if v == "" || v == "NA" || v == "UNKNOWN" {
		return nil
	}
	for _, a := range allowed {
		if a == v {
			out := v
			return &out
		}
	}
	return nil
}

// The destination's enum sets, copied here rather than reflected.
//
// A hard-coded list that can drift from the generated enums is a real risk, and
// the failure it produces is silent: a value that stops being accepted starts
// being dropped, and the drop count is the only clue. Each list is checked
// against the generated constants in the test for this file.
var (
	genderValues    = []string{"MALE", "FEMALE", "TRANSGENDER_MALE", "TRANSGENDER_FEMALE", "INTERSEX", "NON_BINARY"}
	ethnicityValues = []string{"CAUCASIAN", "BLACK", "ASIAN", "INDIAN", "LATIN", "MIDDLE_EASTERN", "MIXED", "OTHER"}
	eyeValues       = []string{"BLUE", "BROWN", "GREY", "GREEN", "HAZEL", "RED"}
	hairValues      = []string{"BLONDE", "BRUNETTE", "BLACK", "RED", "AUBURN", "GREY", "BALD", "VARIOUS", "WHITE", "OTHER"}
	breastValues    = []string{"NATURAL", "FAKE", "NA"}
)

// ToCreateInput converts a source performer into a destination create input.
//
// URLs carry a SiteID, which is a uuid the importer cannot invent. They are
// therefore left off and recorded: attaching a URL with a nil or zero SiteID
// would either violate a foreign key or attach the URL to a meaningless site
// row. The performer itself is still imported; only the URL is lost, and the
// loss is counted rather than assumed.
func (p Performer) ToCreateInput(st *Stats) models.PerformerCreateInput {
	in := models.PerformerCreateInput{
		Name:            p.Name,
		Disambiguation:  p.Disambiguation,
		Aliases:         p.Aliases,
		Birthdate:       p.BirthDate,
		Deathdate:       p.DeathDate,
		Country:         p.Country,
		Height:          p.Height.Int(),
		CareerStartYear: p.CareerStartYear.Int(),
		CareerEndYear:   p.CareerEndYear.Int(),
		HipSize:         p.HipSize.Int(),
		WaistSize:       p.WaistSize.Int(),
	}

	if g := enumOrNil(p.Gender, genderValues); g != nil {
		v := models.GenderEnum(*g)
		in.Gender = &v
	} else if p.Gender != nil && strings.ToUpper(*p.Gender) != "NA" {
		st.drop("performer.gender", *p.Gender)
	}

	if e := enumOrNil(p.Ethnicity, ethnicityValues); e != nil {
		v := models.EthnicityEnum(*e)
		in.Ethnicity = &v
	} else if p.Ethnicity != nil && strings.ToUpper(*p.Ethnicity) != "NA" {
		st.drop("performer.ethnicity", *p.Ethnicity)
	}

	if e := enumOrNil(p.EyeColor, eyeValues); e != nil {
		v := models.EyeColorEnum(*e)
		in.EyeColor = &v
	} else if p.EyeColor != nil && strings.ToUpper(*p.EyeColor) != "NA" {
		st.drop("performer.eye_color", *p.EyeColor)
	}

	if e := enumOrNil(p.HairColor, hairValues); e != nil {
		v := models.HairColorEnum(*e)
		in.HairColor = &v
	} else if p.HairColor != nil && strings.ToUpper(*p.HairColor) != "NA" {
		st.drop("performer.hair_color", *p.HairColor)
	}

	if e := enumOrNil(p.BreastType, breastValues); e != nil {
		v := models.BreastTypeEnum(*e)
		in.BreastType = &v
	} else if p.BreastType != nil && strings.ToUpper(*p.BreastType) != "NA" {
		st.drop("performer.breast_type", *p.BreastType)
	}

	// BandSize is declared `Int` by the source and is an *int here, but the
	// source does emit non-numeric strings on some records ("34DD"). FlexInt
	// absorbs those; a value that is still not a number is dropped and COUNTED
	// rather than truncated to a number that was never in the source.
	in.BandSize = p.BandSize.Int()
	if p.BandSize.Invalid != "" {
		st.drop("performer.band_size", p.BandSize.Invalid)
	}
	// Every other int field gets the same treatment, so one dirty record cannot
	// abort a 111k-performer run at page 1.
	for _, f := range []struct {
		name string
		fx   FlexInt
	}{
		{"height", p.Height},
		{"career_start_year", p.CareerStartYear},
		{"career_end_year", p.CareerEndYear},
		{"hip_size", p.HipSize},
		{"waist_size", p.WaistSize},
	} {
		if f.fx.Invalid != "" {
			st.drop("performer."+f.name, f.fx.Invalid)
		}
	}
	if p.CupSize != nil && strings.TrimSpace(*p.CupSize) != "" {
		in.CupSize = p.CupSize
	}

	in.Tattoos = bodyModifications(p.Tattoos, st, "tattoos")
	in.Piercings = bodyModifications(p.Piercings, st, "piercings")

	return in
}

// bodyModifications converts tattoos/piercings, dropping entries with no
// location.
//
// Location is the only required field on BodyModificationInput, and an entry
// with neither location nor description carries no information at all.
func bodyModifications(in []BodyModification, st *Stats, field string) []models.BodyModificationInput {
	var out []models.BodyModificationInput
	for _, m := range in {
		if m.Location == nil || strings.TrimSpace(*m.Location) == "" {
			st.drop("performer."+field, "(no location)")
			continue
		}
		out = append(out, models.BodyModificationInput{
			Location:    strings.TrimSpace(*m.Location),
			Description: m.Description,
		})
	}
	return out
}

// ToCreateInput converts a source scene.
//
// Date is REQUIRED (a plain string, not a pointer) on the destination, which is
// the one field here that cannot be left empty. The source has two date fields
// and this fork has two as well, so they map across directly, but a scene with
// neither still needs something. Those are marked with an explicit sentinel
// rather than today's date: a fabricated date is indistinguishable from a real
// one once written, and an undated scene should be visibly undated.
const unknownDate = "0000-00-00"

// Valid reports whether a date string is usable.
//
// time.Parse is the check rather than a length test, because the source's dates
// come in several shapes and a malformed one written to a date column is a
// database error 200,000 records later rather than a skipped field here.
func ValidDate(s string) bool {
	if strings.TrimSpace(s) == "" {
		return false
	}
	for _, layout := range []string{"2006-01-02", time.RFC3339, "2006-01", "2006"} {
		if _, err := time.Parse(layout, s); err == nil {
			return true
		}
	}
	return false
}
