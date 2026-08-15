package views

import (
	"bytes"
	"context"
	"html"
	"strings"
	"testing"
)

// TestSandboxSpecsAlign holds the invariant the comparison table depends on:
// every agent lists the same spec keys in the same order, so row i of the table
// is spec i of each agent. A reordered or extra key here would silently print a
// value under the wrong heading.
func TestSandboxSpecsAlign(t *testing.T) {
	agents := sandboxAgents()
	if len(agents) == 0 {
		t.Fatal("sandboxAgents() is empty")
	}
	want := sandboxSpecLabels()
	for _, a := range agents {
		if len(a.Specs) != len(want) {
			t.Fatalf("%s has %d specs, want %d (the table renders one row per label)", a.Key, len(a.Specs), len(want))
		}
		for i, s := range a.Specs {
			if s.K != want[i] {
				t.Errorf("%s spec %d is %q, want %q — the comparison table would mislabel it", a.Key, i, s.K, want[i])
			}
		}
	}
}

// TestSandboxesPageCoversEveryAgent renders the page an anonymous visitor gets
// and checks each agent arrives complete: name, install command, launch command
// and the config variable that makes the sandbox isolated.
func TestSandboxesPageCoversEveryAgent(t *testing.T) {
	page := renderSandboxes(t)

	for _, a := range sandboxAgents() {
		if !strings.Contains(page, a.Name) {
			t.Errorf("page does not name the %s agent", a.Key)
		}
		if !strings.Contains(page, esc(a.Install)) {
			t.Errorf("page is missing the %s install command %q", a.Key, a.Install)
		}
		if !strings.Contains(page, esc(a.Launch)) {
			t.Errorf("page is missing the %s launch command %q", a.Key, a.Launch)
		}
	}
	for _, env := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "PI_CODING_AGENT_DIR"} {
		if !strings.Contains(page, env) {
			t.Errorf("page does not mention %s, which is what pins a sandbox", env)
		}
	}
}

// TestSandboxesPageDocumentsProjectLaunch guards the #project band. The cards
// alone cannot convey the two rules a reader would otherwise get wrong — which
// layer wins, and that a missing sandbox is an error rather than a silent global
// launch — so the notes are asserted alongside them.
func TestSandboxesPageDocumentsProjectLaunch(t *testing.T) {
	page := renderSandboxes(t)

	if !strings.Contains(page, `id="project"`) {
		t.Error("guide is missing the #project band")
	}
	for _, g := range sandboxProject() {
		// esc: templ escapes the apostrophe in "the team's half".
		if !strings.Contains(page, esc(g.Title)) {
			t.Errorf("guide is missing the %q card", g.Title)
		}
		if !strings.Contains(page, esc(g.Cmd)) {
			t.Errorf("guide is missing the command %q", g.Cmd)
		}
	}

	// The split itself: one half is committed, the other never leaves the machine.
	for _, want := range []string{
		".aiagentmemory",
		"~/.sandboxes/agents",
		"Never in the repository",
		"Safe to commit",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("guide does not state %q", want)
		}
	}

	// The precedence chain must be spelled out, top layer to bottom.
	for _, layer := range []string{"--sandbox", "$AIAGENTMEMORY_SANDBOX", "~/.sandboxes/agents", ".aiagentmemory.local"} {
		if !strings.Contains(page, esc(layer)) {
			t.Errorf("precedence chain is missing the %q layer", layer)
		}
	}
	// …and the refusal, which is the behaviour a reader is most likely to assume
	// wrongly.
	if !strings.Contains(page, "never falls back") {
		t.Error("guide does not say load refuses rather than launching unpinned")
	}

	// Both commands appear in the guide's reference list.
	if !strings.Contains(page, esc("aiagentmemory init --sandbox <name> [-- agent flags]")) {
		t.Error("command reference is missing the init row")
	}
	if !strings.Contains(page, esc("aiagentmemory load [-- extra flags]")) {
		t.Error("command reference is missing the load row")
	}
}

// TestSandboxesPageWorksWithoutDatastar guards the no-JavaScript floor: the first
// agent's panel is visible on load, and the comparison table is rendered
// unconditionally, so a visitor whose datastar runtime never arrives still gets
// every agent's essentials rather than a page with two invisible thirds.
func TestSandboxesPageWorksWithoutDatastar(t *testing.T) {
	page := renderSandboxes(t)

	// The first panel's own opening tag must not carry the display:none that
	// pre-hides the others; only that tag is inspected, since the copy buttons
	// inside every panel legitimately hide their "Copied ✓" span the same way.
	first := sandboxAgents()[0]
	open := strings.Index(page, `class="sb-panel"`)
	if open < 0 {
		t.Fatal("no agent panel rendered")
	}
	tag := page[open:]
	tag = tag[:strings.Index(tag, ">")+1]
	if !strings.Contains(tag, esc("$_agent === '"+first.Key+"'")) {
		t.Fatalf("first panel is not %s's: %q", first.Key, tag)
	}
	if strings.Contains(tag, "display:none") {
		t.Errorf("the first agent's panel is hidden on load; nothing would show before datastar boots: %q", tag)
	}
	if !strings.Contains(page, esc(first.Install)) {
		t.Errorf("the first agent's install command %q is not on the page", first.Install)
	}

	// The matrix repeats every agent's facts with no signal gating.
	if !strings.Contains(page, `class="sb-table"`) {
		t.Error("comparison table is missing — it is the no-JavaScript fallback")
	}
	for _, label := range sandboxSpecLabels() {
		if !strings.Contains(page, label) {
			t.Errorf("comparison table is missing the %q row", label)
		}
	}
}

// TestSandboxesCopyButtonsShareOneSignal checks the copy controls use the shared
// _copiedKey signal keyed per button: datastar signals are global to the page, so
// a single boolean would flash "Copied ✓" on every button at once.
func TestSandboxesCopyButtonsShareOneSignal(t *testing.T) {
	page := renderSandboxes(t)

	if !strings.Contains(page, "_copiedKey") {
		t.Fatal("copy buttons do not use the _copiedKey signal")
	}
	for _, a := range sandboxAgents() {
		if !strings.Contains(page, esc("$_copiedKey === '"+a.Key+"-install'")) {
			t.Errorf("%s install copy button is not keyed to its own command", a.Key)
		}
	}
}

// TestLandingLinksSandboxes checks the guide is reachable from the front page —
// an unlinked page is an unfindable one.
func TestLandingLinksSandboxes(t *testing.T) {
	var buf bytes.Buffer
	if err := LandingPage(LandingData{}).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(buf.String(), `href="/sandboxes"`) {
		t.Error("landing page has no link to /sandboxes")
	}
}

// renderSandboxes renders the guide for an anonymous visitor.
func renderSandboxes(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	if err := SandboxesPage(LandingData{}).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	return buf.String()
}

// esc mirrors templ's HTML escaping, so assertions can be written against the
// command a user copies rather than against its escaped form on the wire.
func esc(s string) string { return html.EscapeString(s) }
