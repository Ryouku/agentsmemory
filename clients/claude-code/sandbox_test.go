package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPlanRun pins the `run <name>` resolution order: an existing sandbox always
// wins, a known agent name is the fallback when no sandbox exists, and anything
// else keeps the old install hint rather than exec'ing a surprise binary.
func TestPlanRun(t *testing.T) {
	cases := []struct {
		name          string
		arg           string
		sandboxExists bool
		wantBin       string
		wantConfigDir string
		wantErr       bool
	}{
		{"sandbox wins", "acme", true, "", sandboxDir("acme"), false},
		{"sandbox named claude still wins", "claude", true, "", sandboxDir("claude"), false},
		{"agent fallback", "claude", false, "claude", "", false},
		{"other allowlisted agent", "codex", false, "codex", "", false},
		{"unknown name keeps install hint", "acme", false, "", "", true},
		{"invalid name rejected", "../escape", false, "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := planRun(claudeKit, tc.arg, tc.sandboxExists)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("planRun(%q, %v) = %+v, want an error", tc.arg, tc.sandboxExists, plan)
				}
				return
			}
			if err != nil {
				t.Fatalf("planRun(%q, %v) unexpected error: %v", tc.arg, tc.sandboxExists, err)
			}
			if plan.bin != tc.wantBin || plan.configDir != tc.wantConfigDir {
				t.Errorf("planRun(%q, %v) = (bin=%q configDir=%q), want (bin=%q configDir=%q)",
					tc.arg, tc.sandboxExists, plan.bin, plan.configDir, tc.wantBin, tc.wantConfigDir)
			}
		})
	}
}

// TestResolveAgentBin checks that the Claude override still steers `run claude`
// (and the global `wrap`), while a non-Claude agent is exec'd under its own name.
func TestResolveAgentBin(t *testing.T) {
	t.Setenv("AIAGENTMEMORY_CLAUDE_BIN", "my-claude")

	for _, name := range []string{"", "claude"} {
		got, err := resolveAgentBin(claudeKit, name)
		if err != nil {
			t.Fatalf("resolveAgentBin(%q): %v", name, err)
		}
		if got != "my-claude" {
			t.Errorf("resolveAgentBin(%q) = %q, want my-claude", name, got)
		}
	}

	got, err := resolveAgentBin(claudeKit, "codex")
	if err != nil {
		t.Fatalf("resolveAgentBin(codex): %v", err)
	}
	if got != "codex" {
		t.Errorf("resolveAgentBin(codex) = %q, want codex", got)
	}
}

// TestPlanRunCodexPinsCodexHome checks a sandbox launch pins the selected agent's
// own config variable — pinning CLAUDE_CONFIG_DIR for a codex launch would leave
// codex reading the global ~/.codex and quietly ignore the sandbox.
func TestPlanRunCodexPinsCodexHome(t *testing.T) {
	plan, err := planRun(codexKit, "acme", true)
	if err != nil {
		t.Fatal(err)
	}
	if plan.configEnv != "CODEX_HOME" || plan.configDir != sandboxDir("acme") {
		t.Errorf("planRun(codex, acme) = %+v, want CODEX_HOME=%s", plan, sandboxDir("acme"))
	}

	claude, err := planRun(claudeKit, "acme", true)
	if err != nil {
		t.Fatal(err)
	}
	if claude.configEnv != "CLAUDE_CONFIG_DIR" {
		t.Errorf("planRun(claude, acme).configEnv = %q, want CLAUDE_CONFIG_DIR", claude.configEnv)
	}
}

// TestTakeAgentFlag pins the hand-parsed --agent: it is honoured only in the
// leading position, because everything after the sandbox name belongs to the
// agent we are about to exec.
func TestTakeAgentFlag(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantKit  string
		wantRest []string
		wantErr  bool
	}{
		{"absent defaults to claude", []string{"acme", "-p", "hi"}, agentClaude, []string{"acme", "-p", "hi"}, false},
		{"spaced form", []string{"--agent", "codex", "acme"}, agentCodex, []string{"acme"}, false},
		{"equals form", []string{"--agent=codex", "acme"}, agentCodex, []string{"acme"}, false},
		{"not leading is passed through", []string{"acme", "--agent", "codex"}, agentClaude, []string{"acme", "--agent", "codex"}, false},
		{"empty args", nil, agentClaude, nil, false},
		{"missing value", []string{"--agent"}, "", nil, true},
		{"unknown agent", []string{"--agent", "gemini"}, "", nil, true},
		{"both is not launchable", []string{"--agent", "both"}, "", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kit, rest, err := takeAgentFlag(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("takeAgentFlag(%v) = (%v, %v), want an error", tc.args, kit.name, rest)
				}
				return
			}
			if err != nil {
				t.Fatalf("takeAgentFlag(%v): %v", tc.args, err)
			}
			if kit.name != tc.wantKit || !equalStrings(rest, tc.wantRest) {
				t.Errorf("takeAgentFlag(%v) = (%q, %v), want (%q, %v)", tc.args, kit.name, rest, tc.wantKit, tc.wantRest)
			}
		})
	}
}

// TestTokenEnvAndSetEnv covers the codex token hand-off: the file the install
// wrote is read back as KEY=VALUE, and layering it must replace an existing
// variable rather than append a losing duplicate.
func TestTokenEnvAndSetEnv(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, codexTokenFile),
		[]byte("# comment\n\n"+codexTokenEnvVar+"=TOK\nbroken-line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := tokenEnv(dir)
	if len(got) != 1 || got[codexTokenEnvVar] != "TOK" {
		t.Errorf("tokenEnv = %v, want just %s=TOK", got, codexTokenEnvVar)
	}
	if missing := tokenEnv(filepath.Join(dir, "nope")); missing != nil {
		t.Errorf("tokenEnv(missing dir) = %v, want nil", missing)
	}

	env := setEnv([]string{"PATH=/bin", codexTokenEnvVar + "=STALE"}, codexTokenEnvVar, "FRESH")
	if !equalStrings(env, []string{"PATH=/bin", codexTokenEnvVar + "=FRESH"}) {
		t.Errorf("setEnv did not replace the existing entry: %v", env)
	}
	if env := setEnv([]string{"PATH=/bin"}, "NEW", "1"); !equalStrings(env, []string{"PATH=/bin", "NEW=1"}) {
		t.Errorf("setEnv did not append a new entry: %v", env)
	}
}

// TestDirExists covers the three states planRun cares about: a real sandbox dir,
// a plain file squatting on the path, and nothing at all.
func TestDirExists(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		path string
		want bool
	}{
		{dir, true},
		{file, false},
		{filepath.Join(dir, "missing"), false},
	}
	for _, tc := range cases {
		if got := dirExists(tc.path); got != tc.want {
			t.Errorf("dirExists(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
