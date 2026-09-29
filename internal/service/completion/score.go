// Package completion computes SPEC §7.7's completion score for an entity.
//
// # The score is DERIVED, never stored
//
// This is the package's central decision and it is not a performance choice. A
// stored completion score is a second source of truth sitting next to the columns
// it summarises, and the first time somebody fixes a birthdate the score is wrong
// and nothing says so. The same argument is why StashForge's `proposal_scores` is
// a VIEW: a decision has to be reproducible from its inputs alone.
//
// Nothing in this database has a trigger that would keep a stored score current
// as edits land, so a stored one would drift silently. Computing on read is
// cheaper to reason about than the trigger that would make storing honest, and
// being wrong about a number that is recomputed is impossible.
//
// # The score is not enough on its own
//
// SPEC §7.7 wants a score, and a score is what a progress bar needs. But a bare
// number tells a curator *that* something is missing and not *what*, and a quest
// phrased "improve this performer" is not actionable. So every score comes with
// the list of named missing fields, computed in the same pass so the two can never
// disagree.
//
// # Uncertain values score as ABSENT
//
// A performer whose birthdate is recorded as `1990` has a *year-accurate*
// birthdate, which is worth less than `1990-01-01` and worth less than none at
// all: it is a specific claim, and a specific claim reads as an answer, so a
// curator who trusts it will not go looking for the real value.
//
// The accuracy lives IN the value, not beside it. Migration 42 converted
// `birthdate date` + `birthdate_accuracy varchar` into a single TEXT column and
// dropped the accuracy column, so '1990' / '1990-01' / '1990-01-01' carry their own
// precision. That is a better encoding than a second column -- there is no way for
// the two to disagree -- and it is why the SQL tests the value's LENGTH rather than
// reading an accuracy column.
package completion

import (
	"errors"
	"fmt"
)

// EntityType is what kind of entity a score is for.
type EntityType string

const (
	EntityPerformer EntityType = "performer"
	EntityScene     EntityType = "scene"
	EntityStudio    EntityType = "studio"
	EntitySite      EntityType = "site"
	EntityTag       EntityType = "tag"
)

// AllEntityTypes is every type the package scores.
var AllEntityTypes = []EntityType{
	EntityPerformer, EntityScene, EntityStudio, EntitySite, EntityTag,
}

// Field is one named piece of metadata a score can be missing.
type Field string

// The fields, as they appear in the missing list and in a quest's wording.
//
// Named constants rather than string literals at the call sites, because a missing
// field is a user-facing string: a quest built from a typo'd field name is a quest
// nobody can complete, and the failure is invisible in the score.
const (
	FieldName            Field = "name"
	FieldAliases         Field = "aliases"
	FieldGender          Field = "gender"
	FieldBirthdate       Field = "birthdate"
	FieldEthnicity       Field = "ethnicity"
	FieldCountry         Field = "country"
	FieldEyeColor        Field = "eye_color"
	FieldHairColor       Field = "hair_color"
	FieldHeight          Field = "height"
	FieldMeasurements    Field = "measurements"
	FieldCareerDates     Field = "career_dates"
	FieldDetails         Field = "details"
	FieldURLs            Field = "urls"
	FieldImage           Field = "image"
	FieldDate            Field = "date"
	FieldStudio          Field = "studio"
	FieldSite            Field = "site"
	FieldPerformers      Field = "performers"
	FieldTags            Field = "tags"
	FieldDuration        Field = "duration"
	FieldSnapshotCoverag Field = "snapshot_coverage"
	FieldParentStudio    Field = "parent_studio"
	FieldRegex           Field = "regex"
)

// ValidFor reports whether a field is one this entity type is scored on.
//
// A loop over the weight list rather than a switch, so a field is valid for a type
// if and only if it appears in that type's list -- adding a field to the list is
// the ONLY edit needed, and there is no second place to forget. A switch here
// would be a second source of truth for the same question, which is exactly the
// bug this package exists to avoid.
func (f Field) ValidFor(entityType EntityType) bool {
	spec, ok := specs[entityType]
	if !ok {
		return false
	}
	for _, fw := range spec {
		if fw.Field == f {
			return true
		}
	}
	return false
}

