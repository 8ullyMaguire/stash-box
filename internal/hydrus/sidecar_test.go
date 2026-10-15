package hydrus

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// THE FORMAT, against the shape Hydrus documents for a JSON sidecar: a nested object whose `metadata` holds
// lists of strings, with notes joined as `name: text` (the format's own convention, chosen there because a
// sidecar carries lists of single strings).
//
// Every test here parses the rendered bytes back with encoding/json and asserts on the RESULT rather than on
// a substring: an export that only looks right is the failure mode of hand-built JSON, and Go's encoder
// HTML-escapes (`<` becomes `\u003c`) so a string comparison would be asserting about the encoder.

func parse(t *testing.T, record Record) map[string]any {
	t.Helper()
	body, err := Render(Document{"a-record": SidecarFor(record)})
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(body, &parsed), "the document must be readable JSON: %s", body)
	entry, ok := parsed["a-record"].(map[string]any)
	require.True(t, ok, "the key is the record's id: %s", body)
	return entry
}

func metadata(t *testing.T, record Record) map[string]any {
	t.Helper()
	entry := parse(t, record)
	meta, ok := entry["metadata"].(map[string]any)
	require.True(t, ok, "each entry is the documented sidecar shape")
	return meta
}

func stringsOf(t *testing.T, meta map[string]any, key string) []string {
	t.Helper()
	raw, ok := meta[key].([]any)
	require.True(t, ok, "%q must be present as a list", key)
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		out = append(out, item.(string))
	}
	return out
}

func TestASidecarIsTheShapeHydrusDocuments(t *testing.T) {
	meta := metadata(t, Record{ID: "a-record"})

	// All three lists are ALWAYS present, even empty: an importer that has to guess about a missing key is
	// being told less than the record itself knows.
	assert.Empty(t, stringsOf(t, meta, "tags"))
	assert.Empty(t, stringsOf(t, meta, "urls"))
	assert.Empty(t, stringsOf(t, meta, "notes"))
}

func TestEveryTagIsNamespacedAndTheRecordsOwnAreSorted(t *testing.T) {
	meta := metadata(t, Record{
		ID:         "a-record",
		Studio:     "A Studio",
		Code:       "ABC-123",
		Date:       "2026-10-15",
		Performers: []string{"Zoe", "Ana"},
		Tags:       []string{"zeta", "alpha", "beta"},
	})

	tags := stringsOf(t, meta, "tags")
	assert.Equal(t, []string{
		"studio:A Studio",
		"performer:Zoe", "performer:Ana",
		"tag:alpha", "tag:beta", "tag:zeta",
		"code:ABC-123",
		"date:2026-10-15",
	}, tags)
}

// A TITLE IS NOT A TAG, and this is the failure the split exists to prevent: an importer that routed
// `title: ...` into its tag store would give the file a tag for every sentence in its name.
func TestTheTitleAndDescriptionAreNotesRatherThanTags(t *testing.T) {
	meta := metadata(t, Record{ID: "a-record", Title: "A Title", Details: "A description"})

	assert.Equal(t, []string{"title: A Title", "details: A description"}, stringsOf(t, meta, "notes"))
	for _, tag := range stringsOf(t, meta, "tags") {
		assert.NotContains(t, tag, "A Title")
		assert.NotContains(t, tag, "A description")
	}
}

// A tag is ONE line. An embedded newline would arrive as two tags on import, which is a tag plus a stray
// word rather than the tag that was meant.
func TestATagCannotCarryANewlineIntoSomebodyElsesDatabase(t *testing.T) {
	meta := metadata(t, Record{ID: "a-record", Tags: []string{"two\nlines", "three\r\nlines"}})

	// (The two come back sorted, which is the ordering rule the namespace test states; what this test is
	// about is that neither arrived with a line break in it.)
	assert.Equal(t, []string{"tag:three lines", "tag:two lines"}, stringsOf(t, meta, "tags"))
}

// The document's bytes are STABLE for the same record, or nobody can diff two exports -- and only the part
// whose order carries no meaning is sorted (our own tags), because the rest is the record's own order.
func TestTheSameRecordRendersTheSameBytes(t *testing.T) {
	record := Record{
		ID:         "a-record",
		Studio:     "A Studio",
		Title:      "A Title",
		Performers: []string{"Ana", "Zoe"},
		Tags:       []string{"zeta", "alpha"},
		URLs:       []string{"https://b.example/x", "https://a.example/y"},
	}

	first, err := Render(Document{"a-record": SidecarFor(record)})
	require.NoError(t, err)

	// The tags arrive in a different order the second time, which is what a differently-ordered load looks
	// like. The output must not change.
	record.Tags = []string{"alpha", "zeta"}
	second, err := Render(Document{"a-record": SidecarFor(record)})
	require.NoError(t, err)

	assert.Equal(t, string(first), string(second), "the same record renders the same bytes")

	meta := metadata(t, record)
	assert.Equal(t, []string{"https://b.example/x", "https://a.example/y"}, stringsOf(t, meta, "urls"),
		"the URLs keep the order the record gave them")
}

func TestSomethingSomebodyElseWroteSurvivesAsText(t *testing.T) {
	hostile := `A & B <tag> "quoted" ünïcode 日本語`
	meta := metadata(t, Record{ID: "a-record", Title: hostile, Tags: []string{hostile}})

	assert.Equal(t, []string{"title: " + hostile}, stringsOf(t, meta, "notes"),
		"the text came back exactly as it went in, whatever the encoder had to escape to get it there")
	assert.Equal(t, []string{"tag:" + hostile}, stringsOf(t, meta, "tags"))
}

func TestTheRecordIsTheKeyAndEmptyFieldsAreNotEntries(t *testing.T) {
	document := Document{
		"8f14e45f-ceea-467a-9c1f-1a2b3c4d5e6f": SidecarFor(Record{ID: "8f14e45f-ceea-467a-9c1f-1a2b3c4d5e6f"}),
	}
	body, err := Render(document)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(body, &parsed))
	require.Len(t, parsed, 1)
	_, ok := parsed["8f14e45f-ceea-467a-9c1f-1a2b3c4d5e6f"]
	assert.True(t, ok, "the key is the record's id, which is what its absence is reported by")

	// A record with nothing in it still produces an entry with empty lists, rather than a `null`: a caller
	// that routed on null would treat "no addresses" as "no record".
	assert.NotContains(t, strings.TrimSpace(string(body)), ": null")
}
