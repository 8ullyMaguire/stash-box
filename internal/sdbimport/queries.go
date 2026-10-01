package sdbimport

import "time"

// Queries against a stash-box-compatible source.
//
// FIELD NAMES HERE WERE VERIFIED AGAINST THE LIVE SCHEMA, not assumed. An
// earlier draft guessed `birthdate`, `date`, `weight`, `measurements` and
// `country` on Performer and `date` on Scene; the real fields are
// `birth_date`, `birthdate` does not exist, Scene has `release_date` and
// `production_date` and no `date` at all, and Performer has no `measurements`
// or `weight`. Guessing produced a query that fails validation on the first
// request, which is at least loud — the dangerous version is a query that
// validates and silently returns nulls.
//
// Field selections are minimal on purpose. The full stashdb schema exposes
// dozens of fields per entity, and requesting all of them "in case they are
// useful later" is the difference between hours and a day at 1.1M scenes.

// pagedFields is the page size used everywhere.
//
// 5,000 is the largest the source accepts, verified by probing 100/500/1000/
// 5000 and watching the returned count.
const pagedFields = 5000

// TotalPages converts a record count into a page count at pagedFields per page.
//
// Needed because the source's paged result types expose ONLY `count` and the
// entity list -- there is no `page` and no `num_pages`, verified against
// QueryPerformersResultType/QueryScenesResultType/QueryTagsResultType. A
// paginator that assumes those fields exist gets a validation error on the
// first request, so the arithmetic is done here instead.
func TotalPages(count int) int {
	if count <= 0 {
		return 0
	}
	return (count + pagedFields - 1) / pagedFields
}

// CountPerformers is the first thing a run does: it decides whether the plan is
// minutes or hours, and it is the denominator that makes progress and
// completion mean something rather than being a guess.
const CountPerformers = `{ queryPerformers(input: {per_page: 1}) { count } }`

const performerQuery = `
query($page: Int!) {
  queryPerformers(input: {per_page: 5000, page: $page}) {
    count
    performers {
      id
      name
      disambiguation
      gender
      country
      ethnicity
      eye_color
      hair_color
      height
      birth_date
      death_date
      career_start_year
      career_end_year
      band_size
      breast_type
      cup_size
      hip_size
      waist_size
      tattoos { description location }
      piercings { description location }
      aliases
      urls { url }
    }
  }
}`

// Performer is a source performer.
//
// The field set is exactly what the source offers and no more. `age`, `scene_count`
// and `is_favorite` are omitted: all three are derived or account-local state,
// and importing them would write a number the destination recomputes anyway.
type Performer struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Disambiguation  *string `json:"disambiguation"`
	Gender          *string `json:"gender"`
	Country         *string `json:"country"`
	Ethnicity       *string `json:"ethnicity"`
	EyeColor        *string `json:"eye_color"`
	HairColor       *string `json:"hair_color"`
	Height          FlexInt `json:"height"`
	BirthDate       *string `json:"birth_date"`
	DeathDate       *string `json:"death_date"`
	CareerStartYear FlexInt `json:"career_start_year"`
	CareerEndYear   FlexInt `json:"career_end_year"`
	BandSize        FlexInt `json:"band_size"`
	BreastType      *string `json:"breast_type"`
	CupSize         *string `json:"cup_size"`
	HipSize         FlexInt `json:"hip_size"`
	WaistSize       FlexInt `json:"waist_size"`
	// Tattoos and Piercings are LISTS OF OBJECTS in the source
	// ([BodyModification!] = {description, location}), not strings. Typing them
	// as *string made the query fail validation demanding a selection on them.
	Tattoos   []BodyModification `json:"tattoos"`
	Piercings []BodyModification `json:"piercings"`
	Aliases   []string           `json:"aliases"`
	URLs      []struct {
		URL string `json:"url"`
	} `json:"urls"`

	// Updated is the source's last-write time for the whole record. It is what
	// the incremental sync sorts on and compares against the watermark; see
	// sync.go's shouldStop for why it is not filtered server-side.
	Updated time.Time `json:"updated"`
}

const sceneQuery = `
query($page: Int!) {
  queryScenes(input: {per_page: 5000, page: $page}) {
    count
    scenes {
      id
      title
      details
      director
      duration
      release_date
      production_date
      code
      urls { url }
      studio { id name }
      performers { performer { id name } }
      tags { id name }
    }
  }
}`

