package identification

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Third-party identification sources (SPEC §8, plan step 7a).
//
// A source is a place a person can ask: a reverse image search engine, a
// face-matching service, a community forum. What this file produces is a URL --
// rendering a template is a pure function -- so nothing about this instance or
// its users leaves the box, which is SPEC §8's F8 ("deep links first") being the
// whole of what ships here. A source that CALLS something is a different
// decision, per provider, and belongs behind F10's gate (plan step 7c).
//
// The kinds are not decoration. KindRemoteFace is the set F10 puts behind the
// operator's gate, because handing a performer's face to a third party is a
// different act from handing it a box cover, and the difference has to survive
// into the code that decides what to offer.

// SourceKind is what a source does with the media it is given.
type SourceKind string

const (
	// KindReverseMedia is an engine that takes an image or clip address and looks
	// for where else that image appears.
	KindReverseMedia SourceKind = "reverse_media"
	// KindRemoteFace is a service that matches a FACE. F10's gate applies: a
	// biometric query is a decision about a person who is not in the room, so it
	// is off unless the operator turned that specific provider on.
	KindRemoteFace SourceKind = "remote_face"
	// KindCommunity is a place where a person asks a question and other people
	// answer. F12: a hand-off, never an integration.
	KindCommunity SourceKind = "community"
)

// Valid reports whether k is one of the three kinds this package knows. Config
// parsing goes through this, because a typo'd kind would otherwise produce a
// source that is never offered and never explained.
func (k SourceKind) Valid() bool {
	switch k {
	case KindReverseMedia, KindRemoteFace, KindCommunity:
		return true
	}
	return false
}

// ErrNoMedia is returned when a link is asked for with no media address. It is
// the caller's signal to skip the source rather than an error to report: a query
// with no image yet is a normal state of an early query, and Links() handles it
// by returning an empty list.
var ErrNoMedia = errors.New("no media address to look up")

// ErrUnsafeScheme is returned for a rendered link that is not http(s). An
// operator-supplied template ends up in an href a user clicks, so the scheme
// check is the boundary that keeps a config mistake from being a javascript: or
// data: link on a page.
var ErrUnsafeScheme = errors.New("source link is not an http(s) URL")

// ErrNoHole is returned for a template that contains no {url} placeholder. A
// source that should link to its front page says so with an EMPTY template --
// which is what most of the upload-based services need, since they take a file
// and have no URL parameter to fill in.
var ErrNoHole = errors.New("template has no {url} placeholder")

// Source is one external place a question can be taken to.
type Source struct {
	Name string
	Kind SourceKind
	// Home is where the source lives, for a reader deciding whether to open it.
	Home string
	// Template is the URL with exactly one {url} hole for the media address. An
	// empty Template means the source has no URL parameter -- the link is its
	// Home and false in Link.Prefilled says so.
	Template string
	// Enabled is the operator's answer to "may this be offered". The defaults
	// come from DefaultSources (see the kind's comment for why remote_face is
	// off) and a config entry replaces them by name.
	Enabled bool
}

