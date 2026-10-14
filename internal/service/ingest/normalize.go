package ingest

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// URL normalisation: the queue's dedup key (spec §1 stage 1, §3).
//
// WHY THIS IS A PURE FUNCTION AND NOT A STEP INSIDE THE PIPELINE. Two URLs that differ only
// by a tracking parameter are the same URL, and if normalisation misses one, dedup misses
// it too -- so the cost of a wrong rule here is a duplicate scene per submission, forever,
// and the cost of a wrong rule in the network path is unobservable. As a pure function it
// is a table in a test file; as a step inside a fetch it is a comment nobody can check.
//
// WHAT THIS DOES NOT DO: resolve redirects. That is a network act -- a shortened URL's
// destination can only be learned by asking -- and it belongs to the extractor (stage 4),
// which reports the URL it finally landed on. The spec's stage list said "resolve
// shortened URLs" under normalisation and that was wrong: it put the only I/O in the
// function whose whole value is being testable without I/O.

var (
	// ErrUnsupportedScheme is returned for anything that is not http(s).
	//
	// A scheme is required rather than assumed: `x.com/a` could be http or https, and
	// guessing produces a key that may not match the URL the fetch later uses. The
	// submitter gets an error they can fix instead of a record that silently points
	// somewhere else.
	ErrUnsupportedScheme = errors.New("only http and https URLs can be ingested")
	// ErrNoHost is returned for a URL with an empty host (`https:///a`).
	ErrNoHost = errors.New("URL has no host")
)

// trackingParams are dropped from the key rather than from the URL.
//
// The RAW url is what gets fetched; this list only decides when two submissions are the
// same page. Dropping a parameter the site actually needs would be a fetch bug, so the
// list stays short and consists of values that only ever identify the REFERRER or the
// campaign:
//
//   - utm_* -- Google's campaign set, generated per share.
//   - fbclid / gclid / dclid / msclkid / twclid / igshid / mc_eid -- click ids from the
//     five platforms whose share buttons append them.
//   - ref / referrer / si -- the share form several tube sites use. `si` is also a real
//     parameter name for some CDNs, which is the cost of including it: a site that needs
//     its own `si` value to serve the page it describes would be fetched from the raw URL
//     anyway, so only the KEY is affected, and the key being more permissive merges two
//     URLs the site itself treats as one page.
//   - _hsenc / _hsmi / vero_conv / vero_id -- HubSpot and Vero, same reason.
var trackingParams = map[string]bool{
	"ref":       true,
	"referrer":  true,
	"si":        true,
	"fbclid":    true,
	"gclid":     true,
	"dclid":     true,
	"msclkid":   true,
	"twclid":    true,
	"igshid":    true,
	"mc_eid":    true,
	"_hsenc":    true,
	"_hsmi":     true,
	"vero_conv": true,
	"vero_id":   true,
}

// NormalizeURL returns the canonical form of raw, or an error explaining why it cannot be
// ingested at all.
//
// The rules, each of which has a case in normalize_test.go:
//
//   - Host lowercased. The DNS is case-insensitive, so EXAMPLE.com and example.com are the
//     same host and two submissions differing in case are one submission.
//   - A leading `www.` removed. A deliberate equivalence claim: on the sites this
//     aggregates, www is a name for the same host. It is not true of every host on the
//     internet, and the cost of being wrong is that two sources merge into one record --
//     which is visible in the data, unlike the cost of NOT doing it, which is a duplicate
//     scene per submission that nothing distinguishes from two real sources.
//   - The default port for the scheme (80/443) removed, any other port kept. `:8443` is
//     part of the address; `:443` is not.
//   - The fragment removed. It never reaches a server, so two URLs differing only in
//     `#t=30` are byte-identical requests.
//   - Query parameters sorted by name, and the tracking set above dropped. Sorting is safe
//     because the sorted form is only ever a KEY -- the raw URL is what is fetched -- and
//     `?a=1&b=2` and `?b=2&a=1` are the same page. Values are NOT decoded or re-encoded:
//     a normaliser that rewrites encoding is a second encoder that has to agree with the
//     first one, and disagreement there produces keys that look right and match nothing.
//   - The path kept verbatim, case included, with a single trailing slash removed from a
//     non-root path. Paths are case-SENSITIVE on most of the web, so `/Video/123` and
//     `/video/123` are two URLs, however annoying that is: folding them would send a
//     reader to a 404 while hiding the fact that we merged two different addresses.
//
// NOT folded, on purpose: http vs https. The conservative direction -- never merge two
// URLs the submitter distinguished -- because a site that serves different content on the
// two schemes would otherwise be merged invisibly. The extractor's resolved URL is where
// the schemes actually converge, since a fetch of the http form lands on the https one.
func NormalizeURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", ErrUnsupportedScheme
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("could not parse %q as a URL: %w", raw, err)
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		// Covers both a missing scheme ("" is not http) and a wrong one (ftp, magnet,
		// javascript). Rejecting in one place means there is one message to read rather
		// than a different failure depending on which check fired.
		return "", fmt.Errorf("%w (got %q)", ErrUnsupportedScheme, parsed.Scheme)
	}

	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return "", fmt.Errorf("%w: %q", ErrNoHost, raw)
	}
	host = strings.TrimPrefix(host, "www.")

	if p := parsed.Port(); p != "" && p != defaultPort(scheme) {
		host = host + ":" + p
	}

	path := parsed.EscapedPath()
	if path == "" {
		path = "/"
	}
	if len(path) > 1 {
		path = strings.TrimSuffix(path, "/")
	}

	query := normalizedQuery(parsed.RawQuery)

	// Built by hand rather than with url.URL.String(): the round trip through the struct
	// re-encodes the host and the path, which turns `%7E` into `~` on some inputs and into
	// `%7E` on others depending on which fields were set. One builder, one output.
	out := scheme + "://" + host + path
	if query != "" {
		out += "?" + query
	}
	return out, nil
}

func defaultPort(scheme string) string {
	if scheme == "https" {
		return "443"
	}
	return "80"
}

// normalizedQuery sorts the surviving parameters by name and drops the tracking set.
//
// A parameter with no value is kept as a bare name (`?hd`), because that is a flag the
// site may act on and `?hd=` is a different string to some servers.
func normalizedQuery(rawQuery string) string {
	if rawQuery == "" {
		return ""
	}

	kept := make([]string, 0, 8)
	for _, pair := range strings.Split(rawQuery, "&") {
		if pair == "" {
			continue
		}
		name, _, _ := strings.Cut(pair, "=")
		if trackingParams[strings.ToLower(name)] {
			continue
		}
		// Case-insensitive matching on the NAME without rewriting the pair: `UTM_Source`
		// is as much a tracking parameter as `utm_source`, and lowercasing the name in the
		// output would change a parameter the site may match case-sensitively.
		if strings.HasPrefix(strings.ToLower(name), "utm_") {
			continue
		}
		kept = append(kept, pair)
	}

	sort.Strings(kept)
	return strings.Join(kept, "&")
}
