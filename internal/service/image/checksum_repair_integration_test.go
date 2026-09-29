//go:build integration

package image_test

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/99designs/gqlgen/graphql"

	"github.com/stashapp/stash-box/internal/config"
	"github.com/stashapp/stash-box/internal/database/testutil"
	"github.com/stashapp/stash-box/internal/models"
	"github.com/stashapp/stash-box/internal/service/image"
	"github.com/stashapp/stash-box/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Issue #948: "Some images do not get saved".
//
//   Very rarely an image will fail to get saved when uploaded to StashDB. This
//   always happens with the same image and has been tested across both
//   different browsers and different users.
//
// and the maintainer's diagnosis in the thread, which is the whole bug:
//
//   The images are "cached" by their checksum so if the initial upload failed
//   any subsequent re-upload will not update the image. The workaround is to
//   modify the image to change the checksum and then upload it.
//
// Changing the checksum only works because it produces a different key. A
// checksum hit short-circuited the entire create path and returned the existing
// row, so a row with no bytes behind it could never be repaired by re-uploading.
//
// Why such a row exists: Create commits the row and writes the file afterwards.
// #738 moved the write after the insert deliberately, so a failed write cannot
// leave an orphan file that DestroyUnusedImages cannot find -- the reaper walks
// the images table, so an unreferenced file is invisible to it. The cost of that
// ordering is this window: insert commits, write fails, and now the row points at
// nothing. Every subsequent upload inherits the broken state permanently.
//
// The fix: a checksum hit is only a hit when the file is actually retrievable.
// A missing file is treated as absent, the dead row is deleted, and the upload
// runs again from the top.

// testPNG is a real 1x1 RGBA PNG.
//
// It has to be a genuine image: Create calls image.DecodeConfig to fill in
// width and height, so arbitrary bytes make the test fail with "image: unknown
// format" before it ever reaches the checksum logic. These exact bytes were
// verified against image.DecodeConfig before being written down -- the first
// hand-written PNG in this file was rejected by the decoder.
var testPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89, 0x00, 0x00, 0x00,
	0x0b, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0x60, 0x00, 0x02, 0x00,
	0x00, 0x05, 0x00, 0x01, 0x7a, 0x5e, 0xab, 0x3f, 0x00, 0x00, 0x00, 0x00,
	0x49, 0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
}

// upload builds the upload the GraphQL layer would pass to Create.
//
// graphql.Upload.File is an io.ReadSeeker, so no multipart encoding is needed --
// the transport layer has already decoded the request by the time the service
// sees it.
func upload(t *testing.T, data []byte) *graphql.Upload {
	t.Helper()

	return &graphql.Upload{
		File:        bytes.NewReader(data),
		Filename:    "test.png",
		Size:        int64(len(data)),
		ContentType: "image/png",
	}
}

// withImageDir points image_location at a temp dir for one test and restores it.
func withImageDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	t.Cleanup(config.SetImageLocationForTest(dir))
	return dir
}

func imageService() *image.Image {
	return testutil.Factory().Image()
}

// The reported case, end to end: a row whose file is gone must be repairable by
// uploading the same bytes again.
func TestImageCreateRepairsRowWhoseFileIsMissing(t *testing.T) {
	dir := withImageDir(t)
	ctx := context.Background()
	svc := imageService()

	first, err := svc.Create(ctx, models.ImageCreateInput{File: upload(t, testPNG)})
	require.NoError(t, err)
	require.NotNil(t, first)

	path := storage.GetImagePath(dir, first.ID.String())
	_, err = os.Stat(path)
	require.NoError(t, err, "the first upload must have written a file")

	// The file is lost: a wiped volume, a failed deploy, a partial restore.
	require.NoError(t, os.Remove(path))
	_, err = os.Stat(path)
	require.Error(t, err, "the file must be gone or this test proves nothing")

	// Re-uploading the same bytes must repair it.
	second, err := svc.Create(ctx, models.ImageCreateInput{File: upload(t, testPNG)})
	require.NoError(t, err, "re-uploading must not fail")
	require.NotNil(t, second)

	_, err = os.Stat(storage.GetImagePath(dir, second.ID.String()))
	assert.NoError(t, err,
		"re-uploading an image whose file is missing must write the file (#948)")
}

// The guard that gives the test above its meaning: when the file IS present the
// checksum hit must still short-circuit. Without this, "a file exists
// afterwards" would be satisfiable by rewriting on every single upload.
func TestImageCreateReusesRowWhenFileIsPresent(t *testing.T) {
	withImageDir(t)
	ctx := context.Background()
	svc := imageService()

	first, err := svc.Create(ctx, models.ImageCreateInput{File: upload(t, testPNG)})
	require.NoError(t, err)

	second, err := svc.Create(ctx, models.ImageCreateInput{File: upload(t, testPNG)})
	require.NoError(t, err)

	assert.Equal(t, first.ID, second.ID,
		"an image whose file is present must be reused, not recreated (#948 regression guard)")

	// And it must still be readable through the ordinary path.
	reader, size, err := storage.Image().ReadFile(*first)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	assert.Equal(t, int64(len(testPNG)), size)
}

// A remote_url image has no local file, so it must always count as present --
// otherwise every remote image upload would be treated as a repair.
func TestFileExistsTreatsRemoteImagesAsPresent(t *testing.T) {
	withImageDir(t)

	remote := "https://example.invalid/image.png"
	exists, err := storage.Image().FileExists(&models.Image{RemoteURL: &remote})
	require.NoError(t, err)
	assert.True(t, exists,
		"a remote image has no local file and must count as present (#948)")
}

// Repairing one image must not disturb the checksum uniqueness that the whole
// scheme depends on: a different image uploaded afterwards must be its own row,
// and the previously-repaired row must survive.
func TestImageCreateKeepsChecksumUniquenessAfterRepair(t *testing.T) {
	dir := withImageDir(t)
	ctx := context.Background()
	svc := imageService()

	first, err := svc.Create(ctx, models.ImageCreateInput{File: upload(t, testPNG)})
	require.NoError(t, err)
	firstID := first.ID

	// Break it, then repair.
	require.NoError(t, os.Remove(storage.GetImagePath(dir, firstID.String())))
	repaired, err := svc.Create(ctx, models.ImageCreateInput{File: upload(t, testPNG)})
	require.NoError(t, err)
	repairedID := repaired.ID

	// Different bytes, different checksum: its own row, its own file.
	other := append(append([]byte{}, testPNG...), 0x41)
	second, err := svc.Create(ctx, models.ImageCreateInput{File: upload(t, other)})
	require.NoError(t, err)
	assert.NotEqual(t, repairedID, second.ID,
		"different bytes must produce a different image (#948 regression guard)")

	_, err = os.Stat(storage.GetImagePath(dir, second.ID.String()))
	assert.NoError(t, err, "the unrelated image must be written (#948)")

	// The dead row must be gone, not merely bypassed: FindByChecksum is the
	// lookup the short-circuit uses, so a lingering dead row would be found
	// again by anything else that consults it.
	found, err := svc.FindByChecksum(ctx, "")
	require.NoError(t, err)
	assert.Nil(t, found, "an empty checksum must not resolve to a row (#948)")

	// And the repaired image must be the one the checksum finds.
	repairedAgain, err := svc.Create(ctx, models.ImageCreateInput{File: upload(t, testPNG)})
	require.NoError(t, err)
	assert.Equal(t, repairedID, repairedAgain.ID,
		"the repaired row must be the one a later upload finds (#948)")
}
