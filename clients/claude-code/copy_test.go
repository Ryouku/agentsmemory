package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkipCopy(t *testing.T) {
	// Runtime state is excluded — this is what keeps a ~800 MB global config from
	// landing in a sandbox — while configuration and credentials come through.
	excluded := []struct {
		rel   string
		isDir bool
	}{
		{"projects", true},
		{"sessions", true},
		{"cache", true},
		{"plugins/repos/x/node_modules", true},
		{"logs_2.sqlite", false},
		{"logs_2.sqlite-wal", false},
		{"history.jsonl", false},
		{"installation_id", false},
		{"settings.json.bak", false},
		{"settings.json.bak.1782687959", false},
		{"bin/fd", false},
	}
	for _, tc := range excluded {
		if !skipCopy(tc.rel, tc.isDir) {
			t.Errorf("skipCopy(%q) = false, want it excluded from a config copy", tc.rel)
		}
	}

	kept := []struct {
		rel   string
		isDir bool
	}{
		{"auth.json", false},
		{"config.toml", false},
		{"settings.json", false},
		{"models-store.json", false},
		{".claude.json", false},
		{"plugins", true},
		{"plugins/marketplace.json", false},
		{"skills/my-skill/SKILL.md", false},
		{"extensions/agentsmemory.ts", false},
	}
	for _, tc := range kept {
		if skipCopy(tc.rel, tc.isDir) {
			t.Errorf("skipCopy(%q) = true, but it is configuration worth inheriting", tc.rel)
		}
	}
}

// TestCopyConfigTree covers the three properties the copy has to get right:
// credentials keep their private mode, existing files in the target are never
// clobbered, and excluded bulk stays behind.
func TestCopyConfigTree(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()

	write := func(dir, rel string, mode os.FileMode, body string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}

	write(src, "auth.json", 0o600, `{"token":"secret"}`)
	write(src, "settings.json", 0o644, `{"from":"global"}`)
	write(src, "plugins/marketplace.json", 0o644, `{"plugins":[]}`)
	write(src, "sessions/huge.jsonl", 0o644, strings.Repeat("x", 4096))
	write(src, "logs_2.sqlite", 0o644, "binary")
	// A file the sandbox already has must survive untouched.
	write(dst, "settings.json", 0o644, `{"from":"sandbox"}`)

	stats, err := copyConfigTree(src, dst)
	if err != nil {
		t.Fatalf("copyConfigTree: %v", err)
	}

	// Credentials arrive, and arrive private.
	info, err := os.Stat(filepath.Join(dst, "auth.json"))
	if err != nil {
		t.Fatalf("auth.json not copied: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("auth.json mode = %#o, want 0600 — a copied credential must not widen", perm)
	}

	if _, err := os.Stat(filepath.Join(dst, "plugins/marketplace.json")); err != nil {
		t.Errorf("plugins were not inherited: %v", err)
	}

	// Bulk stays behind.
	for _, rel := range []string{"sessions/huge.jsonl", "logs_2.sqlite"} {
		if _, err := os.Stat(filepath.Join(dst, rel)); err == nil {
			t.Errorf("%s was copied; runtime state must not follow into a sandbox", rel)
		}
	}

	// The sandbox's own file wins.
	body, err := os.ReadFile(filepath.Join(dst, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"from":"sandbox"}` {
		t.Errorf("settings.json = %s, want the sandbox's own copy preserved", body)
	}
	if stats.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1 (the pre-existing settings.json)", stats.Skipped)
	}
	if stats.Files != 2 {
		t.Errorf("Files = %d, want 2 (auth.json + marketplace.json)", stats.Files)
	}
}

// TestCopyConfigTreeSymlink checks a linked plugin dir stays a link instead of
// being duplicated into the sandbox.
func TestCopyConfigTreeSymlink(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	if err := os.Symlink("/opt/some/plugin", filepath.Join(src, "linked-plugin")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := copyConfigTree(src, dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.Readlink(filepath.Join(dst, "linked-plugin"))
	if err != nil {
		t.Fatalf("link not recreated: %v", err)
	}
	if got != "/opt/some/plugin" {
		t.Errorf("link target = %q, want /opt/some/plugin", got)
	}
}

// TestSeedFromGlobalRejectsSelfCopy pins the guard: --copy against the global dir
// itself is a user error (they meant --sandbox), not a silent no-op.
func TestSeedFromGlobalRejectsSelfCopy(t *testing.T) {
	inst, _, _ := newTestInstaller(t, false)
	inst.copyGlobal = true
	inst.targetDir = claudeKit.globalConfigDir(homeDir())

	err := inst.seedFromGlobal()
	if err == nil {
		t.Fatal("seedFromGlobal into the global dir returned nil, want an error")
	}
	if !strings.Contains(err.Error(), "--sandbox") {
		t.Errorf("error %q does not point at the fix (--sandbox / --config-dir)", err)
	}
}

// TestForeignHookPredicate covers what --copy makes possible: a settings.json
// inherited from another config dir carries that dir's Stop hook, which would
// fire the checkpoint twice. Ours is kept, theirs retired, a user's own hook
// never matched.
func TestForeignHookPredicate(t *testing.T) {
	mine := "bash /Users/x/.sandboxes/acme/" + hookFile
	obsolete := foreignHookPredicate(mine)

	if obsolete(mine) {
		t.Error("the hook this install registers was marked obsolete")
	}
	for _, cmd := range []string{
		"bash /Users/x/.claude/" + hookFile,       // inherited by --copy
		"bash /Users/x/.claude/hooks/" + hookFile, // pre-relocation layout
	} {
		if !obsolete(cmd) {
			t.Errorf("%q should be retired: it runs our hook from another config dir", cmd)
		}
	}
	if obsolete("bash /Users/x/.claude/my-own-hook.sh") {
		t.Error("a hook the user wrote must never be dropped")
	}
}
