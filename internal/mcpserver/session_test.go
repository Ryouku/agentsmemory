package mcpserver_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// probeServer stands up an MCP server carrying ONE tool whose only job is to
// record what the handler can see about its caller, wrapped in the SAME
// transport configuration production uses (cmd/server/main.go: WithStateLess).
//
// Deliberately no palace, no database, no real tools: the question ADR-018 T1
// asks is a property of the TRANSPORT, and standing the whole surface up would
// add a dozen ways for this test to fail for reasons that are not the question.
func probeServer(t *testing.T, stateless bool) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	seen := []string{}

	mcpSrv := server.NewMCPServer("probe", "test")
	mcpSrv.AddTool(
		mcp.NewTool("probe", mcp.WithDescription("records the caller's session identity")),
		func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			id := ""
			if s := server.ClientSessionFromContext(ctx); s != nil {
				id = s.SessionID()
			}
			mu.Lock()
			seen = append(seen, id)
			mu.Unlock()
			return mcp.NewToolResultText("ok"), nil
		},
	)
	opts := []server.StreamableHTTPOption{}
	if stateless {
		opts = append(opts, server.WithStateLess(true))
	}
	return httptest.NewServer(server.NewStreamableHTTPServer(mcpSrv, opts...)), &seen
}

// callProbe drives one initialize + one tools/call, optionally sending a
// client-supplied Mcp-Session-Id, and returns the session id the SERVER handed
// back on initialize.
func callProbe(t *testing.T, srv *httptest.Server, clientSessionID string) string {
	t.Helper()
	post := func(body string, sid string) (*http.Response, string) {
		req, err := http.NewRequest(http.MethodPost, srv.URL, bytes.NewBufferString(body))
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if sid != "" {
			req.Header.Set("Mcp-Session-Id", sid)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatalf("do: %v", err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp, string(b)
	}

	initBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"probe","version":"1"}}}`
	resp, _ := post(initBody, clientSessionID)
	served := resp.Header.Get("Mcp-Session-Id")

	send := clientSessionID
	if send == "" {
		send = served // a well-behaved client echoes back what it was given
	}
	callBody := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"probe","arguments":{}}}`
	if _, body := post(callBody, send); !strings.Contains(body, "ok") {
		t.Fatalf("probe tool did not run: %s", body)
	}
	return served
}

// TestSearchHandlerCanNameItsSession establishes, before any migration is
// written, whether a tool handler can say WHICH session asked.
//
// The finding, under production's own configuration: it cannot. cmd/server
// builds its transport with server.WithStateLess(true), which installs
// StatelessSessionIdManager — whose Generate() returns the EMPTY STRING and
// whose Validate() accepts anything without checking. So the server never mints
// an identity, hands the client an empty Mcp-Session-Id on initialize, and a
// well-behaved client echoing that back sends nothing on every later request.
//
// This is the outcome ADR-018 names as acceptable rather than a failure: with no
// identity reachable, T2 is unbuildable as designed and T3 ships alone. That is
// no longer a pending question — T2 was WITHDRAWN on 2026-08-22, the decision
// being that a stateless transport is worth more than per-session attribution.
//
// So this test is now the ONLY thing that would reopen it. It asserts the
// emptiness so that a future switch away from stateless mode — which WOULD make
// attribution possible — is announced by a red test rather than discovered by
// someone wondering why the column is still blank. If it fails, the failure is
// not a bug in the test: it means the premise the withdrawal rests on has
// changed, and ADR-018 T2 should be reconsidered on its merits.
func TestSearchHandlerCanNameItsSession(t *testing.T) {
	srv, seen := probeServer(t, true) // true = production's configuration
	defer srv.Close()

	served := callProbe(t, srv, "")
	if served != "" {
		t.Errorf("stateless mode served a non-empty Mcp-Session-Id %q — StatelessSessionIdManager."+
			"Generate() returns \"\", so this means the transport configuration changed and "+
			"attribution may now be possible; ADR-018 T2 was withdrawn on that premise and "+
			"should be reconsidered", served)
	}
	if len(*seen) != 1 {
		t.Fatalf("probe ran %d times, want 1", len(*seen))
	}
	if got := (*seen)[0]; got != "" {
		t.Errorf("the handler saw session identity %q under stateless mode; if this is now "+
			"non-empty and STABLE across a session, ADR-018 T2 is buildable after all", got)
	}
}

