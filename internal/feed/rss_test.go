package feed

import (
	"encoding/xml"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The document shape, and the two things a reader is unforgiving about: ESCAPING and DATES.
//
// These tests are pure because they can be: the parts of a feed that break in a reader are the parts that
// do not need a database -- a title with an angle bracket in it, a date a parser cannot read -- and the
// integration side (which records appear, and as whom) is a different question with its own file.

func TestTitleWithMarkupSurvivesAsText(t *testing.T) {
	// The record's title is somebody else's text, and this is what happens when it contains the
	// characters XML uses: it must come back EXACTLY as it went in, as text, and the document must still
	// parse. A hand-built string would either fail to parse here or -- worse -- parse into markup.
	hostile := `A & B <script>alert("x")</script> "quoted" 'single'`

	doc := NewDocument("The search", "https://example.test", "")
	doc.Channel.Items = append(doc.Channel.Items, NewItem(
		hostile, "https://example.test/scenes/1", "1", time.Now(), "& <"))

	body, err := Render(doc)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(body), xml.Header), "a reader expects the declaration first")
	assert.NotContains(t, string(body), "<script>", "the markup must not survive as markup")

	var parsed struct {
		Channel struct {
			Items []struct {
				Title       string `xml:"title"`
				Description string `xml:"description"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	require.NoError(t, xml.Unmarshal(body, &parsed), "and the document still parses")
	require.Len(t, parsed.Channel.Items, 1)
	assert.Equal(t, hostile, parsed.Channel.Items[0].Title, "the title came back byte for byte")
	assert.Equal(t, "& <", parsed.Channel.Items[0].Description)
}

// THE DATE FORMAT IS A GO TRAP, and it is why this test names it: `time.RFC1123` spells a UTC offset as
// the string "UTC" -- a Go behaviour, not a choice -- while RSS 2.0 asks for a NUMERIC zone. A feed whose
// dates a parser cannot read is a feed where everything else can be perfect and nothing appears.
func TestPubDateCarriesANumericZoneAndNeverTheWordUTC(t *testing.T) {
	instant := time.Date(2026, 10, 15, 21, 30, 0, 0, time.UTC)

	rendered := FormatTime(instant)
	assert.Equal(t, "Thu, 15 Oct 2026 21:30:00 +0000", rendered)
	assert.NotContains(t, rendered, "UTC", "RFC1123 would have written the word; RSS wants +0000")

	// And a clock from somewhere else is normalised rather than trusted: the document says one thing
	// about the same instant wherever it was built.
	lisbon := time.Date(2026, 10, 15, 23, 30, 0, 0, time.FixedZone("WEST", 2*60*60))
	assert.Equal(t, rendered, FormatTime(lisbon), "the same instant renders the same way in any zone")
}

func TestAnEmptyFeedIsStillAWellFormedDocument(t *testing.T) {
	body, err := Render(NewDocument("Nothing yet", "https://example.test", "a query"))
	require.NoError(t, err)

	var parsed struct {
		Version string `xml:"version,attr"`
		Channel struct {
			Title       string `xml:"title"`
			Description string `xml:"description"`
			Generator   string `xml:"generator"`
			Items       []any  `xml:"item"`
		} `xml:"channel"`
	}
	require.NoError(t, xml.Unmarshal(body, &parsed))
	assert.Equal(t, RSSVersion, parsed.Version)
	assert.Equal(t, "Nothing yet", parsed.Channel.Title)
	assert.Equal(t, Generator, parsed.Channel.Generator)
	assert.Empty(t, parsed.Channel.Items, "no items, and no empty placeholder either")
}

// The guid is the RECORD's identity and the link is where it can be read: two different things, and a
// reader that keyed on the link would show the same record twice when a host changes.
func TestTheGUIDIsTheRecordAndNotTheLink(t *testing.T) {
	doc := NewDocument("The search", "https://example.test", "")
	doc.Channel.Items = append(doc.Channel.Items,
		NewItem("One", "https://example.test/scenes/abc", "abc", time.Now(), ""),
		NewItem("Two", "https://example.test/scenes/def", "def", time.Now(), ""),
	)

	body, err := Render(doc)
	require.NoError(t, err)

	// Counted, so "both guids differ" cannot pass on a document with no items at all.
	var parsed struct {
		Channel struct {
			Items []struct {
				GUID struct {
					IsPermaLink string `xml:"isPermaLink,attr"`
					Value       string `xml:",chardata"`
				} `xml:"guid"`
				Link string `xml:"link"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	require.NoError(t, xml.Unmarshal(body, &parsed))
	require.Len(t, parsed.Channel.Items, 2)

	assert.Equal(t, "abc", parsed.Channel.Items[0].GUID.Value)
	assert.Equal(t, "def", parsed.Channel.Items[1].GUID.Value)
	assert.NotEqual(t, parsed.Channel.Items[0].GUID.Value, parsed.Channel.Items[0].Link,
		"the identity is not the address")
	for _, item := range parsed.Channel.Items {
		assert.Equal(t, "false", item.GUID.IsPermaLink, "and it says so, because a reader assumes otherwise")
	}
}

func TestRenderAlwaysCarriesTheVersionGeneratorAndBuildDate(t *testing.T) {
	// A document built by hand rather than by NewDocument: the fields a caller forgets are the fields
	// Render has to make certain of, or a feed is subtly not a feed.
	body, err := Render(Document{Channel: Channel{Title: "Hand-built", Link: "https://example.test"}})
	require.NoError(t, err)

	var parsed struct {
		Version string `xml:"version,attr"`
		Channel struct {
			Generator     string `xml:"generator"`
			LastBuildDate string `xml:"lastBuildDate"`
		} `xml:"channel"`
	}
	require.NoError(t, xml.Unmarshal(body, &parsed))
	assert.Equal(t, "2.0", parsed.Version)
	assert.Equal(t, Generator, parsed.Channel.Generator)
	assert.Contains(t, parsed.Channel.LastBuildDate, "+0000")
}

func TestItemOrderIsTheOrderTheyWereAdded(t *testing.T) {
	// Newest first is the CALLER's decision (the search's own sort), so the document must not reorder
	// anything: a feed whose order depended on the renderer would disagree with the page it mirrors.
	doc := NewDocument("The search", "", "")
	doc.Channel.Items = append(doc.Channel.Items,
		NewItem("Newest", "/scenes/1", "1", time.Now(), ""),
		NewItem("Older", "/scenes/2", "2", time.Now(), ""),
		NewItem("Oldest", "/scenes/3", "3", time.Now(), ""),
	)

	body, err := Render(doc)
	require.NoError(t, err)

	var parsed struct {
		Channel struct {
			Items []struct {
				Title string `xml:"title"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	require.NoError(t, xml.Unmarshal(body, &parsed))
	require.Len(t, parsed.Channel.Items, 3)
	assert.Equal(t, []string{"Newest", "Older", "Oldest"},
		[]string{parsed.Channel.Items[0].Title, parsed.Channel.Items[1].Title, parsed.Channel.Items[2].Title})
}
