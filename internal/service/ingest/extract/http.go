package extract

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/stashapp/stash-box/internal/service/hostrules"
)

// HTTPExtractor reads a page's OpenGraph metadata. It is the production implementation and
// it has no dependencies beyond net/http: the aggregator's first cut should be able to
// describe a page without shipping a media toolchain.
type HTTPExtractor struct {
	// Client is injected so tests can point it at an httptest server and set timeouts.
	// A nil Client uses defaultClient.
	Client *http.Client
	// Rules is the robots.txt and pacing policy this fetch obeys before it is made.
	//
	// A nil Rules uses hostrules.Default(), which is the process-wide one the health sweep also
	// uses -- and it MUST be the shared one in production, or the two clients would pace themselves
	// independently and knock on one host twice as often as either believes. It is a field so a test
	// can supply its own (no network, no waiting).
	Rules *hostrules.Rules
}

// maxBody bounds what is read from a page. A tube page's <head> is a few KB; the rest is
// markup and a video player, and a page that streams a gigabyte of HTML would otherwise be
// read into memory by a queue worker.
const maxBody = 512 * 1024

// NewHTTPExtractor returns the production extractor.
//
// A constructor rather than a bare struct literal at the call site, so that "how the
// network is talked to" has one address to change -- a proxy, a per-host rate limiter, a
// challenge solver -- without every caller learning about it.
func NewHTTPExtractor() HTTPExtractor {
	return HTTPExtractor{}
}

