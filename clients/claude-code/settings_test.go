package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// readStop is a small test helper that reads settings.json and returns the Stop
// hook array, failing the test on any structural surprise.
func readStop(t *testing.T, path string) []any {
	t.Helper()
	return readHookEvent(t, path, "Stop")
}

// readHookEvent returns the entries registered for one hook event, so a test can
// assert on Stop and SessionStart through the same reader.
func readHookEvent(t *testing.T, path, event string) []any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	hooks, ok := m["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("hooks is %T, want object", m["hooks"])
	}
	entries, ok := hooks[event].([]any)
	if !ok {
		t.Fatalf("%s is %T, want array", event, hooks[event])
	}
	return entries
}

func TestEnsureStopHookFreshFile(t *testing.T) {
	// A brand-new install has no settings.json; ensureStopHook must create it.
	path := filepath.Join(t.TempDir(), "settings.json")
	cmd := "bash /x/hooks/agentsmemory-stop-hook.sh"

	added, err := ensureHook(path, "Stop", cmd, nil)
	if err != nil {
		t.Fatalf("ensureStopHook: %v", err)
	}
	if !added {
		t.Fatal("added = false, want true on a fresh file")
	}
	if stop := readStop(t, path); len(stop) != 1 {
		t.Fatalf("Stop entries = %d, want 1", len(stop))
	}
	if !hookPresent(readStop(t, path), cmd) {
		t.Fatal("hook command not present after install")
	}
}

func TestEnsureStopHookIdempotent(t *testing.T) {
	// Re-running the installer must not duplicate the hook.
	path := filepath.Join(t.TempDir(), "settings.json")
	cmd := "bash /x/hooks/agentsmemory-stop-hook.sh"

	if _, err := ensureHook(path, "Stop", cmd, nil); err != nil {
		t.Fatalf("first ensureStopHook: %v", err)
	}
	added, err := ensureHook(path, "Stop", cmd, nil)
	if err != nil {
		t.Fatalf("second ensureStopHook: %v", err)
	}
	if added {
		t.Fatal("added = true on second run, want false (already present)")
	}
	if stop := readStop(t, path); len(stop) != 1 {
		t.Fatalf("Stop entries = %d, want 1 (no duplicate)", len(stop))
	}
}

func TestEnsureStopHookPreservesExisting(t *testing.T) {
	// Existing settings — including an unrelated Stop hook — must survive, and a
	// timestamped backup of the original must be written.
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	original := []byte(`{
  "model": "claude-opus-4-8",
  "hooks": {
    "Stop": [
      { "hooks": [ { "type": "command", "command": "bash /other/hook.sh" } ] }
    ]
  }
}`)
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := "bash /x/hooks/agentsmemory-stop-hook.sh"
	added, err := ensureHook(path, "Stop", cmd, nil)
	if err != nil {
		t.Fatalf("ensureStopHook: %v", err)
	}
	if !added {
		t.Fatal("added = false, want true")
	}

	stop := readStop(t, path)
	if len(stop) != 2 {
		t.Fatalf("Stop entries = %d, want 2 (existing + ours)", len(stop))
	}
	if !hookPresent(stop, "bash /other/hook.sh") {
		t.Fatal("pre-existing hook was dropped")
	}
	if !hookPresent(stop, cmd) {
		t.Fatal("our hook was not added")
	}

	// The unrelated top-level key must be preserved.
	raw, _ := os.ReadFile(path)
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["model"] != "claude-opus-4-8" {
		t.Fatalf("model = %v, want it preserved", m["model"])
	}

	// A backup of the original bytes must exist.
	backups, _ := filepath.Glob(path + ".bak.*")
	if len(backups) == 0 {
		t.Fatal("no timestamped backup written")
	}
	got, _ := os.ReadFile(backups[0])
	if string(got) != string(original) {
		t.Fatal("backup does not match the original file bytes")
	}
}

func TestEnsureStopHookMalformedRefuses(t *testing.T) {
	// A settings.json we cannot parse must fail loudly and be left untouched,
	// never overwritten.
	path := filepath.Join(t.TempDir(), "settings.json")
	broken := []byte("{ this is not json")
	if err := os.WriteFile(path, broken, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := ensureHook(path, "Stop", "bash /x.sh", nil); err == nil {
		t.Fatal("ensureStopHook accepted malformed JSON, want an error")
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(broken) {
		t.Fatal("malformed settings.json was modified; it must be left untouched")
	}
}

// TestCursorMCPRegistrationPreservesForeignServers is the highest-impact
// assertion in ADR-020.
//
// mcp.json is a file the user shares with every other MCP server they run, and
// this is the first registration path with no CLI between us and it. Every other
// agent's registration goes through `<agent> mcp add`, which merges on our behalf
// and cannot lose anything. Here a careless write silently deletes the user's
// other servers, and they find out when a tool they rely on stops existing.
func TestCursorMCPRegistrationPreservesForeignServers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	prior := `{
  "mcpServers": {
    "someone-elses": {"command": "/usr/local/bin/theirs", "args": ["--flag"]}
  },
  "unrelatedTopLevelKey": {"keep": "me"}
}`
	if err := os.WriteFile(path, []byte(prior), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	entry := map[string]any{"type": "http", "url": "http://localhost:8080/mcp"}
	changed, err := ensureMCPServer(path, "agentsmemory", entry)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if !changed {
		t.Fatal("registering a new server reported no change")
	}

	var got map[string]any
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("the file we wrote does not parse: %v\n%s", err, body)
	}
	servers, _ := got["mcpServers"].(map[string]any)
	if _, ok := servers["someone-elses"]; !ok {
		t.Errorf("the user's own MCP server was lost:\n%s", body)
	}
	if _, ok := servers["agentsmemory"]; !ok {
		t.Errorf("our server was not registered:\n%s", body)
	}
	if _, ok := got["unrelatedTopLevelKey"]; !ok {
		t.Errorf("an unrelated top-level key was dropped:\n%s", body)
	}

	// One backup of the pre-existing file, and re-running writes nothing at all.
	backups, _ := filepath.Glob(path + ".bak.*")
	if len(backups) != 1 {
		t.Errorf("expected exactly 1 backup, got %d", len(backups))
	}
	changed, err = ensureMCPServer(path, "agentsmemory", entry)
	if err != nil {
		t.Fatalf("re-register: %v", err)
	}
	if changed {
		t.Error("re-registering an identical entry rewrote the file")
	}
	if again, _ := filepath.Glob(path + ".bak.*"); len(again) != 1 {
		t.Errorf("a no-op registration left another backup: %d", len(again))
	}
}

// TestCursorMCPRefusesUnparseableJSON: a file we cannot parse is a file we must
// not replace. The same stance ensureHooks takes on settings.json, and it matters
// more here — a hand-edited mcp.json with a trailing comma is common, and
// overwriting it would destroy configuration we never read.
func TestCursorMCPRefusesUnparseableJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	broken := `{"mcpServers": {"theirs": {"command": "x"},}}` // trailing comma
	if err := os.WriteFile(path, []byte(broken), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := ensureMCPServer(path, "agentsmemory", map[string]any{"url": "u"}); err == nil {
		t.Fatal("an unparseable mcp.json was accepted; the next step overwrites it")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(after) != broken {
		t.Errorf("the unparseable file was modified:\n got: %s\nwant: %s", after, broken)
	}
}