// TestTwoSessionsGetDifferentIdentities is T1's falsification step, and it is the
// half that matters.
//
// An identity that is the same for everybody is WORSE than none, because it looks
// like attribution: a column would fill with values, a report would group by them,
// and every session would appear to be one session. This asserts that under
// production's configuration two independent callers are indistinguishable — and
// that the capability nonetheless EXISTS the moment a client supplies its own id,
// because the stateless manager accepts it unvalidated.
//
// That is the shape of the answer T2 needs: not "impossible", but "possible only
// if the client cooperates, and silently degenerate if it does not".
func TestTwoSessionsGetDifferentIdentities(t *testing.T) {
	t.Run("two default clients are indistinguishable", func(t *testing.T) {
		srv, seen := probeServer(t, true)
		defer srv.Close()
		callProbe(t, srv, "")
		callProbe(t, srv, "")
		if len(*seen) != 2 {
			t.Fatalf("probe ran %d times, want 2", len(*seen))
		}
		if (*seen)[0] != (*seen)[1] {
			t.Errorf("two default clients were distinguishable (%q vs %q) — the transport now "+
				"mints per-session identities and ADR-018 T2 should be revisited",
				(*seen)[0], (*seen)[1])
		}
		if (*seen)[0] != "" {
			t.Errorf("the shared identity is %q rather than empty; a non-empty value that is the "+
				"SAME for every caller is the dangerous case — it fills a column and looks like "+
				"attribution", (*seen)[0])
		}
	})

	t.Run("a client that supplies its own id is distinguishable", func(t *testing.T) {
		srv, seen := probeServer(t, true)
		defer srv.Close()
		callProbe(t, srv, "session-alpha")
		callProbe(t, srv, "session-beta")
		if len(*seen) != 2 {
			t.Fatalf("probe ran %d times, want 2", len(*seen))
		}
		if (*seen)[0] == (*seen)[1] {
			t.Errorf("two clients sending DIFFERENT Mcp-Session-Id headers were still "+
				"indistinguishable (%q) — then no client cooperation can rescue attribution "+
				"and T2 is unbuildable outright", (*seen)[0])
		}
		for i, want := range []string{"session-alpha", "session-beta"} {
			if (*seen)[i] != want {
				t.Errorf("caller %d supplied %q and the handler saw %q", i, want, (*seen)[i])
			}
		}
	})
}

// TestProductionStillRunsStateless pins the PREMISE the finding above rests on.
//
// Without it the diagnostic is self-referential: the probe hardcodes stateless
// mode in its own fixture, so a switch of the real server to a stateful manager —
// the change that would make attribution possible — leaves every assertion above
// passing while the finding they record becomes false.
//
// A source check rather than a behavioural one, and that is stated rather than
// hidden: driving the real composition root would stand up a database, a
// listener and an embedder to observe one option, and the option is what matters.
// The grep is deliberately narrow — it asks only whether the transport is still
// built stateless, which is the single fact the finding depends on.
func TestProductionStillRunsStateless(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("server.go"))
	if err != nil {
		t.Fatalf("read server.go: %v", err)
	}
	body := string(src)
	fn := "func StreamHTTP"
	i := strings.Index(body, fn)
	if i < 0 {
		t.Fatal("StreamHTTP is gone; production and the harness have no shared stateless envelope")
	}
	rest := body[i:]
	end := strings.Index(rest, "\nfunc ")
	if end > 0 {
		rest = rest[:end]
	}
	if !strings.Contains(rest, "server.WithStateLess(true)") {
		t.Error("StreamHTTP no longer builds its transport with server.WithStateLess(true). " +
			"That is the premise ADR-018 T1's finding rests on — a stateful manager MINTS a " +
			"session id, so attribution may now be possible and T2 should be revisited rather " +
			"than left withdrawn.")
	}
	if strings.Contains(rest, "server.WithStateLess(false)") {
		t.Error("the transport is explicitly stateful; see above")
	}
	for _, rel := range []string{
		filepath.Join("..", "..", "cmd", "server", "main.go"),
		filepath.Join("..", "mcptest", "harness.go"),
	} {
		src, err := os.ReadFile(rel)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		got := string(src)
		if !strings.Contains(got, "mcpserver.StreamHTTP") {
			t.Errorf("%s no longer calls mcpserver.StreamHTTP; the HTTP envelope has a second owner", rel)
		}
		if strings.Contains(got, "NewStreamableHTTPServer") {
			t.Errorf("%s constructs Streamable HTTP beside StreamHTTP", rel)
		}
	}
}
