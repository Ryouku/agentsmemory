package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// recordedCall captures one commandRunner invocation so tests can assert the
// exact external command sequence the installer would drive.
type recordedCall struct {
	shell string   // non-empty for a runShell call
	name  string   // program, for a run call
	args  []string // args, for a run call
	env   []string // extra env, for a run call
}

// recordingRunner is a fake commandRunner: it records calls instead of executing
// them, so the whole install flow can be exercised without a Claude CLI present.
type recordingRunner struct{ calls []recordedCall }

func (r *recordingRunner) run(name string, args, env []string) error {
	r.calls = append(r.calls, recordedCall{name: name, args: args, env: env})
	return nil
}

func (r *recordingRunner) runShell(script string) error {
	r.calls = append(r.calls, recordedCall{shell: script})
	return nil
}

// rendered flattens a recorded call to a single comparable string: "SHELL: …"
// for a shell pipeline, or the joined args for a run call.
func (c recordedCall) rendered() string {
	if c.shell != "" {
		return "SHELL: " + c.shell
	}
	return strings.Join(c.args, " ")
}

func renderAll(calls []recordedCall) []string {
	out := make([]string, len(calls))
	for i, c := range calls {
		out[i] = c.rendered()
	}
	return out
}

// newTestInstaller builds a Claude Installer wired to a recording runner and a
// temp config dir, with a fixed token so the MCP step always runs
// non-interactively.
func newTestInstaller(t *testing.T, recommended bool) (*Installer, *recordingRunner, string) {
	t.Helper()
	return newTestInstallerFor(t, claudeKit, recommended)
}

// newTestInstallerFor is newTestInstaller for an explicit agent kit, so the codex
// install path is exercised through exactly the same flow as the Claude one.
func newTestInstallerFor(t *testing.T, kit agentKit, recommended bool) (*Installer, *recordingRunner, string) {
	t.Helper()
	dir := t.TempDir()
	rr := &recordingRunner{}
	inst := &Installer{
		targetDir:   dir,
		kit:         kit,
		agentBin:    kit.bin,
		mcpURL:      defaultMCPURL,
		scope:       "user",
		token:       "TESTTOK",
		recommended: recommended,
		out:         &bytes.Buffer{},
		in:          strings.NewReader(""),
		runner:      rr,
	}
	return inst, rr, dir
}

func TestAssetsEmbedded(t *testing.T) {
	// The shipped assets must be embedded; the retired agentsmemory.md must not be.
	for _, name := range []string{"commands/M.md", "commands/am.md", "commands/load-skill.md", hookAsset, bootstrapAsset, piExtensionAsset} {
		data, err := assets.ReadFile(name)
		if err != nil {
			t.Fatalf("asset %s not embedded: %v", name, err)
		}
		if len(data) == 0 {
			t.Fatalf("asset %s is empty", name)
		}
	}
	if _, err := assets.ReadFile("commands/agentsmemory.md"); err == nil {
		t.Fatal("retired commands/agentsmemory.md is embedded but should not be")
	}
}

