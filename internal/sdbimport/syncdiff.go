package sdbimport

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/stashapp/stash-box/internal/models"
)

// LocalPerformer is the destination's view of one performer, as the sync needs
// it: the scalar columns from `performers`, plus the two relations that live in
// their own tables.
//
// It is a separate type from models.Performer on purpose. Aliases and URLs are
// NOT columns on that struct -- they are separate tables joined in at read time --
// so a diff built against it would silently never compare them, and "no aliases
// changed" would be indistinguishable from "aliases were not looked at".
type LocalPerformer struct {
	models.Performer
	Aliases []string
	URLs    []string
}

// Field is one syncable attribute of a performer.
//
// Local is what the destination holds; Up is what the source reports. Both are
// rendered to strings so that one comparison, one report line and one log line
// all speak the same representation. An enum pointer and a source string are
// different types with the same meaning, and normalising them once here is what
// stops that mismatch from becoming a bug.
type Field struct {
	Name  string
	Local string
	Up    string

	// LocalEmpty means the destination has no value here. Only consulted by
	// local-wins, where "empty" is the answer to whether upstream may write.
	LocalEmpty bool

	// LocalWrittenAt is when this fork last wrote the field. Zero means never --
	// the pre-sync state. Only consulted by latest-wins.
	LocalWrittenAt time.Time

	// UpUpdated is the source's `updated` for the whole record, used as the
	// upstream side's write time in latest-wins. See shouldWrite.
	UpUpdated time.Time

	// notWritable marks a difference this sync records but does not plan: the value cannot be written, so
	// planning it would produce a change that is applied as a no-op and re-detected on every run.
	notWritable bool
}

// fieldDiff is the closed list of syncable fields, and the only place one is
// added.
//
// A reflected walk over the structs would start syncing a new column the moment
// someone added it to the source type, which is precisely the change nobody
// reviewed. An explicit list makes a new field a deliberate decision, and lets a
// field be excluded on purpose.
type fieldDiff struct {
	name string
	// local and up render their side to a comparable string.
	local func(l LocalPerformer) string
	up    func(u Performer) string
	// neverSync is set for fields deliberately excluded from syncing.
	neverSync bool
	// writable, when set, answers whether an upstream value is one the destination can represent at all.
	//
	// It exists because "we cannot write this value" and "this value changed" are different statements, and
	// conflating them makes the diff never converge: an enum whose upstream value is the source's "NA"
	// sentinel -- meaning "not applicable" -- was planned as a change on every single run, written as the
	// no-op `enumOrNil` deliberately makes it, and re-detected next time. The report then said "updated 49"
	// for 49 records where nothing had changed and nothing would.
	writable func(up string) bool
}

