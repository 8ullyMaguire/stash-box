package mediaplane

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The sample is the real output of `exportfs -v` on this network's host, including the wrapped client
// line that a naive parser reads as a path. It is captured rather than invented because the awkward
// part is exactly the part an invented sample would leave out.
const exportfsSample = `/personal     	192.168.1.0/24(async,wdelay,nohide,crossmnt,no_subtree_check,fsid=101,sec=sys,rw,insecure,no_root_squash,no_all_squash)
/public      	192.168.1.0/24(async,wdelay,nohide,crossmnt,no_subtree_check,fsid=102,sec=sys,rw,insecure,no_root_squash,no_all_squash)
/media       	192.168.1.0/24(async,wdelay,nohide,crossmnt,no_subtree_check,fsid=103,sec=sys,rw,insecure,no_root_squash,no_all_squash)
/mnt/disk-important/backups
		192.168.1.0/24(async,wdelay,hide,no_subtree_check,fsid=104,sec=sys,rw,insecure,no_root_squash,no_all_squash)
`

// Four exports, not six: the indented line is a continuation and the parser must not turn a client
// address into a path. The count is asserted BESIDE the values for the reason this programme keeps
// re-learning -- an absence assertion over a list whose parsing quietly produced nothing looks exactly
// like a list with nothing wrong in it.
func TestParsingExportfsCountsItsSubjects(t *testing.T) {
	paths := ParseExportTable(exportfsSample)
	want := []string{"/personal", "/public", "/media", "/mnt/disk-important/backups"}
	if len(paths) != len(want) {
		t.Fatalf("parsed %d paths (%v), want %d", len(paths), paths, len(want))
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Errorf("path %d = %q, want %q", i, paths[i], want[i])
		}
	}
}

// The next-door-neighbour case, which is the one character this function is away from getting wrong:
// `/mnt/disk-important/backups2` starts with the same characters as an export and is NOT inside it. A
// string-prefix check refuses it, i.e. refuses a media directory that is shared with nobody.
func TestASiblingWhoseNameStartsWithAnExportIsNotInsideIt(t *testing.T) {
	exports := ParseExportTable(exportfsSample)

	cases := []struct {
		dir     string
		inside  bool
		which   string
		comment string
	}{
		{"/mnt/disk-important/backups2", false, "", "a sibling whose name starts with the export's"},
		{"/mnt/disk-important/backup", false, "", "shorter than the export, and not under it"},
		{"/mnt", false, "", "a directory that CONTAINS an export is not inside it"},
		{"/personal", true, "/personal", "the export itself"},
		{"/personal/alvaro", true, "/personal", "under the export"},
		{"/media/stash-box/thumbs", true, "/media", "under a different export"},
		{"/mnt/disk-important/backups/x", true, "/mnt/disk-important/backups", "under the wrapped entry"},
		{"/mnt/disk-important/stash-box-media", false, "", "the directory this slice tells an operator to use"},
	}
	inside := 0
	for _, c := range cases {
		which, ok := InsideAnExport(c.dir, exports)
		if ok != c.inside || which != c.which {
			t.Errorf("InsideAnExport(%q) = (%q, %v), want (%q, %v) -- %s", c.dir, which, ok, c.which, c.inside, c.comment)
		}
		if ok {
			inside++
		}
	}
	if inside == 0 {
		t.Fatal("no case was found to be inside an export: the search found nothing, so it proves nothing")
	}
}

// The refusal names the export, so an operator knows WHICH export to remove or move out of -- the same
// rule every other refusal in this package follows.
func TestTheExportRefusalNamesTheExport(t *testing.T) {
	exports := []string{"/personal", "/media"}
	err := MediaDirExportProblem("/media/stash-box", exports)
	var exported ErrMediaDirExported
	if !errors.As(err, &exported) {
		t.Fatalf("MediaDirExportProblem = %v, want ErrMediaDirExported", err)
	}
	if exported.Export != "/media" || exported.Path != "/media/stash-box" {
		t.Errorf("the refusal names path %q and export %q", exported.Path, exported.Export)
	}
	for _, want := range []string{"media_dir", "/media"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %q", want, err.Error())
		}
	}
}