func TestInstallCoreWritesAssetsAndRegistersMCP(t *testing.T) {
	inst, rr, dir := newTestInstaller(t, false)
	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}

	// Commands + both hooks must be on disk.
	for _, rel := range []string{"commands/M.md", "commands/am.md", "commands/load-skill.md", hookFile, verifyHookFile, sessionEndHookFile} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("expected %s written: %v", rel, err)
		}
	}

	// Stop hook must be registered pointing at the installed hook.
	wantCmd := "bash " + filepath.Join(dir, hookFile)
	if !hookPresent(readStop(t, filepath.Join(dir, "settings.json")), wantCmd) {
		t.Errorf("Stop hook %q not registered", wantCmd)
	}

	// ...and its SessionStart companion, which is what makes anchor verification
	// automatic rather than a command nobody remembers to run.
	wantVerify := "bash " + filepath.Join(dir, verifyHookFile)
	if !hookPresent(readHookEvent(t, filepath.Join(dir, "settings.json"), "SessionStart"), wantVerify) {
		t.Errorf("SessionStart hook %q not registered", wantVerify)
	}

	// ...and the closing report, which is the only one of the three that sees a
	// whole session.
	wantEnd := "bash " + filepath.Join(dir, sessionEndHookFile)
	if !hookPresent(readHookEvent(t, filepath.Join(dir, "settings.json"), "SessionEnd"), wantEnd) {
		t.Errorf("SessionEnd hook %q not registered", wantEnd)
	}

	// Only the two agentsmemory MCP calls should have run (no extensions).
	want := []string{
		"mcp remove --scope user agentsmemory",
		"mcp add --transport http --scope user agentsmemory " + defaultMCPURL + " --header Authorization: Bearer TESTTOK",
	}
	got := renderAll(rr.calls)
	if !equalStrings(got, want) {
		t.Errorf("command sequence mismatch\n got: %v\nwant: %v", got, want)
	}

	// Every claude call must pin CLAUDE_CONFIG_DIR to the target dir.
	for _, c := range rr.calls {
		if c.shell != "" {
			continue
		}
		if len(c.env) == 0 || c.env[0] != "CLAUDE_CONFIG_DIR="+dir {
			t.Errorf("call %q missing CLAUDE_CONFIG_DIR=%s env, got %v", c.rendered(), dir, c.env)
		}
	}
}

// TestGlobalInstallDoesNotPinConfigDir pins the fix for the silent-no-tools bug:
// a global install must leave the agent's config-dir variable alone. Pinning
// CLAUDE_CONFIG_DIR=~/.claude moves the MCP registry to ~/.claude/.claude.json,
// while a later plain `claude` reads ~/.claude.json and finds nothing.
func TestGlobalInstallDoesNotPinConfigDir(t *testing.T) {
	rr := &recordingRunner{}
	inst := &Installer{
		targetDir: claudeKit.globalConfigDir(homeDir()),
		kit:       claudeKit,
		agentBin:  claudeKit.bin,
		out:       &bytes.Buffer{},
		runner:    rr,
	}
	if err := inst.agent(false, "mcp", "add", "agentsmemory"); err != nil {
		t.Fatalf("agent: %v", err)
	}
	if len(rr.calls) != 1 {
		t.Fatalf("expected 1 recorded call, got %d", len(rr.calls))
	}
	if len(rr.calls[0].env) != 0 {
		t.Errorf("global install pinned %v; it must inherit the environment", rr.calls[0].env)
	}
}

// TestSandboxInstallPinsConfigDir is the other half: a sandbox is not a directory
// the agent looks in on its own, so registration only lands there with the
// variable set — and `aiagentmemory run <name>` exports the same one at launch.
func TestSandboxInstallPinsConfigDir(t *testing.T) {
	rr := &recordingRunner{}
	dir := sandboxDir("acme")
	inst := &Installer{
		targetDir:   dir,
		sandboxName: "acme",
		kit:         claudeKit,
		agentBin:    claudeKit.bin,
		out:         &bytes.Buffer{},
		runner:      rr,
	}
	if err := inst.agent(false, "mcp", "add", "agentsmemory"); err != nil {
		t.Fatalf("agent: %v", err)
	}
	if want := "CLAUDE_CONFIG_DIR=" + dir; len(rr.calls[0].env) == 0 || rr.calls[0].env[0] != want {
		t.Errorf("sandbox install env = %v, want %s", rr.calls[0].env, want)
	}
}

