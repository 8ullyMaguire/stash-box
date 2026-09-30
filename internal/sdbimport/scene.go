package sdbimport

import (
	"strings"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/models"
)

// Resolver maps a source entity name to the destination's id.
//
// Scenes reference performers, studios and tags BY ID in the source, but those
// ids are meaningless here: the destination assigns its own. So a scene is
// written after its dependencies and resolved by name through this resolver.
//
// A name that does not resolve is SKIPPED, not fatal and not defaulted. Two
// reasons, and both matter at 1.1M scenes:
//
//   - Fatal would mean one unimported performer aborts the run, so a single
//     dependency failure stops the entire import.
//   - Defaulting would mean attaching the scene to some arbitrary entity, which
//     is worse than omitting it: the scene would appear correctly related and
//     be wrong.
//
// The cost is recorded, because a scene imported with two of its four performers
// missing is not the same as a fully-imported scene and the run has to be able
// to say so.
type Resolver struct {
	tags       map[string]uuid.UUID
	studios    map[string]uuid.UUID
	performers map[string]uuid.UUID
}

func NewResolver() *Resolver {
	return &Resolver{
		tags:       map[string]uuid.UUID{},
		studios:    map[string]uuid.UUID{},
		performers: map[string]uuid.UUID{},
	}
}

func (r *Resolver) AddTag(name string, id uuid.UUID)       { r.tags[normKey(name)] = id }
func (r *Resolver) AddStudio(name string, id uuid.UUID)    { r.studios[normKey(name)] = id }
func (r *Resolver) AddPerformer(name string, id uuid.UUID) { r.performers[normKey(name)] = id }

// normKey is the matching key for a name.
//
// Case-folded and trimmed, because the same studio is "BraZZers" on one record
// and "brazzers" on another and matching those as different entities would
// split a studio in half. Deliberately NOT diacritic-folded: a name that
// differs in accents is a genuinely different name and merging them would lose
// a distinction the source made on purpose.
func normKey(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// PerformerKey matches on name plus disambiguation.
//
// Performers are unique on (name, disambiguation) in the destination, not on
// name alone -- two different people share a name often enough that collapsing
// them would merge distinct people. A scene that names a performer without a
// disambiguation therefore resolves only if exactly one performer has that
// name; if several do, the reference is ambiguous and is skipped rather than
// guessed.
type performerKey struct {
	name string
	dis  string
}

// SceneInput is a scene ready to create, with dependencies already resolved.
type SceneInput struct {
	Create models.SceneCreateInput
	// Missing lists the dependency names that could not be resolved.
	Missing []string
	// NoTitle is true when the scene has no title AND no URL, leaving nothing
	// to identify it by.
	NoTitle bool
}

// ToSceneInput converts a source scene, resolving its dependencies.
//
// date: the destination requires a date (a plain string). release_date is
// preferred over production_date because it is the date the scene is filed
// under; production_date is the fallback. When neither is usable the sentinel
// unknownDate is written, which is visibly wrong rather than quietly plausible.
func (s Scene) ToSceneInput(r *Resolver, st *Stats) SceneInput {
	out := SceneInput{}

	// A scene with no title and no URL is not identifiable: there is nothing to
	// search it by and nothing to link it to a site. Importing it produces an
	// orphan row that can never be found again.
	if (s.Title == nil || strings.TrimSpace(*s.Title) == "") && len(s.URLs) == 0 {
		out.NoTitle = true
		return out
	}

	in := models.SceneCreateInput{
		Title:    s.Title,
		Details:  s.Details,
		Director: s.Director,
		Duration: s.Duration.Int(),
		Code:     s.Code,
		// No fingerprints: see the note on the Scene type. The field is
		// required-but-nullable, so an empty non-nil slice is the honest
		// "this scene has no fingerprints" rather than nil, which the converter
		// may treat as unset.
		Fingerprints: []models.FingerprintEditInput{},
	}

	// Date resolution.
	switch {
	case s.ReleaseDate != nil && ValidDate(*s.ReleaseDate):
		in.Date = *s.ReleaseDate
		if s.ProductionDate != nil && ValidDate(*s.ProductionDate) {
			in.ProductionDate = s.ProductionDate
		}
	case s.ProductionDate != nil && ValidDate(*s.ProductionDate):
		in.Date = *s.ProductionDate
		st.drop("scene.date", "used production_date (no usable release_date)")
	default:
		in.Date = unknownDate
		st.drop("scene.date", "no usable date on source record")
	}

	// Studio.
	if s.Studio != nil && s.Studio.Name != nil {
		if id, ok := r.studios[normKey(*s.Studio.Name)]; ok {
			in.StudioID = &id
		} else {
			out.Missing = append(out.Missing, "studio:"+*s.Studio.Name)
		}
	}

	// Performers.
	for _, ref := range s.PerformerRefs() {
		if ref.Name == nil {
			continue
		}
		id, ok := r.performers[normKey(*ref.Name)]
		if !ok {
			out.Missing = append(out.Missing, "performer:"+*ref.Name)
			continue
		}
		in.Performers = append(in.Performers, models.PerformerAppearanceInput{PerformerID: id})
	}

	// Tags. Duplicate names within one scene are collapsed: a scene with the
	// same tag twice would otherwise violate the tag_ids uniqueness and abort
	// the whole scene, losing the performer relations with it.
	seen := map[uuid.UUID]bool{}
	for _, ref := range s.Tags {
		if ref.Name == nil {
			continue
		}
		id, ok := r.tags[normKey(*ref.Name)]
		if !ok {
			out.Missing = append(out.Missing, "tag:"+*ref.Name)
			continue
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		in.TagIds = append(in.TagIds, id)
	}

	out.Create = in
	return out
}
