package backup

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// HashFileSHA256 is the artifact's own checksum. SHA-256 is the right choice for
// a file list whose reader is a person or a CI job: it is what sha256sum -c
// speaks, and the artifact's job is to be checkable by tools that already exist.
func HashFileSHA256(path string) (string, error) {
	return hashFile(path, sha256.New())
}

// HashFileMD5 is the algorithm images.checksum uses, from
// internal/service/image's calculateChecksum. It is here because the image store
// is cross-checked against the DATABASE, and the database speaks md5: comparing
// our own sha256 to a value the database never recorded would prove nothing
// about the pair.
func HashFileMD5(path string) (string, error) {
	return hashFile(path, md5.New())
}

func hashFile(path string, h hash.Hash) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hashing %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// CollectFiles returns every regular file under root as a FileEntry, sorted by
// path, with paths relative to root and slash-separated.
//
// Symlinks are skipped and reported as such by the caller's walk, because a
// backup that follows a symlink out of the artifact can copy the whole disk, and
// a backup that copies a dangling one has copied nothing while looking like it
// copied something.
func CollectFiles(root string) ([]FileEntry, error) {
	var files []FileEntry
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink: refusing to include it in an artifact whose file list is a checksum list", path)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file (%s)", path, d.Type())
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		sum, err := HashFileSHA256(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, FileEntry{
			Path:   filepath.ToSlash(rel),
			Size:   info.Size(),
			SHA256: sum,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

// WriteChecksums writes CHECKSUMS.sha256 in the format sha256sum -c reads, with
// paths relative to the artifact root, so `cd <artifact> && sha256sum -c
// CHECKSUMS.sha256` works on a machine that has never heard of this tool. The
// manifest itself is not listed: it is written after this file, and a checksum
// list that includes its own index is a checksum list nobody can use.
func WriteChecksums(root string, files []FileEntry) error {
	var b strings.Builder
	for _, f := range files {
		if f.Path == ManifestName || f.Path == ChecksumName {
			continue
		}
		fmt.Fprintf(&b, "%s  %s\n", f.SHA256, f.Path)
	}
	if err := os.WriteFile(filepath.Join(root, ChecksumName), []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", ChecksumName, err)
	}
	return nil
}

// maxNamedProblems keeps a broken artifact's error readable: the first handful of
// offenders are named, the rest are counted. Naming one file out of four thousand
// is how a reader concludes the other 3,999 are fine.
const maxNamedProblems = 5

// Verify checks an artifact against its own manifest, in both directions: every
// file the manifest lists must be present with the recorded size and sha256, and
// every file present must be listed. The second direction is not paranoia -- an
// artifact is a claim about a set of bytes, and a file that arrived without being
// recorded is either a mistake or something that should not be travelling.
//
// It returns the manifest so a caller can go on to check the artifact's contents
// against the world (a database restored from it, for instance).
func Verify(dir string) (*Manifest, error) {
	m, err := ReadManifest(dir)
	if err != nil {
		return nil, err
	}

	listed := make(map[string]FileEntry, len(m.Files))
	for _, f := range m.Files {
		listed[f.Path] = f
	}

	var problems []string
	for _, f := range m.FileList() {
		path := filepath.Join(dir, filepath.FromSlash(f.Path))
		info, err := os.Stat(path)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: missing", f.Path))
			continue
		}
		if info.Size() != f.Size {
			problems = append(problems, fmt.Sprintf("%s: %d bytes on disk, %d in the manifest", f.Path, info.Size(), f.Size))
			continue
		}
		sum, err := HashFileSHA256(path)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", f.Path, err))
			continue
		}
		if !strings.EqualFold(sum, f.SHA256) {
			problems = append(problems, fmt.Sprintf("%s: sha256 %s on disk, %s in the manifest", f.Path, sum, f.SHA256))
		}
	}

	present, err := CollectFiles(dir)
	if err != nil {
		return nil, err
	}
	for _, f := range present {
		if f.Path == ManifestName {
			continue // written after the list that would have to contain it
		}
		if _, ok := listed[f.Path]; !ok {
			problems = append(problems, fmt.Sprintf("%s: present but not in the manifest", f.Path))
		}
	}

	if len(problems) > 0 {
		return nil, fmt.Errorf("%s is not the artifact it claims to be: %s", dir, summarise(problems))
	}
	return m, nil
}

func summarise(problems []string) string {
	if len(problems) <= maxNamedProblems {
		return strings.Join(problems, "; ")
	}
	return fmt.Sprintf("%s; and %d more", strings.Join(problems[:maxNamedProblems], "; "), len(problems)-maxNamedProblems)
}

// ErrDirNotEmpty guards a backup run. Writing an artifact into a directory that
// already holds one would produce a manifest describing a mixture of two runs,
// and the mixture would verify -- the check would be about the mixture, not about
// either backup.
var ErrDirNotEmpty = errors.New("output directory already contains files")
