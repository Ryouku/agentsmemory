package views

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
)

// TestKeyBlockRevealedShowsInstallCommand renders a revealed API-key block and
// checks it offers the full-install one-paste (carrying the token via
// AGENTSMEMORY_TOKEN and --global) — and only that, not a bare `claude mcp add`.
// Rendering the fragment directly verifies the KeyBlock → InstallBlock →
// installBuilder path without booting the server. Note the `"` around the token is
// HTML-escaped in the <code> text, so the env var and token are asserted
// separately rather than as one quoted string.
func TestKeyBlockRevealedShowsInstallCommand(t *testing.T) {
	var buf bytes.Buffer
	vm := KeyVM{
		TeamID:   "t1",
		Revealed: true,
		Secret:   "SECRET123",
	}
	if err := KeyBlock(vm).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	html := buf.String()

	for _, want := range []string{
		"Install the kit",     // the full-install block label
		"install.sh",          // the bootstrap URL
		"AGENTSMEMORY_TOKEN=", // token passed via env
		"SECRET123",           // the revealed token itself
		"--global",            // non-interactive global mode
	} {
		if !strings.Contains(html, want) {
			t.Errorf("revealed key block missing %q\n---\n%s", want, html)
		}
	}
	// The register-only `claude mcp add` affordance must be gone — the installer
	// replaces it because it also wires hooks/commands/CLAUDE.md.
	if strings.Contains(html, "Add to Claude Code") {
		t.Error("bare 'Add to Claude Code' MCP block should have been removed")
	}
}

// TestInstallCommandShape locks the shape of the command the dashboard hands over
// before any choice is made: it must pipe install.sh into a bash that carries the
// token in AGENTSMEMORY_TOKEN and forwards --global, so the install is fully
// non-interactive. This is also the no-JS fallback rendered into the block, so it
// has to stand on its own even if datastar never boots.
func TestInstallCommandShape(t *testing.T) {
	got := projectBuilder("t1", "TOK").Default()
	for _, want := range []string{
		"curl -fsSL ",
		installScriptURL,
		`AGENTSMEMORY_TOKEN="TOK"`,
		"bash -s -- --global",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("default install command missing %q\n got: %s", want, got)
		}
	}
}

// TestInstallBlockIsBuilder checks the dashboard offers the same picker as the
// public page rather than a fixed --global one-liner, and that the token is inside
// the command it builds — the reason the builder is worth having here at all.
func TestInstallBlockIsBuilder(t *testing.T) {
	var buf bytes.Buffer
	vm := KeyVM{TeamID: "t1", Revealed: true, Secret: "SECRET123"}
	if err := KeyBlock(vm).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	html := buf.String()

	b := projectBuilder("t1", "SECRET123")
	for _, want := range []string{
		esc(b.SetMode("project")),               // the per-project tab
		`data-bind="` + b.sig("_agent") + `"`,   // the agent picker
		`data-bind="` + b.sig("_sbname") + `"`,  // the sandbox name
		`data-bind="` + b.sig("_optcopy") + `"`, // and the flags
		`data-bind="` + b.sig("_optshared") + `"`,
		`data-bind="` + b.sig("_optrec") + `"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("dashboard install block is missing builder control %q", want)
		}
	}
	// The assembled command must carry the token, not just the fallback line.
	if !strings.Contains(html, esc(`AGENTSMEMORY_TOKEN=\"SECRET123\"`)) {
		t.Error("the built command does not embed the workspace token")
	}
	// Displayed and copied command are one expression, as on the landing page.
	if !strings.Contains(html, `data-text="`+esc(b.InstallExpr())+`"`) {
		t.Error("command block does not render the assembled expression")
	}
	// The launch line follows a sandboxed install, gated to the project tab.
	if !strings.Contains(html, `data-text="`+esc(b.RunExpr())+`"`) {
		t.Error("dashboard does not show how to open the sandbox it just built")
	}
}

// TestInstallBlocksDoNotShareSignals is the one that matters. Datastar signals are
// global and several projects' key blocks can be revealed at once, so two builders
// on one page must not share a signal — otherwise naming a sandbox under one
// workspace silently rewrites another workspace's install command.
func TestInstallBlocksDoNotShareSignals(t *testing.T) {
	render := func(teamID, secret string) string {
		var buf bytes.Buffer
		vm := KeyVM{TeamID: teamID, Revealed: true, Secret: secret}
		if err := KeyBlock(vm).Render(context.Background(), &buf); err != nil {
			t.Fatalf("render %s: %v", teamID, err)
		}
		return buf.String()
	}
	// Real ids: UUID dashes are not valid in a JS identifier, so the suffix must
	// strip them rather than emit "$_sbname_6bc552d2-63fa".
	page := render("6bc552d2-63fa-4a2e-b068-f64bc7dfb748", "AAA") +
		render("11111111-2222-3333-4444-555555555555", "BBB")

	for _, sig := range []string{"_mode", "_agent", "_sbname", "_optcopy", "_optshared", "_optrec", "_copiedInstall"} {
		// The bare, unsuffixed name must never appear as a bound signal: that is
		// exactly the collision this design exists to prevent.
		if strings.Contains(page, `data-bind="`+sig+`"`) {
			t.Errorf("signal %q is bound unsuffixed, so two projects would share it", sig)
		}
	}
	a := projectBuilder("6bc552d2-63fa-4a2e-b068-f64bc7dfb748", "AAA")
	c := projectBuilder("11111111-2222-3333-4444-555555555555", "BBB")
	if a.sig("_sbname") == c.sig("_sbname") {
		t.Fatal("two workspaces resolved to the same signal namespace")
	}
	if strings.ContainsAny(a.Suffix, "-{}$ .") {
		t.Errorf("suffix %q is not a safe JS identifier fragment", a.Suffix)
	}
	for _, want := range []string{a.sig("_sbname"), c.sig("_sbname")} {
		if !strings.Contains(page, `data-bind="`+want+`"`) {
			t.Errorf("expected namespaced signal %q on the page", want)
		}
	}
	// Each block must also carry its own token, not the other's.
	if !strings.Contains(page, "AAA") || !strings.Contains(page, "BBB") {
		t.Error("each block should embed its own workspace token")
	}
}

// TestTokenAlphabetIsShellSafe guards the invariant installBuilder.base relies on.
// The token is interpolated into a double-quoted shell word without escaping,
// which is only safe while tokens cannot contain a quote, a backslash, or a shell
// metacharacter. That is a property of tenant.GenerateToken, not of this package,
// so if the token format ever changes this test fails and points at the line that
// assumed otherwise — rather than shipping a command that breaks its own quoting.
func TestTokenAlphabetIsShellSafe(t *testing.T) {
	safe := regexp.MustCompile(`^[0-9a-f]{64}$`)
	for i := 0; i < 32; i++ {
		tok, _, err := tenant.GenerateToken()
		if err != nil {
			t.Fatalf("GenerateToken: %v", err)
		}
		if !safe.MatchString(tok) {
			t.Fatalf("token %q is no longer plain hex — installBuilder.base must now shell-escape it", tok)
		}
	}
}