// THE FALSE-PASS REGRESSION. As an unprivileged user, `exportfs -v` on the host this was written against
// prints NOTHING and exits ZERO -- it cannot take the lock file -- so a reader that trusted it reported
// "this host exports nothing" on a host exporting five paths, and every media directory was then outside
// every export. An unreadable table is therefore an ERROR, and the message says UNKNOWN so that a caller
// reading it knows the difference between "no exports" and "I could not tell".
func TestAnUnreadableExportTableIsAnErrorRatherThanAnEmptyList(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: mode 000 does not deny access, so this test would prove nothing")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "etab")
	if err := os.WriteFile(path, []byte("\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

	paths, err := exportedPathsFrom(path)
	if err == nil {
		t.Fatalf("an unreadable export table returned %v and no error: a reader that cannot read the "+
			"table must not report that nothing is exported", paths)
	}
	if !strings.Contains(err.Error(), "UNKNOWN") {
		t.Errorf("the error does not say the answer is unknown, so a caller may read it as a pass: %q", err)
	}
}

// A READABLE table is parsed, and an EMPTY one is legitimately zero exports -- the control for the test
// above, without which "always return an error" would look like a fix.
func TestAReadableTableIsParsedAndAnEmptyOneMeansNoExports(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "etab")
	if err := os.WriteFile(path, []byte(exportfsSample), 0o644); err != nil {
		t.Fatal(err)
	}
	paths, err := exportedPathsFrom(path)
	if err != nil {
		t.Fatalf("exportedPathsFrom(real table) = %v", err)
	}
	if len(paths) != 4 {
		t.Errorf("parsed %d paths from the fixture, want 4: %v", len(paths), paths)
	}

	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	paths, err = exportedPathsFrom(empty)
	if err != nil || len(paths) != 0 {
		t.Errorf("an empty table = (%v, %v), want no paths and no error: a host that exports nothing has "+
			"no exported directory to be inside of", paths, err)
	}
}

// With no table on the host, the fallback decides: `exportfs -v` that fails is an error, and one that
// prints nothing is a host without an NFS server. Both directions, because a fallback that turned every
// failure into "no exports" would restore the false pass by a different route.
func TestTheFallbackToExportfsDistinguishesFailureFromNoExports(t *testing.T) {
	previous := exportfsCmd
	t.Cleanup(func() { exportfsCmd = previous })

	missing := filepath.Join(t.TempDir(), "no-such-etab")

	exportfsCmd = func() ([]byte, error) { return []byte(exportfsSample), nil }
	paths, err := exportedPathsFrom(missing)
	if err != nil || len(paths) != 4 {
		t.Errorf("fallback to a working exportfs = (%v, %v), want the fixture's 4 paths", paths, err)
	}

	exportfsCmd = func() ([]byte, error) { return nil, errors.New("no such file or directory") }
	if paths, err := exportedPathsFrom(missing); err == nil {
		t.Errorf("a failing fallback returned %v and no error: whether this host exports anything is "+
			"then unknown, and unknown is not zero", paths)
	}

	exportfsCmd = func() ([]byte, error) { return []byte("  \n"), nil }
	paths, err = exportedPathsFrom(missing)
	if err != nil || len(paths) != 0 {
		t.Errorf("a silent fallback = (%v, %v), want no paths and no error", paths, err)
	}
}

// An unconfigured directory is not inside anything, and the control: the same directory OUTSIDE every
// export is accepted. Without this arm the invariant could refuse every path and still pass.
func TestNoConfiguredDirectoryAndAnOutsideDirectoryAreBothAccepted(t *testing.T) {
	exports := ParseExportTable(exportfsSample)
	if err := MediaDirExportProblem("", exports); err != nil {
		t.Errorf("MediaDirExportProblem(\"\") = %v, want nil", err)
	}
	if err := MediaDirExportProblem("/mnt/disk-important/stash-box-media", exports); err != nil {
		t.Errorf("MediaDirExportProblem(outside dir) = %v, want nil", err)
	}
}
