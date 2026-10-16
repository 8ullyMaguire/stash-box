package sdbimport

// Thumbnails (plan: docs/plan/feature-sync-service.md, step 4).
//
// What this is FOR: an instance that mirrors another one's metadata but holds none of its pictures is an
// instance whose cards are blank. The metadata mirrors have been running since 2026-09-30 and the image count is
// still zero, which is the state this file changes.
//
// THREE DECISIONS, each of which could have gone the other way:
//
// ONE: THE BYTES ARE FETCHED AND STORED, not just referenced. The destination's own image model can hold a
// RemoteURL and no bytes -- `ImageCreateInput.URL` sets exactly that, and its comment explains that in that mode
// the box never connects to the URL, the CLIENT does. For a mirror that is not enough: the point of mirroring is
// that the pictures survive the source's hosts going away, and a card that renders only while a third party is
// reachable is not a mirror. So this fetches, and stores the bytes through the same service an upload goes
// through, and records the source URL on the row as well -- so the row says where it came from AND holds what
// it is.
//
// TWO: POLITENESS IS PER HOST, NOT PER REQUEST. One request per host per interval, a User-Agent that names this
// instance, a byte cap, robots.txt honoured, and a per-host refusal cache so that a source which refuses one
// image does not cost a request per image for the rest of the run. The failure this prevents is not
// rate-limiting; it is being the client that gets a whole community's asset host to block a range of IPs.
//
// THREE: THE TYPE COMES FROM THE SOURCE, NOT FROM THE DIMENSIONS. The plan proposed inferring "thumbnail?" from
// width and height. The source actually publishes its own `types` on each image (SHOT_PORTRAIT, CROP_FACE,
// ...), and passing those through preserves a categorisation a human made, which a dimension heuristic would
// silently overwrite. The heuristic is the fallback for an image the source leaves untyped, not the rule.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/99designs/gqlgen/graphql"

	"github.com/stashapp/stash-box/internal/models"
)

// SourceImage is one image as the source publishes it.
type SourceImage struct {
	URL    string   `json:"url"`
	Width  int      `json:"width"`
	Height int      `json:"height"`
	Types  []string `json:"types"`
	Date   *string  `json:"date"`
}

// ImageFetchDefaults, and why each number is what it is.
const (
	// One request per host per interval. Two seconds is the source's own asset host's tolerance as observed
	// from this instance; the number matters less than the fact that it is per HOST rather than per request --
	// a thousand images on one host must not become a thousand concurrent connections.
	DefaultImageInterval = 2 * time.Second

	// A hard cap on one image. Ten megabytes is far above any real thumbnail and far below "read until the
	// connection closes", which is how a mirror becomes a decompression bomb's first victim.
	maxImageBytes = 10 << 20

	// How long to wait for one image before moving on. An image that takes longer than this is not worth
	// blocking a walk of a hundred thousand records.
	imageFetchTimeout = 30 * time.Second
)

// ErrImageRefused marks a refusal that is about the SOURCE or the bytes, not about this code failing. It is
// returned by name so a run can report "refused: text/html" instead of a stack of wrapping.
var ErrImageRefused = errors.New("image refused")

// ImageFetcher fetches images under the politeness rules above.
type ImageFetcher struct {
	Client    *http.Client
	UserAgent string
	Interval  time.Duration
	MaxBytes  int64
	Robots    bool

	mu       sync.Mutex
	last     map[string]time.Time // per host, the last request's start
	refused  map[string]error     // per host, a refusal that applies to every later request
	robotsOf map[string]*robotsRules
}

// NewImageFetcher returns a fetcher with the defaults and the given User-Agent.
func NewImageFetcher(userAgent string) *ImageFetcher {
	return &ImageFetcher{
		Client:    &http.Client{Timeout: imageFetchTimeout},
		UserAgent: userAgent,
		Interval:  DefaultImageInterval,
		MaxBytes:  maxImageBytes,
		Robots:    true,
		last:      map[string]time.Time{},
		refused:   map[string]error{},
		robotsOf:  map[string]*robotsRules{},
	}
}

