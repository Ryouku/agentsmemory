package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"
)

// upstreamTo builds an upstream pointed at a test server, so the proxy tests
// exercise the real HTTP path rather than a stubbed client.
func upstreamTo(t *testing.T, srv *httptest.Server) *upstream {
	t.Helper()
	up, err := newUpstream("", srv.URL, "", "")
	if err != nil {
		t.Fatalf("newUpstream: %v", err)
	}
	return up
}

// runProxy drives the proxy over a canned stdin and returns the lines it wrote.
func runProxy(t *testing.T, up *upstream, stdin string) []string {
	t.Helper()
	var out strings.Builder
	if err := runStdioProxy(context.Background(), up, strings.NewReader(stdin), &out); err != nil {
		t.Fatalf("runStdioProxy: %v", err)
	}
	got := strings.TrimSuffix(out.String(), "\n")
	if got == "" {
		return nil
	}
	return strings.Split(got, "\n")
}

// TestProxyForwardsJSONResponse is the ordinary case: the server answers a tool
// call with application/json and the proxy relays it verbatim on one line.
func TestProxyForwardsJSONResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); !strings.Contains(got, "text/event-stream") {
			t.Errorf("Accept = %q, must offer text/event-stream or a strict server refuses the SSE upgrade", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "tools/list") {
			t.Errorf("server did not receive the original message, got %q", body)
		}
		w.Header().Set("Content-Type", "application/json")
		// Indented on purpose: the response must still reach stdout as ONE line.
		_, _ = io.WriteString(w, "{\n  \"jsonrpc\": \"2.0\",\n  \"id\": 1,\n  \"result\": {}\n}")
	}))
	defer srv.Close()

	lines := runProxy(t, upstreamTo(t, srv), `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`+"\n")

	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1: %q", len(lines), lines)
	}
	if lines[0] != `{"jsonrpc":"2.0","id":1,"result":{}}` {
		t.Fatalf("response not compacted onto one line: %q", lines[0])
	}
}

// TestProxyForwardsSSEEvents covers the upgrade the server performs whenever a
// tool emits notifications: several JSON-RPC messages arrive in one HTTP
// response, and every one of them must reach the agent as its own line.
func TestProxyForwardsSSEEvents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// Exactly the framing mcp-go's writeSSEEvent produces.
		_, _ = io.WriteString(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\n")
		_, _ = io.WriteString(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":7,\"result\":{\"ok\":true}}\n\n")
	}))
	defer srv.Close()

	lines := runProxy(t, upstreamTo(t, srv), `{"jsonrpc":"2.0","id":7,"method":"tools/call"}`+"\n")

	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2 (notification + result): %q", len(lines), lines)
	}
	if !strings.Contains(lines[0], "notifications/progress") {
		t.Errorf("first line should be the notification, got %q", lines[0])
	}
	if !strings.Contains(lines[1], `"id":7`) {
		t.Errorf("second line should be the result, got %q", lines[1])
	}
}

// TestProxyDropsNotificationAck pins the 202 path. A notification has no reply,
// so emitting anything here would inject a phantom message the agent cannot
// match to a request.
func TestProxyDropsNotificationAck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	lines := runProxy(t, upstreamTo(t, srv), `{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n")

	if len(lines) != 0 {
		t.Fatalf("202 must produce no output, got %q", lines)
	}
}

// TestProxyReportsTransportFailure is the reason the proxy does not simply exit
// on a dead server: the agent is waiting on a reply, and without one the tool
// call hangs forever instead of failing.
func TestProxyReportsTransportFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close() // nothing is listening now

	lines := runProxy(t, upstreamTo(t, srv), `{"jsonrpc":"2.0","id":"abc","method":"tools/list"}`+"\n")

	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1 error reply: %q", len(lines), lines)
	}
	var reply struct {
		ID    json.RawMessage `json:"id"`
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &reply); err != nil {
		t.Fatalf("error reply is not valid JSON: %v (%q)", err, lines[0])
	}
	// String ids must survive as strings — the agent matches on them exactly.
	if string(reply.ID) != `"abc"` {
		t.Errorf("id = %s, want \"abc\"", reply.ID)
	}
	if reply.Error.Code != jsonRPCInternalError {
		t.Errorf("code = %d, want %d", reply.Error.Code, jsonRPCInternalError)
	}
}

// TestProxyStaysSilentForUnanswerableNotification checks the other half of the
// failure path: a notification that could not be delivered still gets no reply,
// because answering one violates JSON-RPC.
func TestProxyStaysSilentForUnanswerableNotification(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close()

	lines := runProxy(t, upstreamTo(t, srv), `{"jsonrpc":"2.0","method":"notifications/cancelled"}`+"\n")

	if len(lines) != 0 {
		t.Fatalf("a failed notification must not be answered, got %q", lines)
	}
}

// TestProxyHandlesFinalMessageWithoutNewline guards the last request of a
// session: an agent that closes the pipe without a trailing newline must still
// get its answer.
func TestProxyHandlesFinalMessageWithoutNewline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":9,"result":{}}`)
	}))
	defer srv.Close()

	lines := runProxy(t, upstreamTo(t, srv), `{"jsonrpc":"2.0","id":9,"method":"ping"}`) // no "\n"

	if len(lines) != 1 {
		t.Fatalf("unterminated final message was dropped: %q", lines)
	}
}