func TestInstallRecommendedSequence(t *testing.T) {
	inst, rr, _ := newTestInstaller(t, true)
	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}

	bin := expandTilde(codebaseMemoryBin)
	want := []string{
		// core: our MCP first
		"mcp remove --scope user agentsmemory",
		"mcp add --transport http --scope user agentsmemory " + defaultMCPURL + " --header Authorization: Bearer TESTTOK",
		// recommended: codebase-memory installer + registration
		"SHELL: " + codebaseMemoryInstall,
		"mcp remove --scope user codebasememory",
		"mcp add --transport stdio --scope user codebasememory -- " + bin,
		// recommended: plugins
		"plugin marketplace add agenticnotetaking/eidos",
		"plugin install eidos@eidos",
		"plugin marketplace add openai/codex-plugin-cc",
		"plugin install codex@openai-codex",
	}
	got := renderAll(rr.calls)
	if !equalStrings(got, want) {
		t.Errorf("recommended sequence mismatch\n got: %v\nwant: %v", got, want)
	}
}

func TestInstallWritesMemoryBootstrap(t *testing.T) {
	// A default install must drop the always-on protocol and wire CLAUDE.md to
	// import it, so the memory-first workflow applies without typing /am.
	inst, _, dir := newTestInstaller(t, false)
	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, bootstrapFile)); err != nil {
		t.Errorf("expected %s written: %v", bootstrapFile, err)
	}
	claudeMd, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	if !strings.Contains(string(claudeMd), memoryImportLine) {
		t.Errorf("CLAUDE.md does not import the bootstrap: %q", claudeMd)
	}
}

func TestResolveInstallTarget(t *testing.T) {
	home := "/home/u"
	global := filepath.Join(home, ".claude")

	// --global cannot be combined with the other target selectors.
	for _, tc := range []struct{ sandbox, claudeDir string }{
		{sandbox: "proj"},
		{claudeDir: "/x"},
	} {
		if _, _, _, err := resolveInstallTarget(claudeKit, true, false, tc.sandbox, tc.claudeDir, home); err == nil {
			t.Errorf("resolveInstallTarget(global, %q, %q) = nil error, want conflict", tc.sandbox, tc.claudeDir)
		}
	}

	// Precedence and the explicit-target flag.
	cases := []struct {
		name         string
		global       bool
		local        bool
		sandbox      string
		claudeDir    string
		wantTarget   string
		wantSandbox  string
		wantExplicit bool
	}{
		{"global flag", true, false, "", "", global, "", true},
		{"sandbox", false, false, "proj", "", sandboxDir("proj"), "proj", true},
		{"claude-dir", false, false, "", "/custom", "/custom", "", true},
		{"bare default", false, false, "", "", global, "", false},
		// --local implies global, and implies it EXPLICITLY: a self-hoster must not
		// be stopped by the interactive global-vs-sandbox prompt.
		{"local implies global", false, true, "", "", global, "", true},
		// ...but only as a default. A named target still wins, so "--local
		// --sandbox proj" is a local server in an isolated config, not an error.
		{"local yields to sandbox", false, true, "proj", "", sandboxDir("proj"), "proj", true},
		{"local yields to claude-dir", false, true, "", "/custom", "/custom", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target, sandbox, explicit, err := resolveInstallTarget(claudeKit, tc.global, tc.local, tc.sandbox, tc.claudeDir, home)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if target != tc.wantTarget || sandbox != tc.wantSandbox || explicit != tc.wantExplicit {
				t.Errorf("got (target=%q sandbox=%q explicit=%v), want (target=%q sandbox=%q explicit=%v)",
					target, sandbox, explicit, tc.wantTarget, tc.wantSandbox, tc.wantExplicit)
			}
		})
	}

	// An invalid sandbox name is rejected here too (defense in depth with the CLI).
	if _, _, _, err := resolveInstallTarget(claudeKit, false, false, "../escape", "", home); err == nil {
		t.Error("resolveInstallTarget accepted an invalid sandbox name, want an error")
	}
}

func TestResolveClaudeBinOverride(t *testing.T) {
	got, err := resolveClaudeBin("my-claude")
	if err != nil {
		t.Fatal(err)
	}
	if got != "my-claude" {
		t.Errorf("resolveClaudeBin(override) = %q, want my-claude", got)
	}
}