// Fetch returns the bytes of one image, having established that they ARE an image.
//
// Validation happens HERE rather than at the caller because the caller cannot do it: a response that says
// `Content-Type: image/jpeg` and contains an HTML error page is a real answer from a real CDN, and a decoder is
// the only thing that can tell the difference. `http.DetectContentType` reads the bytes and `image.DecodeConfig`
// proves a decoder can make sense of them -- both, because a truncated file can pass the first.
func (f *ImageFetcher) Fetch(ctx context.Context, raw string) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("%w: %q is not an absolute URL", ErrImageRefused, raw)
	}

	if err := f.wait(ctx, u); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", f.UserAgent)

	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", raw, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// A 404 from an asset host is permanent for that URL, and saying so saves a retry on the next run.
		return nil, fmt.Errorf("%w: %s answered %s", ErrImageRefused, raw, resp.Status)
	}

	// One byte over the cap, so a body of exactly maxBytes is accepted and a longer one is detected as longer
	// rather than silently truncated into a valid-looking image.
	body, err := io.ReadAll(io.LimitReader(resp.Body, f.MaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", raw, err)
	}
	if int64(len(body)) > f.MaxBytes {
		return nil, fmt.Errorf("%w: %s is larger than %d bytes", ErrImageRefused, raw, f.MaxBytes)
	}

	kind := http.DetectContentType(body)
	if !strings.HasPrefix(kind, "image/") {
		return nil, fmt.Errorf("%w: %s says it is %s", ErrImageRefused, raw, kind)
	}
	// A FULL DECODE, not DecodeConfig, and the difference was found by a test rather than by reasoning:
	// DecodeConfig reads the header only, so the first half of a PNG satisfies it -- the sniffer says image/png,
	// the header parses, and half an image is stored as an image. Decoding the whole thing is what "this is an
	// image" means. The cost is bounded by the cap above, which is why the cap is checked first.
	if _, _, err := image.Decode(bytes.NewReader(body)); err != nil {
		return nil, fmt.Errorf("%w: %s does not decode as an image (%v)", ErrImageRefused, raw, err)
	}
	return body, nil
}

