package identification

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The media address is deliberately nasty: a port, a query with two parameters
// AND a fragment, and a space. A naive concatenation of this into
// `...?url=` truncates at the first `&`, and the source then searches for a
// prefix of the address -- a failure that looks exactly like the engine not
// knowing the image.
const trickyMedia = "https://cdn.example.com:8443/a b.jpg?token=1&size=full#page"

func boolPtr(v bool) *bool { return &v }

func TestSourceLinkEncodesTheMediaAddressSoTheSourceGetsAllOfIt(t *testing.T) {
	src := Source{
		Name:     "SauceNAO",
		Kind:     KindReverseMedia,
		Home:     "https://saucenao.com",
		Template: "https://saucenao.com/search.php?url={url}",
		Enabled:  true,
	}

	link, err := src.LinkFor(trickyMedia)
	require.NoError(t, err)
	assert.True(t, link.Prefilled)

	// Read the link the way the ENGINE will: parse it and look at what the
	// `url` parameter actually contains. Asserting on the raw string would pass
	// for a link that is merely spelled differently; this fails unless the
	// engine receives the whole address.
	u, err := url.Parse(link.URL)
	require.NoError(t, err)
	assert.Equal(t, "saucenao.com", u.Host)
	assert.Equal(t, trickyMedia, u.Query().Get("url"),
		"the source must receive the WHOLE media address, including the second query parameter and the fragment")
}

func TestSourceLinkWithNoTemplateIsTheFrontDoorAndSaysSo(t *testing.T) {
	src := Source{Name: "FaceCheck.ID", Kind: KindRemoteFace, Home: "https://facecheck.id", Enabled: true}

	link, err := src.LinkFor(trickyMedia)
	require.NoError(t, err)
	assert.Equal(t, "https://facecheck.id", link.URL)
	assert.False(t, link.Prefilled, "an upload-based source hands the reader nothing prefilled, and the field says so")
}

func TestSourceLinkRefusals(t *testing.T) {
	t.Run("no media address", func(t *testing.T) {
		src := Source{Name: "TinEye", Kind: KindReverseMedia, Home: "https://tineye.com", Enabled: true}
		_, err := src.LinkFor("   ")
		assert.ErrorIs(t, err, ErrNoMedia)
	})

	t.Run("template with no hole", func(t *testing.T) {
		// A template with no {url} is a config mistake with two plausible
		// intentions, so it is refused by name instead of silently rendering a
		// link to the front page: the operator who wanted that leaves the
		// template empty.
		src := Source{Name: "Half Configured", Home: "https://example.com",
			Template: "https://example.com/search", Enabled: true}
		_, err := src.LinkFor(trickyMedia)
		assert.ErrorIs(t, err, ErrNoHole)
	})

	t.Run("non-http scheme", func(t *testing.T) {
		// The template comes from config and ends up in an href. This is the
		// boundary that stops a config mistake becoming script on a page.
		src := Source{Name: "Hostile", Home: "https://example.com",
			Template: "javascript:alert({url})", Enabled: true}
		_, err := src.LinkFor(trickyMedia)
		assert.ErrorIs(t, err, ErrUnsafeScheme)
	})
}

func TestLinksSkipsDisabledSourcesAndEmptyMediaIsNotAnError(t *testing.T) {
	sources := []Source{
		{Name: "On", Kind: KindReverseMedia, Home: "https://on.example", Enabled: true},
		{Name: "Off", Kind: KindReverseMedia, Home: "https://off.example", Enabled: false},
	}

	links, err := Links(sources, trickyMedia)
	require.NoError(t, err)
	require.Len(t, links, 1)
	assert.Equal(t, "On", links[0].Source)

	// "Nothing to look up yet" is a state of an early query, not a fault.
	links, err = Links(sources, "")
	require.NoError(t, err)
	assert.Empty(t, links)
}

func TestLinksReportsABrokenSourceRatherThanDroppingIt(t *testing.T) {
	sources := []Source{
		{Name: "Fine", Kind: KindReverseMedia, Home: "https://fine.example", Enabled: true},
		{Name: "Broken", Kind: KindReverseMedia, Home: "https://broken.example",
			Template: "https://broken.example/search", Enabled: true},
	}

	_, err := Links(sources, trickyMedia)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoHole,
		"a link that silently vanishes from the page is a config mistake nobody can see")
}

