// Package hydrus renders records this instance holds as Hydrus import JSON (plan slice 13c).
//
// PURE: records in, bytes out. Every decision it makes is about a FORMAT, and a format is exactly the kind
// of thing that should be readable and testable without a database.
//
// THE SHAPE IS HYDRUS'S OWN, read from its documentation rather than recalled: a JSON sidecar is a nested
// object whose `metadata` holds lists of strings under `urls` and `tags`
// (https://hydrusnetwork.github.io/hydrus/advanced_sidecars.html), and notes are the same lists with the
// name and the text joined as `name: text` -- the format's own convention, chosen there precisely because a
// sidecar carries lists of single strings.
//
// HYDROUS SIDECARS ARE PER FILE and this instance has no file: it holds claims about works and the addresses
// of pages about them, and the hashes clients reported. So the document is a BUNDLE of sidecars keyed by the
// record's id, and each VALUE is exactly the documented per-file shape -- which makes splitting it into
// per-record files a key/value loop rather than a transformation.
package hydrus

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Metadata is what a sidecar carries for one record.
//
// The two lists are ALWAYS present, even when empty: a sidecar whose `urls` key is missing is a file an
// importer has to guess about, while an empty list is the record saying it has no addresses, which is true
// here for most records -- this instance holds the addresses it was told about, and no more.
type Metadata struct {
	Tags []string `json:"tags"`
	URLs []string `json:"urls"`
	// Notes, in the format's own convention for them: a note is TWO strings (a name and a text) and a
	// sidecar carries lists of ONE, so they are joined as `name: text`. Keeping them out of `tags` is not a
	// technicality -- an importer that routed `title: ...` to its tag store would give the file a tag for
	// every sentence in its name.
	Notes []string `json:"notes"`
}

// Sidecar is one record's entry.
type Sidecar struct {
	Metadata Metadata `json:"metadata"`
}

// Document is the bundle, keyed by record id.
type Document map[string]Sidecar

// Record is a record as this instance holds it, flattened for rendering.
//
// It is a struct of plain fields rather than a `models.Scene` on purpose: the format's mapping is the thing
// under test, and a test that has to build a Scene, its studio, its performers, its tags and its URLs before
// it can ask "what does a record with no title look like" is a test nobody writes.
type Record struct {
	ID         string
	Title      string
	Details    string
	Code       string
	Date       string
	Studio     string
	Performers []string
	Tags       []string
	URLs       []string
}

// Tag namespaces, and they are this programme's own vocabulary rather than Hydrus's.
//
// EVERY string this document emits as a tag is namespaced, so an importer can take exactly what it wants and
// leave the rest -- and so nothing here can be mistaken for one of Hydrus's own `system:` tags. A member who
// wants our tag names bare uses Hydrus's string processor, which exists for exactly this kind of rewrite
// (the sidecar documentation says so while describing the prefix/filter step).
const (
	TagStudio    = "studio:"
	TagPerformer = "performer:"
	TagSelf      = "tag:"
	TagCode      = "code:"
	TagDate      = "date:"
	NoteTitle    = "title: "
	NoteDetails  = "details: "
)

// SidecarFor renders one record.
//
// THE ORDER IS FIXED (studio, performers as loaded, then our own tags SORTED, then code, then date) and the
// URLs keep the order the loader returned. A document whose bytes changed between two runs over the same
// record would be a document nobody could diff, and sorting only the part whose order carries no meaning is
// how that is achieved without pretending the rest is arbitrary.
func SidecarFor(record Record) Sidecar {
	tags := make([]string, 0, len(record.Performers)+len(record.Tags)+3)

	if strings.TrimSpace(record.Studio) != "" {
		tags = append(tags, TagStudio+clean(record.Studio))
	}
	for _, performer := range record.Performers {
		if strings.TrimSpace(performer) != "" {
			tags = append(tags, TagPerformer+clean(performer))
		}
	}

	// Our own tag vocabulary, sorted: these are the words a member searches by, and their order in the
	// database is an implementation detail of the join rather than part of the record.
	own := make([]string, 0, len(record.Tags))
	for _, tag := range record.Tags {
		if strings.TrimSpace(tag) != "" {
			own = append(own, TagSelf+clean(tag))
		}
	}
	sort.Strings(own)
	tags = append(tags, own...)

	if strings.TrimSpace(record.Code) != "" {
		tags = append(tags, TagCode+clean(record.Code))
	}
	if strings.TrimSpace(record.Date) != "" {
		tags = append(tags, TagDate+clean(record.Date))
	}

	urls := make([]string, 0, len(record.URLs))
	for _, url := range record.URLs {
		if strings.TrimSpace(url) != "" {
			urls = append(urls, strings.TrimSpace(url))
		}
	}

	// The record's NAME and its description are NOTES, which is where a reader of them belongs: a title is a
	// sentence, and Hydrus's tag conventions have no place for one.
	notes := make([]string, 0, 2)
	if strings.TrimSpace(record.Title) != "" {
		notes = append(notes, NoteTitle+clean(record.Title))
	}
	if strings.TrimSpace(record.Details) != "" {
		notes = append(notes, NoteDetails+clean(record.Details))
	}

	return Sidecar{Metadata: Metadata{Tags: tags, URLs: urls, Notes: notes}}
}

// clean normalises tag text the way Hydrus reads it: a tag is ONE line and its outer whitespace is not part
// of it. An embedded newline would split one tag into two on import, which is the difference between a tag
// and a tag plus a stray word.
func clean(text string) string {
	text = strings.ReplaceAll(text, "\r\n", " ")
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.ReplaceAll(text, "\r", " ")
	return strings.Join(strings.Fields(text), " ")
}

// Render produces the document: a bundle of sidecars, indented so a member can read and diff it.
func Render(doc Document) ([]byte, error) {
	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("could not render the export: %w", err)
	}
	return append(body, '\n'), nil
}
