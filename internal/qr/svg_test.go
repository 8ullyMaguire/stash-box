package qr

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// THE INVITE URL THIS IS FOR. The long one is not filler: a QR code's version (and therefore its
// module count) depends on the payload's length, so a test that only ever encodes a short string
// never exercises the matrix the real invite link produces.
const (
	shortURL = "https://example.test/invite/1b0e2f3a"
	longURL  = "https://stash.example.network/invite/01a10641-c1ef-7d92-bbd9-1d0808cf0e64"
)

// THE QUIET ZONE IS THE ONE THING A MATRIX-ONLY LIBRARY LEAVES TO ITS CALLER, and a scanner that
// fails for a missing margin is indistinguishable from a scanner that fails for a broken encoder. The
// renderer refuses a matrix without it, so this test is about whether the margin is REALLY there (the
// claim), and the renderer's refusal is checked below with a matrix that lacks it.
func TestTheMatrixCarriesTheQuietZoneTheRendererRequires(t *testing.T) {
	matrix, err := Bitmap(longURL)
	require.NoError(t, err)
	require.NotEmpty(t, matrix)

	side := len(matrix)
	require.Equal(t, side, len(matrix[0]), "the matrix is square")

	// Counted, not eyeballed: the margin is at least the four modules the specification requires.
	leading := 0
	for _, on := range matrix[side/2] {
		if on {
			break
		}
		leading++
	}
	assert.GreaterOrEqual(t, leading, QuietZone,
		"the matrix carries a quiet zone; without it the renderer would refuse to render")

	// And no dark module inside it, in any of the four margins.
	require.NoError(t, assertQuietZone(matrix))
}

// The renderer REFUSES a matrix with no margin rather than emitting a code that might not scan: this
// is the one place the renderer has to be able to say no, and the assertion is on its refusal.
func TestTheRendererRefusesAMatrixWithoutAMargin(t *testing.T) {
	full := make([][]bool, 0, 8)
	for i := 0; i < 8; i++ {
		row := make([]bool, 8)
		for j := range row {
			row[j] = i == 7 || j == 7 // dark right into the edge through the last column/row
		}
		full = append(full, row)
	}
	err := assertQuietZone(full)
	require.Error(t, err, "a matrix with dark modules at the edge is not renderable")
	assert.Contains(t, err.Error(), "quiet zone")
}

// The SVG's geometry is in MODULE units and its size in pixels, so a QR is the same picture at any
// scale. The three assertions here are the three things a consumer depends on: the document is
// standalone SVG, the viewBox matches the matrix, and the pixels moved with the scale.
func TestTheSVGIsAStandaloneDocumentSizedByTheMatrix(t *testing.T) {
	matrix, err := Bitmap(shortURL)
	require.NoError(t, err)
	side := len(matrix)

	svg, err := SVG(shortURL, 4)
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(svg, "<svg "), "a bare SVG document, not a wrapper")
	assert.True(t, strings.HasSuffix(svg, "</svg>"))
	assert.Contains(t, svg, `viewBox="0 0 `+strconv.Itoa(side)+` `+strconv.Itoa(side)+`"`)
	assert.Contains(t, svg, `width="`+strconv.Itoa(side*4)+`"`)
	assert.NotContains(t, svg, "<script", "no script, no external reference: this gets inlined")

	// The scale moves the SIZE and not the geometry.
	bigger, err := SVG(shortURL, 12)
	require.NoError(t, err)
	assert.Contains(t, bigger, `viewBox="0 0 `+strconv.Itoa(side)+` `+strconv.Itoa(side)+`"`)
	assert.Contains(t, bigger, `width="`+strconv.Itoa(side*12)+`"`)
	assert.NotContains(t, bigger, `width="`+strconv.Itoa(side*4)+`"`)
}

// One rect per RUN of dark modules, not one per module: the picture is identical and the document is a
// fraction of the size. This asserts the compression is real, by comparing the element count against
// the number of dark modules the matrix actually has.
func TestDarkModulesAreDrawnAsRuns(t *testing.T) {
	matrix, err := Bitmap(shortURL)
	require.NoError(t, err)

	dark := 0
	runs := 0
	for _, row := range matrix {
		inRun := false
		for _, on := range row {
			if on {
				dark++
				if !inRun {
					runs++
				}
			}
			inRun = on
		}
	}
	require.Greater(t, dark, 0, "the subject: this matrix has dark modules")

	svg, err := SVG(shortURL, 4)
	require.NoError(t, err)
	rects := strings.Count(svg, "<rect")

	assert.Greater(t, rects, 0, "something was drawn")
	assert.Equal(t, runs+1, rects,
		"one rect per run plus the background: %d runs for %d dark modules", runs, dark)
	assert.Less(t, rects, dark, "and that is fewer elements than one-per-module: %d < %d", rects, dark)
}

// THE VERIFICATION THE PLAN ASKS FOR: the rendered code decodes back to the content with an
// INDEPENDENT decoder. zbarimg is a different implementation of the same standard, reached through
// rsvg-convert, and it is the only assertion here that could catch an encoder that renders a
// beautiful, unscannable square.
//
// Skipped rather than failed when the tools are absent, so the suite stays runnable anywhere; the live
// walk runs it unconditionally on the host where they exist.
func TestTheRenderedCodeDecodesBackWithAnIndependentDecoder(t *testing.T) {
	zbar, err := exec.LookPath("zbarimg")
	if err != nil {
		t.Skip("zbarimg is not installed: the independent decoder is unavailable here")
	}
	rsvg, err := exec.LookPath("rsvg-convert")
	if err != nil {
		t.Skip("rsvg-convert is not installed, so the SVG cannot be handed to zbarimg")
	}

	for _, content := range []string{shortURL, longURL} {
		t.Run(content, func(t *testing.T) {
			svg, err := SVG(content, 6)
			require.NoError(t, err)

			dir := t.TempDir()
			svgPath := filepath.Join(dir, "invite.svg")
			pngPath := filepath.Join(dir, "invite.png")
			require.NoError(t, os.WriteFile(svgPath, []byte(svg), 0o600))

			out, err := exec.Command(rsvg, "-w", "600", svgPath, "-o", pngPath).CombinedOutput()
			require.NoError(t, err, "rsvg-convert: %s", out)

			out, err = exec.Command(zbar, "--quiet", "--raw", pngPath).CombinedOutput()
			require.NoError(t, err, "zbarimg: %s", out)

			assert.Equal(t, content, strings.TrimSpace(string(out)),
				"the decoded payload is the invite URL, byte for byte")
		})
	}
}

// The payload is rendered into an attribute, so it never reaches the document as markup: an SVG that
// interpolated a URL with a quote in it would be an injection, and the escape is asserted rather than
// assumed (the renderer's label attribute is static, but the assertion documents the rule).
func TestTheDocumentContainsNoUnescapedMarkup(t *testing.T) {
	svg, err := SVG(shortURL, 4)
	require.NoError(t, err)
	// The content appears NOWHERE as text: the SVG is geometry, so the URL cannot leak into the
	// document as markup or as a comment.
	assert.NotContains(t, svg, shortURL)
	assert.NotContains(t, svg, "<!--")
	assert.Regexp(t, regexp.MustCompile(`^<svg [^>]*>.*</svg>$`), svg)
}