// TestDefaultSourcesAreOfferableAndTheDefaultIsTheSafeOne asserts the catalogue's
// own properties, including F10's default: every face service is OFF until an
// operator turns one on.
func TestDefaultSourcesAreOfferableAndTheDefaultIsTheSafeOne(t *testing.T) {
	sources := DefaultSources()
	require.NotEmpty(t, sources)

	names := map[string]Source{}
	for _, s := range sources {
		require.NotEmpty(t, s.Name)
		assert.True(t, s.Kind.Valid(), "%s has kind %q", s.Name, s.Kind)
		assert.NotContains(t, names, s.Name, "duplicate source name %s", s.Name)
		names[s.Name] = s

		// Every entry must be openable even with no template: a dead Home is a
		// dead link in a user's browser.
		link, err := s.LinkFor(trickyMedia)
		require.NoError(t, err, "%s", s.Name)
		assert.True(t, strings.HasPrefix(link.URL, "http"), "%s: %s", s.Name, link.URL)

		// F10: a remote_face link is a biometric query, and the default is the
		// operator's decision rather than ours.
		if s.Kind == KindRemoteFace {
			assert.False(t, s.Enabled, "%s matches faces and must start disabled (F10)", s.Name)
		}
	}

	// The parameterised ones are the point of F8: an engine that takes an
	// address gets the address.
	for _, name := range []string{"SauceNAO", "IQDB", "trace.moe", "TinEye", "Yandex Images"} {
		src, ok := names[name]
		require.True(t, ok, "%s is in the spec's catalogue", name)
		assert.True(t, src.Enabled, "%s is a reverse-image search and needs no gate", name)
		link, err := src.LinkFor(trickyMedia)
		require.NoError(t, err)
		assert.True(t, link.Prefilled, "%s takes an address, so the link carries it", name)
	}
}

func TestApplyOverrides(t *testing.T) {
	base := []Source{
		{Name: "SauceNAO", Kind: KindReverseMedia, Home: "https://saucenao.com",
			Template: "https://saucenao.com/search.php?url={url}", Enabled: true},
		{Name: "FaceCheck.ID", Kind: KindRemoteFace, Home: "https://facecheck.id"},
	}

	t.Run("disables a built-in by name", func(t *testing.T) {
		out := ApplyOverrides(base, []SourceOverride{{Name: "SauceNAO", Enabled: boolPtr(false)}})
		require.Len(t, out, 2)
		assert.False(t, out[0].Enabled)
	})

	t.Run("enables a face service when the operator says so", func(t *testing.T) {
		out := ApplyOverrides(base, []SourceOverride{{Name: "FaceCheck.ID", Enabled: boolPtr(true)}})
		assert.True(t, out[1].Enabled, "F10's gate is the operator's to open")
	})

	t.Run("a nil enabled leaves the default alone", func(t *testing.T) {
		out := ApplyOverrides(base, []SourceOverride{{Name: "SauceNAO", Home: "https://mirror.example"}})
		assert.True(t, out[0].Enabled, "changing a URL is not a statement about offering it")
		assert.Equal(t, "https://mirror.example", out[0].Home)
	})

	t.Run("a new source is appended and OFF", func(t *testing.T) {
		out := ApplyOverrides(base, []SourceOverride{
			{Name: "My Engine", Kind: KindReverseMedia, Home: "https://mine.example",
				Template: "https://mine.example/?q={url}"},
		})
		require.Len(t, out, 3)
		assert.Equal(t, "My Engine", out[2].Name)
		assert.False(t, out[2].Enabled,
			"adding a source to the config is not the same statement as turning it on")
	})

	t.Run("an empty template cannot clear a built-in one", func(t *testing.T) {
		out := ApplyOverrides(base, []SourceOverride{{Name: "SauceNAO", Enabled: boolPtr(false)}})
		assert.Contains(t, out[0].Template, "{url}",
			"\"I did not set this\" and \"remove the placeholder\" are indistinguishable in a string")
	})

	t.Run("matching is exact, so a typo adds rather than redefines", func(t *testing.T) {
		out := ApplyOverrides(base, []SourceOverride{
			{Name: "saucenao", Kind: KindReverseMedia, Home: "https://typo.example",
				Template: "https://typo.example/?q={url}", Enabled: boolPtr(true)},
		})
		require.Len(t, out, 3, "a different spelling is a different source")
		assert.Equal(t, "saucenao", out[2].Name)
		// The built-in is untouched: the typo added a source instead of
		// quietly redefining the one the catalogue ships.
		assert.Equal(t, "https://saucenao.com", out[0].Home)
		assert.Equal(t, "https://saucenao.com/search.php?url={url}", out[0].Template)
	})
}

func TestSourceKindValid(t *testing.T) {
	for _, k := range []SourceKind{KindReverseMedia, KindRemoteFace, KindCommunity} {
		assert.True(t, k.Valid())
	}
	assert.False(t, SourceKind("reverse").Valid())
	assert.False(t, SourceKind("").Valid())
	assert.False(t, errors.Is(ErrNoMedia, ErrNoHole))
}