func TestValidSandboxName(t *testing.T) {
	valid := []string{"proj", "proj1", "my-project", "team_work"}
	for _, name := range valid {
		if err := validSandboxName(name); err != nil {
			t.Errorf("validSandboxName(%q) = %v, want nil", name, err)
		}
	}
	// Reject traversal, separators, leading-dot hidden names, and control bytes.
	invalid := []string{"", ".", "..", "a/b", "../escape", `a\b`, ".ssh", "a.b", "bad name", "x\x00y"}
	for _, name := range invalid {
		if err := validSandboxName(name); err == nil {
			t.Errorf("validSandboxName(%q) = nil, want an error", name)
		}
	}
}

func TestPromptInstallModeSandbox(t *testing.T) {
	// A typed, valid name switches the install to that sandbox.
	inst := &Installer{
		targetDir: filepath.Join(homeDir(), ".claude"),
		out:       &bytes.Buffer{},
		in:        strings.NewReader("myproj\n"),
	}
	inst.promptInstallMode()
	if inst.sandboxName != "myproj" {
		t.Errorf("sandboxName = %q, want myproj", inst.sandboxName)
	}
	if want := sandboxDir("myproj"); inst.targetDir != want {
		t.Errorf("targetDir = %q, want %q", inst.targetDir, want)
	}
}

func TestPromptInstallModeGlobalOnBlank(t *testing.T) {
	// Pressing Enter (blank) keeps the global default untouched.
	global := filepath.Join(homeDir(), ".claude")
	inst := &Installer{targetDir: global, out: &bytes.Buffer{}, in: strings.NewReader("\n")}
	inst.promptInstallMode()
	if inst.sandboxName != "" {
		t.Errorf("sandboxName = %q, want empty", inst.sandboxName)
	}
	if inst.targetDir != global {
		t.Errorf("targetDir = %q, want %q (unchanged)", inst.targetDir, global)
	}
}

func TestPromptInstallModeSkipped(t *testing.T) {
	// An explicit --sandbox/--claude-dir (explicitTarget) or --yes must skip the
	// prompt entirely: even a name waiting on stdin is ignored, so the target set
	// by the flags is preserved.
	for _, tc := range []struct {
		name string
		inst *Installer
	}{
		{"explicitTarget", &Installer{targetDir: "/x", explicitTarget: true, out: &bytes.Buffer{}, in: strings.NewReader("myproj\n")}},
		{"yes", &Installer{targetDir: "/x", yes: true, out: &bytes.Buffer{}, in: strings.NewReader("myproj\n")}},
		{"dryRun", &Installer{targetDir: "/x", dryRun: true, out: &bytes.Buffer{}, in: strings.NewReader("myproj\n")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.inst.promptInstallMode()
			if tc.inst.sandboxName != "" || tc.inst.targetDir != "/x" {
				t.Errorf("prompt not skipped: sandbox=%q target=%q", tc.inst.sandboxName, tc.inst.targetDir)
			}
		})
	}
}

func TestPromptInstallModeInvalidThenEOF(t *testing.T) {
	// An invalid name is rejected; with no more input (EOF) the loop must not spin
	// forever — it falls back to the global default rather than hanging.
	global := filepath.Join(homeDir(), ".claude")
	var out bytes.Buffer
	inst := &Installer{targetDir: global, out: &out, in: strings.NewReader("bad name")}
	inst.promptInstallMode()
	if inst.sandboxName != "" || inst.targetDir != global {
		t.Errorf("expected global fallback, got sandbox=%q target=%q", inst.sandboxName, inst.targetDir)
	}
	if !strings.Contains(out.String(), "invalid sandbox name") {
		t.Errorf("expected an invalid-name message, got %q", out.String())
	}
}

