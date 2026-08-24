package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcptest"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
)

func TestMineClaudeCrossesTheProductionMCPPath(t *testing.T) {
	harness := mcptest.New(t)
	root := t.TempDir()
	project := filepath.Join(root, "-work-acme")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "session-123.jsonl"), []byte(mineFixture), 0o600); err != nil {
		t.Fatal(err)
	}

	command := mineClaudeCommand()
	var out bytes.Buffer
	command.Writer = &out
	command.ErrWriter = &out
	err := command.Run(t.Context(), []string{
		"mine-claude",
		"--dir", root,
		"--wing", "wing_client_path",
		"--room", "sessions",
		"--min-chars", "1",
		"--limit", "0",
		"--mcp-url", harness.Endpoint(),
	})
	if err != nil {
		t.Fatalf("mine-claude: %v\n%s", err, out.String())
	}

	drawers, err := harness.Drawers.List(t.Context(), mcptest.TeamID, "wing_client_path", "sessions", 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(drawers) == 0 {
		t.Fatalf("command reported success but production am_mine stored nothing:\n%s", out.String())
	}
	joined := ""
	for _, drawer := range drawers {
		joined += drawer.Content
	}
	for _, want := range []string{"health check races", "gate the check"} {
		if !strings.Contains(joined, want) {
			t.Errorf("mined content lost %q: %s", want, joined)
		}
	}
	if strings.Contains(joined, "kubectl get pods") {
		t.Errorf("tool traffic reached the production corpus: %s", joined)
	}
}

func TestVerifyCrossesProductionListAndMarkHandlers(t *testing.T) {
	harness := mcptest.New(t)
	const wing = "wing_verify_path"
	filed, err := harness.Drawers.Add(t.Context(), mcptest.TeamID, palace.AddInput{
		Wing: wing, Room: "decisions", Content: "Target is intentionally anchored.", SourceFile: "decision.md",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(filed.Drawers) != 1 {
		t.Fatalf("filed drawers = %d, want 1", len(filed.Drawers))
	}
	if _, err := harness.Drawers.AddAnchors(t.Context(), mcptest.TeamID, filed.Drawers[0].ID, []palace.AnchorInput{
		{Path: "target.go", Snippet: "func Target() {}"},
	}); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "target.go"), []byte("func Target() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := verifyCommand()
	var out bytes.Buffer
	command.Writer = &out
	command.ErrWriter = &out
	err = command.Run(t.Context(), []string{
		"verify",
		"--root", root,
		"--wing", wing,
		"--mcp-url", harness.Endpoint(),
	})
	if err != nil {
		t.Fatalf("verify: %v\n%s", err, out.String())
	}

	anchors, err := harness.Drawers.ListAnchors(t.Context(), mcptest.TeamID, palace.AnchorFilter{Wing: wing})
	if err != nil {
		t.Fatal(err)
	}
	if len(anchors) != 1 || anchors[0].Status != palace.AnchorVerified || anchors[0].Line != 1 {
		t.Fatalf("production anchor verdict = %#v, want verified at line 1\n%s", anchors, out.String())
	}
}
