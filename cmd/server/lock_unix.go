//go:build !windows

package main

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// tryLock takes an exclusive flock on f without blocking.
//
// flock is per open file description, not per process, so two independent opens
// of the same path conflict even inside one process — which is what makes this
// testable without spawning a second binary. The kernel releases the lock when
// the descriptor closes, including when the process is SIGKILLed, so a crashed
// server leaves nothing to clean up.
func tryLock(f *os.File) error {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return errLocked
	}
	return err
}

// unlock releases the flock held on f.
func unlock(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
