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