// The fields each entity type is scored on, WITH their weights.
//
// Weights are not decoration. A scene with no duration cannot be identified —
// there is nothing to seek to — while a scene with no tag can still be found and
// curated. Scoring them equally would rank a perfectly good untagged scene
// alongside an unusable one, and every quest generated from that ranking would
// send curators at the wrong thing.
//
// The weights are all over 100 and the total is normalised at scoring time, so a
// new field can be added with a weight chosen on its own merits rather than
// requiring every other weight to be rebalanced.
var (
	performerFields = []fieldWeight{
		// Always present: `name` is NOT NULL. It is in the list anyway so the
		// per-type lists are the same shape and a caller that iterates them
		// instead of hard-coding "name is always fine" is not wrong.
		{FieldName, 0},

		// Identity. An alias matters because a performer is frequently known by
		// something other than their stage name, and an unaliased performer cannot
		// be found by the name a user remembers.
		{FieldAliases, 10},

		// The single highest-value performer fact, and the one quests ask for most.
		{FieldBirthdate, 15},
		{FieldGender, 5},
		{FieldEthnicity, 3},
		{FieldCountry, 8},
		{FieldEyeColor, 2},
		{FieldHairColor, 2},

		// Body measurements: individually trivial, collectively they are what makes
		// two similarly-named performers distinguishable. Scored as one field
		// because a curator fills them in as a block.
		{FieldHeight, 5},
		{FieldMeasurements, 5},

		{FieldCareerDates, 5},

		{FieldURLs, 10},
		{FieldImage, 10},
		{FieldDetails, 10},
	}

	sceneFields = []fieldWeight{
		{FieldName, 0},

		// Duration is the most important scene fact by a wide margin: the
		// identification board and the snapshot collage both need a time axis, and
		// without one neither can render. Weighted to match.
		{FieldDuration, 20},

		// A scene with no studio cannot be traced to a producer, and a scene with no
		// performers cannot be credited. Both are the minimum for the scene to be
		// findable through the directory.
		{FieldStudio, 15},
		{FieldPerformers, 20},
		{FieldDate, 5},

		// ONE field, not two. scene_urls is (scene_id, site_id, url), so a linked
		// url IS a link to the site it came from: there is no way to have one
		// without the other, and scoring both would credit a single URL twice and
		// inflate every scene's score by a field nobody can fill on its own. The
		// weight is the sum of what the two would have been, because the work a
		// curator does is the same either way.
		{FieldURLs, 18},
		{FieldTags, 7},
		{FieldImage, 5},
		{FieldDetails, 10},

		// Snapshot coverage is SPEC §7.7's own input and SPEC §8's product. It is
		// weighted as a whole: a scene with 3 of the 12 frames a collage needs is
		// not meaningfully more complete than one with none, and a per-frame score
		// would make a barely-started collage look nearly finished.
		{FieldSnapshotCoverag, 10},
	}

	// A studio is name + urls + logo + parent. There is no details column, so
	// there is no details field; a weight list naming a column the schema does not
	// have would put every studio in the archive permanently below 100 and make
	// the completion bar unmoveable, which reads as "this feature is broken".
	studioFields = []fieldWeight{
		{FieldName, 0},
		{FieldURLs, 40},
		{FieldImage, 30},
		// The parent studio is what places a studio in a network, and a studio
		// that is not in a network is much harder to find through the directory.
		{FieldParentStudio, 30},
	}

	// A site is name + url + description + a URL-extraction regex. The regex is
	// what lets Stash import scenes from the site automatically, so it is the
	// field that turns a site from a list entry into a working source -- and it
	// is the one nobody fills in by hand, which is exactly what a quest is for.
	siteFields = []fieldWeight{
		{FieldName, 0},
		{FieldURLs, 35},
		{FieldDetails, 35},
		{FieldRegex, 30},
	}

	// A tag is one name and one description. There is nothing else to have, and
	// inventing fields to fill a bar would mean a well-named tag always scores
	// 100 while the useful information is the description.
	tagFields = []fieldWeight{
		{FieldName, 40},
		{FieldDetails, 60},
	}
)

// fieldWeight is one scored field and how much it is worth.
type fieldWeight struct {
	Field Field
	// Weight is in arbitrary units, normalised per entity type. Zero means the
	// field is expected to always be present and contributes nothing to the
	// denominator.
	Weight int
}