func TestPromptModeThenTokenShareReader(t *testing.T) {
	// The mode prompt and the token prompt read from ONE stream: line 1 picks the
	// sandbox, line 2 is consumed as the token. A shared bufio.Reader is what makes
	// this work — a second reader would drop the buffered token line.
	inst := &Installer{
		targetDir: filepath.Join(homeDir(), ".claude"),
		out:       &bytes.Buffer{},
		in:        strings.NewReader("myproj\nTOKEN123\n"),
	}
	inst.promptInstallMode()
	if inst.sandboxName != "myproj" {
		t.Fatalf("sandboxName = %q, want myproj", inst.sandboxName)
	}
	if got := inst.resolveToken(); got != "TOKEN123" {
		t.Errorf("resolveToken() = %q, want TOKEN123 (reader not shared?)", got)
	}
}

func TestDryRunnerRedactsToken(t *testing.T) {
	// --dry-run must never echo a bearer token to stdout or a captured log.
	var buf bytes.Buffer
	d := dryRunner{out: &buf}
	if err := d.run("claude",
		[]string{"mcp", "add", "--header", "Authorization: Bearer SUPERSECRET"},
		[]string{"CLAUDE_CONFIG_DIR=/x"}); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if strings.Contains(got, "SUPERSECRET") {
		t.Errorf("dry-run output leaked the token: %q", got)
	}
	if !strings.Contains(got, "Authorization: Bearer ***") {
		t.Errorf("expected a redacted header, got %q", got)
	}
}

// TestInstallCodexCore covers the codex layout end to end: the same command
// markdown lands in prompts/ instead of commands/, the Stop hook registers in
// hooks.json instead of settings.json, AGENTS.md carries the protocol inlined
// (there is no @import on codex), and the MCP is registered with
// --bearer-token-env-var since `codex mcp add` has no static-header flag.
func TestInstallCodexCore(t *testing.T) {
	inst, rr, dir := newTestInstallerFor(t, codexKit, false)
	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}

	for _, rel := range []string{"prompts/M.md", "prompts/am.md", "prompts/load-skill.md", hookFile} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("expected %s written: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "commands")); err == nil {
		t.Error("codex install wrote a commands/ dir; codex reads prompts/")
	}

	wantCmd := "bash " + filepath.Join(dir, hookFile)
	if !hookPresent(readStop(t, filepath.Join(dir, "hooks.json")), wantCmd) {
		t.Errorf("Stop hook %q not registered in hooks.json", wantCmd)
	}

	// AGENTS.md must hold the protocol itself: an @import line would be inert.
	agentsMd, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	if strings.Contains(string(agentsMd), memoryImportLine) {
		t.Errorf("AGENTS.md uses an @import, which codex does not resolve: %q", agentsMd)
	}
	if !strings.Contains(string(agentsMd), "agentsmemory — operating protocol") {
		t.Errorf("AGENTS.md does not carry the inlined protocol: %q", agentsMd)
	}

	// The token is persisted for the wrapper to export, and must not be readable
	// by anyone else — codex reads it from the environment, not from its config.
	tokenPath := filepath.Join(dir, tokenFile)
	info, err := os.Stat(tokenPath)
	if err != nil {
		t.Fatalf("stat %s: %v", tokenFile, err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("%s mode = %#o, want 0600", tokenFile, perm)
	}
	raw, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(raw)), tokenEnvVar+"=TESTTOK"; got != want {
		t.Errorf("token file = %q, want %q", got, want)
	}

	want := []string{
		"mcp remove agentsmemory",
		"mcp add agentsmemory --url " + defaultMCPURL + " --bearer-token-env-var " + tokenEnvVar,
	}
	if got := renderAll(rr.calls); !equalStrings(got, want) {
		t.Errorf("command sequence mismatch\n got: %v\nwant: %v", got, want)
	}

	// Registration must land in the config dir we are installing into.
	for _, c := range rr.calls {
		if len(c.env) == 0 || c.env[0] != "CODEX_HOME="+dir {
			t.Errorf("call %q missing CODEX_HOME=%s env, got %v", c.rendered(), dir, c.env)
		}
	}
}

