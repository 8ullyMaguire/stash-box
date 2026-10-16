package backup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteArtifactTailListsEveryFileItWrote(t *testing.T) {
	// This is the test the integration drill earned: the first version collected
	// the file list BEFORE writing CHECKSUMS.sha256, so the manifest omitted it and
	// the artifact failed its own strict verification. The ordering is testable
	// without a database because the directory is the only input.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, DumpName), []byte("a dump"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewManifest("v-test", fixedTime())
	if err := WriteArtifactTail(dir, m); err != nil {
		t.Fatalf("WriteArtifactTail: %v", err)
	}

	listed := map[string]bool{}
	for _, f := range m.Files {
		listed[f.Path] = true
	}
	for _, want := range []string{DumpName, ReadmeName, ChecksumName} {
		if !listed[want] {
			t.Errorf("the manifest does not list %s: %v", want, m.FileList())
		}
	}
	if listed[ManifestName] {
		t.Error("the manifest lists itself: it cannot have a checksum of itself recorded in itself")
	}
	if _, err := Verify(dir); err != nil {
		t.Fatalf("the artifact the tool just wrote does not verify against itself: %v", err)
	}
}
