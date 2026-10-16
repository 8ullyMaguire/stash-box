package mediaplane

// The VARIANT LADDER's storage (plan phase 10, slice 10c-1). See mediaplane.go for the package's purpose.
//
// WHAT THIS SLICE IS, AND WHAT IT IS NOT. It is the layout and the two invariants -- where a stored variant
// lives, that the tree it lives in is still the tree this process started with, and that the resize cache is
// not inside it. It is NOT the producer: nothing here fetches an upstream image, re-encodes it or writes a
// variant. That is the next slice, and naming it is part of this one, because a layout with no producer is
// exactly the shape this programme keeps finding -- a thing that exists and nothing asks for.
//
// IT HAS A CALLER, WHICH IS THE POINT. The image route asks this package for a variant BEFORE it goes to the
// database, so a stored variant is served without a query and the layout is exercised by every request that
// reaches it. A layout nobody reads would be a comment.
//
// WHY A SEPARATE DIRECTORY FROM THE RESIZE CACHE, when both hold derived bytes. The cache is a CACHE: it may
// be deleted at any moment, by an operator, by a cleanup job, and the instance is expected to carry on. The
// media directory holds the instance's own copies of images it ingested -- still derived, still re-fetchable,
// but the thing the ladder produces rather than the thing it speeds up. Two lifetimes, so two directories,
// and the invariant below is the consequence of the distinction rather than a tidiness rule: a cache inside
// the store means one `rm -rf` of the cache can take the store with it.
//
// THE SECOND INVARIANT IS THE ONE THE USB DISK EARNED. A directory that was on a mounted disk when the
// process started, and is on the root filesystem now, means the disk is gone -- and the path still EXISTS, so
// existence and readability both pass. That is the shape store.go's own comment names as the gap this slice
// closes: a stale mount point leaves an empty directory behind, and an instance that served from it would
// answer 404 for every image while its startup line still claimed it was serving.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/gofrs/uuid"

	"github.com/stashapp/stash-box/internal/config"
)

// VariantWidths are the widths the ladder stores, ascending. Naming them here is the same decision as the
// measured storage table in the plan: one bounded thumbnail per entity, not a variant per request.
//
// THEY ARE A SUBSET OF WHAT THE ROUTE CAN ASK FOR, and that is an invariant rather than a coincidence. The
// image route accepts size=300, 600 or 1280 (internal/api/routes_image.go's allowedSizes), so a ladder that
// stored 320 and 480 -- which is what this said an hour ago, taken from the measurement rather than from the
// route -- would have produced files that NO REQUEST CAN EVER ASK FOR: the exact "a thing nothing asks for"
// shape this programme keeps finding, arrived at by writing the second list from the first one's intent
// instead of reading it. A test in package api asserts the subset relation, because that is the only place
// both lists are visible.
//
// 1280 IS DELIBERATELY NOT STORED. It is requestable, and it is the width whose re-encode is several times
// the bytes of the others (the measurement's own scaling: 320 px ~15 KB, 480 px ~30 KB, so 1280 px is ~200 KB
// or more) for a size a thumbnail grid never asks for. A request for it falls through to the instance's own
// image path, which is the right answer for a large view: serve it from the store, or not at all.
//
// A request for a width that is not in this list is not a refusal -- it falls through. The ladder is an
// accelerator with a bounded footprint, not a gate on what may be requested.
var VariantWidths = []int{300, 600}

// ErrStorageUnusable is returned when the media directory cannot be served from at all.
var ErrStorageUnusable = errors.New("the media directory cannot be served from")

// ErrMountChanged is returned when the media directory is on a different filesystem than the one this
// process resolved at startup.
//
// The message says what an operator has to check, because the fact that produced it -- a directory that
// exists and is readable and is the WRONG directory -- is invisible to every other check this package makes.
var ErrMountChanged = errors.New("the media directory is on a different filesystem than when this instance " +
	"started: the disk it was on is no longer mounted there (`media_dir` now resolves to a directory on " +
	"another filesystem, which is where an unmounted mount point ends up)")

// ErrDirsOverlap is returned when the resize cache is inside the media directory or is the same directory.
var ErrDirsOverlap = errors.New("the resize cache must not be inside the media directory (`cache_path` and " +
	"`media_dir`): the cache is deletable and the store is not, so one cleanup of the cache would take the " +
	"instance's images with it")

// resolved and resolveErr are the process-wide store, built once.
//
// Once per process, for the reason the resize cache does the same: the directory and the filesystem it is on
// are facts about the process's environment, and re-reading them per request would make a per-request syscall
// the only thing standing between a stale path and a served byte -- while ALSO making the device id
// meaningless, since the comparison that catches an unmounted disk is only meaningful against the value taken
// when the process STARTED.
var (
	resolved    *Store
	resolveOnce sync.Once
	resolveErr  error
)

// ResolveStore returns this process's media store, building it on first use.
//
// A nil store with a nil error is not a failure: it means no media directory is configured, which is the
// shipped default, and callers must treat it as "the ladder is off". An error is a configuration this
// instance cannot serve from, and it is returned to every caller rather than once, so that a startup line
// and a request agree about it.
func ResolveStore() (*Store, error) {
	resolveOnce.Do(func() {
		resolved, resolveErr = NewStore(config.GetMediaDir(), config.GetImageCachePath())
	})
	return resolved, resolveErr
}