// TestInstallCodexRecommended pins the codex extension set: codebase-memory only,
// registered in codex's stdio form, with no Claude plugin-marketplace calls.
func TestInstallCodexRecommended(t *testing.T) {
	inst, rr, _ := newTestInstallerFor(t, codexKit, true)
	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}

	want := []string{
		"mcp remove agentsmemory",
		"mcp add agentsmemory --url " + defaultMCPURL + " --bearer-token-env-var " + tokenEnvVar,
		"SHELL: " + codebaseMemoryInstall,
		"mcp remove codebasememory",
		"mcp add codebasememory -- " + expandTilde(codebaseMemoryBin),
	}
	if got := renderAll(rr.calls); !equalStrings(got, want) {
		t.Errorf("recommended sequence mismatch\n got: %v\nwant: %v", got, want)
	}
}

// TestResolveInstallTargetCodex checks the global default follows the agent:
// ~/.codex, not ~/.claude. A sandbox stays one shared dir — the two agents never
// collide on a filename — so `--agent both --sandbox x` yields a single config.
func TestResolveInstallTargetCodex(t *testing.T) {
	home := "/home/u"
	target, _, _, err := resolveInstallTarget(codexKit, true, false, "", "", home)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".codex"); target != want {
		t.Errorf("codex global target = %q, want %q", target, want)
	}

	target, sandbox, _, err := resolveInstallTarget(codexKit, false, false, "proj", "", home)
	if err != nil {
		t.Fatal(err)
	}
	if target != sandboxDir("proj") || sandbox != "proj" {
		t.Errorf("codex sandbox target = (%q, %q), want (%q, proj)", target, sandbox, sandboxDir("proj"))
	}
}

// TestInstallPiCore covers the pi layout end to end. pi is the agent with no MCP
// client and no hooks, so the install must land the bridge extension, persist the
// endpoint alongside the token for it to read, and drive no agent CLI at all —
// there is no `pi mcp add` to call.
func TestInstallPiCore(t *testing.T) {
	inst, rr, dir := newTestInstallerFor(t, piKit, false)
	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}

	for _, rel := range []string{"prompts/M.md", "prompts/am.md", "prompts/load-skill.md", piExtensionAsset} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("expected %s written: %v", rel, err)
		}
	}

	// No hook script and no hook JSON: pi has neither, and a stray .sh would only
	// suggest a gate that never fires.
	if _, err := os.Stat(filepath.Join(dir, hookFile)); err == nil {
		t.Error("pi install wrote a Stop-hook script; pi has no hook system")
	}
	for _, name := range []string{"settings.json", "hooks.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			t.Errorf("pi install wrote %s; pi registers no hooks", name)
		}
	}

	// AGENTS.md must hold the protocol itself — pi resolves no @import either.
	agentsMd, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	if strings.Contains(string(agentsMd), memoryImportLine) {
		t.Errorf("AGENTS.md uses an @import, which pi does not resolve: %q", agentsMd)
	}
	if !strings.Contains(string(agentsMd), "agentsmemory — operating protocol") {
		t.Errorf("AGENTS.md does not carry the inlined protocol: %q", agentsMd)
	}

	// The extension reads both the token and the endpoint from the environment,
	// so both are persisted, and only the owner may read them.
	tokenPath := filepath.Join(dir, tokenFile)
	info, err := os.Stat(tokenPath)
	if err != nil {
		t.Fatalf("stat %s: %v", tokenFile, err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("%s mode = %#o, want 0600", tokenFile, perm)
	}
	raw, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	want := tokenEnvVar + "=TESTTOK\n" + mcpURLEnvVar + "=" + defaultMCPURL + "\n"
	if string(raw) != want {
		t.Errorf("token file = %q, want %q", raw, want)
	}

	if got := renderAll(rr.calls); len(got) != 0 {
		t.Errorf("pi install ran agent CLI commands %v, want none (pi has no mcp subcommand)", got)
	}
}

