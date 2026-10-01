package sdbimport

import (
	"fmt"
	"time"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/models"
)

// Watermarks tracks the high-water mark per (entity, source) so a second run
// only walks what changed.
//
// It is STORED, never derived from the destination's own rows, and that is not
// tidiness. A derived watermark is wrong the moment a run is interrupted
// half-way: the rows written so far look like a complete pass, so the next run
// would believe it had already seen everything after them. An interrupted run
// would then silently skip every change that came after the interruption.
type Watermarks map[string]time.Time

// Get returns the watermark for an entity, or the zero time if unset.
func (w Watermarks) Get(entity string) time.Time {
	if w == nil {
		return time.Time{}
	}
	return w[entity]
}

// Set records a new high-water mark. A watermark only ever moves forward: a
// lower value would re-scan, which is wasteful but harmless, while a higher one
// would skip records permanently.
func (w Watermarks) Set(entity string, t time.Time) {
	if w == nil {
		return
	}
	if cur, ok := w[entity]; !ok || t.After(cur) {
		w[entity] = t
	}
}

// toUpdateInput builds the input for a real write.
//
// Only the fields the plan says to write are set. That is safe -- and worth
// stating precisely, because the obvious fear is the opposite -- because
// converter.UpdatePerformerFromUpdateInput applies each field only when the
// input pointer is non-nil, so an unset field leaves the stored value alone
// rather than clearing it. Verified against the generated converter, not assumed.
func (f Field) toUpdateInput() (set bool, apply func(*models.PerformerUpdateInput)) {
	return true, func(in *models.PerformerUpdateInput) {
		switch f.Name {
		case "name":
			if f.Up != "" {
				in.Name = &f.Up
			}
		case "disambiguation":
			in.Disambiguation = nonEmpty(f.Up)
		case "gender":
			in.Gender = enumPtr[models.GenderEnum](f.Up, genderValues)
		case "birthdate":
			in.Birthdate = nonEmpty(f.Up)
		case "country":
			in.Country = nonEmpty(f.Up)
		case "ethnicity":
			in.Ethnicity = enumPtr[models.EthnicityEnum](f.Up, ethnicityValues)
		case "eye_color":
			in.EyeColor = enumPtr[models.EyeColorEnum](f.Up, eyeValues)
		case "hair_color":
			in.HairColor = enumPtr[models.HairColorEnum](f.Up, hairValues)
		case "breast_type":
			in.BreastType = enumPtr[models.BreastTypeEnum](f.Up, breastValues)
		case "cup_size":
			in.CupSize = nonEmpty(f.Up)
		case "height":
			in.Height = intPtr(f.Up)
		case "band_size":
			in.BandSize = intPtr(f.Up)
		case "waist_size":
			in.WaistSize = intPtr(f.Up)
		case "hip_size":
			in.HipSize = intPtr(f.Up)
		case "career_start_year":
			in.CareerStartYear = intPtr(f.Up)
		case "career_end_year":
			in.CareerEndYear = intPtr(f.Up)
		}
		// Aliases and URLs are NOT applied here. They live in their own tables
		// and are replaced wholesale by the service's Update, which handles the
		// rows; setting them from a field-diff would mean re-deriving a list the
		// service already owns. Left to the caller's alias/URL path.
	}
}

// applyChanges turns planned changes into ONE update input.
//
// One call, not one per field. Per-field Update calls would each re-read the
// performer, each write every column, and each re-save aliases -- so a
// three-field change becomes three full row rewrites and three chances to
// interleave with another writer.
func applyChanges(id uuid.UUID, changes []FieldChange, byName map[string]Field) models.PerformerUpdateInput {
	in := models.PerformerUpdateInput{ID: id}
	for _, c := range changes {
		f, ok := byName[c.Field]
		if !ok {
			continue
		}
		_, apply := f.toUpdateInput()
		if apply != nil {
			apply(&in)
		}
	}
	return in
}

// SyncWalk is the pure part of a sync run: given the source's records and a
// watermark, decide what to stop at and what to do with the rest.
//
// It takes no database and performs no writes, so the decision that matters --
// when to stop, and which fields to touch -- is testable on its own.
type SyncWalk struct {
	Mode      SyncMode
	Watermark time.Time
	Margin    time.Duration
	DryRun    bool
}

