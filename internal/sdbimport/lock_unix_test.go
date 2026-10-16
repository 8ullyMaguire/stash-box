//go:build unix

package sdbimport

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The lock is what stops a timer run and a manual run from diffing against a database the other is changing.
// The property that matters is the NEGATIVE one: the second acquire fails, by name, and the first release makes
// the lock available again -- a lock that is never released is a sync that never runs again.
func TestTheLockRefusesASecondRunAndReleases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sync.lock")

	first, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("the first acquire failed: %v", err)
	}
	if _, err := AcquireLock(path); !errors.Is(err, ErrSyncLocked) {
		t.Errorf("the second acquire = %v, want ErrSyncLocked", err)
	}

	// The file is written so an operator can see who holds it, and it is not empty.
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(blob) == 0 {
		t.Error("the lock file names no pid")
	}

	first.Release()
	second, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("after release the lock was not available: %v", err)
	}
	second.Release()
	// Releasing twice must not close a descriptor twice.
	second.Release()

	if _, err := AcquireLock(""); err == nil {
		t.Error("AcquireLock accepted an empty path: a lock nobody agrees on is not a lock")
	}
}