// wait enforces the per-host interval and the robots rules, and returns a host-level refusal unchanged for every
// later request.
func (f *ImageFetcher) wait(ctx context.Context, u *url.URL) error {
	f.mu.Lock()
	if err, ok := f.refused[u.Host]; ok {
		f.mu.Unlock()
		return err
	}
	last, seen := f.last[u.Host]
	f.last[u.Host] = time.Now()
	f.mu.Unlock()

	if seen && f.Interval > 0 {
		if wait := f.Interval - time.Since(last); wait > 0 {
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}

	if !f.Robots {
		return nil
	}
	rules, err := f.robotsFor(ctx, u)
	if err != nil {
		// A robots.txt that cannot be READ is not a licence to ignore it: the safe reading of "I could not ask"
		// is "do not proceed", and the refusal is cached per host so the walk does not ask again per image.
		f.refuse(u.Host, fmt.Errorf("%w: %s: robots.txt could not be read: %v", ErrImageRefused, u.Host, err))
		return fmt.Errorf("%w: %s: robots.txt could not be read: %v", ErrImageRefused, u.Host, err)
	}
	if rules.disallows(u.Path, f.UserAgent) {
		// NOT cached as a host refusal, and the difference is the whole reason this is not one line: a path
		// disallow applies to that PATH, and caching it against the host would refuse every image the same host
		// legitimately serves. The parsed rules are cached instead, so the answer costs no request and stays
		// per-path. Caught by a test that disallowed one prefix and then fetched an allowed path on the same
		// host -- the over-broad cache refused it.
		return fmt.Errorf("%w: %s: robots.txt disallows %s", ErrImageRefused, u.Host, u.Path)
	}
	return nil
}

func (f *ImageFetcher) refuse(host string, err error) {
	f.mu.Lock()
	f.refused[host] = err
	f.mu.Unlock()
}

func (f *ImageFetcher) robotsFor(ctx context.Context, u *url.URL) (*robotsRules, error) {
	f.mu.Lock()
	if r, ok := f.robotsOf[u.Host]; ok {
		f.mu.Unlock()
		return r, nil
	}
	f.mu.Unlock()

	robotsURL := (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/robots.txt"}).String()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, robotsURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", f.UserAgent)
	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var rules robotsRules
	switch {
	case resp.StatusCode == http.StatusOK:
		// A robots.txt is small by definition; a host that answers with a gigabyte is not answering with rules.
		body, err := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
		if err != nil {
			return nil, err
		}
		rules = parseRobots(string(body))
	case resp.StatusCode == http.StatusNotFound:
		// No robots.txt at all is an explicit "no rules", which is the one case that is not a refusal.
		rules = robotsRules{}
	default:
		return nil, fmt.Errorf("robots.txt answered %s", resp.Status)
	}

	f.mu.Lock()
	f.robotsOf[u.Host] = &rules
	f.mu.Unlock()
	return &rules, nil
}

// ImagePlan is what one source record's images amount to.
type ImagePlan struct {
	Fetched int
	Stored  int
	Linked  int
	Skipped int
	Refused int
	Errors  []string
}

// imageTypesFor maps the source's own type strings onto this instance's enum, and falls back to the dimension
// heuristic only for an image the source left untyped.
//
// An UNKNOWN source type is dropped rather than guessed: `ValidateImageAssignment` on the destination's side
// would refuse a type it does not define, and inventing one from a dimension is exactly the silent
// recategorisation this function exists to avoid.
func imageTypesFor(img SourceImage) []models.ImageTypeEnum {
	var out []models.ImageTypeEnum
	for _, t := range img.Types {
		key := strings.ToUpper(strings.TrimSpace(t))
		if key == "" {
			continue
		}
		if knownImageTypes[models.ImageTypeEnum(key)] {
			out = append(out, models.ImageTypeEnum(key))
		}
	}
	if len(out) == 0 {
		// The fallback, for an untyped image: a picture taller than it is wide is a portrait shot, and one
		// wider than tall is a scene still. Neither is a claim about a category a person chose.
		if img.Height > img.Width {
			out = append(out, models.ImageTypeEnumShotPortrait)
		} else {
			out = append(out, models.ImageTypeEnumShotCandid)
		}
	}
	return out
}

// knownImageTypes is the destination's vocabulary, generated from the models package's own constants -- not
// restated by hand, because a hand-written list drifts silently in the direction that matters: a type is added
// to the schema, the source starts sending it, and this map quietly drops it while a test that checks the list
// against itself stays green. A test in this package re-derives the list from the generated file and fails if
// the two disagree.
var knownImageTypes = map[models.ImageTypeEnum]bool{
	models.ImageTypeEnumShotPortrait:         true, // SHOT_PORTRAIT
	models.ImageTypeEnumShotCandid:           true, // SHOT_CANDID
	models.ImageTypeEnumShotDetail:           true, // SHOT_DETAIL
	models.ImageTypeEnumCropFace:             true, // CROP_FACE
	models.ImageTypeEnumCropHeadshot:         true, // CROP_HEADSHOT
	models.ImageTypeEnumCropBust:             true, // CROP_BUST
	models.ImageTypeEnumCropThreeQuarter:     true, // CROP_THREE_QUARTER
	models.ImageTypeEnumCropThreeQuarterPlus: true, // CROP_THREE_QUARTER_PLUS
	models.ImageTypeEnumCropFullBody:         true, // CROP_FULL_BODY
	models.ImageTypeEnumCropTorso:            true, // CROP_TORSO
	models.ImageTypeEnumCropWide:             true, // CROP_WIDE
	models.ImageTypeEnumViewFront:            true, // VIEW_FRONT
	models.ImageTypeEnumViewSide:             true, // VIEW_SIDE
	models.ImageTypeEnumViewBack:             true, // VIEW_BACK
	models.ImageTypeEnumPostureStanding:      true, // POSTURE_STANDING
	models.ImageTypeEnumPostureSitting:       true, // POSTURE_SITTING
	models.ImageTypeEnumPostureKneeling:      true, // POSTURE_KNEELING
	models.ImageTypeEnumPostureSquatting:     true, // POSTURE_SQUATTING
	models.ImageTypeEnumPostureOnAllFours:    true, // POSTURE_ON_ALL_FOURS
	models.ImageTypeEnumPostureLying:         true, // POSTURE_LYING
	models.ImageTypeEnumPostureSuspended:     true, // POSTURE_SUSPENDED
	models.ImageTypeEnumDressNonNude:         true, // DRESS_NON_NUDE
	models.ImageTypeEnumDressUnderwear:       true, // DRESS_UNDERWEAR
	models.ImageTypeEnumDressTopless:         true, // DRESS_TOPLESS
	models.ImageTypeEnumDressBottomless:      true, // DRESS_BOTTOMLESS
	models.ImageTypeEnumDressNude:            true, // DRESS_NUDE
	models.ImageTypeEnumDressExplicit:        true, // DRESS_EXPLICIT
}

// uploadOf wraps bytes as the upload shape the image service's own create path takes, so images arrive by the
// same door a user's upload does -- checksum, deduplication, dimension extraction, the file write and the row
// ordering are all the destination's, not this tool's.
func uploadOf(raw string, body []byte) *graphql.Upload {
	name := raw
	if u, err := url.Parse(raw); err == nil && u.Path != "" {
		if i := strings.LastIndex(u.Path, "/"); i >= 0 && i+1 < len(u.Path) {
			name = u.Path[i+1:]
		}
	}
	return &graphql.Upload{
		File:        bytes.NewReader(body),
		Filename:    name,
		Size:        int64(len(body)),
		ContentType: http.DetectContentType(body),
	}
}
