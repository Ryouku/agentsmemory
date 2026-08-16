// lock.go keeps two servers off one database.
//
// The database is the resource that needs protecting, not the listener. Before
// this guard, a second `serve` was free to start against a database another
// process was already using — over a second TCP port, or by unlinking a live
// Unix socket out from under the first server (listen.go cannot tell a live
// socket from a crashed one: stat reports the same srw------- for both). The
// first server kept running, kept its database handle, and logged nothing,
// while agents holding pooled connections to the old socket inode carried on
// talking to it. Two servers, one database, no indication anywhere.
//
// Guarding the database rather than the socket covers every route into that
// state — two TCP ports, two socket paths, or one of each — with one check.
// It also makes listen.go's stale-socket removal provably safe: while we hold
// this lock no other server on this database exists, so a socket file left at
// the path is stale by construction.
//
// The policy is incumbent-wins. A server already serving keeps its connections
// and its database; the newcomer fails fast and exits. The reverse would mean a
// mistyped command in another terminal could kill a live server.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// errLocked reports that another process already holds the lock. Each platform
// shim maps its own "would block" error onto this sentinel so the portable code
// below can distinguish "someone is already serving" — an ordinary, expected
// outcome the operator needs a clear message for — from a genuine I/O failure.
var errLocked = errors.New("lock is held by another process")

// lockSuffix is appended to the database path to name its lock file.
//
// A sidecar file rather than the database itself: SQLite manages its own
// advisory locks on that file, and layering ours onto the same inode invites
// confusion for no gain. It sits beside the -wal and -shm sidecars WAL already
// creates.
const lockSuffix = ".lock"

// lockDB takes an exclusive, non-blocking lock covering dbPath and returns the
// handle that holds it. Closing the returned value releases the lock, though a
// server normally just holds it until the process exits — the kernel drops it
// then, including on SIGKILL, which is the whole reason this is a file lock and
// not a pid file.
//
// A refusal here is not an error condition to work around: if another server is
// using this database, the correct outcome is for this process to stop.
func lockDB(dbPath string) (io.Closer, error) {
	path := dbPath + lockSuffix

	// O_CREATE, never O_EXCL: the lock file is expected to survive the process
	// that made it. Its *existence* means nothing — only the lock held on it
	// does — so a file left behind by a crash is simply relocked.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", path, err)
	}

	if err := tryLock(f); err != nil {
		holder := lockHolder(f)
		f.Close()
		if errors.Is(err, errLocked) {
			return nil, fmt.Errorf("another agentsmemory server is already using %s%s", dbPath, holder)
		}
		// Every other failure is fatal too. A filesystem that cannot honour this
		// lock (a network mount, typically) is one where SQLite's own locking is
		// already unreliable, so refusing to start is the honest answer.
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}

	// Record who holds it, purely so the *next* process to be refused can name
	// us. Nothing reads this to make a decision — the lock itself is the
	// authority — so a torn or empty read costs only a vaguer error message.
	if err := writeHolder(f); err != nil {
		unlock(f)
		f.Close()
		return nil, fmt.Errorf("stamp lock file %s: %w", path, err)
	}

	return &dbLock{file: f}, nil
}

// dbLock owns a held database lock. It exists so callers can defer a Close
// without knowing which platform primitive is underneath.
type dbLock struct {
	file *os.File
}

// Close releases the lock and closes the underlying file. The lock file itself
// is deliberately left on disk: unlinking it would let the next process create
// and lock a *different* inode at the same path while a third still holds this
// one, which is exactly the race the lock is meant to prevent.
func (l *dbLock) Close() error {
	if err := unlock(l.file); err != nil {
		l.file.Close()
		return err
	}
	return l.file.Close()
}

// writeHolder stamps the current pid into the lock file, replacing whatever a
// previous holder left. Called only once the lock is held, so there is no
// competing writer.
func writeHolder(f *os.File) error {
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.WriteAt([]byte(strconv.Itoa(os.Getpid())), 0); err != nil {
		return err
	}
	return f.Sync()
}

// lockHolder reads the pid stamped by whoever holds the lock and formats it for
// an error message, or returns "" if it cannot be read. Best effort by design:
// the holder may not have stamped it yet, and the answer only affects how
// helpful the refusal reads.
func lockHolder(f *os.File) string {
	buf := make([]byte, 32)
	n, err := f.ReadAt(buf, 0)
	if n == 0 && err != nil {
		return ""
	}
	pid := strings.TrimSpace(string(buf[:n]))
	if pid == "" {
		return ""
	}
	return " (pid " + pid + ")"
}