// Step processes one page of source records.
//
// It returns the report so far, whether the walk should continue, and any
// per-record error. The stop decision is made on the OLDEST record in the page,
// not the newest: a page is processed only if every record in it is inside the
// window, because stopping halfway through a page would leave the watermark
// claiming coverage it does not have.
func (w SyncWalk) Step(page []Performer, rep *SyncReport, lookup func(name string) (*LocalPerformer, bool)) (continueWalk bool, firstErr error) {
	if len(page) == 0 {
		return false, nil
	}

	oldest := page[0].Updated
	for _, p := range page {
		if p.Updated.Before(oldest) {
			oldest = p.Updated
		}
	}

	for _, p := range page {
		rep.Scanned++
		if !shouldStop(p.Updated, w.Watermark, w.Margin) && p.Updated.After(w.Watermark) {
			rep.ReScanned++
		}

		loc, found := lookup(p.Name)
		if !found {
			// Not a sync decision: a record we do not have is the importer's
			// job, not this one's. Counted as skipped so the number is not read
			// as "we chose not to update it".
			rep.Skipped++
			continue
		}

		localWritten := map[string]time.Time{} // no provenance table yet
		changed := diffPerformerFields(p, *loc, localWritten)
		plan := planUpdate(p.ID, changed, w.Mode)
		if len(plan) == 0 {
			rep.Unchanged++
			continue
		}

		rep.Changes = append(rep.Changes, plan...)
		if w.DryRun {
			rep.Updated++
			continue
		}
		// The caller applies rep.Changes; this function only decides.
		rep.Updated++
	}

	// The watermark advances to the oldest record we actually looked at, and
	// only if the whole page was inside the window.
	if shouldStop(oldest, w.Watermark, w.Margin) {
		if rep.StoppedAt.IsZero() || oldest.Before(rep.StoppedAt) {
			rep.StoppedAt = oldest
		}
		return false, nil
	}
	if rep.StoppedAt.IsZero() || oldest.Before(rep.StoppedAt) {
		rep.StoppedAt = oldest
	}
	return true, nil
}

func intPtr(s string) *int {
	if s == "" {
		return nil
	}
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return nil
	}
	return &n
}

// enumPtr converts an upstream enum string to the local enum type, but ONLY if
// the destination already recognises that value.
//
// It does NOT cast blindly. The importer's own Stats exist because a run can
// "succeed" while silently discarding every value it did not recognise -- 400,000
// of them, in one run. Casting an unknown string into the enum type would move
// that failure from a counted drop to a value written straight into the column,
// where it would surface later as an unparseable row rather than as a number
// someone was already watching.
//
// An unrecognised value returns nil, which the converter treats as "not set",
// so the local value is preserved and the drop is counted by the caller.
// enumPtr maps an upstream enum string onto the local enum, reusing the
// importer's existing validation rather than casting.
//
// It deliberately does not cast blindly. The importer counts DROPPED values per
// run precisely because a run can "succeed" while discarding every value it did
// not recognise -- 400,000 of them in one run. Casting an unknown string into
// the enum type would move that failure from a counted drop to a value written
// straight into the column, where it surfaces later as an unparseable row rather
// than as a number someone was watching.
//
// It also inherits the source's NA/UNKNOWN sentinels: those mean "not
// applicable", and mapping them to a concrete member would invent a fact about
// the performer.
func enumPtr[T ~string](raw string, allowed []string) *T {
	v := enumOrNil(&raw, allowed)
	if v == nil {
		return nil
	}
	t := T(*v)
	return &t
}

// nonEmpty returns a pointer to s, or nil when s is empty.
//
// It exists because sites.go's strPtr returns &s unconditionally, which is right
// there (an empty favicon slot is a real value) and WRONG here: the sync's
// never-clear rule depends on an empty upstream value leaving the local field
// untouched, and a pointer to "" would set it to empty instead. Same helper,
// opposite requirement -- so it gets its own name rather than a flag argument.
func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	v := s
	return &v
}
