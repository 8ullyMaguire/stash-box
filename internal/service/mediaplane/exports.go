package mediaplane

// The EXPORT INVARIANT (plan phase 10, slice 10b-2).
//
// A media directory inside an NFS export is not a private directory. On this network the disk that a
// media directory would naturally live on is exported to the whole LAN with `sec=sys`,
// `no_root_squash` and `insecure`, so anything under an exported path is readable AND WRITABLE from
// every host that mounts it -- and one of the exports is already mounted elsewhere under the name
// `/media`. The failure is silent in both directions: nothing in the instance's own behaviour changes,
// and the exposure is invisible from inside the process.
//
// So the invariant is a CHECK rather than a paragraph: a configured directory that sits inside any
// exported path is refused, and the refusal names the export it is inside. It runs in a test and in the
// walk (against the live `exportfs -v` of the host), not on the request path -- an export added while
// an instance is running is an operator action, and answering 403 for every image because somebody
// edited /etc/exports is not the behaviour anybody wants. What belongs on the request path is whether
// the bytes can be read; what belongs here is whether reading them would publish them.
//
// WHY THE COMPARISON IS BY PATH COMPONENT AND NOT BY STRING PREFIX. `/mnt/disk-important/backups` is an
// export, and `/mnt/disk-important/backups2` is a DIFFERENT DIRECTORY that starts with the same
// characters. A string-prefix check calls the second one exported and refuses a directory that is not
// shared with anybody; the check has a test for exactly that pair, because it is the mistake this
// function is one character away from.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ErrMediaDirExported is returned when the configured media directory is inside an NFS export.
type ErrMediaDirExported struct {
	Path   string
	Export string
}

func (e ErrMediaDirExported) Error() string {
	return fmt.Sprintf("`media_dir` (%s) is inside the NFS export %s: everything under that path is "+
		"readable and writable from every host that mounts it. Move the media directory outside every "+
		"export, or remove the export", e.Path, e.Export)
}

// EtabPath is the kernel's own export table -- the file `exportfs` reads and writes.
//
// It is read DIRECTLY rather than through `exportfs -v`, and that is a correctness decision with a
// measurement behind it. As an unprivileged user, `exportfs -v` on the host this was written against
// prints NOTHING and exits ZERO, because it cannot take the lock file. A reader that trusted it would
// report "this host exports nothing" on a host that exports five paths, and every media directory would
// then be "outside every export" -- a FALSE PASS, which is the one direction of error an invariant must
// not have. The file itself is world-readable (0644 in practice), so the honest source was available the
// whole time.
const EtabPath = "/var/lib/nfs/etab"

// exportfsCmd is a seam for the fallback path only. It exists so that the fallback can be tested without
// a host: everything else in this file is pure.
var exportfsCmd = func() ([]byte, error) { return exec.Command("exportfs", "-v").Output() }

// ExportedPaths reads this host's LIVE export table, or reports that it could not.
//
// Three outcomes, and the middle one is the point:
//
//  1. the table is readable -> its paths. An EMPTY table is legitimately zero exports, and the invariant
//     then has nothing to refuse: a host that exports nothing has no exported directory to be inside of;
//  2. the table EXISTS and cannot be read -> an ERROR, never an empty list. This is the state an
//     unprivileged `exportfs -v` disguises as "no exports" (see EtabPath);
//  3. there is no table -> fall back to `exportfs -v`, which on a host with no NFS server prints nothing
//     and exits cleanly; empty output there means no exports, and a failed command is an error.
//
// A caller must treat an error as UNKNOWN and not as a pass. The walk does.
func ExportedPaths() ([]string, error) { return exportedPathsFrom(EtabPath) }

// exportedPathsFrom is the same reader against a chosen path, so that the three outcomes above can be
// exercised from fixtures. The real table lives in /var/lib/nfs, is owned by root, and must not be
// tampered with to test a function -- an invariant test that edits the thing it is checking is its own
// kind of defect.
func exportedPathsFrom(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		return ParseExportTable(string(data)), nil
	case errors.Is(err, os.ErrNotExist):
		out, execErr := exportfsCmd()
		if execErr != nil {
			return nil, fmt.Errorf("there is no %s on this host and `exportfs -v` failed, so whether this "+
				"host exports anything is unknown: %w", path, execErr)
		}
		if strings.TrimSpace(string(out)) == "" {
			return nil, nil
		}
		return ParseExportTable(string(out)), nil
	default:
		return nil, fmt.Errorf("the export table (%s) exists and could not be read, so whether this host "+
			"exports anything is UNKNOWN: %w", path, err)
	}
}

// ParseExportTable takes an export table -- /var/lib/nfs/etab or the output of `exportfs -v`, which are
// the same shape -- and returns the exported paths.
//
// The format is one export per entry, with the client list possibly on the NEXT line and indented --
// so a line that starts with whitespace is a continuation and not a path. That is the whole parser,
// and it is a function rather than three lines inside ExportedPaths so that the awkward part (the
// wrapped line, which the real output on this network does contain) can be asserted from a captured
// sample rather than only from a live host.
func ParseExportTable(out string) []string {
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if line != strings.TrimLeft(line, " \t") {
			continue // an indented continuation: the client list of the entry above
		}
		if fields := strings.Fields(line); len(fields) > 0 {
			paths = append(paths, fields[0])
		}
	}
	return paths
}

// InsideAnExport reports whether dir is inside any exported path, and which one.
//
// Component-wise, and that matters: see the file's header. A directory that CONTAINS an export is not
// inside it (`/mnt` is not inside `/mnt/disk-important/backups`), and neither is a sibling whose name
// merely starts with the same characters.
func InsideAnExport(dir string, exports []string) (string, bool) {
	abs := filepath.Clean(dir)
	for _, e := range exports {
		ex := filepath.Clean(strings.TrimSpace(e))
		if strings.TrimSpace(e) == "" {
			continue
		}
		if abs == ex || strings.HasPrefix(abs, ex+string(filepath.Separator)) {
			return ex, true
		}
	}
	return "", false
}

// MediaDirExportProblem returns the export a configured directory sits inside, or nil.
//
// An empty directory is not a problem: an instance that has not named one serves nothing from anywhere,
// and "no media directory is inside an export" is true.
func MediaDirExportProblem(dir string, exports []string) error {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	if ex, ok := InsideAnExport(dir, exports); ok {
		return ErrMediaDirExported{Path: filepath.Clean(dir), Export: ex}
	}
	return nil
}
