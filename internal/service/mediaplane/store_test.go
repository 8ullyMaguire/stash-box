package mediaplane

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stashapp/stash-box/internal/config"
)

// The empty value is a REFUSAL, not a pass. An instance with no media directory configured and the
// switch on would answer 404 for every image it holds a row for -- indistinguishable, from outside, from
// an instance that has no such image. Asserted by identity so the message an operator reads is the one
// this package wrote, not a copy that drifted.
func TestNoMediaDirIsRefusedByName(t *testing.T) {
	err := MediaDirErrorFor("")
	if !errors.Is(err, ErrNoMediaDir) {
		t.Fatalf("MediaDirErrorFor(\"\") = %v, want ErrNoMediaDir", err)
	}
	if !strings.Contains(err.Error(), "media_dir") {
		t.Errorf("the refusal does not name the key: %q", err.Error())
	}
}

// Whitespace is the same as empty, because a config file with `media_dir: " "` is a person who meant to
// leave it blank -- and a path of one space would stat as a missing file and be reported as a
// directory problem the operator could not see in their own config.
func TestAWhitespaceOnlyMediaDirIsTreatedAsUnset(t *testing.T) {
	if err := MediaDirErrorFor("   "); !errors.Is(err, ErrNoMediaDir) {
		t.Errorf("MediaDirErrorFor(\"   \") = %v, want ErrNoMediaDir", err)
	}
}

// The three unusable states name the PATH and the REASON, because "media_dir is unusable" does not tell
// an operator with several instances which config to open, and the three fixes are different.
func TestTheThreeUnusableStatesNameThePathAndTheReason(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "not-here")
	notADir := filepath.Join(dir, "a-file")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		path   string
		reason string
	}{
		{"missing", missing, "does not exist"},
		{"not a directory", notADir, "not a directory"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := MediaDirErrorFor(c.path)
			var unusable ErrMediaDirUnusable
			if !errors.As(err, &unusable) {
				t.Fatalf("MediaDirErrorFor(%q) = %v, want ErrMediaDirUnusable", c.path, err)
			}
			if unusable.Path != c.path {
				t.Errorf("the refusal names path %q, want %q", unusable.Path, c.path)
			}
			if !strings.Contains(unusable.Reason, c.reason) {
				t.Errorf("the refusal's reason is %q, want it to say %q", unusable.Reason, c.reason)
			}
			if !strings.Contains(err.Error(), "media_dir") {
				t.Errorf("the refusal does not name the key: %q", err.Error())
			}
		})
	}
}

// A directory that exists is accepted, and that is the CONTROL for the refusals above: a check that
// refused everything would pass every test in this file up to here and make the feature unusable. The
// temp dir is real, empty, and readable -- exactly what the ladder's first slice will write into.
func TestAnExistingReadableDirectoryIsAccepted(t *testing.T) {
	if err := MediaDirErrorFor(t.TempDir()); err != nil {
		t.Errorf("MediaDirErrorFor(existing dir) = %v, want nil", err)
	}
}

// Mode 000 stats fine and cannot be served from: opening is what answers that question, and it is the
// part stat alone would miss. Skipped as root, where the permission bits do not bind.
func TestADirectoryThatCannotBeReadIsRefused(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: mode 000 does not deny access, so this test would prove nothing")
	}
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })

	err := MediaDirErrorFor(locked)
	var unusable ErrMediaDirUnusable
	if !errors.As(err, &unusable) {
		t.Fatalf("MediaDirErrorFor(mode 000) = %v, want ErrMediaDirUnusable", err)
	}
	if !strings.Contains(unusable.Reason, "cannot be read") {
		t.Errorf("the reason is %q, want it to say the directory cannot be read", unusable.Reason)
	}
}

// THE ORDER, which is the part of the gate a reader is most likely to get wrong when the two checks are
// added to each other. An instance that serves no image bytes must be told THAT, even when its media
// directory is also broken: the operator has not agreed to serve images, so sending them to fix a
// directory is sending them to the second of two things. Asserted by identity rather than by text,
// because two refusals that read the same are two refusals a caller cannot tell apart.
func TestTheSwitchIsReportedBeforeTheStorage(t *testing.T) {
	previousImages, previousDelivery, previousDir := config.C.ImagesEnabled, config.C.ContentDelivery, config.C.MediaDir
	t.Cleanup(func() {
		config.C.ImagesEnabled, config.C.ContentDelivery, config.C.MediaDir = previousImages, previousDelivery, previousDir
	})

	config.C.ImagesEnabled, config.C.ContentDelivery = false, false
	config.C.MediaDir = filepath.Join(t.TempDir(), "also-missing")
	if err := RequireImageBytes(); !errors.Is(err, ErrNoImageBytes) {
		t.Errorf("RequireImageBytes() with the switch off and a broken directory = %v, want ErrNoImageBytes", err)
	}
}

// The gate follows the storage as well as the switch -- both directions, because a gate that always
// refused would pass the test above and make the capability unbuildable.
func TestTheGateFollowsTheMediaDirBothWays(t *testing.T) {
	previousImages, previousDelivery, previousDir := config.C.ImagesEnabled, config.C.ContentDelivery, config.C.MediaDir
	t.Cleanup(func() {
		config.C.ImagesEnabled, config.C.ContentDelivery, config.C.MediaDir = previousImages, previousDelivery, previousDir
	})

	config.C.ImagesEnabled, config.C.ContentDelivery = true, false

	config.C.MediaDir = ""
	if err := RequireImageBytes(); !errors.Is(err, ErrNoMediaDir) {
		t.Errorf("RequireImageBytes() with the switch on and no directory = %v, want ErrNoMediaDir", err)
	}

	config.C.MediaDir = t.TempDir()
	if err := RequireImageBytes(); err != nil {
		t.Errorf("RequireImageBytes() with the switch on and a real directory = %v, want nil", err)
	}
}

// The startup line has to distinguish three states, and the middle one is the whole reason it changed:
// an instance whose switch is ON and whose directory is missing would otherwise log the same line as a
// working one. Each arm asserts the words an operator would search for.
func TestTheImageServingLineNamesAllThreeStates(t *testing.T) {
	previousImages, previousDelivery, previousDir := config.C.ImagesEnabled, config.C.ContentDelivery, config.C.MediaDir
	t.Cleanup(func() {
		config.C.ImagesEnabled, config.C.ContentDelivery, config.C.MediaDir = previousImages, previousDelivery, previousDir
	})

	config.C.ImagesEnabled, config.C.ContentDelivery, config.C.MediaDir = false, false, ""
	off := imageServingLine()
	if !strings.Contains(off, "refused") || !strings.Contains(off, "images_enabled") {
		t.Errorf("the off line does not read as a refusal naming the key: %q", off)
	}

	config.C.ImagesEnabled, config.C.MediaDir = true, ""
	broken := imageServingLine()
	if !strings.Contains(broken, "ENABLED BUT REFUSED AT THE STORAGE") || !strings.Contains(broken, "media_dir") {
		t.Errorf("the broken line does not name the state and the key: %q", broken)
	}
	if broken == off {
		t.Error("the switch-on-with-no-directory state logs the same line as the switch-off state")
	}

	dir := t.TempDir()
	config.C.MediaDir = dir
	served := imageServingLine()
	if !strings.Contains(served, dir) {
		t.Errorf("the served line does not name the directory %q: %q", dir, served)
	}
	if served == broken {
		t.Error("the served state logs the same line as the broken one")
	}
}