// Scene is a source scene.
//
// FINGERPRINTS ARE DELIBERATELY NOT IMPORTED, and this is the most important
// omission in the file. A fingerprint identifies a specific video file. The
// source's fingerprints point at files that do not exist on this instance, so
// importing them would create rows that resolve to nothing while making
// scene-by-fingerprint LOOK populated — an instance that appears to have
// fingerprint coverage and cannot match a single upload. A scene with no
// fingerprints is also exactly what a hand-added scene looks like, so the empty
// state is honest rather than broken.
type Scene struct {
	ID       string  `json:"id"`
	Title    *string `json:"title"`
	Details  *string `json:"details"`
	Director *string `json:"director"`
	Duration FlexInt `json:"duration"`
	// ReleaseDate and ProductionDate are both carried. They are different facts
	// (when a scene was published vs when it was produced) and collapsing them
	// loses one; the destination has only one date field, so the importer
	// prefers release_date and falls back to production_date.
	ReleaseDate    *string `json:"release_date"`
	ProductionDate *string `json:"production_date"`
	Code           *string `json:"code"`
	URLs           []struct {
		URL string `json:"url"`
	} `json:"urls"`
	Studio *EntityRef `json:"studio"`
	// Performers is [PerformerAppearance!] = {performer}, so it is one level
	// deeper than the tags list beside it. Requesting `performers { id name }`
	// fails validation: PerformerAppearance exposes only `performer`.
	Performers []struct {
		Performer EntityRef `json:"performer"`
	} `json:"performers"`
	Tags []EntityRef `json:"tags"`
}

// PerformerRefs flattens the appearance wrapper so callers get plain refs.
func (s Scene) PerformerRefs() []EntityRef {
	out := make([]EntityRef, 0, len(s.Performers))
	for _, pa := range s.Performers {
		out = append(out, pa.Performer)
	}
	return out
}

// BodyModification is a tattoo or piercing. Both fields are nullable, and a
// modification with neither is dropped rather than imported as a blank row.
type BodyModification struct {
	Description *string `json:"description"`
	Location    *string `json:"location"`
}

// EntityRef references an entity by id and name. Only the name is used: the
// destination assigns its own ids, and matching on the source id would create a
// false impression that the two instances share an id space. They do not.
type EntityRef struct {
	ID   string  `json:"id"`
	Name *string `json:"name"`
}

const tagQuery = `
query($page: Int!) {
  queryTags(input: {per_page: 5000, page: $page}) {
    count
    tags { id name description aliases }
  }
}`

// Tag is a source tag.
type Tag struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description *string  `json:"description"`
	Aliases     []string `json:"aliases"`
}

const studioQuery = `
query($page: Int!) {
  queryStudios(input: {per_page: 5000, page: $page}) {
    count
    studios { id name urls { url } parent { id name } }
  }
}`

// Studio is a source studio.
type Studio struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URLs []struct {
		URL string `json:"url"`
	} `json:"urls"`
	Parent *EntityRef `json:"parent"`
}

const siteQuery = `
query {
  querySites {
    count
    sites { id name url }
  }
}`

// Site is a source site.
//
// NOTE the singular `url`: unlike every other entity, Site has `url: String` and
// NOT `urls: [SiteURL]`. Requesting `urls` fails validation outright.
type Site struct {
	ID   string  `json:"id"`
	Name string  `json:"name"`
	URL  *string `json:"url"`
}

// Count queries. Separate from the paged queries because the result type of a
// paged query carries the list too, and a run that only wants the total would
// download 5,000 records to learn a number.
const (
	CountTags    = `{ queryTags(input: {per_page: 1}) { count } }`
	CountStudios = `{ queryStudios(input: {per_page: 1}) { count } }`
	CountScenes  = `{ queryScenes(input: {per_page: 1}) { count } }`
)

// URLStrings flattens a performer's URL objects to plain strings.
func (p Performer) URLStrings() []string {
	out := make([]string, 0, len(p.URLs))
	for _, u := range p.URLs {
		if u.URL != "" {
			out = append(out, u.URL)
		}
	}
	return out
}

// URLStrings flattens a scene's URL objects to plain strings.
func (s Scene) URLStrings() []string {
	out := make([]string, 0, len(s.URLs))
	for _, u := range s.URLs {
		if u.URL != "" {
			out = append(out, u.URL)
		}
	}
	return out
}

// SiteCountQuery is separate from siteQuery because querySites takes no input
// and is not paged: it is the only entity whose count and rows come back
// together.
const SiteCountQuery = `{ querySites { count } }`