// LinkFor renders one source's link for a media address.
//
// The rendered address is percent-encoded: a media URL carrying `?` or `&` would
// otherwise split the outer query and the source would search for a truncated
// address -- the failure is silent and looks like the engine not knowing the
// image, so the encoding is the whole reason this is a function with tests
// rather than a string concatenation at the call site.
func (s Source) LinkFor(mediaURL string) (Link, error) {
	if strings.TrimSpace(mediaURL) == "" {
		return Link{}, ErrNoMedia
	}

	rendered := s.Home
	prefilled := false
	if s.Template != "" {
		if !strings.Contains(s.Template, "{url}") {
			return Link{}, fmt.Errorf("%s: %w", s.Name, ErrNoHole)
		}
		rendered = strings.ReplaceAll(s.Template, "{url}", url.QueryEscape(mediaURL))
		prefilled = true
	}

	u, err := url.Parse(rendered)
	if err != nil {
		return Link{}, fmt.Errorf("%s: %w", s.Name, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return Link{}, fmt.Errorf("%s (%q): %w", s.Name, u.Scheme, ErrUnsafeScheme)
	}

	return Link{Source: s.Name, Kind: s.Kind, URL: rendered, Prefilled: prefilled}, nil
}

// Link is one rendered source link for a piece of media.
type Link struct {
	Source string
	Kind   SourceKind
	URL    string
	// Prefilled is false when the source takes an upload rather than an address,
	// so the reader arrives at its front door with nothing in hand.
	Prefilled bool
}

// Links renders every enabled source's link for one media address.
//
// An empty mediaURL returns an empty list and no error: "there is nothing to
// look up yet" is a state of a query, not a fault. A source whose own definition
// is broken (a template with no hole, a non-http scheme) DOES return an error,
// because the alternative is a link that silently disappears from the page and a
// config mistake nobody can see.
func Links(sources []Source, mediaURL string) ([]Link, error) {
	if strings.TrimSpace(mediaURL) == "" {
		return nil, nil
	}

	links := make([]Link, 0, len(sources))
	for _, s := range sources {
		if !s.Enabled {
			continue
		}
		link, err := s.LinkFor(mediaURL)
		if err != nil {
			if errors.Is(err, ErrNoMedia) {
				continue
			}
			return nil, err
		}
		links = append(links, link)
	}
	return links, nil
}

// DefaultSources is the built-in catalogue from SPEC §8.1.
//
// Three rules decided what is in it and what shape each entry has:
//
//   - Only sources whose URL form is KNOWN are parameterised. Where the address
//     was not certain (FindIt.moe, ImgOps, SauceMatch, AVScan, Revideo and the
//     face services), the entry is a link to the front page instead, which is
//     honest and still useful -- those services take an uploaded file, so a
//     prefilled address is not what they need anyway.
//   - The face services are remote_face and therefore START DISABLED (F10). The
//     spec's own catalogue note is the reason: this is the one integration whose
//     failure no test detects, so it is the operator's decision and not a
//     default.
//   - Sources named in the spec's catalogue with no verified address at all (the
//     Stash Forum starter guide, FaceSpy) are deliberately ABSENT rather than
//     guessed at. An operator can add them with a config entry; inventing a URL
//     here would be a dead link in a user's browser and nothing would say why.
//
// Local models (StashFace ONNX, MegaFace, the WD14 taggers) are NOT here: they
// are not links, they run in the operator's own infrastructure, and they are the
// 7c/7d providers.
func DefaultSources() []Source {
	return []Source{
		{Name: "SauceNAO", Kind: KindReverseMedia, Home: "https://saucenao.com",
			Template: "https://saucenao.com/search.php?url={url}", Enabled: true},
		{Name: "IQDB", Kind: KindReverseMedia, Home: "https://iqdb.org",
			Template: "https://iqdb.org/?url={url}", Enabled: true},
		{Name: "trace.moe", Kind: KindReverseMedia, Home: "https://trace.moe",
			Template: "https://trace.moe/?url={url}", Enabled: true},
		{Name: "TinEye", Kind: KindReverseMedia, Home: "https://tineye.com",
			Template: "https://tineye.com/search?url={url}", Enabled: true},
		{Name: "Yandex Images", Kind: KindReverseMedia, Home: "https://yandex.com/images",
			Template: "https://yandex.com/images/search?rpt=imageview&url={url}", Enabled: true},
		// Upload-first: the link is the front door and nothing is prefilled.
		{Name: "SauceMatch", Kind: KindReverseMedia, Home: "https://saucematch.com", Enabled: true},
		{Name: "AVScan", Kind: KindReverseMedia, Home: "https://avscan.cc", Enabled: true},
		{Name: "Revideo", Kind: KindReverseMedia, Home: "https://revideo.io", Enabled: true},
		{Name: "FindIt.moe", Kind: KindReverseMedia, Home: "https://findit.moe", Enabled: true},
		{Name: "ImgOps", Kind: KindReverseMedia, Home: "https://imgops.com", Enabled: true},

		// F10: off until an operator turns one on, one at a time.
		{Name: "FaceCheck.ID", Kind: KindRemoteFace, Home: "https://facecheck.id"},
		{Name: "PornstarByFace", Kind: KindRemoteFace, Home: "https://pornstarbyface.com"},
		{Name: "xzeno.ai", Kind: KindRemoteFace, Home: "https://xzeno.ai"},
		{Name: "SocialFinder.ai", Kind: KindRemoteFace, Home: "https://socialfinder.ai"},
		{Name: "Camgirlfinder", Kind: KindRemoteFace, Home: "https://camgirlfinder.net"},
		{Name: "OnlyFinder", Kind: KindRemoteFace, Home: "https://onlyfinder.com"},

		{Name: "r/TOMP", Kind: KindCommunity, Home: "https://www.reddit.com/r/TOMP",
			Template: "https://www.reddit.com/r/TOMP/submit?url={url}", Enabled: true},
		{Name: "r/HentaiSource", Kind: KindCommunity, Home: "https://www.reddit.com/r/HentaiSource",
			Template: "https://www.reddit.com/r/HentaiSource/submit?url={url}", Enabled: true},
		{Name: "NameThatPorn", Kind: KindCommunity, Home: "https://namethatporn.com", Enabled: true},
	}
}

// SourceOverride is an operator's change to the built-in catalogue: the same
// source with fields replaced, or a new source that is not in the catalogue at
// all. It carries the raw config values so the caller can tell "not specified"
// (nil) from "set to false", which a bool cannot.
type SourceOverride struct {
	Name     string
	Kind     SourceKind
	Home     string
	Template string
	Enabled  *bool
}

// ApplyOverrides returns the catalogue with the overrides applied, in catalogue
// order, with new names appended in the order they were given.
//
// Matching is by exact name and is case sensitive: "saucenao" is a NEW source
// beside "SauceNAO", not a rename of it. Case-insensitive matching would let a
// config typo silently redefine a built-in entry, which is the kind of change
// nobody notices until the link goes somewhere unexpected. An override that
// names a source already in the list replaces it, so the operator's copy is the
// one offered.
func ApplyOverrides(sources []Source, overrides []SourceOverride) []Source {
	out := make([]Source, len(sources))
	copy(out, sources)

	for _, o := range overrides {
		if strings.TrimSpace(o.Name) == "" {
			continue
		}
		replaced := false
		for i := range out {
			if out[i].Name != o.Name {
				continue
			}
			applyOverride(&out[i], o)
			replaced = true
			break
		}
		if replaced {
			continue
		}

		added := Source{Name: o.Name, Enabled: false}
		applyOverride(&added, o)
		out = append(out, added)
	}
	return out
}

// applyOverride copies the fields the override actually set. A new source with
// no `enabled` is OFF: adding a source to the config is not the same statement
// as turning it on, and defaulting a new entry to on would make an unnoticed
// config addition the thing that starts offering a link.
func applyOverride(dst *Source, o SourceOverride) {
	dst.Name = o.Name
	if o.Kind != "" {
		dst.Kind = o.Kind
	}
	if o.Home != "" {
		dst.Home = o.Home
	}
	// Template is copied unconditionally when non-empty, and an empty Template
	// cannot clear a built-in one: "I did not set this" and "I meant to remove
	// the placeholder" are indistinguishable in a string, and the destructive
	// reading is the wrong default.
	if o.Template != "" {
		dst.Template = o.Template
	}
	if o.Enabled != nil {
		dst.Enabled = *o.Enabled
	}
}