func defaultClient() *http.Client {
	return &http.Client{
		Timeout: 20 * time.Second,
		// Ten redirects is the net/http default; a shortener chain longer than that is a
		// loop, and the client's own error for it is clearer than a truncated read.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}
}

func (e HTTPExtractor) Extract(ctx context.Context, url string) (Metadata, error) {
	// THE HOST'S OWN RULES, BEFORE OURS. robots.txt and the per-host pace are asked first, and a
	// refusal is returned as its own error so the queue row says "robots.txt disallowed this" rather
	// than "unreachable" -- an operator reading the second would go looking for a network problem
	// that does not exist. See internal/service/hostrules for what is honoured and why it is not
	// configurable.
	rules := e.Rules
	if rules == nil {
		rules = hostrules.Default()
	}
	if err := rules.BeforeFetch(ctx, url); err != nil {
		if errors.Is(err, hostrules.ErrDisallowed) {
			// A rule the host stated. Returned as its own error so the queue row says so.
			return Metadata{}, err
		}
		// The host could not state its terms (5xx, or no answer at all). That is the host not
		// answering rather than a decision about this client, so it is reported as what it is: the
		// caller's ErrUnreachable arm, which is the "try later" one. BOTH are wrapped, so the chain
		// says `unreachable because robots was unavailable` and a caller can match either fact --
		// the pipeline needs the first, and a test or a log reader wants the second.
		return Metadata{}, fmt.Errorf("%w: %w", ErrUnreachable, err)
	}

	client := e.Client
	if client == nil {
		client = defaultClient()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		// A URL http cannot even build a request for is not retryable: the normaliser
		// should have refused it, so reaching here means the two disagree, and a retry
		// would disagree the same way.
		return Metadata{}, fmt.Errorf("%w: %v", ErrUnsupportedPage, err)
	}
	// A generic UA, because some sites serve a bare "go-http-client" either a challenge
	// page or nothing. Not spoofing a browser: the point is to be identifiable, and a
	// site that refuses this is a site we should not be scraping anyway.
	req.Header.Set("User-Agent", "stash-box-aggregator/1.0 (+metadata only)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := client.Do(req)
	if err != nil {
		// Includes the timeout and the redirect loop: both are "try later" rather than
		// "this content does not exist".
		return Metadata{}, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return Metadata{}, fmt.Errorf("%w: the server answered %d", ErrGone, resp.StatusCode)
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		// A login wall or a paywall. Permanent, and specifically NOT ErrGone: the content
		// exists, so a later crawl with credentials could have it, and the queue row must
		// not be closed as "there is nothing here".
		return Metadata{}, fmt.Errorf("%w: the server answered %d", ErrUnsupportedPage, resp.StatusCode)
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return Metadata{}, fmt.Errorf("%w: the server answered %d", ErrUnreachable, resp.StatusCode)
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return Metadata{}, fmt.Errorf("%w: the server answered %d", ErrUnsupportedPage, resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType != "" && !strings.Contains(contentType, "html") {
		// A direct link to a video file, or JSON. Refused rather than guessed at: the
		// pipeline creates a scene from this metadata, and a record whose title is
		// "application/octet-stream" is worse than a queue row saying why not.
		return Metadata{}, fmt.Errorf("%w: Content-Type is %q, not HTML", ErrUnsupportedPage, contentType)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return Metadata{}, fmt.Errorf("%w: reading the body: %v", ErrUnreachable, err)
	}

	meta := parseOpenGraph(string(body))
	meta.Source = "og"
	// The URL the fetch landed on, when it differs from the one asked for: a shortened URL
	// resolved to its destination, which is where the content actually lives.
	if resp.Request != nil && resp.Request.URL != nil && resp.Request.URL.String() != url {
		meta.ResolvedURL = resp.Request.URL.String()
	}
	return meta, nil
}

var _ Extractor = HTTPExtractor{}

// parseOpenGraph pulls the metadata out of an HTML document.
//
// A hand-rolled scan of the `<meta>` tags rather than an HTML parser dependency, and the
// cost is stated: this is not a general HTML parser and must not become one. It reads the
// attributes of `<meta …>` elements in either order, with either quote character, in any
// case -- which is the whole of the variation OpenGraph markup actually exhibits in the
// wild -- and it does not attempt to handle a `<meta>` inside a comment, a script or a
// CDATA block. Those exist on some pages; their effect here is a wrong title on one scene,
// which an edit fixes, not a crash or an injection.
func parseOpenGraph(html string) Metadata {
	var (
		meta  Metadata
		title string // <title>, the fallback when og:title is absent
	)

	for _, tag := range metaTags(html) {
		attrs := parseAttributes(tag)
		property := strings.ToLower(attrs["property"])
		if property == "" {
			// `name=` is what non-OpenGraph metadata uses (Twitter cards, plain
			// description). Only a few of those carry information we want, so this is
			// deliberately narrow rather than treating name= as equivalent to property=.
			property = strings.ToLower(attrs["name"])
		}
		content := attrs["content"]

		switch property {
		case "og:title":
			if meta.Title == "" {
				meta.Title = content
			}
		case "og:description":
			// Kept nowhere yet: the description is the studio's marketing copy and the
			// scene's `details` field is for what this instance knows. Parsed so the
			// switch documents the decision rather than silently ignoring the tag.
			_ = content
		case "og:video:duration", "og:video:duration:seconds":
			if d, err := strconv.Atoi(strings.TrimSpace(content)); err == nil && d > 0 {
				meta.DurationSeconds = d
			} else {
				// Some sites state it as an ISO 8601 duration (PT12M34S). Refusing to
				// guess keeps a wrong duration out of the record; the field is optional.
				meta.DurationSeconds = 0
			}
		case "og:image":
			if meta.ThumbnailURL == "" {
				meta.ThumbnailURL = content
			}
		case "og:video:release_date", "og:release_date", "article:published_time":
			// Only the date part is kept: these are sometimes full timestamps
			// (2024-03-01T12:00:00Z) and sometimes bare dates, and scenes.date holds a
			// date. Truncating here rather than storing a timestamp in a text column keeps
			// two spellings of the same day from being two different values.
			if meta.Date == "" {
				if d := normalizeDate(content); d != "" {
					meta.Date = d
				}
			}
		default:
			if strings.HasPrefix(property, "og:video:tag") {
				// `og:video:tag` is where several sites put performer names. Each tag is a
				// separate meta element, so this accumulates rather than assigning.
				if v := strings.TrimSpace(content); v != "" {
					meta.PerformerNames = append(meta.PerformerNames, v)
				}
			}
		}
	}

	if title == "" {
		title = documentTitle(html)
	}
	if meta.Title == "" {
		meta.Title = title
	}
	meta.Title = strings.TrimSpace(meta.Title)

	// The confidence is the extractor's estimate of how much of a record this supports,
	// and it is a product of what was found rather than a constant: a title alone is thin
	// (and several sites use a generic site-wide title, which is indistinguishable from
	// here), a title plus a duration plus a date is a record an edit could be built on.
	switch {
	case meta.Title == "":
		meta.Confidence = 0
	case meta.DurationSeconds > 0 || meta.Date != "":
		meta.Confidence = 0.7
	default:
		meta.Confidence = 0.3
	}
	return meta
}

// documentTitle reads the <title> element, which is the fallback when a page states no
// og:title.
//
// The fallback is worth having and worth distrusting: on a tube site it is frequently the
// SITE's title with no scene title at all ("BigTube — free videos"), so it produces a
// record whose title names nothing. The confidence below accounts for that by staying low
// when a title is all there is, and the review path is what a person is for.
func documentTitle(html string) string {
	lower := strings.ToLower(html)
	start := strings.Index(lower, "<title")
	if start < 0 {
		return ""
	}
	open := strings.IndexByte(html[start:], '>')
	if open < 0 {
		return ""
	}
	start += open + 1
	end := strings.Index(lower[start:], "</title")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(unescapeAttr(html[start : start+end]))
}

// normalizeDate reduces a stated date to YYYY-MM-DD, or returns "" when it cannot.
//
// Deliberately total and dumb: it accepts the ISO forms pages actually emit and refuses
// everything else rather than guessing at a locale. A date this function cannot read is a
// missing date, which is safe; a date it reads wrongly is a wrong production date on a
// public record, which is not.
func normalizeDate(v string) string {
	v = strings.TrimSpace(v)
	if len(v) < 10 {
		return ""
	}
	candidate := v[:10]
	for i, c := range candidate {
		switch {
		case i == 4 || i == 7:
			if c != '-' {
				return ""
			}
		case c < '0' || c > '9':
			return ""
		}
	}
	return candidate
}

// metaTags returns the raw text of every `<meta …>` element, without its angle brackets.
//
// The tag NAME is matched case-insensitively -- `<META>` is a meta tag, and uppercase
// markup is what hand-written and older templates emit -- while the ORIGINAL text is what
// gets sliced out, because attribute values are case-sensitive and lowercasing the whole
// document would rewrite the metadata itself. Measured, not assumed: the uppercase case is
// a row in TestParseOpenGraph, and it failed before this lowercased haystack existed.
func metaTags(html string) []string {
	var tags []string
	lower := strings.ToLower(html)
	for i := 0; ; {
		start := strings.Index(lower[i:], "<meta")
		if start < 0 {
			return tags
		}
		start += i
		// `<metafoo` is not a meta tag. The next character must end the name.
		next := start + len("<meta")
		if next < len(html) {
			c := html[next]
			if c != ' ' && c != '\t' && c != '\n' && c != '\r' && c != '/' && c != '>' {
				i = start + len("<meta")
				continue
			}
		}
		end := strings.IndexByte(html[next:], '>')
		if end < 0 {
			return tags
		}
		tags = append(tags, html[next:next+end])
		i = next + end
	}
}

// parseAttributes reads `key="value"`, `key='value'` and bare `key` attributes.
func parseAttributes(tag string) map[string]string {
	attrs := map[string]string{}
	for i := 0; i < len(tag); {
		for i < len(tag) && isSpace(tag[i]) {
			i++
		}
		nameStart := i
		for i < len(tag) && !isSpace(tag[i]) && tag[i] != '=' && tag[i] != '/' {
			i++
		}
		if nameStart == i {
			i++
			continue
		}
		name := strings.ToLower(tag[nameStart:i])

		for i < len(tag) && isSpace(tag[i]) {
			i++
		}
		if i >= len(tag) || tag[i] != '=' {
			attrs[name] = ""
			continue
		}
		i++ // consume '='
		for i < len(tag) && isSpace(tag[i]) {
			i++
		}
		if i < len(tag) && (tag[i] == '"' || tag[i] == '\'') {
			quote := tag[i]
			i++
			valueStart := i
			for i < len(tag) && tag[i] != quote {
				i++
			}
			attrs[name] = unescapeAttr(tag[valueStart:i])
			i++ // consume the closing quote
			continue
		}
		valueStart := i
		for i < len(tag) && !isSpace(tag[i]) {
			i++
		}
		attrs[name] = unescapeAttr(tag[valueStart:i])
	}
	return attrs
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// unescapeAttr handles the five entities that appear in attribute values. Full entity
// decoding is a bigger table than this deserves: `&amp;` in a URL matters and the rest are
// cosmetic.
func unescapeAttr(v string) string {
	if !strings.Contains(v, "&") {
		return v
	}
	replacer := strings.NewReplacer(
		"&amp;", "&",
		"&quot;", `"`,
		"&#39;", "'",
		"&apos;", "'",
		"&lt;", "<",
		"&gt;", ">",
	)
	return replacer.Replace(v)
}
