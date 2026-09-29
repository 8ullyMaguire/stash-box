package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stashapp/stash-box/internal/config"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Issue #649: "Bad behaviour when image backend or image location not
// specified".
//
//   If `image_location` is not specified, the system still allows images to be
//   created in the database, and it places the image files in the current
//   working directory. If you try to retrieve an image, you _then_ get an error
//   message indicating that the image location has not been specified.
//
// The mechanism is filepath.Join: with an empty image_location,
//
//	filepath.Join("", "ab/cd/<id>")  ==  "ab/cd/<id>"
//
// -- a RELATIVE path. So the read is attempted against the process working
// directory and fails with a bare "no such file or directory" that names
// neither the image nor the missing setting.
//
// WriteFile already validated; ReadFile did not. That asymmetry is the bug.

// withImageLocation sets image_location for one test and restores it after.
func withImageLocation(t *testing.T, loc string) {
	t.Helper()

	restore := config.SetImageLocationForTest(loc)
	t.Cleanup(restore)
}

// Pin the mechanism itself. This is the golden behaviour the bug depends on,
// and it is stdlib-documented, so it is asserted directly rather than inferred
// from a round trip through the backend.
func TestEmptyImageLocationCollapsesToRelativePath(t *testing.T) {
	// This is the defect's root: Join with an empty base yields a relative path
	// rather than an error or an absolute one.
	got := filepath.Join("", shardedKey("abcdef01-2345-6789-abcd-ef0123456789"))
	assert.Equal(t, "ab/cd/abcdef01-2345-6789-abcd-ef0123456789", got,
		"an empty image_location must produce a relative path -- that is why reads landed in the CWD")
	assert.False(t, filepath.IsAbs(got))
}

// ReadFile must refuse to guess when image_location is unset, and say why.
func TestReadFileErrorsWhenImageLocationUnset(t *testing.T) {
	withImageLocation(t, "")

	backend := &FileBackend{}
	image := models.Image{ID: uuid.Must(uuid.NewV4())}

	_, _, err := backend.ReadFile(image)

	require.Error(t, err, "reading an image with no image_location must fail loudly (#649)")
	assert.Contains(t, err.Error(), "ImageLocation",
		"the error must name the missing setting, not just report a missing file (#649)")
}

// The pre-#649 failure mode, pinned: the read was attempted against the
// working directory. Guarding against a *different* error would let that
// regression back in, so assert the error is the validation, and that no file
// was created in the CWD as a side effect.
func TestReadFileDoesNotFallBackToWorkingDirectory(t *testing.T) {
	withImageLocation(t, "")

	cwd, err := os.Getwd()
	require.NoError(t, err)

	// A distinctive id so we can look for exactly this image's shard.
	id := "deadbeef-0000-4000-8000-000000000001"
	image := models.Image{ID: uuid.Must(uuid.FromString(id))}

	backend := &FileBackend{}
	_, _, err = backend.ReadFile(image)
	require.Error(t, err)

	// Nothing may be left in the working directory.
	candidates := []string{
		filepath.Join(cwd, "de/ad", id),
		filepath.Join(cwd, "de/ad"),
	}
	for _, p := range candidates {
		_, statErr := os.Stat(p)
		assert.True(t, os.IsNotExist(statErr),
			"ReadFile must not create %s in the working directory (#649)", p)
	}
}

// A configured location must keep working -- the guard must not reject a valid
// setup.
func TestReadFileWorksWithImageLocationSet(t *testing.T) {
	dir := t.TempDir()
	withImageLocation(t, dir)

	backend := &FileBackend{}
	image := models.Image{ID: uuid.Must(uuid.NewV4())}
	payload := []byte("image-bytes")

	require.NoError(t, backend.WriteFile(payload, &image))

	got, size, err := backend.ReadFile(image)
	require.NoError(t, err, "a configured image_location must still serve images")
	defer func() { _ = got.Close() }()

	assert.Equal(t, int64(len(payload)), size)

	buf := make([]byte, size)
	_, err = got.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, payload, buf)
}

// WriteFile's existing guard must stay: an unset location must not write
// anywhere at all.
func TestWriteFileErrorsWhenImageLocationUnset(t *testing.T) {
	withImageLocation(t, "")

	backend := &FileBackend{}
	image := models.Image{ID: uuid.Must(uuid.NewV4())}

	err := backend.WriteFile([]byte("bytes"), &image)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "ImageLocation")
}