// TestInstallPiRecommended pins that --recommended adds nothing for pi: the
// codebase-memory MCP is stdio and the eidos/codex plugins are Claude
// marketplaces, so neither has anything to attach to.
func TestInstallPiRecommended(t *testing.T) {
	inst, rr, _ := newTestInstallerFor(t, piKit, true)
	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}
	if got := renderAll(rr.calls); len(got) != 0 {
		t.Errorf("pi --recommended ran %v, want no commands", got)
	}
}

// TestPiGlobalConfigDirNested checks the one structural difference in pi's kit:
// its default config dir is two levels deep, so globalDir carries a separator.
func TestPiGlobalConfigDirNested(t *testing.T) {
	home := "/home/u"
	if got, want := piKit.globalConfigDir(home), filepath.Join(home, ".pi", "agent"); got != want {
		t.Errorf("piKit.globalConfigDir = %q, want %q", got, want)
	}
}

// TestInstallMigratesLegacyHookDir covers the upgrade path for a config dir
// created before the hook was relocated: the old hooks/ directory must be gone
// (pi halts its launch on one, and sandboxes are shared), the script must live
// flat in the config dir, and the stale Stop entry pointing at the deleted file
// must be pruned rather than left to fail on every stop.
func TestInstallMigratesLegacyHookDir(t *testing.T) {
	inst, _, dir := newTestInstaller(t, false)

	legacy := filepath.Join(dir, legacyHookRel)
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("#!/usr/bin/env bash\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	legacyCmd := "bash " + legacy
	if _, err := ensureHook(filepath.Join(dir, "settings.json"), "Stop", legacyCmd, nil); err != nil {
		t.Fatal(err)
	}

	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "hooks")); err == nil {
		t.Error("hooks/ still exists after install; pi halts its launch on it")
	}
	if _, err := os.Stat(filepath.Join(dir, hookFile)); err != nil {
		t.Errorf("relocated hook not written: %v", err)
	}

	stop := readStop(t, filepath.Join(dir, "settings.json"))
	if hookPresent(stop, legacyCmd) {
		t.Error("the stale Stop entry survived; it would run a deleted file on every stop")
	}
	if want := "bash " + filepath.Join(dir, hookFile); !hookPresent(stop, want) {
		t.Errorf("relocated Stop hook %q not registered", want)
	}
}

