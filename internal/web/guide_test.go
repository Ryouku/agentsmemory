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

// TestWindowsGuideServesMarkdown is the sibling check for /windows-guide. Beyond
// the shared contract (Markdown, no unresolved placeholder) it asserts the two
// things that make the guide useful at all: the MCP endpoint an assistant has to
// write into the config, and a section per supported client — a guide that lost
// one of those would still serve 200 while being useless to the reader it is for.
func TestWindowsGuideServesMarkdown(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://memory.example/windows-guide", nil)
	rec := httptest.NewRecorder()

	(&Server{}).handleWindowsGuide(rec, req)

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
	if !strings.Contains(body, "http://memory.example") {
		t.Fatal("request origin not substituted into the guide")
	}
	if !strings.Contains(body, "https://aiagentmemory.dev/mcp") {
		t.Fatal("guide is missing the remote MCP endpoint")
	}
	for _, client := range []string{"VS Code", "Cursor", "Claude Desktop"} {
		if !strings.Contains(body, client) {
			t.Errorf("guide has no section for %s", client)
		}
	}
}
