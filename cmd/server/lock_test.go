package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// dbPath returns a database path in a fresh temp dir. The lock file lands beside
// it, so nothing here touches the developer's real database.
func dbPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "agentsmemory.db")
}

// A first server claims the database and a second is refused. The two locks are
// taken in one process on purpose: flock is per open file description, so this
// is the same conflict two separate servers would hit, without spawning one.
func TestLockDB_SecondCallerRefused(t *testing.T) {
	path := dbPath(t)

	first, err := lockDB(path)
	if err != nil {
		t.Fatalf("first lockDB: %v", err)
	}
	defer first.Close()

	second, err := lockDB(path)
	if err == nil {
		second.Close()
		t.Fatal("second lockDB succeeded; the database is not guarded")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not name the database, so the operator cannot tell which one is contended", err)
	}
}

// The refusal names the process holding the database — the operator's very next
// question after "it is already in use".
func TestLockDB_ErrorNamesHoldingPID(t *testing.T) {
	path := dbPath(t)

	first, err := lockDB(path)
	if err != nil {
		t.Fatalf("first lockDB: %v", err)
	}
	defer first.Close()

	_, err = lockDB(path)
	if err == nil {
		t.Fatal("second lockDB succeeded")
	}
	want := "pid " + strconv.Itoa(os.Getpid())
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err, want)
	}
}

// Releasing the lock hands the database to the next server, so a clean restart
// is not blocked by its own predecessor.
func TestLockDB_ReleaseAllowsReacquire(t *testing.T) {
	path := dbPath(t)

	first, err := lockDB(path)
	if err != nil {
		t.Fatalf("first lockDB: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("release: %v", err)
	}

	second, err := lockDB(path)
	if err != nil {
		t.Fatalf("lockDB after release: %v", err)
	}
	second.Close()
}

// A lock file left behind by a killed server is reusable. This is the whole
// reason for a file lock over a pid file: the kernel drops the lock when the
// process dies, so there is no stale state to detect or clean up, and the file
// sitting on disk means nothing by itself.
func TestLockDB_StaleFileIsReusable(t *testing.T) {
	path := dbPath(t)

	// A leftover file with a foreign pid, exactly as a SIGKILLed server leaves it.
	if err := os.WriteFile(path+lockSuffix, []byte("999999"), 0o600); err != nil {
		t.Fatal(err)
	}

	lock, err := lockDB(path)
	if err != nil {
		t.Fatalf("lockDB over a stale lock file: %v", err)
	}
	lock.Close()
}

// Releasing must not unlink the lock file. Removing it would let a later server
// create and lock a different inode at the same path while another still holds
// the first — the race this lock exists to prevent.
func TestLockDB_ReleaseKeepsLockFile(t *testing.T) {
	path := dbPath(t)

	lock, err := lockDB(path)
	if err != nil {
		t.Fatalf("lockDB: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatalf("release: %v", err)
	}

	if _, err := os.Stat(path + lockSuffix); err != nil {
		t.Errorf("lock file gone after release: %v", err)
	}
}

// The lock is taken on a sidecar, never on the database itself, so it cannot
// interfere with SQLite's own locking — and it must not conjure a database file
// into existence for a path that has none yet.
func TestLockDB_DoesNotTouchTheDatabaseFile(t *testing.T) {
	path := dbPath(t)

	lock, err := lockDB(path)
	if err != nil {
		t.Fatalf("lockDB: %v", err)
	}
	defer lock.Close()

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("lockDB created or touched %s (err = %v); it must only use the sidecar", path, err)
	}
}
