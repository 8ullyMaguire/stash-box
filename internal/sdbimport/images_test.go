package sdbimport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stashapp/stash-box/internal/models"
)

func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 6))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// A host that answers with HTML and a 200 is the case this validation exists for: it is a real answer from a
// real CDN, and only a decoder can tell it from a picture.
func TestAFileThatIsNotAnImageIsRefusedByName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg") // a LIE, which is the point
		fmt.Fprint(w, "<html><body>not found</body></html>")
	}))
	defer srv.Close()

	f := NewImageFetcher("test/1.0")
	f.Interval = 0
	_, err := f.Fetch(context.Background(), srv.URL+"/x.jpg")
	if !errors.Is(err, ErrImageRefused) {
		t.Fatalf("Fetch = %v, want ErrImageRefused: a 200 with a lying content-type is what a decoder is for", err)
	}
	if !strings.Contains(err.Error(), "text/html") {
		t.Errorf("the refusal does not say what the host actually sent: %v", err)
	}
}

// THE DECODE CHECK NEEDS ITS OWN SUBJECT, and this is the pass's test lesson: the HTML case above is caught by
// the content-type check first, so deleting the decode call left that test green. A truncated PNG is the subject
// only the decoder can catch -- the first bytes make http.DetectContentType say `image/png`, and half an image
// is not an image.
func TestATruncatedImageIsRefusedByTheDecoderAndNotByTheSniffer(t *testing.T) {
	full := pngBytes(t)
	truncated := full[:len(full)/2]
	if kind := http.DetectContentType(truncated); !strings.HasPrefix(kind, "image/") {
		t.Fatalf("the fixture is not doing its job: the sniffer already rejects it as %s, so this test would "+
			"pass without the decoder", kind)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(truncated)
	}))
	defer srv.Close()

	f := NewImageFetcher("test/1.0")
	f.Interval = 0
	_, err := f.Fetch(context.Background(), srv.URL+"/half.png")
	if !errors.Is(err, ErrImageRefused) {
		t.Fatalf("Fetch of a truncated PNG = %v, want ErrImageRefused from the decoder", err)
	}
	if !strings.Contains(err.Error(), "decode") {
		t.Errorf("the refusal does not name the decoder: %v", err)
	}
}

func TestA404IsRefusedByNameAndDoesNotLookLikeAFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	f := NewImageFetcher("test/1.0")
	f.Interval = 0
	_, err := f.Fetch(context.Background(), srv.URL+"/gone.jpg")
	if !errors.Is(err, ErrImageRefused) {
		t.Fatalf("Fetch = %v, want ErrImageRefused", err)
	}
}

// The cap is on BYTES READ, not on a header: a host that sends more than it promised must not be followed.
func TestAnImageLargerThanTheCapIsRefused(t *testing.T) {
	body := pngBytes(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(body)
	}))
	defer srv.Close()

	f := NewImageFetcher("test/1.0")
	f.Interval = 0
	f.MaxBytes = int64(len(body) - 1)
	if _, err := f.Fetch(context.Background(), srv.URL+"/big.png"); !errors.Is(err, ErrImageRefused) {
		t.Fatalf("Fetch = %v, want ErrImageRefused for a body over the cap", err)
	}

	// And exactly at the cap is accepted, so the boundary is the body's size and not an off-by-one that would
	// silently refuse every image of exactly the cap.
	f.MaxBytes = int64(len(body))
	if _, err := f.Fetch(context.Background(), srv.URL+"/big.png"); err != nil {
		t.Fatalf("an image of exactly the cap was refused: %v", err)
	}
}

func TestAnImageIsFetchedAndReturned(t *testing.T) {
	body := pngBytes(t)
	var served int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			http.NotFound(w, r)
			return
		}
		served++
		w.Header().Set("Content-Type", "image/png")
		w.Write(body)
	}))
	defer srv.Close()

	f := NewImageFetcher("test/1.0")
	f.Interval = 0
	got, err := f.Fetch(context.Background(), srv.URL+"/ok.png")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("returned %d bytes, want %d", len(got), len(body))
	}
	if served != 1 {
		t.Errorf("the server was asked %d times for one image", served)
	}
}

