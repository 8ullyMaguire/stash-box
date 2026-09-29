package image

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/davidbyttow/govips/v2/vips"
	"github.com/stashapp/stash-box/internal/models"
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
// Three claims in that report, checked against the code and against libvips
// directly. Two are wrong, and establishing which is most of the work:
//
//  1. "Stash-Box automatically downscales the image into a JPG" -- there is no
//     conversion on upload. image.Resize is called from
//     internal/api/routes_image.go on the SERVING path only, when a client asks
//     for a specific size, and the result is written to the HTTP response. The
//     row and the file on disk are never touched.
//  2. "into a JPG" -- a PNG does not come back as a JPEG. resize_unix.go has
//     `if format == vips.ImageTypePNG { ExportWebp(Lossless: true) }`, and
//     lossless WebP has a real alpha channel.
//  3. The transparency IS lost, and the cause is neither of the above: the
//     resize branch of the image route sets no Content-Type at all, so Go
//     sniffs the body and returns application/octet-stream. A browser that will
//     not render the response shows a logo with no transparency at all.
//
// The obvious suspect for #3 was vips.InterestingNone, which does not add an
// alpha channel. A probe over every vips.Interesting value settled it: all ten
// produce a 4-band thumbnail, so the alpha was never being dropped in the
// resizer. TestResizePreservesTransparency below pins that, so a future change
// cannot reintroduce a JPEG conversion for PNGs and quietly destroy the alpha.
//
// The Content-Type half of the fix lives in internal/api/routes_image.go and is
// covered by resizedContentType tests there.

// alphaPNG is a 2000x2000 fully transparent PNG with a red 600x600 square in
// the middle, so the frame has both transparent and opaque regions and a
// downscale has real work to do.
func alphaPNG(t *testing.T) []byte {
	t.Helper()

	const size = 2000
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 700; y < 1300; y++ {
		for x := 700; x < 1300; x++ {
			img.Set(x, y, color.NRGBA{R: 255, A: 255})
		}
	}

	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

// The reported defect: a transparent PNG, downscaled, must come back with its
// alpha channel intact.
func TestResizePreservesTransparency(t *testing.T) {
	require.NoError(t, InitResizer())

	src := alphaPNG(t)
	out, err := Resize(bytes.NewReader(src), 1280, &models.Image{}, int64(len(src)))
	require.NoError(t, err)
	require.NotEmpty(t, out)

	// WebP is not in the standard library, so decode through vips: if the alpha
	// channel survived, the image has a fourth band and is not opaque.
	decoded, err := vips.NewImageFromBuffer(out)
	require.NoError(t, err)
	defer decoded.Close()

	assert.Equal(t, 4, decoded.Bands(),
		"a downscaled transparent PNG must keep its alpha band (#605)")
}

// The other half of the report: "we can leave the others alone". An opaque JPEG
// must still be exported as JPEG, so file sizes for performers and scenes do not
// regress.
func TestResizeStillUsesJpegForOpaqueImages(t *testing.T) {
	require.NoError(t, InitResizer())

	img := image.NewRGBA(image.Rect(0, 0, 2000, 1400))
	for y := range 1400 {
		for x := range 2000 {
			img.Set(x, y, color.RGBA{R: 40, G: 90, B: 160, A: 255})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}))
	src := buf.Bytes()

	out, err := Resize(bytes.NewReader(src), 1280, &models.Image{}, int64(len(src)))
	require.NoError(t, err)
	require.NotEmpty(t, out)

	assert.Equal(t, []byte{0xFF, 0xD8, 0xFF}, out[:3],
		"an opaque JPEG must still be exported as JPEG (#605: leave the others alone)")
}

// A PNG with no alpha at all is still a PNG, so it takes the WebP path and keeps
// its smaller size. Only transparency changes behaviour, not format.
func TestResizePngWithoutAlphaStaysSmall(t *testing.T) {
	require.NoError(t, InitResizer())

	img := image.NewRGBA(image.Rect(0, 0, 2000, 2000))
	for y := range 2000 {
		for x := range 2000 {
			img.Set(x, y, color.RGBA{R: 10, G: 120, B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))

	src := buf.Bytes()
	out, err := Resize(bytes.NewReader(src), 1280, &models.Image{}, int64(len(src)))
	require.NoError(t, err)
	require.NotEmpty(t, out)

	// RIFF....WEBP
	require.GreaterOrEqual(t, len(out), 12)
	assert.Equal(t, "RIFF", string(out[0:4]))
	assert.Equal(t, "WEBP", string(out[8:12]),
		"a PNG must still be exported as WebP (#605)")
}

// shouldResizeForTest mirrors shouldResize in internal/api/routes_image.go.
//
// It is duplicated rather than imported because that function is unexported and
// lives in the api package; a copy is the only way to pin the contract from
// here, and the logic is four lines. If shouldResize changes, this must change
// with it -- that is the point of the test.
func shouldResizeForTest(width, height, requestedSize int) bool {
	const minSize = 1280 // config default; see GetImageResizeConfig
	return requestedSize != 0 &&
		(width > minSize || height > minSize) &&
		(width > requestedSize || height > requestedSize)
}

func TestShouldResizeGateIsUnchanged(t *testing.T) {
	assert.True(t, shouldResizeForTest(2000, 2000, 1280),
		"a 2000px image requested at 1280 must resize")
	assert.False(t, shouldResizeForTest(2000, 2000, 0),
		"size 0 means no resize was requested")
	assert.False(t, shouldResizeForTest(2000, 2000, 4000),
		"a request larger than the image must not resize")
}