// syncableFields is ordered for readable reports, not for lookup speed.
var syncableFields = []fieldDiff{
	{name: "name",
		local: func(l LocalPerformer) string { return l.Name },
		up:    func(u Performer) string { return u.Name }},

	{name: "disambiguation",
		local: func(l LocalPerformer) string { return normStr(l.Disambiguation) },
		up:    func(u Performer) string { return normStr(u.Disambiguation) }},

	{name: "gender", writable: representable(genderValues),
		local: func(l LocalPerformer) string { return enumStr(l.Gender) },
		up:    func(u Performer) string { return normStr(u.Gender) }},

	{name: "birthdate",
		local: func(l LocalPerformer) string { return normStr(l.BirthDate) },
		up:    func(u Performer) string { return normStr(u.BirthDate) }},

	{name: "country",
		local: func(l LocalPerformer) string { return normStr(l.Country) },
		up:    func(u Performer) string { return normStr(u.Country) }},

	{name: "ethnicity", writable: representable(ethnicityValues),
		local: func(l LocalPerformer) string { return enumStr(l.Ethnicity) },
		up:    func(u Performer) string { return normStr(u.Ethnicity) }},

	{name: "eye_color", writable: representable(eyeValues),
		local: func(l LocalPerformer) string { return enumStr(l.EyeColor) },
		up:    func(u Performer) string { return normStr(u.EyeColor) }},

	{name: "hair_color", writable: representable(hairValues),
		local: func(l LocalPerformer) string { return enumStr(l.HairColor) },
		up:    func(u Performer) string { return normStr(u.HairColor) }},

	{name: "breast_type", writable: representable(breastValues),
		local: func(l LocalPerformer) string { return enumStr(l.BreastType) },
		up:    func(u Performer) string { return normStr(u.BreastType) }},

	{name: "cup_size",
		local: func(l LocalPerformer) string { return normStr(l.CupSize) },
		up:    func(u Performer) string { return normStr(u.CupSize) }},

	{name: "height",
		local: func(l LocalPerformer) string { return intStr(l.Height) },
		up:    func(u Performer) string { return flexStr(u.Height) }},

	{name: "band_size",
		local: func(l LocalPerformer) string { return intStr(l.BandSize) },
		up:    func(u Performer) string { return flexStr(u.BandSize) }},

	{name: "waist_size",
		local: func(l LocalPerformer) string { return intStr(l.WaistSize) },
		up:    func(u Performer) string { return flexStr(u.WaistSize) }},

	{name: "hip_size",
		local: func(l LocalPerformer) string { return intStr(l.HipSize) },
		up:    func(u Performer) string { return flexStr(u.HipSize) }},

	{name: "career_start_year",
		local: func(l LocalPerformer) string { return intStr(l.CareerStartYear) },
		up:    func(u Performer) string { return flexStr(u.CareerStartYear) }},

	{name: "career_end_year",
		local: func(l LocalPerformer) string { return intStr(l.CareerEndYear) },
		up:    func(u Performer) string { return flexStr(u.CareerEndYear) }},
}

// diffPerformerFields returns the fields that differ between the source record
// and the destination's, in report order.
//
// Lists (aliases, URLs) are handled separately from syncableFields because they
// compare as sets and live in their own tables, so they cannot be rendered by a
// per-field closure over the two structs.
func diffPerformerFields(up Performer, loc LocalPerformer, localWritten map[string]time.Time) []Field {
	out := make([]Field, 0, len(syncableFields)+2)

	for _, fd := range syncableFields {
		if fd.neverSync {
			continue
		}
		l, r := fd.local(loc), fd.up(up)
		if fd.writable != nil && !fd.writable(r) {
			// A value the destination cannot represent is not a change. It is not a DROP either, which is why
			// it is not counted: dropped means "the source has data we chose not to keep" and this means "the
			// source has no data".
			continue
		}
		// An ABSENT upstream value is never a request to clear the local one.
		//
		// The wire format cannot distinguish "the source has no value for this
		// field" from "this query did not ask for the field". Both arrive as
		// empty. Treating them as the first would clear a curator's height
		// because the source happened not to carry one -- irreversible, silent,
		// and discovered only by noticing a data loss weeks later.
		//
		// So the rule is one-directional: upstream may fill or change, never
		// clear. A genuine upstream deletion (a URL removed, an alias retracted)
		// will not propagate, and that is the trade: the safe failure is a stale
		// value, not a destroyed one.
		if r != "" && !fieldEq(fd.name, l, r) {
			out = append(out, Field{
				Name:           fd.name,
				Local:          l,
				Up:             r,
				LocalEmpty:     l == "",
				LocalWrittenAt: localWritten[fd.name],
			})
		}
	}

	if len(up.Aliases) > 0 && !listEq(loc.Aliases, up.Aliases) {
		out = append(out, Field{
			Name:           "aliases",
			Local:          strings.Join(loc.Aliases, ", "),
			Up:             strings.Join(up.Aliases, ", "),
			LocalEmpty:     len(loc.Aliases) == 0,
			LocalWrittenAt: localWritten["aliases"],
		})
	}

	upURLs := make([]string, 0, len(up.URLs))
	for _, u := range up.URLs {
		upURLs = append(upURLs, u.URL)
	}
	if len(upURLs) > 0 && !listEq(loc.URLs, upURLs) {
		// A URL DIFFERENCE IS CAPTURED, NOT PLANNED.
		//
		// The sync cannot write a URL: a URL carries a SiteID, a uuid this code cannot invent, so attaching one
		// would violate a foreign key or attach it to a meaningless site row. The import path made the same
		// choice and recorded it (mapping.go). Planning it anyway made every run report the same performers as
		// changed -- writes that were deliberately no-ops, re-detected forever, inflating "updated" until the
		// number meant nothing.
		//
		// It is counted instead, so a reader still sees that URLs differ and the report does not claim the two
		// sides agree about them.
		out = append(out, Field{
			Name:           "urls",
			Local:          strings.Join(loc.URLs, ", "),
			Up:             strings.Join(upURLs, ", "),
			LocalEmpty:     len(loc.URLs) == 0,
			LocalWrittenAt: localWritten["urls"],
			notWritable:    true,
		})
	}

	return out
}

