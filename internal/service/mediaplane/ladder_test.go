package mediaplane

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gofrs/uuid"
)

// The path scheme, asserted on a known id so that a change to it is visible rather than discoverable by an
// operator whose files stopped being found. The two-character subdirectory and the width in the filename are
// the two parts that matter: one keeps a directory listable on a spinning disk, the other is what makes a
// variant addressable at all.
func TestVariantPathIsTheDocumentedScheme(t *testing.T) {
	s, err := NewStore(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.FromStringOrNil("3f1c0a52-1111-4222-8333-444455556666")

	got := s.VariantPath(id, 600)
	want := filepath.Join(s.Dir(), "3f", "3f1c0a52-1111-4222-8333-444455556666_600.webp")
	if got != want {
		t.Fatalf("VariantPath = %q, want %q", got, want)
	}
}

// No media directory is NOT an error and NOT an empty store: the shipped default is an instance with no media
// directory, and every caller has to be able to tell "the ladder is off" from "the ladder has nothing yet".
func TestNoMediaDirIsANilStoreAndNotAFailure(t *testing.T) {
	s, err := NewStore("", "")
	if s != nil || err != nil {
		t.Fatalf("NewStore(\"\") = (%v, %v), want (nil, nil): an instance with no media directory is not broken",
			s, err)
	}
}

func TestAStoreOverAnUnusableDirectoryIsRefusedByName(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-here")
	_, err := NewStore(missing, "")
	var unusable ErrMediaDirUnusable
	if !errors.As(err, &unusable) {
		t.Fatalf("NewStore(missing dir) = %v, want ErrMediaDirUnusable", err)
	}
	if unusable.Path != missing {
		t.Errorf("the refusal names %q, want %q", unusable.Path, missing)
	}
}

// THE OVERLAP INVARIANT, in all four directions, because the failure it prevents is asymmetric: a cache
// inside the store means one cleanup of the cache takes the instance's images with it, and a store inside the
// cache means the same cleanup takes the store. The control -- two sibling directories -- is the arm that
// stops this from being "refuse everything".
func TestTheResizeCacheMustNotOverlapTheMediaDir(t *testing.T) {
	root := t.TempDir()
	media := filepath.Join(root, "media")
	cache := filepath.Join(root, "cache")
	// Every directory a case names is CREATED, including the ones the case expects to be refused: the
	// refusal under test is the overlap, and a fixture that also tripped the existence check would be
	// testing which of two mistakes this reports first rather than whether it reports this one. (The
	// existence check does run first, and that ordering is deliberate -- an operator is told about the path
	// they cannot use, not about a collision with a directory that is not there.)
	for _, d := range []string{media, cache, filepath.Join(media, "resized"), filepath.Join(cache, "stored")} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		name     string
		media    string
		cache    string
		overlaps bool
	}{
		{"the same directory", media, media, true},
		{"cache inside the store", media, filepath.Join(media, "resized"), true},
		{"store inside the cache", filepath.Join(cache, "stored"), cache, true},
		{"siblings", media, cache, false},
		{"no cache configured", media, "", false},
	}
	refused := 0
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := NewStore(c.media, c.cache)
			if c.overlaps {
				refused++
				if !errors.Is(err, ErrDirsOverlap) {
					t.Fatalf("NewStore(%q, %q) = %v, want ErrDirsOverlap", c.media, c.cache, err)
				}
				// The refusal names BOTH paths: an operator resolving it has to see which two settings
				// collided, and one of them may be inherited from a file they have not opened.
				for _, want := range []string{"cache_path", "media_dir", filepath.Clean(c.media)} {
					if !contains(err.Error(), want) {
						t.Errorf("the refusal does not mention %q: %q", want, err.Error())
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("NewStore(%q, %q) = %v, want a store", c.media, c.cache, err)
			}
		})
	}
	if refused == 0 {
		t.Fatal("no case overlapped: the invariant was never exercised")
	}
}

