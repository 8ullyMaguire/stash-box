package backup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/stashapp/stash-box/internal/storage"
)

// ImageRow is the part of an `images` row the artifact's cross-check needs.
type ImageRow struct {
	ID       string
	Checksum string
}

// ImageAudit is what the store and the database say about each other. Every count
// is recorded rather than only the failures: a reader comparing two backups needs
// to see which number moved, and "the artifact holds every file the store had" and
// "every image row in the database has a file" are different claims.
type ImageAudit struct {
	Verified        int   // rows whose file is present and matches the database's md5
	RowsWithoutFile int   // rows with no file in the store
	FilesWithoutRow int   // files in the store that no row accounts for
	Files           int   // files found in the store
	Bytes           int64 // their total size
}

// VerifyImagesAgainstStore is the check that makes "a consistent snapshot" mean
// something for a store that is not inside the database's snapshot.
//
// THE RULE IS THE INTERSECTION, AND THAT IS A MEASUREMENT, NOT A PREFERENCE. The
// first version of this function required a file for every row whose
// `images.organized` column was true, on the assumption that the column means
// "these bytes are on disk". It does not: `organized` is a moderator's sign-off on
// an image's categorization (internal/service/image.SetOrganized), and the
// instance this was measured against has an organized row with no file and
// fourteen unorganized rows, ten of which do have files. Nothing in the schema
// records "this row is supposed to have local bytes", so a tool that demands a
// file for a row cannot be run against a real instance -- and one that quietly
// skipped mismatches could not tell a corrupt store from a complete one.
//
// So: every file present is checked against the checksum of the row that names its
// path, and a MISMATCH is a failure -- that is corruption, and it is the thing
// this check exists to find. A row with no file is counted. A file with no row is
// counted. An instance with no store configured at all is the same case as one
// whose store directory does not exist: nothing to check, every row counted, and
// the counts and the note in the manifest are what tell the reader which of the
// two situations they are looking at.
func VerifyImagesAgainstStore(store string, rows []ImageRow) (ImageAudit, error) {
	audit := ImageAudit{}
	if store == "" {
		// No store configured: this instance keeps no image bytes of its own, which
		// is a posture the app supports (images_enabled and content_delivery can both
		// be off). Every row therefore counts as one without a file, and the manifest
		// records that -- refusing here would make the tool unusable on exactly the
		// instances whose configuration says so out loud.
		audit.RowsWithoutFile = len(rows)
		return audit, nil
	}
	if _, err := os.Stat(store); err != nil {
		if os.IsNotExist(err) {
			// No store at all: every row is one without a file, which is a fact
			// about the instance rather than a fault in it.
			audit.RowsWithoutFile = len(rows)
			return audit, nil
		}
		return audit, fmt.Errorf("image store %s: %w", store, err)
	}

	byPath := make(map[string]ImageRow, len(rows))
	for _, row := range rows {
		if len(row.ID) < 4 {
			return audit, fmt.Errorf("image %q: id is too short to derive a store path", row.ID)
		}
		byPath[storage.GetImagePath(store, row.ID)] = row
	}

	var problems []string
	seen := make(map[string]bool, len(rows))
	err := eachStoreFile(store, func(path string, size int64) error {
		audit.Files++
		audit.Bytes += size
		row, ok := byPath[path]
		if !ok {
			audit.FilesWithoutRow++
			return nil
		}
		seen[path] = true
		sum, err := HashFileMD5(path)
		if err != nil {
			return err
		}
		if !strings.EqualFold(sum, row.Checksum) {
			problems = append(problems, fmt.Sprintf("image %s: md5 %s on disk, %s in the database", row.ID, sum, row.Checksum))
			return nil
		}
		audit.Verified++
		return nil
	})
	if err != nil {
		return audit, err
	}

	audit.RowsWithoutFile = len(rows) - len(seen)
	if len(problems) > 0 {
		return audit, fmt.Errorf("the image store does not match the database, so this copy is not a copy of these rows: %s", summarise(problems))
	}
	return audit, nil
}

// eachStoreFile walks a store and reports every regular file with its size. It is
// shared with the copy so that what is verified and what is copied cannot drift
// apart: one function decides what "the store's files" means.
//
// A symlink is refused rather than followed. Following one copies the disk behind
// it; copying a dangling one has copied nothing while looking like it copied
// something.
func eachStoreFile(store string, fn func(path string, size int64) error) error {
	info, err := os.Stat(store)
	if err != nil {
		return fmt.Errorf("image store %s: %w", store, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("image store %s is not a directory", store)
	}
	return filepath.WalkDir(store, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink inside the image store: a backup that follows one copies something outside the store", path)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return fn(path, info.Size())
	})
}
