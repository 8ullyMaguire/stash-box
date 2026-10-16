//go:build unix

package sdbimport

// The single-instance lock (plan step 3): a timer run and a manual run must not overlap, because two concurrent
// walks each diff against a database the other is changing and the report of both is nonsense.
//
// flock rather than a pid file: a pid file left behind by a killed run blocks every later run until someone
// notices, while an flock is released by the kernel when the process dies, however it died.

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

type fileLock struct{ f *os.File }

// AcquireLock takes the lock at path, or returns ErrSyncLocked if another run holds it.
func AcquireLock(path string) (*fileLock, error) {
	if path == "" {
		return nil, errors.New("no lock path: a lock nobody agrees on is not a lock")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening the lock file %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrSyncLocked
		}
		return nil, fmt.Errorf("locking %s: %w", path, err)
	}
	// Best effort, for an operator reading the file by hand.
	if err := f.Truncate(0); err == nil {
		_, _ = f.WriteString(pidLine())
		_ = f.Sync()
	}
	return &fileLock{f: f}, nil
}

// Release drops the lock. Idempotent, because a caller that releases in a defer and again on an error path must
// not close a descriptor twice.
func (l *fileLock) Release() {
	if l == nil || l.f == nil {
		return
	}
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	_ = l.f.Close()
	l.f = nil
}