// POLITENESS IS PER HOST. The interval is measured between requests to the SAME host, and the first request to a
// host is never delayed -- otherwise a walk of one image per host would sleep through its entire run.
func TestTheIntervalIsPerHostAndNotPerRequest(t *testing.T) {
	body := pngBytes(t)
	var times []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			http.NotFound(w, r)
			return
		}
		times = append(times, time.Now())
		w.Header().Set("Content-Type", "image/png")
		w.Write(body)
	}))
	defer srv.Close()

	f := NewImageFetcher("test/1.0")
	f.Interval = 150 * time.Millisecond
	ctx := context.Background()
	started := time.Now()
	if _, err := f.Fetch(ctx, srv.URL+"/a.png"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Fetch(ctx, srv.URL+"/b.png"); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(started)

	if len(times) != 2 {
		t.Fatalf("the server saw %d requests, want 2", len(times))
	}
	// The assertion is on the CLIENT's elapsed time, because that is what the interval governs: the two fetches
	// plus the wait between them cannot have taken less than one interval. Measuring the server's arrival times
	// instead is what made the first version of this test flaky -- a request arrives a fraction of a millisecond
	// after the fetcher records its start, so the gap between arrivals is legitimately a hair under the
	// interval, and the test asserted the wrong quantity rather than the code failing.
	if elapsed < f.Interval {
		t.Errorf("two fetches to one host took %s, want at least one interval (%s)", elapsed, f.Interval)
	}
	// And the server-side gap, with the tolerance the measurement actually needs.
	if gap := times[1].Sub(times[0]); gap < f.Interval-time.Millisecond {
		t.Errorf("two requests to one host arrived %s apart, want at least %s", gap, f.Interval)
	}
}

// A HOST THAT DISALLOWS IS REFUSED ONCE, NOT ONCE PER IMAGE. The refusal cache is what stops a walk from paying
// a request per image to learn the same answer.
func TestARobotsDisallowRefusesAndIsAskedOnlyOnce(t *testing.T) {
	body := pngBytes(t)
	var robotsAsked, imagesAsked int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			robotsAsked++
			fmt.Fprint(w, "User-agent: *\nDisallow: /private/\n")
			return
		}
		imagesAsked++
		w.Header().Set("Content-Type", "image/png")
		w.Write(body)
	}))
	defer srv.Close()

	f := NewImageFetcher("test/1.0")
	f.Interval = 0
	ctx := context.Background()

	_, err := f.Fetch(ctx, srv.URL+"/private/a.png")
	if !errors.Is(err, ErrImageRefused) {
		t.Fatalf("Fetch of a disallowed path = %v, want ErrImageRefused", err)
	}
	// A second image under the same prefix is refused from the cache: no request to the host at all.
	if _, err := f.Fetch(ctx, srv.URL+"/private/b.png"); !errors.Is(err, ErrImageRefused) {
		t.Fatalf("the second disallowed fetch = %v, want the cached refusal", err)
	}
	if imagesAsked != 0 {
		t.Errorf("the host was asked for %d images it disallows", imagesAsked)
	}

	// A path the rules allow is still fetched, so the refusal is scoped to what was disallowed rather than to
	// the host.
	if _, err := f.Fetch(ctx, srv.URL+"/public/c.png"); err != nil {
		t.Fatalf("an allowed path was refused: %v", err)
	}
	if robotsAsked != 1 {
		t.Errorf("robots.txt was asked for %d times, want 1", robotsAsked)
	}
}

// A robots.txt that cannot be read is not a licence to proceed. The failure must be a refusal, not a fetch.
func TestARobotsThatCannotBeReadIsARefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(pngBytes(t))
	}))
	defer srv.Close()

	f := NewImageFetcher("test/1.0")
	f.Interval = 0
	if _, err := f.Fetch(context.Background(), srv.URL+"/a.png"); !errors.Is(err, ErrImageRefused) {
		t.Fatalf("Fetch = %v, want ErrImageRefused when robots.txt answers 500", err)
	}
}