// A directory on a different filesystem is the state an unmounted disk leaves behind -- a perfectly ordinary
// empty directory where a mount point used to be -- and it is the one failure that existence and readability
// both pass. The device is set directly because mounting two filesystems in a unit test is not a thing.
func TestAMountThatChangedIsRefused(t *testing.T) {
	s, err := NewStore(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Check(); err != nil {
		t.Fatalf("Check() on a healthy store = %v", err)
	}
	if !s.DeviceChecked() {
		t.Skip("this platform reports no device ids, so the check cannot be exercised")
	}

	s.device++ // the same path, a different filesystem: the disk went away

	// checkErr, NOT `err`: writing `if err := s.Check(); !errors.Is(err, ...)` and then reading `err` in the
	// assertions below reaches the OUTER err -- the nil one from NewStore -- so the messages would be
	// asserted against a nil error. It panicked here rather than passing vacuously, which is the lucky
	// version of that mistake; with a non-nil outer error it would have asserted about the wrong value and
	// stayed green.
	checkErr := s.Check()
	if !errors.Is(checkErr, ErrMountChanged) {
		t.Fatalf("Check() after the mount changed = %v, want ErrMountChanged", checkErr)
	}
	// The refusal has to say what to look at, since the path itself is fine.
	for _, want := range []string{"different filesystem", "media_dir"} {
		if !contains(checkErr.Error(), want) {
			t.Errorf("the refusal does not say %q: %q", want, checkErr.Error())
		}
	}
	// And it reaches a caller through the read path, not only through Check.
	if _, err := s.OpenVariant(uuid.Must(uuid.NewV4()), 300); !errors.Is(err, ErrMountChanged) {
		t.Errorf("OpenVariant after the mount changed = %v, want ErrMountChanged", err)
	}
}

// The two answers a read can give, and the difference between them: a variant this instance HAS is bytes, and
// one it has never produced is nil with no error -- the caller's cue to fall through rather than to stop.
func TestOpenVariantAnswersWithBytesOrWithNothing(t *testing.T) {
	s, err := NewStore(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.Must(uuid.NewV4())

	if reader, err := s.OpenVariant(id, 300); reader != nil || err != nil {
		t.Fatalf("OpenVariant with no file = (%v, %v), want (nil, nil)", reader, err)
	}

	path := s.VariantPath(id, 300)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("RIFF....WEBP"), 0o644); err != nil {
		t.Fatal(err)
	}

	reader, err := s.OpenVariant(id, 300)
	if err != nil || reader == nil {
		t.Fatalf("OpenVariant with a file = (%v, %v)", reader, err)
	}
	defer reader.Close()
	buf := make([]byte, 32)
	n, _ := reader.Read(buf)
	if string(buf[:n]) != "RIFF....WEBP" {
		t.Errorf("read %q, want the stored bytes", buf[:n])
	}

	// A width the ladder does not store is not a refusal either: the request falls through to the instance's
	// own path, which is the only place a large view can be served from.
	//
	// THE FILE IS WRITTEN FIRST, and that is the whole of this arm. Asking for 1280 while no 1280 file exists
	// answered (nil, nil) whether or not the width rule was there -- the mutation that turns IsVariantWidth
	// into `width > 0` survived this test, because the FIXTURE'S ABSENCE produced the answer the rule was
	// supposed to produce. The same shape as any absence assertion whose subject is not present: it asserts
	// what the situation happens to be rather than what the code does. With a 1280 file on disk, only the
	// width rule can produce the fall-through.
	unscaled := s.VariantPath(id, 1280)
	if err := os.WriteFile(unscaled, []byte("RIFF....WEBPVP8L"), 0o644); err != nil {
		t.Fatal(err)
	}
	if reader, err := s.OpenVariant(id, 1280); reader != nil || err != nil {
		t.Errorf("OpenVariant at an unstored width, with a file present = (%v, %v), want (nil, nil): the width "+
			"the ladder does not store must fall through even when a file of that name exists", reader, err)
	}
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
