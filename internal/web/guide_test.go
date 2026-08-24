package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestClaudeGuideServesMarkdown checks the public /claude-guide handler: it returns
// the embedded guide as Markdown and substitutes the request's origin into the
// dashboard link. handleClaudeGuide reads no Server fields, so a zero-value Server
// is enough to exercise it.
func TestClaudeGuideServesMarkdown(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://memory.example/claude-guide", nil)
	rec := httptest.NewRecorder()

	(&Server{}).handleClaudeGuide(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
		t.Fatalf("Content-Type = %q, want text/markdown", ct)
	}

	body := rec.Body.String()
	// The base-URL placeholder must be resolved to the request origin, leaving no
	// literal template token in what an agent fetches.
	if strings.Contains(body, guideBaseURLPlaceholder) {
		t.Fatalf("placeholder %q was not substituted", guideBaseURLPlaceholder)
	}
	if !strings.Contains(body, "http://memory.example") {
		t.Fatal("request origin not substituted into the guide")
	}
	// The install command an agent must run has to be present verbatim.
	if !strings.Contains(body, "install --global --token") {
		t.Fatal("guide is missing the --global --token install command")
	}
}

// TestWindowsGuideRedirectsToTheInstallGuide pins a URL we promised and then
// moved.
//
// /windows-guide's per-client configuration now lives in /install-memory-mcp, but
// the old URL has been handed to assistants and linked from the landing page, so
// it stays alive as a redirect. The failure this prevents is specific: an agent
// that fetches a 404 does not go looking for the replacement — it reports back
// that setup is impossible, which is worse than any stale content would have
// been.
//
// It asserts the STATUS and the TARGET. A redirect to the wrong place is the same
// dead end wearing a 301.
func TestWindowsGuideRedirectsToTheInstallGuide(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://memory.example/windows-guide", nil)
	rec := httptest.NewRecorder()

	(&Server{}).handleWindowsGuide(rec, req)

	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want %d (permanent redirect)", rec.Code, http.StatusMovedPermanently)
	}
	if loc := rec.Header().Get("Location"); loc != "/install-memory-mcp" {
		t.Fatalf("Location = %q, want /install-memory-mcp", loc)
	}
}

// TestBootstrapMemoryServesMarkdown covers /bootstrap-memory on the same terms as
// the two install guides, and then asserts the things that make THIS document
// useful rather than merely present.
//
// The content assertions are deliberate. A route that serves 200 and the right
// content type proves the wiring and nothing about the document, and this one is
// handed to an agent that will act on it unsupervised: it has to say that it is
// not an installer (a reader who starts installing has already failed), it has to
// carry the acceptance test, and it has to carry the subagent recall check, which
// is the only step that can falsify the whole setup. Losing any of those leaves a
// document that still serves and no longer works.
func TestBootstrapMemoryServesMarkdown(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://memory.example/bootstrap-memory", nil)
	rec := httptest.NewRecorder()

	(&Server{}).handleBootstrapMemory(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
		t.Fatalf("Content-Type = %q, want text/markdown", ct)
	}

	body := rec.Body.String()
	if strings.Contains(body, guideBaseURLPlaceholder) {
		t.Fatalf("placeholder %q was not substituted", guideBaseURLPlaceholder)
	}
	for _, want := range []string{
		"not** an installer guide",          // the reader must not start installing
		"Spawn a subagent to verify recall", // §9.2b — the only step that can falsify the setup
		"Acceptance test",                   // §13 — run it, do not assume
		"am_status",                         // the call that proves which palace answered
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("bootstrap-memory document is missing %q", want)
		}
	}
}

// TestInstallMemoryMCPServesMarkdown covers /install-memory-mcp and asserts the
// facts an assistant acts on without supervision.
//
// The two connection facts are the whole document: a client that gets the
// endpoint or the auth header wrong fails at first use, far from this page. The
// handoff is asserted because a connected server is an EMPTY palace — a guide
// that stops at registration leaves the human with a working MCP and no memory,
// which is the failure this page exists inside a chain to prevent. And the
// token rule is asserted because an assistant that invents a credential produces
// a config that looks correct and is not.
//
// The handoff is matched as a HEADING rather than as a bare URL. The document
// links /bootstrap-memory elsewhere in passing, so a substring check passed even
// with the handoff step deleted — it asserted that the words appear, not that the
// reader is sent there.
func TestInstallMemoryMCPServesMarkdown(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://memory.example/install-memory-mcp", nil)
	rec := httptest.NewRecorder()

	(&Server{}).handleInstallMemoryMCP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
		t.Fatalf("Content-Type = %q, want text/markdown", ct)
	}

	body := rec.Body.String()
	if strings.Contains(body, guideBaseURLPlaceholder) {
		t.Fatalf("placeholder %q was not substituted", guideBaseURLPlaceholder)
	}
	for _, want := range []string{
		"http://memory.example/mcp",                    // the endpoint, origin-substituted
		"Authorization: Bearer",                        // the only auth this server takes
		"### → http://memory.example/bootstrap-memory", // the handoff AS A STEP, not a passing mention
		"Never invent",                                 // the rule that keeps a real credential real
		"claude mcp add",                               // a host with a registration command
		"codex mcp add",                                // and one whose token rides the environment
		// Absorbed from /windows-guide, which now redirects here. These are the
		// per-client specifics the redirect depends on: losing one strands that
		// client's users at a page that no longer answers them, and the redirect
		// makes the loss invisible because the URL still works.
		`%APPDATA%\Code\User\mcp.json`,   // VS Code
		"${input:agentsmemory-token}",    // ...and the form that keeps the token off disk
		`%USERPROFILE%\.cursor\mcp.json`, // Cursor
		"claude_desktop_config.json",     // Claude Desktop
		"mcp-remote",                     // its Node.js bridge route
		"copilot-instructions.md",        // where the protocol goes, per client
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("install-memory-mcp document is missing %q", want)
		}
	}
}