// Result is an entity's completion, computed on read.
type Result struct {
	EntityType EntityType
	// Score is 0-100, rounded. The rounding is in Score() rather than here so a
	// caller doing arithmetic on the raw fraction can still have it.
	Score int
	// Missing is the named fields that are absent, in the order of the weight
	// list, so the most valuable gap is first in a quest's wording.
	Missing []Field
	// Total is the weight of every scored field, and Earned is the weight of the
	// present ones. Exposed because a caller explaining a score ("30 of 100")
	// needs them, and recomputing the denominator from the field list would be a
	// second place for the weights to live.
	Total  int
	Earned int
}

// Score computes an entity's score from a set of present-field booleans.
//
// Takes a set rather than reading the database itself, so the FORMULA is a pure
// function that a test can exercise with hand-written fixtures, and the queries
// that gather the facts are a separate, separately-tested concern.
//
// present is consulted once per field; a field the caller does not mention counts
// as ABSENT, not as present. Defaulting to present would make an incomplete caller
// report a complete entity, which is the failure that matters here: a curator sent
// to fix something that is already fixed.
func Score(entityType EntityType, present map[Field]bool) (Result, error) {
	spec, ok := specs[entityType]
	if !ok {
		return Result{}, fmt.Errorf("%w: %q", ErrUnknownEntityType, entityType)
	}

	score := Result{EntityType: entityType, Missing: []Field{}}
	for _, fw := range spec {
		if fw.Weight == 0 {
			// Always-expected field (the name). Not counted either way, so an
			// entity with a name is not penalised for a field the schema already
			// guarantees.
			continue
		}
		score.Total += fw.Weight
		if present[fw.Field] {
			score.Earned += fw.Weight
			continue
		}
		score.Missing = append(score.Missing, fw.Field)
	}

	score.Score = percent(score.Earned, score.Total)
	return score, nil
}

// percent is the 0-100 conversion, guarding the empty case.
//
// An entity type whose every field is weight-zero would divide by zero. That
// cannot happen with the current lists (each has scored fields), but the guard
// costs one line and a division by zero on a read path is a panic in somebody
// else's request.
func percent(earned, total int) int {
	if total <= 0 {
		return 100
	}
	// Integer arithmetic on purpose: a float would make 1/3 round differently on
	// different paths, and a completion score that differs between two readers of
	// the same row is a bug report waiting to happen.
	return (earned*100 + total/2) / total
}

// ErrUnknownEntityType is returned for a type this version does not score.
//
// A sentinel rather than a bare error, because "this type has no completion spec"
// and "this type's completion could not be computed" want different handling from
// a caller, and only the second is a failure worth retrying.
var ErrUnknownEntityType = errors.New("unknown completion entity type")

// specs is the per-type weight list. The single place a field is declared
// scorable, so a field cannot be valid for one type and absent from another's
// definition without the difference being visible here.
var specs = map[EntityType][]fieldWeight{
	EntityPerformer: performerFields,
	EntityScene:     sceneFields,
	EntityStudio:    studioFields,
	EntitySite:      siteFields,
	EntityTag:       tagFields,
}

// TotalWeight is the weight an entity type can earn in total.
//
// Exposed so a caller rendering "30 of 100" does not sum the weight list itself,
// which would be a second copy of the weights.
func TotalWeight(entityType EntityType) (int, error) {
	spec, ok := specs[entityType]
	if !ok {
		return 0, fmt.Errorf("%w: %q", ErrUnknownEntityType, entityType)
	}
	total := 0
	for _, fw := range spec {
		total += fw.Weight
	}
	return total, nil
}

// Fields returns the fields an entity type is scored on, in weight order.
//
// The ORDER matters and is not incidental: `Missing` is returned in this order so
// the most valuable gap is first, and a quest built from it reads as "add a
// birthdate, then a country" rather than alphabetically.
func Fields(entityType EntityType) ([]Field, error) {
	spec, ok := specs[entityType]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownEntityType, entityType)
	}
	out := make([]Field, 0, len(spec))
	for _, fw := range spec {
		out = append(out, fw.Field)
	}
	return out, nil
}
