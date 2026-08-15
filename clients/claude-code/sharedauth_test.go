package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPlanSharedAuth pins which credential files each agent shares — and that a
// credential the global config does not have is skipped, since linking to a
// missing file would leave the sandbox with a dangling link the agent cannot
// read.
func TestPlanSharedAuth(t *testing.T) {
	all := func(string) bool { return true }
	none := func(string) bool { return false }

	links := planSharedAuth(piKit, "/g/.pi/agent", "/s/acme", all)
	if len(links) != 2 {
		t.Fatalf("pi links = %d, want 2 (auth.json + models-store.json)", len(links))
	}
	if links[0].Target != filepath.Join("/g/.pi/agent", "auth.json") {
		t.Errorf("link target = %q, want the global auth.json", links[0].Target)
	}
	if links[0].Link != filepath.Join("/s/acme", "auth.json") {
		t.Errorf("link path = %q, want it inside the sandbox", links[0].Link)
	}

	if got := planSharedAuth(codexKit, "/g/.codex", "/s/acme", all); len(got) != 1 {
		t.Errorf("codex links = %d, want 1 (auth.json)", len(got))
	}
	if got := planSharedAuth(piKit, "/g/.pi/agent", "/s/acme", none); len(got) != 0 {
		t.Errorf("links = %d, want none when the global config has no credentials", len(got))
	}
}

// TestReplaceWithLink covers the three states the target can be in, including the
// one that must never lose data: a real credential file is moved aside, not
// deleted.
func TestReplaceWithLink(t *testing.T) {
	dir := t.TempDir()
	global := filepath.Join(dir, "global-auth.json")
	if err := os.WriteFile(global, []byte(`{"global":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	sandbox := filepath.Join(dir, "sandbox")
	if err := os.MkdirAll(sandbox, 0o755); err != nil {
		t.Fatal(err)
	}
	link := sharedAuthLink{Name: "auth.json", Target: global, Link: filepath.Join(sandbox, "auth.json")}

	// 1. Nothing there yet.
	if err := replaceWithLink(link); err != nil {
		t.Fatalf("fresh link: %v", err)
	}
	if dest, err := os.Readlink(link.Link); err != nil || dest != global {
		t.Fatalf("link = %q, %v; want %q", dest, err, global)
	}

	// 2. Already correct — a re-run must not churn it.
	if err := replaceWithLink(link); err != nil {
		t.Fatalf("idempotent link: %v", err)
	}

	// 3. A real file is backed up, never dropped.
	if err := os.Remove(link.Link); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(link.Link, []byte(`{"sandbox":"own"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := replaceWithLink(link); err != nil {
		t.Fatalf("replace regular file: %v", err)
	}
	if dest, err := os.Readlink(link.Link); err != nil || dest != global {
		t.Errorf("link = %q, %v; want %q", dest, err, global)
	}
	entries, err := os.ReadDir(sandbox)
	if err != nil {
		t.Fatal(err)
	}
	var backedUp bool
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "auth.json.bak.") {
			backedUp = true
		}
	}
	if !backedUp {
		t.Error("the sandbox's own credential was replaced without a backup")
	}
}

// TestBrokenSharedAuth is the safety net for the one risk of the symlink
// approach: an agent that writes credentials atomically replaces the link with a
// regular file and the sandbox silently stops sharing.
func TestBrokenSharedAuth(t *testing.T) {
	dir := t.TempDir()
	global := filepath.Join(dir, "global.json")
	if err := os.WriteFile(global, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "cfg")
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}

	// A dir that never opted in reports nothing.
	if got := brokenSharedAuth(cfg); len(got) != 0 {
		t.Errorf("brokenSharedAuth without a marker = %v, want none", got)
	}

	if err := os.WriteFile(filepath.Join(cfg, sharedAuthMarker), []byte("auth.json\nmodels-store.json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(global, filepath.Join(cfg, "auth.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// Intact link + a file that is simply absent (the agent will recreate it) are
	// both fine.
	if got := brokenSharedAuth(cfg); len(got) != 0 {
		t.Errorf("brokenSharedAuth = %v, want none while the link is intact", got)
	}

	// The agent replaced the link with a real file.
	if err := os.Remove(filepath.Join(cfg, "auth.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "auth.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := brokenSharedAuth(cfg)
	if len(got) != 1 || got[0] != "auth.json" {
		t.Errorf("brokenSharedAuth = %v, want [auth.json]", got)
	}
}

// TestLinkSharedAuthRejectsSelfLink pins the guard: sharing the global config
// with itself would link a file to itself.
func TestLinkSharedAuthRejectsSelfLink(t *testing.T) {
	inst, _, _ := newTestInstaller(t, false)
	inst.sharedAuth = true
	inst.targetDir = claudeKit.globalConfigDir(homeDir())

	err := inst.linkSharedAuth()
	if err == nil {
		t.Fatal("linkSharedAuth into the global dir returned nil, want an error")
	}
	if !strings.Contains(err.Error(), "--sandbox") {
		t.Errorf("error %q does not point at the fix", err)
	}
}

// TestLinkSharedAuthWritesMarker checks the end-to-end install path: the
// credential becomes a link and the marker records it, which is what makes a
// later break detectable.
func TestLinkSharedAuthWritesMarker(t *testing.T) {
	inst, _, dir := newTestInstallerFor(t, piKit, false)
	inst.sharedAuth = true

	// Stand in for the global pi config.
	home := t.TempDir()
	t.Setenv("HOME", home)
	global := piKit.globalConfigDir(home)
	if err := os.MkdirAll(global, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(global, "auth.json"), []byte(`{"providers":17}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := inst.linkSharedAuth(); err != nil {
		t.Fatalf("linkSharedAuth: %v", err)
	}

	dest, err := os.Readlink(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatalf("auth.json is not a link: %v", err)
	}
	if want := filepath.Join(global, "auth.json"); dest != want {
		t.Errorf("link → %q, want %q", dest, want)
	}
	// models-store.json is absent from the stand-in global config, so it must not
	// have been linked into a dangling path.
	if _, err := os.Lstat(filepath.Join(dir, "models-store.json")); err == nil {
		t.Error("linked models-store.json even though the global config has none")
	}
	marker, err := os.ReadFile(filepath.Join(dir, sharedAuthMarker))
	if err != nil {
		t.Fatalf("marker not written: %v", err)
	}
	if strings.TrimSpace(string(marker)) != "auth.json" {
		t.Errorf("marker = %q, want just auth.json", marker)
	}
}
