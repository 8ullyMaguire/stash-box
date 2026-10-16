//go:build !unix

package sdbimport

// On a platform without flock the lock degrades to "not enforced", and SAYS SO rather than pretending: the
// deployment target is Linux and a silent no-op lock would turn "the timer and a manual run cannot overlap" into
// a claim about a file that is never locked.

import (
	"errors"
	"fmt"
	"runtime"
)

type fileLock struct{}

// AcquireLock reports that locking is unavailable here instead of pretending to lock.
func AcquireLock(path string) (*fileLock, error) {
	return nil, fmt.Errorf("the sync lock is not implemented on %s: %w", runtime.GOOS, ErrLockUnsupported)
}

// Release exists so the caller's defer compiles unchanged.
func (l *fileLock) Release() {}

// ErrLockUnsupported is returned by AcquireLock where flock is unavailable.
var ErrLockUnsupported = errors.New("locking is unavailable on this platform")
