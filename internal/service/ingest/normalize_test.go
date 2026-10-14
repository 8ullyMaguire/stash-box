package ingest

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The normaliser's rules as a table. Each case is named for the rule it pins, because a
// failure here should read as "the rule about ports broke" and not as "one of eighteen
// URLs".
func TestNormalizeURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"the host is lowercased", "https://EXAMPLE.com/a", "https://example.com/a"},
		{"a leading www is removed", "https://www.example.com/a", "https://example.com/a"},
		{"the default https port is removed", "https://example.com:443/a", "https://example.com/a"},
		{"the default http port is removed", "http://example.com:80/a", "http://example.com/a"},
		{"a non-default port is kept", "https://example.com:8443/a", "https://example.com:8443/a"},
		{"the fragment is dropped", "https://example.com/a#t=30", "https://example.com/a"},
		{"a tracking parameter is dropped", "https://example.com/a?utm_source=nl", "https://example.com/a"},
		{"an uppercased tracking parameter is dropped", "https://example.com/a?UTM_Source=nl", "https://example.com/a"},
		{"a click id is dropped", "https://example.com/a?fbclid=xyz", "https://example.com/a"},
		{"parameters are sorted by name", "https://example.com/a?b=2&a=1", "https://example.com/a?a=1&b=2"},
		{"a parameter the site may need is kept verbatim", "https://example.com/a?id=AbC%2Fdef", "https://example.com/a?id=AbC%2Fdef"},
		{"a valueless flag is kept as a bare name", "https://example.com/a?hd&b=2", "https://example.com/a?b=2&hd"},
		{"a trailing slash is removed", "https://example.com/a/", "https://example.com/a"},
		{"the root path is not emptied by that", "https://example.com", "https://example.com/"},
		{"the PATH case is preserved (it is case-sensitive)", "https://example.com/Video/123", "https://example.com/Video/123"},
		{"surrounding whitespace is trimmed", "  https://example.com/a  ", "https://example.com/a"},
		{"http is NOT folded into https", "http://example.com/a", "http://example.com/a"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := NormalizeURL(c.in)
			require.NoError(t, err, "%q should normalise", c.in)
			assert.Equal(t, c.want, got)
		})
	}
}

func TestNormalizeURLRefuses(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"no scheme (ambiguous between http and https)", "example.com/a"},
		{"a non-http scheme", "ftp://example.com/a"},
		{"a javascript URL", "javascript:alert(1)"},
		{"no host", "https:///a"},
		{"empty input", ""},
		{"whitespace only", "   "},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := NormalizeURL(c.in)
			assert.Error(t, err, "%q must be refused rather than fetched", c.in)
			assert.Empty(t, got, "a refused URL must not come back with a usable key")
		})
	}
}

// The claim the normaliser exists for, asserted as an EQUALITY between two inputs rather
// than as a single expected output.
//
// A table of one-output-per-input cannot express this: every case above would still pass
// if the two URLs normalised to two different strings, because each one is only checked
// against itself. The whole point of the function is that these collapse, so that is what
// is tested.
func TestNormalizationFoldsWhatDedupDependsOn(t *testing.T) {
	pairs := [][2]string{
		{
			"https://www.Example.com/video/123/?utm_source=twitter&utm_medium=social#t=90",
			"https://example.com/video/123",
		},
		{
			"https://example.com/video/123?b=2&a=1",
			"https://example.com/video/123?a=1&b=2",
		},
	}

	for _, pair := range pairs {
		t.Run(pair[0], func(t *testing.T) {
			left, err := NormalizeURL(pair[0])
			require.NoError(t, err)
			right, err := NormalizeURL(pair[1])
			require.NoError(t, err)
			assert.Equal(t, right, left,
				"these two are the same page, and a key that says otherwise makes every "+
					"submission of the second one a duplicate scene")
		})
	}
}
