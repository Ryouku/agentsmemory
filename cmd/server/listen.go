// listen.go decides what the serving entry points actually bind. The server has
// always been a TCP listener on Addr; --socket adds a Unix domain socket as an
// alternative transport for the same chi router, so nothing above this file
// knows or cares which one it got.
//
// The socket path exists for self-hosted installs. Local mode serves an
// unauthenticated /mcp, and a 0600 socket answers that with filesystem
// permissions: only the user who owns it can connect, where a loopback port is
// open to every process on the machine.
package main

import (
	"fmt"
	"net"
	"os"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
)

// socketPerm is the mode a listening socket is created with: owner-only.
//
// This is the whole security argument for --local over a socket, so it is not a
// default to be relaxed casually — widening it hands every local user read and
// write access to every memory in the database.
const socketPerm os.FileMode = 0o600

// listenerFor opens the listener the server should serve on: a Unix domain
// socket when cfg.SocketPath is set, otherwise the TCP cfg.Addr. Callers serve
// the same handler on whichever comes back.
//
// The returned listener is the caller's to close. Closing a unix listener also
// unlinks its socket file, which is why nothing here registers a cleanup.
func listenerFor(cfg config.Config) (net.Listener, error) {
	if cfg.SocketPath == "" {
		return net.Listen("tcp", cfg.Addr)
	}
	return listenUnix(cfg.SocketPath)
}

// listenUnix binds path as a Unix domain socket and tightens it to socketPerm.
//
// A crashed process leaves its socket file behind and the next bind fails with
// "address already in use", so a stale socket is cleared first. That removal is
// deliberately narrow: only a file that is actually a socket is unlinked. Being
// pointed at a regular file — a typo landing on a database or a config — must
// fail loudly rather than silently delete the operator's data.
func listenUnix(path string) (net.Listener, error) {
	if err := clearStaleSocket(path); err != nil {
		return nil, err
	}

	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen on socket %s: %w", path, err)
	}

	// Chmod after bind, not before: the socket does not exist until Listen
	// creates it, and it is created subject to the process umask — which on a
	// typical box yields 0755 and would leave the unauthenticated endpoint open
	// to every local user. An explicit chmod is what actually makes it 0600.
	//
	// A failure here is fatal rather than a warning: continuing would serve the
	// endpoint at unknown, possibly world-writable permissions, which is exactly
	// the outcome this mode exists to prevent.
	if err := os.Chmod(path, socketPerm); err != nil {
		ln.Close()
		return nil, fmt.Errorf("secure socket %s: %w", path, err)
	}
	return ln, nil
}

// clearStaleSocket removes path when it is a leftover socket file, and reports
// an error when it is anything else. A path that does not exist is fine — that
// is the normal first start.
func clearStaleSocket(path string) error {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect socket path %s: %w", path, err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refusing to use %s as a socket: a %s already exists there", path, describeMode(info.Mode()))
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove stale socket %s: %w", path, err)
	}
	return nil
}

// describeMode names what is sitting at a path, so the refusal above reads as an
// instruction ("a directory already exists there") rather than a mode bitmask.
func describeMode(m os.FileMode) string {
	switch {
	case m.IsDir():
		return "directory"
	case m.IsRegular():
		return "regular file"
	default:
		return "file"
	}
}
