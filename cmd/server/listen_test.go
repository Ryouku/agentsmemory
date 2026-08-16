package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
)

// TestListenerForTCP pins the default: with no socket path the server keeps
// binding TCP, which is what makes --socket additive rather than a migration.
func TestListenerForTCP(t *testing.T) {
	ln, err := listenerFor(config.Config{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("listenerFor: %v", err)
	}
	defer ln.Close()

	if _, ok := ln.Addr().(*net.TCPAddr); !ok {
		t.Fatalf("expected a TCP listener, got %T", ln.Addr())
	}
}

// shortSocketPath returns a socket path short enough to bind. The sockaddr_un
// path field is 104 bytes on macOS and 108 on Linux, and t.TempDir() spends most
// of that on the test name — producing a bind failure of "invalid argument" that
// looks nothing like "your path is too long".
func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "am")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

// TestListenerForUnix checks the socket is created where asked and, critically,
// that it ends up owner-only. The 0600 mode is the entire security argument for
// running local mode's unauthenticated /mcp on a socket, so it is asserted
// rather than assumed.
func TestListenerForUnix(t *testing.T) {
	path := shortSocketPath(t)

	ln, err := listenerFor(config.Config{Addr: "127.0.0.1:0", SocketPath: path})
	if err != nil {
		t.Fatalf("listenerFor: %v", err)
	}
	defer ln.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("socket not created: %v", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("%s is not a socket (mode %v)", path, info.Mode())
	}
	if perm := info.Mode().Perm(); perm != socketPerm {
		t.Fatalf("socket mode = %v, want %v — the unauthenticated endpoint would be exposed to other local users", perm, socketPerm)
	}
}

// TestListenerForUnixReplacesStaleSocket covers the crash-restart path: a
// process that died left its socket file behind, and binding must succeed
// anyway instead of failing with "address already in use".
func TestListenerForUnixReplacesStaleSocket(t *testing.T) {
	path := shortSocketPath(t)

	stale, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("seed stale socket: %v", err)
	}
	// Go unlinks a unix socket on Close, which is the clean-shutdown path and
	// leaves nothing stale behind. Disabling that reproduces what a SIGKILL
	// actually leaves on disk: a socket file with no process behind it.
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	if err := stale.Close(); err != nil {
		t.Fatalf("close stale listener: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stale socket did not survive close, cannot test the restart path: %v", err)
	}

	ln, err := listenerFor(config.Config{SocketPath: path})
	if err != nil {
		t.Fatalf("listenerFor over a stale socket: %v", err)
	}
	defer ln.Close()
}

// TestListenerForUnixRefusesRegularFile is the safety case: a mistyped --socket
// that lands on a database or a config must fail loudly, never be silently
// unlinked to make room for a socket.
func TestListenerForUnixRefusesRegularFile(t *testing.T) {
	path := filepath.Join(filepath.Dir(shortSocketPath(t)), "precious.db")
	if err := os.WriteFile(path, []byte("real data"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	ln, err := listenerFor(config.Config{SocketPath: path})
	if err == nil {
		ln.Close()
		t.Fatal("expected an error when the socket path holds a regular file")
	}

	// The file must still be there — that is the point of the check.
	body, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("the existing file was destroyed: %v", readErr)
	}
	if string(body) != "real data" {
		t.Fatalf("the existing file was overwritten: %q", body)
	}
}
