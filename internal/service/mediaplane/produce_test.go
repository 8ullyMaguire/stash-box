package mediaplane

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofrs/uuid"

	boximage "github.com/stashapp/stash-box/internal/image"
)

// A source photograph big enough that a 300 px variant is unambiguously a resize rather than the source with a
// new name: 1200x900, with structure in it so the encoder has something to compress.
func sourceJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1200, 900))
	for y := 0; y < 900; y++ {
		for x := 0; x < 1200; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: uint8((x * y) % 256), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// THE ROUND TRIP, THROUGH THE INSTANCE'S OWN PIPELINE -- and this test is why the format decision changed.
//
// The first version of the layout said "webp at 80, one format, not negotiated per request", which came from
// the storage measurement (Pillow). This instance cannot encode webp at all: internal/image exports webp only
// for PNG sources and JPEG at image_jpeg_quality otherwise, through libvips. A test that only checked "some
// bytes came out" would have passed for either claim; this one asserts what the pipeline DOES -- a JPEG source
// produces JPEG, because that is the branch libvips takes -- so the documentation and the binary agree.
func TestProducingAVariantUsesTheInstancesOwnPipeline(t *testing.T) {
	if err := boximage.InitResizer(); err != nil {
		t.Skipf("this build cannot start the image pipeline (%v), so there is nothing to measure here", err)
	}
	src := sourceJPEG(t)

	out, err := ProduceVariant(bytes.NewReader(src), int64(len(src)), 300)
	if err != nil {
		t.Fatalf("ProduceVariant: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("the producer returned no bytes")
	}
	if len(out) >= len(src) {
		t.Errorf("the variant is %d bytes against a %d-byte source: a thumbnail that grew is not a thumbnail",
			len(out), len(src))
	}

	format, ok := SniffVariantFormat(out)
	if !ok {
		t.Fatal("the produced bytes are neither of the two formats the store accepts")
	}
	if format != VariantFormatJPEG {
		t.Errorf("a JPEG source produced a %q variant; the instance's resizer exports webp only for PNG "+
			"sources, so this says the two disagree", format)
	}

	// It must be READABLE as what it claims to be, at a size the width bounds.
	img, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decoding the produced variant as jpeg: %v", err)
	}
	if b := img.Bounds(); b.Dx() > 300 && b.Dy() > 300 {
		t.Errorf("variant is %dx%d for a 300 px request", b.Dx(), b.Dy())
	}

	// And the same call through the store writes it where a read will find it, sniffed rather than labelled.
	dir := t.TempDir()
	store, err := NewStore(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.Must(uuid.NewV4())
	path, err := store.ProduceAndStore(id, 300, bytes.NewReader(src), int64(len(src)))
	if err != nil {
		t.Fatalf("ProduceAndStore: %v", err)
	}
	if path != store.VariantPath(id, 300, VariantFormatJPEG) {
		t.Errorf("stored at %q, want the jpeg path for this image", path)
	}
	reader, contentType, err := store.OpenVariant(id, 300)
	if err != nil || reader == nil {
		t.Fatalf("reading back what was just written = (%v, %v)", reader, err)
	}
	defer reader.Close()
	if contentType != "image/jpeg" {
		t.Errorf("content type = %q, want image/jpeg", contentType)
	}
}

// THE CASE AN AGGREGATOR ACTUALLY MEETS: an upstream that answers 200 with something that is not a picture.
// The bytes are refused by name, and -- the half that matters -- NOTHING IS WRITTEN, because everything that
// exists in the media directory is served.
func TestProducingRefusesBytesThatAreNotAPicture(t *testing.T) {
	cases := map[string][]byte{
		"an SVG":                  []byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect width="10" height="10"/></svg>`),
		"an HTML error page":      []byte("<!DOCTYPE html><html><body>404 Not Found</body></html>"),
		"a truncated JPEG":        {0xFF, 0xD8},
		"an empty body":           {},
		"someone's webp-mime lie": append([]byte("GIF89a"), make([]byte, 64)...),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			store, err := NewStore(dir, "")
			if err != nil {
				t.Fatal(err)
			}
			id := uuid.Must(uuid.NewV4())
			if _, err := store.WriteVariant(id, 300, data); !errors.Is(err, ErrUnknownVariantFormat) {
				t.Fatalf("WriteVariant(%s) = %v, want ErrUnknownVariantFormat", name, err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Errorf("the refusal left %d entries in the media directory: everything that exists there is "+
					"served, so a refused write must leave nothing", len(entries))
			}
		})
	}
}

func TestAWidthTheLadderDoesNotStoreIsRefusedByName(t *testing.T) {
	src := []byte("RIFF....WEBP")
	store, err := NewStore(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.WriteVariant(uuid.Must(uuid.NewV4()), 1280, src); !errors.Is(err, ErrVariantWidthNotStored) {
		t.Fatalf("WriteVariant at an unstored width = %v, want ErrVariantWidthNotStored: the width is refused "+
			"before the bytes are sniffed, and the message must name the WIDTH rather than the format", err)
	}
	if _, err := ProduceVariant(bytes.NewReader(src), int64(len(src)), 1280); !errors.Is(err, ErrVariantWidthNotStored) {
		t.Fatalf("ProduceVariant at an unstored width = %v, want ErrVariantWidthNotStored", err)
	}
}

// A file in the media directory whose extension the ladder does not write is INVISIBLE to it, and the request
// falls through to the instance's own path -- which is the right answer for a file this producer did not put
// there (an operator's own copy, a leftover from an older layout). It is deliberately not "served anyway under
// a guessed content type": that is how a browser is handed a file it will not render.
func TestAFileOfAnotherFormatIsInvisibleToTheLadder(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.Must(uuid.NewV4())
	path := store.VariantPath(id, 300, "gif")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("GIF89a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if reader, contentType, err := store.OpenVariant(id, 300); reader != nil || contentType != "" || err != nil {
		t.Fatalf("OpenVariant over a .gif = (%v, %q, %v), want the fall-through", reader, contentType, err)
	}
}

// THE STRUCTURAL INVARIANT, and the reason it is here rather than a test of the guard itself: OpenVariant
// refuses a file whose extension has no content type, but the read side only ever ASKS for the spellings
// VariantPaths produces -- so that guard can only fire if the two lists disagree. This is the test that keeps
// them agreeing: every spelling a read will try must have a content type, or a variant would be served with an
// empty Content-Type header.
func TestEverySpellingTheReaderTriesHasAContentType(t *testing.T) {
	store, err := NewStore(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.Must(uuid.NewV4())
	paths := store.VariantPaths(id, 300)
	if len(paths) == 0 {
		t.Fatal("VariantPaths returned nothing: this test would pass vacuously")
	}
	for _, p := range paths {
		format := strings.TrimPrefix(filepath.Ext(p), ".")
		if _, ok := VariantContentTypes[format]; !ok {
			t.Errorf("the reader will try %q, whose format %q has no content type", p, format)
		}
	}
	for format := range VariantContentTypes {
		if _, ok := SniffVariantFormat(map[string][]byte{
			VariantFormatWebP: []byte("RIFF\x00\x00\x00\x00WEBP"),
			VariantFormatJPEG: {0xFF, 0xD8, 0xFF},
		}[format]); !ok {
			t.Errorf("VariantContentTypes names %q, which SniffVariantFormat never produces: a format the "+
				"producer cannot write", format)
		}
	}
}

func TestProducingASourceWithNoLengthIsRefused(t *testing.T) {
	if _, err := ProduceVariant(bytes.NewReader([]byte("x")), 0, 300); err == nil {
		t.Fatal("a source of length zero was accepted: a source with no length is a source that was not fetched")
	}
}

// The two magic-number readers, on their own, including the near-misses.
func TestSniffingReadsTheBytesAndNotTheName(t *testing.T) {
	jpegBytes, err := os.ReadFile(filepath.Join(t.TempDir(), "nothing"))
	_ = jpegBytes
	_ = err
	pngBuf := new(bytes.Buffer)
	if err := png.Encode(pngBuf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		data   []byte
		format string
		ok     bool
	}{
		{"webp", []byte("RIFF\x00\x00\x00\x00WEBPVP8 "), VariantFormatWebP, true},
		{"jpeg", []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10}, VariantFormatJPEG, true},
		{"a png is not stored by the ladder", pngBuf.Bytes(), "", false},
		{"html", []byte("<html>"), "", false},
		{"RIFF but not webp (a wav)", []byte("RIFF\x00\x00\x00\x00WAVEfmt "), "", false},
		{"nothing at all", []byte{}, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			format, ok := SniffVariantFormat(c.data)
			if ok != c.ok || format != c.format {
				t.Errorf("SniffVariantFormat = (%q, %v), want (%q, %v)", format, ok, c.format, c.ok)
			}
		})
	}
}
