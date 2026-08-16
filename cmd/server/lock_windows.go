//go:build windows

package main

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockBytes is how much of the lock file to lock. Windows locks byte ranges
// rather than whole files, and the range need only be one all holders agree on —
// the file's contents are a diagnostic pid, never read for a decision.
const lockBytes = 1

// tryLock takes an exclusive lock on f's first byte without blocking.
//
// LOCKFILE_FAIL_IMMEDIATELY is the LOCK_NB equivalent: without it LockFileEx
// waits for the holder instead of reporting the conflict. Windows releases the
// lock when the handle closes — including on an abnormal exit — matching the
// flock behaviour the unix build relies on.
func tryLock(f *os.File) error {
	err := windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, lockBytes, 0, new(windows.Overlapped),
	)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return errLocked
	}
	return err
}

// unlock releases the byte-range lock held on f.
func unlock(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, lockBytes, 0, new(windows.Overlapped))
}