// ResetStoreForTest drops the process-wide store so a test can build another one. It exists because the
// alternative -- a global that a test cannot put back -- makes every test after the first one depend on the
// order it ran in.
func ResetStoreForTest() {
	resolveOnce = sync.Once{}
	resolved, resolveErr = nil, nil
}

// Store is the media directory, resolved once at startup and checked on the way to serving anything.
type Store struct {
	// dir is the media directory as configured, cleaned but not absolutised: an error message should name
	// the path the operator wrote.
	dir string

	// cacheDir is the resize cache's directory, for the overlap invariant. Empty when the instance has no
	// resize cache configured, which is not an error -- an instance that stores variants and caches nothing
	// is a valid configuration.
	cacheDir string

	// device is the filesystem the media directory was on when this process resolved it. Zero means the
	// check could not be made (a platform without device ids), and it is then SKIPPED rather than passed
	// silently: `DeviceChecked` says which happened, so a caller can report the difference.
	device uint64

	// deviceChecked records whether a device id was available at startup.
	deviceChecked bool
}

// NewStore resolves the media directory for serving, and refuses a configuration whose two directories
// overlap.
//
// It returns nil with a nil error when no media directory is configured: an instance that has not named one
// serves no image bytes and has nothing to resolve. A caller must treat nil as "the ladder is off", not as
// "the ladder is empty".
func NewStore(mediaDir, cacheDir string) (*Store, error) {
	dir := filepath.Clean(strings.TrimSpace(mediaDir))
	if strings.TrimSpace(mediaDir) == "" {
		return nil, nil
	}
	if err := MediaDirErrorFor(dir); err != nil {
		return nil, err
	}

	// The overlap check is on the CONFIGURED paths, before anything is created: two paths that are the same
	// directory, or one inside the other, is an operator mistake that would take effect the first time the
	// cache was cleaned.
	cache := filepath.Clean(strings.TrimSpace(cacheDir))
	if cache != "" && cache != "." {
		if cache == dir || strings.HasPrefix(cache+string(filepath.Separator), dir+string(filepath.Separator)) ||
			strings.HasPrefix(dir+string(filepath.Separator), cache+string(filepath.Separator)) {
			return nil, fmt.Errorf("%w: cache_path=%s media_dir=%s", ErrDirsOverlap, cache, dir)
		}
	}

	s := &Store{dir: dir, cacheDir: cache}
	s.device, s.deviceChecked = deviceOf(dir)
	return s, nil
}

// Dir is the media directory this store resolved, for a startup line or a test.
func (s *Store) Dir() string { return s.dir }

// DeviceChecked reports whether the startup device id could be read, i.e. whether Check can compare.
func (s *Store) DeviceChecked() bool { return s.deviceChecked }

// Check reports whether this instance can still serve from the media directory.
//
// TWO FACTS, and the second is the one no other check in this package can make: the directory has to be
// readable still, and it has to be on the filesystem it was on at startup. The second is what catches a
// disk that was unmounted, because the mount point left behind is a perfectly ordinary empty directory.
func (s *Store) Check() error {
	if s == nil {
		return ErrNoMediaDir
	}
	if err := MediaDirErrorFor(s.dir); err != nil {
		return err
	}
	if !s.deviceChecked {
		return nil
	}
	now, ok := deviceOf(s.dir)
	if !ok {
		return nil
	}
	if now != s.device {
		return ErrMountChanged
	}
	return nil
}

// VariantPath is where a stored variant of an image lives.
//
// `{id}_{width}.webp` in a two-character subdirectory, which is the resize cache's own spelling extended by
// one level: an operator who has looked at the cache recognises this, and a directory with a hundred thousand
// entries in it is a directory that is slow to list on the spinning disk this is likely to live on.
//
// The extension is part of the path rather than a variable because the ladder's output format is a DECISION
// (webp at 80 for both widths, measured at a median 14.9 KB at 320 px and 30.2 KB at 480 px). A format
// negotiated per request would make the layout depend on who asked.
func (s *Store) VariantPath(id uuid.UUID, width int) string {
	name := id.String()
	return filepath.Join(s.dir, name[:2], fmt.Sprintf("%s_%d.webp", name, width))
}

// OpenVariant returns a stored variant, or nil when this instance does not have one.
//
// nil WITH NO ERROR is the "not produced yet" answer, and it is distinct from an error: an instance that has
// never fetched this image's bytes is not broken, and the caller falls through to its own path. An error is
// reserved for the storage being unusable, which is the caller's reason to stop rather than to continue.
func (s *Store) OpenVariant(id uuid.UUID, width int) (io.ReadCloser, error) {
	if s == nil {
		return nil, nil
	}
	if err := s.Check(); err != nil {
		return nil, err
	}
	if !IsVariantWidth(width) {
		return nil, nil
	}
	f, err := os.Open(s.VariantPath(id, width))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return f, nil
}

// IsVariantWidth reports whether a requested width is one the ladder stores.
func IsVariantWidth(width int) bool {
	for _, w := range VariantWidths {
		if w == width {
			return true
		}
	}
	return false
}

// deviceOf reads the filesystem id a path is on. The second return value is false when the platform does not
// answer, which is a fact about the platform and not about the path.
//
// It exists as a function so that the one caller that must NOT be guessed at -- "is this still the disk it
// was" -- has a single implementation, and so a test can construct the state it needs rather than mounting
// two filesystems.
func deviceOf(path string) (uint64, bool) {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return 0, false
	}
	return uint64(st.Dev), true
}
