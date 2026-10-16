package mediaplane

// The media DIRECTORY (plan phase 10, slice 10b-1). See mediaplane.go for the package's purpose.
//
// WHY THIS IS A SECOND REFUSAL AND NOT PART OF THE FIRST. `images_enabled` and `media_dir` are two
// facts an operator can be wrong about independently, and the failure they produce is the same
// silence: an instance whose switch is on and whose directory is missing answers 404 for every image
// (because there is nothing to serve), which is indistinguishable from an instance that has no such
// image. This programme has already refused that shape twice -- the federation answer endpoint refuses
// BY NAME rather than answering empty, and the image gate answers 403 rather than 404 -- so a
// configured-but-unusable directory gets its own error, naming the key AND the path.
//
// WHY THE CHECK RUNS PER REQUEST RATHER THAN ONCE AT STARTUP. The interesting failure for the storage
// this key will usually name (an external disk on this network) is that it GOES AWAY: unplugged,
// remounted, or dropped by the bus. A check performed once at startup would let an instance serve
// nothing for as long as it takes an operator to notice, while its log line still claimed it was
// serving. A stat per image request is the cheapest way to answer the question the caller is actually
// asking -- "can this be served NOW" -- and a transient mount problem then reads as the posture
// refusal it is, rather than as a broken instance.
//
// NAMED GAP, not overlooked: if the disk is unmounted, its mount point is usually still a directory on
// the root filesystem, so this check passes and requests fall through to the handler (which then
// answers its own 404). Detecting that needs a comparison against something stable -- the simplest
// version being to record the directory's device id at startup and refuse when it changes -- and it
// belongs with the slice that actually reads bytes from here, which is the variant ladder. Until then
// this comment is the record.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/stashapp/stash-box/internal/config"
)

// ErrNoMediaDir is what a caller returns when this instance is configured to serve image bytes and no
// directory has been named to serve them from.
//
// The message names the key on purpose, the same rule the switch's own errors follow: the reader is an
// operator who has to find the line to change from the message alone.
var ErrNoMediaDir = errors.New(
	"this instance serves image bytes but `media_dir` is not set: there is no directory to serve them from")

// ErrMediaDirUnusable is returned when a directory IS configured and cannot be served from.
//
// It carries both halves deliberately. The key alone ("media_dir is unusable") does not tell an
// operator which instance's config they are looking at when they have several, and the path alone
// does not tell them the key was the thing that named it. The reason is the third part: "does not
// exist", "is not a directory" and "cannot be read" are three different fixes.
type ErrMediaDirUnusable struct {
	Path   string
	Reason string
}

func (e ErrMediaDirUnusable) Error() string {
	return fmt.Sprintf("this instance serves image bytes but `media_dir` (%s) cannot be used: %s", e.Path, e.Reason)
}

// MediaDirError reports whether this instance can serve image bytes from its configured directory.
func MediaDirError() error {
	return MediaDirErrorFor(config.GetMediaDir())
}

// MediaDirErrorFor is the same check against an explicit path.
//
// It takes the path rather than reading the global for the reason the package's other checks are split
// the same way: a check that could only be exercised by mutating process-wide config would be a check
// whose test proves less than it looks like it does, and the callers that care about a specific path
// (the startup line, the invariant tests, the walk) would have no way to ask about it.
//
// The path is returned as WRITTEN apart from cleaning, because that is what the operator has to go and
// look at: a relative path reported as an absolute one they never typed sends them looking in the
// wrong file.
func MediaDirErrorFor(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return ErrNoMediaDir
	}

	abs := filepath.Clean(dir)

	st, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrMediaDirUnusable{Path: abs, Reason: "the path does not exist"}
		}
		return ErrMediaDirUnusable{Path: abs, Reason: err.Error()}
	}
	if !st.IsDir() {
		return ErrMediaDirUnusable{Path: abs, Reason: "the path is not a directory"}
	}

	// Opening answers the question stat cannot: a directory whose mode is 000 stats fine and cannot be
	// served from. The file is closed immediately -- this asks about ACCESS, not about contents, and
	// the slice that reads bytes will hold its own handle.
	f, err := os.Open(abs)
	if err != nil {
		return ErrMediaDirUnusable{Path: abs, Reason: "the directory cannot be read: " + err.Error()}
	}
	_ = f.Close()

	return nil
}