// TestProxyForwardsBearerToken covers the multi-tenant path: the proxy is the
// only thing holding the API key, so it must present it exactly as the HTTP gate
// expects.
func TestProxyForwardsBearerToken(t *testing.T) {
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
	}))
	defer srv.Close()

	up, err := newUpstream("", srv.URL, "secret-key", "")
	if err != nil {
		t.Fatalf("newUpstream: %v", err)
	}
	runProxy(t, up, `{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n")

	if seen != "Bearer secret-key" {
		t.Fatalf("Authorization = %q, want %q", seen, "Bearer secret-key")
	}
}

// TestStdioCommandForwardsRegistrationWing exercises the full CLI path. A unit
// test that sets the header directly could pass while --wing is undeclared or
// its parsed value never reaches newUpstream — the unreachable-feature defect
// this repository explicitly guards against.
func TestStdioCommandForwardsRegistrationWing(t *testing.T) {
	const wing = "wing_acme"
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get(mcpprotocol.WingHeader)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
	}))
	defer srv.Close()

	var stdout strings.Builder
	cmd := stdioCommandWithIO(
		config.Default(),
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n"),
		&stdout,
	)
	if err := cmd.Run(context.Background(), []string{"mcp-stdio", "--url", srv.URL, "--wing", wing}); err != nil {
		t.Fatalf("mcp-stdio: %v", err)
	}
	if seen != wing {
		t.Fatalf("%s = %q, want %q", mcpprotocol.WingHeader, seen, wing)
	}
}

// TestProxyOverUnixSocket is the end-to-end shape the feature exists for: a
// server bound to a socket via listenerFor, reached by the proxy through
// --socket. It ties both halves of this change together.
func TestProxyOverUnixSocket(t *testing.T) {
	path := shortSocketPath(t)

	ln, err := listenUnix(path)
	if err != nil {
		t.Fatalf("listenUnix: %v", err)
	}
	defer ln.Close()

	go http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != mcpPath {
			t.Errorf("proxy posted to %q, want %q", r.URL.Path, mcpPath)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{"transport":"unix"}}`)
	}))

	up, err := newUpstream(path, "", "", "")
	if err != nil {
		t.Fatalf("newUpstream: %v", err)
	}
	lines := runProxy(t, up, `{"jsonrpc":"2.0","id":1,"method":"am_status"}`+"\n")

	if len(lines) != 1 || !strings.Contains(lines[0], `"transport":"unix"`) {
		t.Fatalf("socket round trip failed: %q", lines)
	}
}

// TestNewUpstreamRequiresATarget keeps the CLI honest: with neither flag there
// is nothing to dial, and that should be a clear error rather than a confusing
// connection refused later.
func TestNewUpstreamRequiresATarget(t *testing.T) {
	if _, err := newUpstream("", "", "", ""); err == nil {
		t.Fatal("expected an error when neither --socket nor --url is set")
	}
}

// TestNewUpstreamPrefersSocket documents the precedence: --socket is the more
// specific instruction, so it wins over a --url that is always defaulted.
func TestNewUpstreamPrefersSocket(t *testing.T) {
	up, err := newUpstream("/tmp/whatever.sock", defaultProxyURL, "", "")
	if err != nil {
		t.Fatalf("newUpstream: %v", err)
	}
	if up.endpoint != socketURL {
		t.Fatalf("endpoint = %q, want the socket placeholder %q", up.endpoint, socketURL)
	}
}

// TestProxySurvivesGarbageLine checks the loop does not die on input it cannot
// parse. The server owns protocol errors, so a bad line is forwarded and
// whatever comes back is relayed — one broken message must not end the session.
func TestProxySurvivesGarbageLine(t *testing.T) {
	var posts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		posts++
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{}}`, posts)
	}))
	defer srv.Close()

	lines := runProxy(t, upstreamTo(t, srv), "not json at all\n"+`{"jsonrpc":"2.0","id":2,"method":"ping"}`+"\n")

	if posts != 2 {
		t.Fatalf("proxy stopped early: forwarded %d messages, want 2", posts)
	}
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %q", len(lines), lines)
	}
}

// TestProxyHandlesLargeMessage is the bufio.Reader justification: a drawer body
// well past a default scanner buffer must survive the trip intact rather than be
// silently truncated into invalid JSON.
func TestProxyHandlesLargeMessage(t *testing.T) {
	big := strings.Repeat("x", 512<<10)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var msg struct {
			Params struct {
				Content string `json:"content"`
			} `json:"params"`
		}
		if err := json.Unmarshal(body, &msg); err != nil {
			t.Errorf("server received malformed JSON (%d bytes): %v", len(body), err)
		}
		if len(msg.Params.Content) != len(big) {
			t.Errorf("content truncated: got %d bytes, want %d", len(msg.Params.Content), len(big))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
	}))
	defer srv.Close()

	payload, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"content": big},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	runProxy(t, upstreamTo(t, srv), string(payload)+"\n")
}