// TestLegacyHookDirKeptWhenNotOnlyOurs guards the destructive edge: a hooks/
// directory holding the user's own script keeps that script (and the directory).
// Their files outweigh our warning.
func TestLegacyHookDirKeptWhenNotOnlyOurs(t *testing.T) {
	inst, _, dir := newTestInstaller(t, false)

	hooksDir := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{filepath.Base(legacyHookRel), "user-own-hook.sh"} {
		if err := os.WriteFile(filepath.Join(hooksDir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(hooksDir, "user-own-hook.sh")); err != nil {
		t.Errorf("the user's own hook was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, legacyHookRel)); err == nil {
		t.Error("our legacy hook script should still have been removed")
	}
}

func TestResolveAgentKits(t *testing.T) {
	// No --agent must keep the pre-codex behaviour: Claude, nothing else.
	for _, name := range []string{"", "claude", "CLAUDE"} {
		kits, err := resolveAgentKits(name)
		if err != nil {
			t.Fatalf("resolveAgentKits(%q): %v", name, err)
		}
		if len(kits) != 1 || kits[0].name != agentClaude {
			t.Errorf("resolveAgentKits(%q) = %+v, want just claude", name, kits)
		}
	}
	kits, err := resolveAgentKits("both")
	if err != nil {
		t.Fatal(err)
	}
	// "both" predates pi and must keep meaning exactly Claude + codex, so an
	// existing script never grows a third install target behind the user's back.
	if len(kits) != 2 || kits[0].name != agentClaude || kits[1].name != agentCodex {
		t.Errorf("resolveAgentKits(both) = %+v, want [claude codex]", kits)
	}

	kits, err = resolveAgentKits("pi")
	if err != nil {
		t.Fatal(err)
	}
	if len(kits) != 1 || kits[0].name != agentPi {
		t.Errorf("resolveAgentKits(pi) = %+v, want just pi", kits)
	}

	kits, err = resolveAgentKits("all")
	if err != nil {
		t.Fatal(err)
	}
	if len(kits) != 3 || kits[0].name != agentClaude || kits[1].name != agentCodex || kits[2].name != agentPi {
		t.Errorf("resolveAgentKits(all) = %+v, want [claude codex pi]", kits)
	}

	if _, err := resolveAgentKits("gemini"); err == nil {
		t.Error("resolveAgentKits(gemini) = nil error, want a rejection")
	}
	if _, err := resolveAgentKit("both"); err == nil {
		t.Error("resolveAgentKit(both) = nil error, want a rejection (run launches one agent)")
	}
}

// equalStrings reports whether two string slices are element-wise equal.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestWingReachesEveryClientOrSaysWhyNot pins the parity --wing promises. It is
// a promise about the CONNECTION — every call carries the wing, so a write lands
// in the right project even when the agent names none — and a promise silently
// unkept is worse than one refused: the memories still land, just in the wrong
// wing. Claude carries it as a `mcp add --header`, pi as an env var its bridge
// turns back into a header, and the two that cannot carry it must say so.
func TestWingReachesEveryClientOrSaysWhyNot(t *testing.T) {
	const wing = "wing_acme"

	t.Run("claude sends the header", func(t *testing.T) {
		inst, rr, _ := newTestInstallerFor(t, claudeKit, false)
		inst.wing = wing
		if err := inst.registerAgentsMemoryMCP(); err != nil {
			t.Fatalf("register: %v", err)
		}
		var sawHeader bool
		for _, call := range rr.calls {
			for i, a := range call.args {
				if a == "--header" && i+1 < len(call.args) && strings.Contains(call.args[i+1], wingHeader+": "+wing) {
					sawHeader = true
				}
			}
		}
		if !sawHeader {
			t.Fatalf("claude registration must pass %s; calls were %+v", wingHeader, rr.calls)
		}
	})

	t.Run("pi persists it for its bridge", func(t *testing.T) {
		inst, _, dir := newTestInstallerFor(t, piKit, false)
		inst.wing = wing
		if err := inst.registerAgentsMemoryMCP(); err != nil {
			t.Fatalf("register: %v", err)
		}
		env, err := os.ReadFile(inst.tokenPath())
		if err != nil {
			t.Fatalf("read pi env: %v", err)
		}
		if !strings.Contains(string(env), wingEnvVar+"="+wing) {
			t.Fatalf("pi env must carry %s; got %q", wingEnvVar, env)
		}
		// The extension is what turns that variable into a header, so the asset
		// installed beside it must actually read one and send the other.
		ext, err := os.ReadFile(filepath.Join(dir, piExtensionAsset))
		if err != nil {
			t.Fatalf("read pi extension: %v", err)
		}
		for _, want := range []string{wingEnvVar, strings.ToLower(wingHeader)} {
			if !strings.Contains(string(ext), want) {
				t.Errorf("pi bridge must reference %q to keep the wing promise", want)
			}
		}
	})

	t.Run("codex says it cannot", func(t *testing.T) {
		inst, _, _ := newTestInstallerFor(t, codexKit, false)
		out := &bytes.Buffer{}
		inst.out = out
		inst.wing = wing
		if err := inst.registerAgentsMemoryMCP(); err != nil {
			t.Fatalf("register: %v", err)
		}
		if !strings.Contains(out.String(), "cannot ride this connection") {
			t.Fatalf("codex install must warn that the wing is dropped; got %q", out.String())
		}
	})
}