// fieldEq is the comparison for one field, with the two special cases called out
// rather than buried.
//
// The name is the only field compared case-insensitively. A case-only difference
// is the same name -- "Jane Doe" and "jane doe" -- and treating it as a change
// would rewrite every record on every run for no reason.
func fieldEq(name, l, r string) bool {
	if name == "name" {
		return strings.EqualFold(strings.TrimSpace(l), strings.TrimSpace(r))
	}
	return l == r
}

// shouldWrite decides whether a differing field is written, per mode.
//
// Latest-wins is an APPROXIMATION and the approximation is deliberate. We can
// observe when this fork last wrote a field, and when upstream last touched the
// record -- but not when upstream last touched THIS field. Using the record's
// `updated` as the field's write time is right whenever upstream's edit is its
// own reason to revisit the record, which is the normal case. It is wrong when a
// curator here edited the same field after upstream did, within the same window:
// upstream's older correction would then look newer than it is and win.
//
// The alternative -- real per-field provenance on the upstream side -- is not ours
// to build. The approximation is stated here rather than hidden, so a reader can
// judge it.
func shouldWrite(f Field, mode SyncMode) bool {
	switch mode {
	case ModeUpstream:
		return true
	case ModeLocal:
		// Only fill a gap. A field the destination already has is left alone,
		// including when upstream disagrees.
		return f.LocalEmpty
	case ModeLatest:
		if f.LocalWrittenAt.IsZero() {
			// Never written locally: upstream can only be an improvement.
			return true
		}
		return f.UpUpdated.After(f.LocalWrittenAt)
	}
	return false
}

// planUpdate turns a set of differing fields into the changes to report.
//
// Kept separate from the write so a dry run and a real run compute the same plan
// and differ only in whether it is applied. A dry run that derives its report by
// a different path is not evidence about the real path.
// planUpdate also reports how many differences it declined to plan, so a caller can say "this record has
// differences, none of which are writable" rather than silently reading as "nothing to do".
func planUpdate(upID string, fields []Field, mode SyncMode) (changes []FieldChange, notWritable int) {
	for _, f := range fields {
		if f.notWritable {
			notWritable++
			continue
		}
		if shouldWrite(f, mode) {
			changes = append(changes, FieldChange{EntityID: upID, Field: f.Name, Old: f.Local, New: f.Up})
		}
	}
	return changes, notWritable
}

// representable reports whether an upstream value is one the destination's enum set contains.
//
// It is the same rule enumOrNil applies at the write, asked one step earlier -- and asking it here is the whole
// difference between "we will not write this" and "this changed".
func representable(allowed []string) func(string) bool {
	return func(up string) bool { return enumOrNil(&up, allowed) != nil }
}

// --- rendering helpers -----------------------------------------------------
//
// Each renders one side to a comparable string. nil and empty collapse to "",
// because they are different states in the database but the same statement, and
// separating them would report a change on every field the source never sets.

func normStr(p *string) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(*p)
}

func intStr(p *int) string {
	if p == nil {
		return ""
	}
	return strconv.Itoa(*p)
}

func flexStr(f FlexInt) string {
	if f.Value == nil {
		return ""
	}
	return strconv.Itoa(*f.Value)
}

// enumStr renders a local enum pointer. Every enum in models is a named string
// type with a String() method; a nil renders as "".
func enumStr[T fmt.Stringer](p *T) string {
	if p == nil {
		return ""
	}
	// The constraint is Stringer, so *T satisfies it too -- Go does not infer
	// that automatically for a generic pointer receiver.
	var s fmt.Stringer = *p
	return s.String()
}
