package web

import (
	_ "embed"
	"net/http"
	"strings"
)

// claudeGuide is the agent-facing install guide served at /claude-guide as raw
// Markdown. It is written for Claude (or any coding agent) to self-install the
// kit: ask the user for their workspace token, then run the download +
// `install --global --token` commands. Shipping it as plain Markdown (not a styled
// page) keeps it clean for an agent to fetch and parse.
//
//go:embed claude-guide.md
var claudeGuide string

// windowsGuide is the agent-facing install guide served at /windows-guide, for
// the clients the CLI kit cannot reach: the installer is a bash script plus a
// Linux/macOS binary (clients/claude-code/sandbox.go uses syscall.Exec, which does
// not exist on Windows), so Windows, VS Code and Claude Desktop users have no
// installer to run. They do not need one — agentsmemory is a remote MCP server, so
// this guide walks an assistant through writing the user-level MCP config for its
// own host instead. Markdown for the same reason as claudeGuide.
//
//go:embed windows-guide.md
var windowsGuide string

// guideBaseURLPlaceholder marks where the guide's "sign in at <url>" step should
// carry this server's public origin. It is substituted per request so the link
// points at whatever host the request arrived through (localhost in dev, the real
// domain in production) — the same reasoning as the migration command in skills.go.
const guideBaseURLPlaceholder = "{{BASE_URL}}"

// handleClaudeGuide serves the install guide as raw Markdown at /claude-guide. It
// is public (no auth) and deliberately not a templ/HTML page: an agent curls it
// and reads the commands directly, so HTML chrome would only add noise. The only
// dynamic part is the dashboard URL, filled in from the request.
func (s *Server) handleClaudeGuide(w http.ResponseWriter, r *http.Request) {
	serveGuide(w, r, claudeGuide)
}

// handleWindowsGuide serves the no-CLI install guide as raw Markdown at
// /windows-guide, on the same terms as /claude-guide: public, unstyled, and
// origin-substituted.
func (s *Server) handleWindowsGuide(w http.ResponseWriter, r *http.Request) {
	serveGuide(w, r, windowsGuide)
}

// serveGuide writes one embedded guide as Markdown with the base-URL placeholder
// resolved to the origin the request arrived through. Both guides are served
// identically, so the substitution and the content type live here rather than
// being repeated per handler.
func serveGuide(w http.ResponseWriter, r *http.Request, guide string) {
	body := strings.ReplaceAll(guide, guideBaseURLPlaceholder, requestBaseURL(r))
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	_, _ = w.Write([]byte(body))
}
