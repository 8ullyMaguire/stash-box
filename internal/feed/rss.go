// Package feed renders RSS 2.0 documents for this instance's read surfaces (plan slice 13b).
//
// PURE, and deliberately so: it takes strings and times and returns bytes. The escaping rules, the date
// format and the element order are the parts a feed reader is unforgiving about, and they are the parts
// that do not need a database to test -- so they live here, where a test can hand them a scene title full
// of angle brackets and read the answer.
package feed

import (
	"encoding/xml"
	"fmt"
	"time"
)

// RSSVersion is the only version this package emits. There is no atom namespace: nothing here needs one,
// and a namespace a reader has to resolve is a thing that can break for no benefit.
const RSSVersion = "2.0"

// Generator names the software in the channel, so a reader's "what made this" question has an answer.
const Generator = "stash-box"

// Document is an RSS 2.0 document.
type Document struct {
	XMLName xml.Name `xml:"rss"`
	Version string   `xml:"version,attr"`
	Channel Channel  `xml:"channel"`
}

// Channel is the feed itself: what it is, where it points, and what it currently holds.
type Channel struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
	Generator   string `xml:"generator"`
	// The instant this document was built. Stated rather than implied: a reader that tells a member
	// "nothing has arrived" should be able to say when it last asked.
	LastBuildDate string `xml:"lastBuildDate,omitempty"`
	Items         []Item `xml:"item"`
}

// Item is one record in the feed.
type Item struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	GUID        GUID   `xml:"guid"`
	PubDate     string `xml:"pubDate,omitempty"`
	Description string `xml:"description,omitempty"`
}

// GUID is the item's identity, and it is NOT the link.
//
// `isPermaLink="false"` says so explicitly, and the value is the record's id: a title changes, a link can
// be rebuilt at a different host, and a reader that keyed on either would show the same record twice.
type GUID struct {
	IsPermaLink bool   `xml:"isPermaLink,attr"`
	Value       string `xml:",chardata"`
}

// NewDocument starts a channel.
func NewDocument(title, link, description string) Document {
	return Document{
		Version: RSSVersion,
		Channel: Channel{
			Title:         title,
			Link:          link,
			Description:   description,
			Generator:     Generator,
			Items:         []Item{},
			LastBuildDate: FormatTime(time.Now()),
		},
	}
}

// NewItem builds one item. The description is PLAIN TEXT and is escaped as such: a description is not a
// place to render a third party's markup, and the alternative (declaring it HTML and sanitising) is a
// sanitizer this programme does not need to own.
func NewItem(title, link, guid string, pubDate time.Time, description string) Item {
	return Item{
		Title:       title,
		Link:        link,
		GUID:        GUID{IsPermaLink: false, Value: guid},
		PubDate:     FormatTime(pubDate),
		Description: description,
	}
}

// FormatTime renders an instant the way RSS 2.0 requires: RFC 822 with a NUMERIC zone.
//
// `time.RFC1123Z` and not `time.RFC1123`: the latter spells a UTC offset as the string "UTC" (a Go
// behaviour, not a choice), and RFC 822 wants a numeric zone. Feed parsers are lenient about a lot of
// things and a date they cannot read is one of the ways a whole feed disappears.
func FormatTime(t time.Time) string {
	return t.UTC().Format(time.RFC1123Z)
}

// Render produces the document bytes, with the XML declaration a reader expects first.
//
// `xml.Marshal` rather than a text template: it is what escapes a scene title containing `<`, `&` or a
// quote, and a template that forgets one of those produces a document that either fails to parse or --
// worse -- parses into something the record does not say.
func Render(doc Document) ([]byte, error) {
	if doc.Version == "" {
		doc.Version = RSSVersion
	}
	if doc.Channel.Items == nil {
		doc.Channel.Items = []Item{}
	}
	if doc.Channel.Generator == "" {
		doc.Channel.Generator = Generator
	}
	// The build date too, and for the same reason: it is the field a caller forgets, and a reader that
	// cannot say when it last asked cannot tell "nothing has arrived" from "this stopped working".
	if doc.Channel.LastBuildDate == "" {
		doc.Channel.LastBuildDate = FormatTime(time.Now())
	}

	body, err := xml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("could not render the feed: %w", err)
	}

	out := make([]byte, 0, len(body)+len(xml.Header))
	out = append(out, xml.Header...)
	return append(out, body...), nil
}
