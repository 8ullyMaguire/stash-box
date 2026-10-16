package backup

import (
	"crypto/md5"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stashapp/stash-box/internal/storage"
)

func fixedTime() time.Time { return time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC) }

// storeWithFile writes body at the path the server itself would read the image
// from, so the test is about the mapping production uses rather than a path
// convention invented here.
func storeWithFile(t *testing.T, id, body string) (store string, path string) {
	t.Helper()
	store = t.TempDir()
	path = storage.GetImagePath(store, id)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return store, path
}

func md5Of(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestVerifyImagesMatchesFilesToTheirRows(t *testing.T) {
	const id = "d06cf97b-1ac0-469b-9e1a-1ff2b67a40d5"
	store, _ := storeWithFile(t, id, "image bytes")

	audit, err := VerifyImagesAgainstStore(store, []ImageRow{{ID: id, Checksum: md5Of("image bytes")}})
	if err != nil {
		t.Fatalf("VerifyImagesAgainstStore: %v", err)
	}
	if audit.Verified != 1 || audit.Files != 1 || audit.RowsWithoutFile != 0 || audit.FilesWithoutRow != 0 {
		t.Errorf("audit = %+v, want one verified file and nothing else", audit)
	}
	if audit.Bytes != int64(len("image bytes")) {
		t.Errorf("Bytes = %d, want %d", audit.Bytes, len("image bytes"))
	}
}

func TestVerifyImagesFailsOnAChangedByte(t *testing.T) {
	const id = "d06cf97b-1ac0-469b-9e1a-1ff2b67a40d5"
	store, path := storeWithFile(t, id, "image bytes")
	// Same length, different content: the file is still there, still the right
	// size, and no longer the image the database says it is.
	if err := os.WriteFile(path, []byte("IMAGE BYTES"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := VerifyImagesAgainstStore(store, []ImageRow{{ID: id, Checksum: md5Of("image bytes")}})
	if err == nil {
		t.Fatal("a file whose bytes no longer match the database's checksum was accepted: that is the corruption this check exists to find")
	}
	if !strings.Contains(err.Error(), id) || !strings.Contains(err.Error(), "md5") {
		t.Errorf("the failure does not name the image and the mismatch: %v", err)
	}
}

func TestVerifyImagesCountsARowWithNoFileInsteadOfFailing(t *testing.T) {
	// MEASURED, not assumed: `images.organized` is a moderator's categorization
	// sign-off, not a claim that the bytes are on disk -- the instance this was
	// written against has an organized row with no file and unorganized rows that
	// have one. Nothing in the schema says a row must have local bytes, so a row
	// with no file is a fact to record, and only a mismatch is a failure.
	const id = "d06cf97b-1ac0-469b-9e1a-1ff2b67a40d5"
	store := t.TempDir()

	audit, err := VerifyImagesAgainstStore(store, []ImageRow{{ID: id, Checksum: md5Of("image bytes")}})
	if err != nil {
		t.Fatalf("a row with no file failed the check: %v", err)
	}
	if audit.RowsWithoutFile != 1 || audit.Verified != 0 {
		t.Errorf("audit = %+v, want one row without a file and nothing verified", audit)
	}
}

func TestVerifyImagesCountsFilesNoRowAccountsFor(t *testing.T) {
	// A file the database does not reference is copied -- the artifact is a copy of
	// the store -- and it is counted, because "the artifact holds every file the
	// store had" is a different claim from "every row has a file".
	const id = "d06cf97b-1ac0-469b-9e1a-1ff2b67a40d5"
	store, _ := storeWithFile(t, id, "the bytes of one image")
	if err := os.WriteFile(filepath.Join(store, "a-file-nobody-references"), []byte("orphan"), 0o644); err != nil {
		t.Fatal(err)
	}

	audit, err := VerifyImagesAgainstStore(store, []ImageRow{{ID: id, Checksum: md5Of("the bytes of one image")}})
	if err != nil {
		t.Fatalf("VerifyImagesAgainstStore: %v", err)
	}
	if audit.Files != 2 || audit.FilesWithoutRow != 1 || audit.Verified != 1 {
		t.Errorf("audit = %+v, want 2 files, 1 orphan, 1 verified", audit)
	}
}

func TestVerifyImagesHandlesAStoreThatIsNotThere(t *testing.T) {
	// A metadata-only instance has no store, and that is not a fault: every row is
	// counted as one without a file, and the backup proceeds.
	const id = "d06cf97b-1ac0-469b-9e1a-1ff2b67a40d5"
	audit, err := VerifyImagesAgainstStore(filepath.Join(t.TempDir(), "never-created"), []ImageRow{
		{ID: id, Checksum: "whatever"},
		{ID: "11111111-2222-4333-8444-555555555555", Checksum: "whatever"},
	})
	if err != nil {
		t.Fatalf("a missing store failed the check: %v", err)
	}
	if audit.RowsWithoutFile != 2 || audit.Files != 0 {
		t.Errorf("audit = %+v, want 2 rows without files and no files", audit)
	}
}

func TestVerifyImagesCountsEveryRowWhenNoStoreIsConfigured(t *testing.T) {
	// An instance with no image_location keeps no bytes of its own -- the app
	// supports that posture explicitly (images_enabled and content_delivery can both
	// be off). Refusing here would make the tool unusable on exactly the instances
	// whose configuration says so out loud; the counts and the note are how a reader
	// tells "no store" from "a store that is missing files".
	audit, err := VerifyImagesAgainstStore("", []ImageRow{
		{ID: "d06cf97b-1ac0-469b-9e1a-1ff2b67a40d5", Checksum: "x"},
		{ID: "11111111-2222-4333-8444-555555555555", Checksum: "y"},
	})
	if err != nil {
		t.Fatalf("rows with no store configured failed the check: %v", err)
	}
	if audit.RowsWithoutFile != 2 || audit.Files != 0 || audit.Verified != 0 {
		t.Errorf("audit = %+v, want two rows without files and nothing else", audit)
	}
}

func TestVerifyImagesRefusesAnIDTooShortToPlace(t *testing.T) {
	_, err := VerifyImagesAgainstStore(t.TempDir(), []ImageRow{{ID: "ab", Checksum: "x"}})
	if err == nil {
		t.Fatal("an id too short to derive a store path was accepted")
	}
}

func TestEachStoreFileRefusesASymlink(t *testing.T) {
	store := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("not mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(store, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	err := eachStoreFile(store, func(string, int64) error { return nil })
	if err == nil {
		t.Fatal("a symlink in the store was walked: one backup follows it and copies the disk behind it")
	}
}