func TestRobotsRules(t *testing.T) {
	rules := parseRobots(`
# a comment
User-agent: *
Disallow: /all/
Disallow: /*.zip$

User-agent: stash-box-sync
Disallow: /agent-only/
Allow: /whatever

User-agent: other
Disallow: /other-only/
`)
	cases := []struct {
		path, agent string
		want        bool
	}{
		{"/all/x.png", "stash-box-sync/1.0", true},
		{"/agent-only/x.png", "stash-box-sync/1.0", true},
		{"/other-only/x.png", "stash-box-sync/1.0", false}, // another agent's group
		{"/other-only/x.png", "other/1.0", true},           // its own group
		{"/x.zip", "stash-box-sync/1.0", true},             // the $ anchor
		{"/x.zip.jpg", "stash-box-sync/1.0", false},        // and it is anchored
		{"/deep/x.zip", "stash-box-sync/1.0", true},        // * matches any prefix
		{"/public/x.png", "stash-box-sync/1.0", false},     // nothing applies
	}
	for _, c := range cases {
		if got := rules.disallows(c.path, c.agent); got != c.want {
			t.Errorf("disallows(%q, %q) = %v, want %v", c.path, c.agent, got, c.want)
		}
	}
}

// The source's own types are passed through; the dimension heuristic is the fallback for an untyped image, not
// the rule. A type the destination does not define is DROPPED rather than guessed: sending it would make the
// destination's validator refuse the whole image.
func TestImageTypesComeFromTheSourceAndFallBackToDimensions(t *testing.T) {
	typed := imageTypesFor(SourceImage{Width: 100, Height: 300, Types: []string{"CROP_FACE"}})
	if len(typed) != 1 || typed[0] != models.ImageTypeEnumCropFace {
		t.Errorf("typed = %v, want the source's own CROP_FACE", typed)
	}

	untypedPortrait := imageTypesFor(SourceImage{Width: 100, Height: 300})
	if len(untypedPortrait) != 1 || untypedPortrait[0] != models.ImageTypeEnumShotPortrait {
		t.Errorf("an untyped tall image = %v, want the portrait fallback", untypedPortrait)
	}
	untypedWide := imageTypesFor(SourceImage{Width: 300, Height: 100})
	if len(untypedWide) != 1 || untypedWide[0] != models.ImageTypeEnumShotCandid {
		t.Errorf("an untyped wide image = %v, want the candid fallback", untypedWide)
	}

	unknown := imageTypesFor(SourceImage{Width: 1, Height: 1, Types: []string{"NO_SUCH_TYPE"}})
	if len(unknown) != 1 || unknown[0] != models.ImageTypeEnumShotCandid {
		t.Errorf("an unknown source type = %v, want the fallback rather than the unknown key", unknown)
	}
}

// The vocabulary is GENERATED FROM THE MODELS PACKAGE, and this test re-derives it. A hand-written list drifts
// in the direction that matters: a type is added to the schema, the source sends it, and the map silently drops
// it while a test that checks the list against itself stays green.
func TestTheImageTypeVocabularyMatchesTheGeneratedConstants(t *testing.T) {
	blob, err := os.ReadFile(filepath.Join("..", "models", "generated_models.go"))
	if err != nil {
		t.Skipf("the generated models are not readable from here: %v", err)
	}
	re := regexp.MustCompile(`(ImageTypeEnum[A-Za-z0-9]+)\s+ImageTypeEnum\s*=\s*"([A-Z_]+)"`)
	found := re.FindAllStringSubmatch(string(blob), -1)
	if len(found) < 20 {
		t.Fatalf("found only %d image type constants; the pattern is wrong and this test proves nothing", len(found))
	}
	for _, m := range found {
		if !knownImageTypes[models.ImageTypeEnum(m[2])] {
			t.Errorf("the source can send %s (%s) and this map does not know it, so the type is dropped "+
				"silently", m[2], m[1])
		}
	}
	if len(knownImageTypes) != len(found) {
		t.Errorf("the map holds %d types and the generated models define %d: they have drifted",
			len(knownImageTypes), len(found))
	}
}
