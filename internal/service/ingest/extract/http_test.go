package extract

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The extraction and the HTTP status mapping, as tables.
//
// WHY THESE ARE UNIT TESTS AND THE PIPELINE'S ARE INTEGRATION TESTS. What is being checked
// here is a parse of a string and a mapping from a status code to one of three errors --
// neither involves a database, and neither would be checked better through one. The
// pipeline's rules need a real schema (a constraint can reject a write, and a claim is
// SKIP LOCKED), so those live in the integration suite; the boundary is where the
// dependency is, not where the code lives.

func TestParseOpenGraph(t *testing.T) {
	tests := []struct {
		name string
		html string
		want Metadata
	}{
		{
			name: "the ordinary page",
			html: `<html><head>
				<meta property="og:title" content="A Scene Title">
				<meta property="og:video:duration" content="1432">
				<meta property="og:image" content="https://img.invalid/a.jpg">
				</head></html>`,
			want: Metadata{Title: "A Scene Title", DurationSeconds: 1432, ThumbnailURL: "https://img.invalid/a.jpg"},
		},
		{
			// Attribute order varies by template, and a parser that assumed `property` came
			// first would silently drop the title on half the real pages.
			name: "attributes in the other order",
			html: `<meta content="Reversed Attributes" property="og:title">`,
			want: Metadata{Title: "Reversed Attributes"},
		},
		{
			name: "single quotes and an uppercase name",
			html: `<META PROPERTY='og:title' CONTENT='Shouty Markup'>`,
			want: Metadata{Title: "Shouty Markup"},
		},
		{
			name: "unquoted values",
			html: `<meta property=og:title content=Bare-Value>`,
			want: Metadata{Title: "Bare-Value"},
		},
		{
			// Self-closing and with a slash in the attribute, which is the shape an XHTML
			// template emits and the one a naive `>` split mangles.
			name: "self-closing with a trailing slash",
			html: `<meta property="og:title" content="Slashy"/>`,
			want: Metadata{Title: "Slashy"},
		},
		{
			// `&amp;` in a title is extremely common (studio names, "X & Y") and a title
			// that reads "A &amp; B" on a public record is a parse bug a reader sees.
			name: "entities in the value",
			html: `<meta property="og:title" content="A &amp; B &quot;quoted&quot;">`,
			want: Metadata{Title: `A & B "quoted"`},
		},
		{
			name: "the title element as a fallback",
			html: `<html><head><title>From The Title Element</title></head></html>`,
			want: Metadata{Title: "From The Title Element"},
		},
		{
			// og:title wins when both are present, which is the point of preferring it:
			// <title> on a tube site is frequently the site's name.
			name: "og:title beats the title element",
			html: `<html><head><title>BigSite - free videos</title>
				<meta property="og:title" content="The Real Title"></head></html>`,
			want: Metadata{Title: "The Real Title"},
		},
		{
			name: "og:video:tag accumulates as performers",
			html: `<meta property="og:video:tag" content="First Performer">
				<meta property="og:video:tag" content="Second Performer">`,
			want: Metadata{PerformerNames: []string{"First Performer", "Second Performer"}},
		},
		{
			name: "a full timestamp is reduced to its date",
			html: `<meta property="og:title" content="Dated">
				<meta property="og:video:release_date" content="2023-11-02T10:30:00Z">`,
			want: Metadata{Title: "Dated", Date: "2023-11-02"},
		},
		{
			// Refused rather than guessed: a wrong production date on a public record is
			// worse than a missing one, and locale-dependent parsing is how that happens.
			name: "an unparseable date is dropped, not guessed at",
			html: `<meta property="og:title" content="No Date">
				<meta property="og:video:release_date" content="02/11/2023">`,
			want: Metadata{Title: "No Date"},
		},
		{
			// The boundary check on the tag NAME: `<metafoo>` is not a `<meta>`, and a
			// prefix match would read its attributes as metadata.
			name: "<metafoo> is not a meta tag",
			html: `<metafoo property="og:title" content="Not This One">`,
			want: Metadata{},
		},
		{
			name: "no metadata at all",
			html: `<html><body><p>Nothing to read</p></body></html>`,
			want: Metadata{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseOpenGraph(tc.html)
			assert.Equal(t, tc.want.Title, got.Title)
			assert.Equal(t, tc.want.Date, got.Date)
			assert.Equal(t, tc.want.DurationSeconds, got.DurationSeconds)
			assert.Equal(t, tc.want.ThumbnailURL, got.ThumbnailURL)
			assert.Equal(t, tc.want.PerformerNames, got.PerformerNames)
		})
	}
}

// TestParseOpenGraphConfidence pins the confidence rule, because it is what the
// auto-approve threshold reads: a title alone must not be confident enough to skip review,
// and a title plus a length must be.
func TestParseOpenGraphConfidence(t *testing.T) {
	tests := []struct {
		name string
		html string
		want float64
	}{
		{"nothing found is zero", `<html></html>`, 0},
		{"a title alone is thin evidence", `<meta property="og:title" content="Just A Title">`, 0.3},
		{"a title and a duration is a record", `<meta property="og:title" content="T"><meta property="og:video:duration" content="60">`, 0.7},
		{"a title and a date is a record", `<meta property="og:title" content="T"><meta property="og:video:release_date" content="2024-01-01">`, 0.7},
		{"a duration with no title is still nothing to name the record with", `<meta property="og:video:duration" content="60">`, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.InDelta(t, tc.want, parseOpenGraph(tc.html).Confidence, 0.0001)
		})
	}
}

