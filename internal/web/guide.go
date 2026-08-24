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

// installMemoryMCP is the harness-agnostic MCP registration document served at
// /install-memory-mcp. It is the front door for every client: agentsmemory is a
// remote server, so connecting reduces to a URL and a bearer token, and each
// host differs only in where that pair is written.
//
// It exists alongside the two deeper guides rather than replacing them.
// /claude-guide installs the CLI kit (macOS and Linux) and /windows-guide carries
// the per-client config for VS Code, Cursor and Claude Desktop; this one answers
// "how do I connect AT ALL" for a host neither covers — which on Windows is the
// only question, because the bash installer cannot run there. It hands off to
// /bootstrap-memory, since a connected server is an empty palace until the memory
// model is built inside it.
//
//go:embed install-memory-mcp.md
var installMemoryMCP string

// bootstrapMemory is the memory-model handoff document served at
// /bootstrap-memory as raw Markdown. Unlike the two install guides above it
// assumes the MCP is already connected: it covers what a team must build INSIDE
// a palace that already answers — the rooms, the two auto-loaded skills, the
// knowledge-graph rules, how to recall, and how a session resumes work the last
// one left unfinished.
//
// It is served rather than shipped in the repository because the reader is an
// agent that has this URL and not a checkout, which is also why it is Markdown
// with no HTML chrome.
//
//go:embed bootstrap-memory.md
var bootstrapMemory string

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

// handleInstallMemoryMCP serves the harness-agnostic MCP registration document as
// raw Markdown at /install-memory-mcp, on the same terms as the other guides:
// public, unstyled, and origin-substituted.
func (s *Server) handleInstallMemoryMCP(w http.ResponseWriter, r *http.Request) {
	serveGuide(w, r, installMemoryMCP)
}

// handleBootstrapMemory serves the memory-model handoff document as raw Markdown
// at /bootstrap-memory, on the same terms as the install guides: public,
// unstyled, and origin-substituted.
func (s *Server) handleBootstrapMemory(w http.ResponseWriter, r *http.Request) {
	serveGuide(w, r, bootstrapMemory)
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
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	_, _ = w.Write([]byte(resolveBaseURL(guide, r)))
}

// resolveBaseURL fills the {{BASE_URL}} placeholder in an embedded document with
// the origin this request arrived through. It is split out of serveGuide because
// the discovery surface (sitemap.go) embeds the same placeholder in documents it
// serves as text/plain rather than Markdown, and only the content type differs.
func resolveBaseURL(body string, r *http.Request) string {
	return strings.ReplaceAll(body, guideBaseURLPlaceholder, requestBaseURL(r))
}
