package backup

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// writeArtifact builds the smallest honest artifact: a dump, a checksum list and
// a manifest that lists what is there.
func writeArtifact(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m := NewManifest("v-test", fixedTime())
	collected, err := CollectFiles(dir)
	if err != nil {
		t.Fatalf("CollectFiles: %v", err)
	}
	if err := WriteChecksums(dir, collected); err != nil {
		t.Fatalf("WriteChecksums: %v", err)
	}
	collected, err = CollectFiles(dir)
	if err != nil {
		t.Fatalf("CollectFiles after checksums: %v", err)
	}
	m.Files = collected
	if err := m.Write(dir); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return dir
}

func TestCollectFilesIsSortedRelativeAndSlashed(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "images", "d0", "6c"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "images", "d0", "6c", "one"), []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, DumpName), []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := CollectFiles(dir)
	if err != nil {
		t.Fatalf("CollectFiles: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("got %d files, want 2: %+v", len(files), files)
	}
	if files[0].Path != DumpName {
		t.Errorf("not sorted by path: %+v", files)
	}
	if files[1].Path != "images/d0/6c/one" {
		t.Errorf("path = %q, want a slash-separated path relative to the artifact root", files[1].Path)
	}
	if files[1].Size != 3 {
		t.Errorf("size = %d, want 3", files[1].Size)
	}
	if len(files[1].SHA256) != 64 {
		t.Errorf("sha256 = %q, want 64 hex characters", files[1].SHA256)
	}
}

func TestCollectFilesRefusesASymlink(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("not mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err := CollectFiles(dir)
	if err == nil {
		t.Fatal("a symlink was silently included: an artifact's file list is a checksum list, and a link is not the bytes it points at")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("the refusal does not name the problem: %v", err)
	}
}

func TestWriteChecksumsIsSha256sumCompatible(t *testing.T) {
	dir := writeArtifact(t, map[string]string{DumpName: "dump", "images/one": "image"})
	data, err := os.ReadFile(filepath.Join(dir, ChecksumName))
	if err != nil {
		t.Fatalf("reading %s: %v", ChecksumName, err)
	}
	line := regexp.MustCompile(`^[0-9a-f]{64}  \S+$`)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2 (the manifest itself must not be listed: it is written after this file): %q", len(lines), string(data))
	}
	for _, l := range lines {
		if !line.MatchString(l) {
			t.Errorf("line %q is not in the form sha256sum -c reads", l)
		}
	}
	if strings.Contains(string(data), ManifestName) {
		t.Error("the checksum list lists itself or the manifest: neither can be checked by it")
	}
}

func TestVerifyAcceptsAnIntactArtifact(t *testing.T) {
	dir := writeArtifact(t, map[string]string{DumpName: "dump", "images/one": "image"})
	m, err := Verify(dir)
	if err != nil {
		t.Fatalf("Verify on an intact artifact: %v", err)
	}
	if len(m.Files) == 0 {
		t.Fatal("Verify returned a manifest with no files: the check has no subject")
	}
}

func TestVerifyFailsOnAnAlteredFile(t *testing.T) {
	dir := writeArtifact(t, map[string]string{DumpName: "dump", "images/one": "image"})
	// SAME SIZE, one byte different: a check that only compares sizes passes this
	// and a check that compares hashes cannot.
	path := filepath.Join(dir, DumpName)
	if err := os.WriteFile(path, []byte("dumX"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Verify(dir)
	if err == nil {
		t.Fatal("a byte-for-byte change of the same size was accepted")
	}
	if !strings.Contains(err.Error(), DumpName) || !strings.Contains(err.Error(), "sha256") {
		t.Errorf("the failure does not name the file and the mismatch: %v", err)
	}
}

func TestVerifyFailsOnAGrownFile(t *testing.T) {
	dir := writeArtifact(t, map[string]string{DumpName: "dump"})
	f, err := os.OpenFile(filepath.Join(dir, DumpName), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("more"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	_, err = Verify(dir)
	if err == nil {
		t.Fatal("an appended-to dump was accepted")
	}
	if !strings.Contains(err.Error(), "bytes on disk") {
		t.Errorf("a size change is not reported as one: %v", err)
	}
}

func TestVerifyFailsOnAMissingFile(t *testing.T) {
	dir := writeArtifact(t, map[string]string{DumpName: "dump", "images/one": "image"})
	if err := os.Remove(filepath.Join(dir, "images/one")); err != nil {
		t.Fatal(err)
	}
	_, err := Verify(dir)
	if err == nil {
		t.Fatal("a missing image was accepted")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("the failure does not say the file is missing: %v", err)
	}
}

func TestVerifyFailsOnAFileNobodyRecorded(t *testing.T) {
	dir := writeArtifact(t, map[string]string{DumpName: "dump"})
	if err := os.WriteFile(filepath.Join(dir, "stray"), []byte("who put this here"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Verify(dir)
	if err == nil {
		t.Fatal("a file that arrived without being recorded was accepted: an artifact is a claim about a set of bytes")
	}
	if !strings.Contains(err.Error(), "not in the manifest") {
		t.Errorf("the failure does not explain itself: %v", err)
	}
}

func TestVerifyRefusesADirectoryThatIsNotAnArtifact(t *testing.T) {
	_, err := Verify(t.TempDir())
	if !errors.Is(err, ErrNoManifest) {
		t.Fatalf("Verify of an empty directory = %v, want ErrNoManifest", err)
	}
}