// TestHTTPExtractorStatusMapping is the table that decides whether the queue retries.
//
// Every arm here is a different answer to "should we ask again?", and getting one wrong is
// either a queue that retries a 404 forever or content silently dropped as permanent:
//
//   - gone: the content is not there. Permanent, and the row says so.
//   - unsupported: a login wall, a paywall, a body we cannot read. Permanent, and NOT
//     'gone': the content exists, so a later crawl with credentials could have it.
//   - unreachable: a bad day for the host. Transient, with a backoff.
func TestHTTPExtractorStatusMapping(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		ctype   string
		wantErr error
	}{
		{"200 html is read", http.StatusOK, "text/html; charset=utf-8", nil},
		{"404 is gone", http.StatusNotFound, "text/html", ErrGone},
		{"410 is gone", http.StatusGone, "text/html", ErrGone},
		{"403 is a login wall, not a deletion", http.StatusForbidden, "text/html", ErrUnsupportedPage},
		{"401 is a login wall", http.StatusUnauthorized, "text/html", ErrUnsupportedPage},
		{"429 is a bad day, not a permanent answer", http.StatusTooManyRequests, "text/html", ErrUnreachable},
		{"503 is a bad day", http.StatusServiceUnavailable, "text/html", ErrUnreachable},
		{"500 is a bad day", http.StatusInternalServerError, "text/html", ErrUnreachable},
		// A 4xx we did not enumerate: permanent, because retrying a 400-series answer is
		// asking a question the server already answered.
		{"418 is refused as unreadable rather than retried", http.StatusTeapot, "text/html", ErrUnsupportedPage},
		// A direct link to a media file. Reading it as HTML would create a record titled
		// "application/octet-stream", which is worse than a queue row saying why not.
		{"a non-HTML body is refused", http.StatusOK, "video/mp4", ErrUnsupportedPage},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.ctype != "" {
					w.Header().Set("Content-Type", tc.ctype)
				}
				w.WriteHeader(tc.status)
				_, _ = fmt.Fprint(w, `<meta property="og:title" content="Whatever">`)
			}))
			defer srv.Close()

			meta, err := NewHTTPExtractor().Extract(context.Background(), srv.URL)
			if tc.wantErr == nil {
				require.NoError(t, err)
				assert.Equal(t, "Whatever", meta.Title)
				assert.Equal(t, "og", meta.Source)
				return
			}
			require.Error(t, err)
			assert.True(t, errors.Is(err, tc.wantErr),
				"expected %v, got %v", tc.wantErr, err)
		})
	}
}

// TestHTTPExtractorErrorKindsAreDistinct is the assertion that the three kinds are not
// accidentally the same error behind an alias: errors.Is would report a match for a wrapped
// ErrGone when asked about ErrUnsupportedPage if one wrapped the other, and the queue
// would then treat a deletion and a paywall alike.
func TestHTTPExtractorErrorKindsAreDistinct(t *testing.T) {
	notFound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer notFound.Close()
	forbidden := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer forbidden.Close()

	_, goneErr := NewHTTPExtractor().Extract(context.Background(), notFound.URL)
	_, wallErr := NewHTTPExtractor().Extract(context.Background(), forbidden.URL)

	require.ErrorIs(t, goneErr, ErrGone)
	assert.NotErrorIs(t, goneErr, ErrUnsupportedPage, "a 404 must not read as a login wall")
	require.ErrorIs(t, wallErr, ErrUnsupportedPage)
	assert.NotErrorIs(t, wallErr, ErrGone, "a 403 must not read as a deletion")
}

// TestHTTPExtractorFollowsARedirect: a shortener's destination is where the content is, so
// the source is recorded against it rather than against the link that expires.
func TestHTTPExtractorFollowsARedirect(t *testing.T) {
	var dest *httptest.Server
	dest = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, `<meta property="og:title" content="The Destination">`)
	}))
	defer dest.Close()

	short := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dest.URL+"/long/url", http.StatusFound)
	}))
	defer short.Close()

	meta, err := NewHTTPExtractor().Extract(context.Background(), short.URL)
	require.NoError(t, err)
	assert.Equal(t, "The Destination", meta.Title, "the redirect was followed")
	assert.Equal(t, dest.URL+"/long/url", meta.ResolvedURL,
		"and the URL it landed on is reported, so the source is recorded against it")
}

// TestHTTPExtractorDoesNotReportAResolvedURLWhenNothingMoved: an empty ResolvedURL means
// "same URL", so a non-empty one that happens to equal the request would make every
// ordinary fetch look like a redirect and every source look like it moved.
func TestHTTPExtractorDoesNotReportAResolvedURLWhenNothingMoved(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, `<meta property="og:title" content="Same Place">`)
	}))
	defer srv.Close()

	meta, err := NewHTTPExtractor().Extract(context.Background(), srv.URL)
	require.NoError(t, err)
	assert.Empty(t, meta.ResolvedURL)
}

// TestHTTPExtractorReportsAnUnreachableHost, without a listener: the port is bound and
// closed, which is the closest thing to a dead host a unit test can arrange.
func TestHTTPExtractorReportsAnUnreachableHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening now

	_, err := NewHTTPExtractor().Extract(context.Background(), url)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnreachable)
}
