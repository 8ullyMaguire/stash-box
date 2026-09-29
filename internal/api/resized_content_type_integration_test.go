//go:build integration

package api_test

import (
	"os"
	"strings"
	"testing"

	"github.com/stashapp/stash-box/internal/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Issue #605: "[Bug Report] Downscaled Studio Logos Lose Transparency".
//
//   Practically every studio image uploaded to the database will be a PNG file
//   of a logo on a transparent background. If the image is too large (greater
//   than 1280px in either dimension), Stash-Box automatically downscales the
//   image into a JPG. Because it's no longer a PNG, the transparency is lost and
//   replaced with a black background.
//
// The report's mechanism is wrong twice over. There is no conversion on upload,
// and a PNG is not converted to a JPEG -- it becomes lossless WebP, which has an
// alpha channel. (The resizer half of that is pinned in
// internal/image/resize_alpha_test.go, along with the probe that disproved the
// obvious suspect, vips.InterestingNone.)
//
// The transparency is lost because the resize branch of the image route never
// set a Content-Type. Go sniffs the body instead, finds WebP it has no detector
// for, and returns application/octet-stream. A browser will not render that, so
// the studio logo simply does not display -- which reads to a user as a logo
// with a black background rather than a transparent one.
//
// These are unit tests on the detector. ResizedContentType is exported
// specifically so this test can live in package api_test alongside every other
// test in this directory; the unexported spelling would have forced this one
// file into package api, which no other test here uses.

// A WebP body must be labelled image/webp. This is the case that was broken:
// PNG in, WebP out, no header set.
func TestResizedContentTypeDetectsWebp(t *testing.T) {
	webp := make([]byte, 32)
	copy(webp[0:4], "RIFF")
	copy(webp[8:12], "WEBP")

	assert.Equal(t, "image/webp", api.ResizedContentType(webp),
		"a resized PNG must be served as image/webp (#605)")
}

// The report's "leave the others alone": an opaque source still becomes a JPEG,
// and it must be labelled as one so file sizes and rendering are unchanged.
func TestResizedContentTypeDetectsJpeg(t *testing.T) {
	jpegBytes := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F'}

	assert.Equal(t, "image/jpeg", api.ResizedContentType(jpegBytes),
		"a resized opaque image must be served as image/jpeg (#605)")
}

// The windows/darwin resizer keeps PNG as PNG, so the detector must handle it
// too, or those builds would regress to application/octet-stream.
func TestResizedContentTypeDetectsPng(t *testing.T) {
	pngBytes := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00}

	assert.Equal(t, "image/png", api.ResizedContentType(pngBytes),
		"a resized PNG from the non-libvips resizer must be served as image/png (#605)")
}

// An unrecognised body must still be labelled as an image. Returning
// application/octet-stream is what broke rendering in the first place, so the
// fallback must never reintroduce it.
func TestResizedContentTypeFallsBackToAnImageType(t *testing.T) {
	for name, body := range map[string][]byte{
		"empty":     {},
		"short":     {0x01},
		"garbage":   []byte("not an image at all, just some bytes"),
		"truncated": {0xFF, 0xD8},
	} {
		got := api.ResizedContentType(body)
		assert.Equal(t, "image/jpeg", got,
			"%s: an unrecognised body must still be served as an image (#605)", name)
	}
}

// The gap that made the four tests above nearly vacuous: they exercise the
// detector, not the route. Deleting the `w.Header().Set("Content-Type",
// ResizedContentType(data))` line from the resize branch of
// internal/api/routes_image.go left all four of them green, because Go's default
// sniffer still labels the body and nothing asserts the header is set at all.
//
// This test reads the source of the resize branch and requires the header to be
// set there. Source inspection is normally a smell in a test, and it is here for
// a specific reason: the route needs a full HTTP request with a real stored
// image, a configured image_location and a resize-enabled config to reach that
// branch, and none of that is reachable from this package's harness. A grep that
// pins the wiring is weaker than a request-level test and stronger than
// nothing, and the comment above says so rather than leaving the gap implicit.
//
// If a request-level test is ever added here, delete this one.
func TestResizeBranchSetsContentType(t *testing.T) {
	src, err := os.ReadFile("routes_image.go")
	require.NoError(t, err)

	text := string(src)

	// Locate the resize branch.
	branch := text[strings.Index(text, "if shouldResize("):]
	end := strings.Index(branch, "\n\t// Serve full image")
	require.Greater(t, end, 0, "could not find the end of the resize branch")
	branch = branch[:end]

	assert.Contains(t, branch, `w.Header().Set("Content-Type"`,
		"the resize branch must set an explicit Content-Type (#605): without it "+
			"Go sniffs the body and returns application/octet-stream, which "+
			"browsers refuse to render")
	assert.Contains(t, branch, "ResizedContentType(data)",
		"the resize branch must label the body with the format the resizer "+
			"actually produced (#605)")
}
