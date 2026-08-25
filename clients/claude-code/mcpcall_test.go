package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpcli"
	"github.com/mark3labs/mcp-go/mcp"
)

// searchSchema mirrors the am_search input schema closely enough to exercise
// coercion: a required string, a number, and a boolean-shaped extra.
var searchSchema = map[string]any{
	"query":        map[string]any{"type": "string"},
	"limit":        map[string]any{"type": "number"},
	"max_distance": map[string]any{"type": "number"},
	"verbose":      map[string]any{"type": "boolean"},
}

func TestParseToolArgsCoercesToSchemaTypes(t *testing.T) {
	args := mcpcli.ParseArgs([]string{"limit=3", "verbose=true"}, []string{"auth bug"}, searchSchema, "query")

	if got, ok := args["limit"].(float64); !ok || got != 3 {
		t.Errorf("limit = %#v, want float64(3) — a string would make the server fall back to its default", args["limit"])
	}
	if got, ok := args["verbose"].(bool); !ok || got != true {
		t.Errorf("verbose = %#v, want bool(true)", args["verbose"])
	}
	if got := args["query"]; got != "auth bug" {
		t.Errorf("query = %#v, want the bare positional folded under the primary key", got)
	}
}

func TestParseToolArgsLeavesUndeclaredAndUnparsableValuesAsStrings(t *testing.T) {
	// A hex drawer id must survive as a string, and a value that does not parse
	// as its declared type is passed through so the server reports the error.
	props := map[string]any{"id": map[string]any{"type": "string"}, "limit": map[string]any{"type": "number"}}
	args := mcpcli.ParseArgs([]string{"limit=many", "room=decisions"}, []string{"25f83165ab"}, props, "id")

	if got := args["id"]; got != "25f83165ab" {
		t.Errorf("id = %#v, want the untouched string", got)
	}
	if got := args["limit"]; got != "many" {
		t.Errorf("limit = %#v, want the raw string when it does not parse as a number", got)
	}
	if got := args["room"]; got != "decisions" {
		t.Errorf("room = %#v, want a string for a property the schema does not declare", got)
	}
}

func TestParseToolArgsHybridSyntax(t *testing.T) {
	// -a may arrive in the cli-parsed slice or still be sitting in the tail,
	// depending on flag order; both must land, and an explicit -a beats the
	// positional for the same key.
	args := mcpcli.ParseArgs(nil, []string{"positional", "-a", "limit=5", "wing=wing_x", "--arg", "room=diary"}, searchSchema, "query")

	if got := args["query"]; got != "positional" {
		t.Errorf("query = %#v, want the positional", got)
	}
	if got, ok := args["limit"].(float64); !ok || got != 5 {
		t.Errorf("limit = %#v, want float64(5) from the -a in the tail", args["limit"])
	}
	if got := args["wing"]; got != "wing_x" {
		t.Errorf("wing = %#v, want the bare key=value token", got)
	}
	if got := args["room"]; got != "diary" {
		t.Errorf("room = %#v, want the --arg in the tail", got)
	}

	explicit := mcpcli.ParseArgs([]string{"query=explicit"}, []string{"positional"}, searchSchema, "query")
	if got := explicit["query"]; got != "explicit" {
		t.Errorf("query = %#v, want the explicit -a to win over the positional", got)
	}
}

func TestParseToolArgsWithoutPrimaryDropsPositional(t *testing.T) {
	// am_status takes no arguments: a stray positional must not invent one.
	args := mcpcli.ParseArgs(nil, []string{"stray"}, nil, "")
	if len(args) != 0 {
		t.Errorf("args = %#v, want empty for a tool with no required input", args)
	}
}

func TestIsReadOnlyTool(t *testing.T) {
	// Classification comes from the live MCP contract, so non-list verbs such as
	// recall_stats and newly added reads require no client release.
	for _, name := range []string{
		"am_status", "am_search", "am_recall_stats", "am_list_anchors",
	} {
		tool := mcp.NewTool(name, mcp.WithReadOnlyHintAnnotation(true))
		if !mcpcli.IsReadOnly(tool) {
			t.Errorf("isReadOnlyTool(%q) = false, want true", name)
		}
	}

	// Names cannot grant authority: even a list_* tool is refused when the server
	// classifies it as a write.
	for _, name := range []string{
		"am_add_drawer", "am_mine", "am_list_destroy_everything",
	} {
		tool := mcp.NewTool(name, mcp.WithReadOnlyHintAnnotation(false))
		if mcpcli.IsReadOnly(tool) {
			t.Errorf("isReadOnlyTool(%q) = true, want false — the CLI must never write", name)
		}
	}
	if mcpcli.IsReadOnly(mcp.Tool{Name: "am_unclassified"}) {
		t.Error("tool without readOnlyHint was accepted; missing policy must fail closed")
	}
}

func TestPrimaryArgComesFromTheLiveSchema(t *testing.T) {
	search := mcp.Tool{Name: "am_search", InputSchema: mcp.ToolInputSchema{Required: []string{"query"}}}
	if got := mcpcli.PrimaryArg(search); got != "query" {
		t.Errorf("primaryArg(am_search) = %q, want query", got)
	}
	status := mcp.Tool{Name: "am_status"}
	if got := mcpcli.PrimaryArg(status); got != "" {
		t.Errorf("primaryArg(am_status) = %q, want empty", got)
	}
}

func TestFindRemoteToolAcceptsBothNameForms(t *testing.T) {
	tools := []mcp.Tool{{Name: "am_search"}, {Name: "am_status"}}
	if _, ok := mcpcli.FindTool(tools, "search"); !ok {
		t.Error("findRemoteTool(search) = not found, want the am_-prefixed tool")
	}
	if _, ok := mcpcli.FindTool(tools, "nope"); ok {
		t.Error("findRemoteTool(nope) = found, want not found")
	}
}

func TestTokenFromEnvFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, tokenFile)
	// The shape registerCodexMCP/registerPiMCP write: KEY=value lines.
	if err := os.WriteFile(path, []byte("AGENTSMEMORY_TOKEN=sk_live_env\nAGENTSMEMORY_MCP_URL=https://x/mcp\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := tokenFromEnvFile(path); got != "sk_live_env" {
		t.Errorf("tokenFromEnvFile = %q, want sk_live_env", got)
	}
	if got := tokenFromEnvFile(filepath.Join(dir, "absent.env")); got != "" {
		t.Errorf("tokenFromEnvFile(absent) = %q, want empty", got)
	}
}

func TestTokenFromClaudeJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude.json")
	// The shape `claude mcp add --header "Authorization: Bearer <token>"` leaves.
	body := `{"mcpServers":{"other":{"command":"x"},"agentsmemory":{"type":"http","url":"` + defaultMCPURL + `","headers":{"Authorization":"Bearer sk_live_json"}}}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := tokenFromClaudeJSON(path); got != "sk_live_json" {
		t.Errorf("tokenFromClaudeJSON = %q, want sk_live_json (Bearer stripped)", got)
	}

	// A config without our server, and one that is not JSON at all, both mean
	// "no token here" rather than an error that would abort the search.
	noServer := filepath.Join(dir, "none.json")
	if err := os.WriteFile(noServer, []byte(`{"mcpServers":{"serena":{"command":"x"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := tokenFromClaudeJSON(noServer); got != "" {
		t.Errorf("tokenFromClaudeJSON(no agentsmemory) = %q, want empty", got)
	}
	broken := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(broken, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := tokenFromClaudeJSON(broken); got != "" {
		t.Errorf("tokenFromClaudeJSON(malformed) = %q, want empty", got)
	}
}

func TestTokenFromConfigDirPrefersTheEnvFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, tokenFile), []byte("AGENTSMEMORY_TOKEN=sk_env\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"),
		[]byte(`{"mcpServers":{"agentsmemory":{"headers":{"Authorization":"Bearer sk_json"}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// The env file is ours and holds the token verbatim; the Claude config is the
	// agent's, so it is the fallback.
	got, source := tokenFromConfigDir(dir)
	if got != "sk_env" {
		t.Errorf("token = %q, want sk_env from %s", got, tokenFile)
	}
	if !strings.HasSuffix(source, tokenFile) {
		t.Errorf("source = %q, want the %s path", source, tokenFile)
	}

	if got, _ := tokenFromConfigDir(t.TempDir()); got != "" {
		t.Errorf("token = %q, want empty for a dir with no install", got)
	}
}

func TestPrintRemoteToolsListsOnlyCallableTools(t *testing.T) {
	tools := []mcp.Tool{
		mcp.NewTool("am_search", mcp.WithDescription("Semantically recall drawers."), mcp.WithString("query", mcp.Required()), mcp.WithReadOnlyHintAnnotation(true)),
		mcp.NewTool("am_status", mcp.WithDescription("Wake-up call."), mcp.WithReadOnlyHintAnnotation(true)),
		mcp.NewTool("am_add_drawer", mcp.WithDescription("File a verbatim memory."), mcp.WithString("content", mcp.Required()), mcp.WithReadOnlyHintAnnotation(false)),
	}
	var out bytes.Buffer
	if err := mcpcli.PrintTools(&out, tools, false); err != nil {
		t.Fatal(err)
	}
	got := out.String()

	if !strings.Contains(got, "search <query>") {
		t.Errorf("catalogue missing the primary-arg hint for search:\n%s", got)
	}
	if strings.Contains(got, "add_drawer") {
		t.Errorf("catalogue lists a write tool:\n%s", got)
	}
	if !strings.Contains(got, "2 read-only tools (of 3 on the production MCP surface)") {
		t.Errorf("catalogue counts wrong:\n%s", got)
	}
	if !strings.Contains(got, "1 write tools are not callable here") {
		t.Errorf("catalogue does not account for the refused tools:\n%s", got)
	}
}

func TestPrintCallResultPrettyPrintsJSONAndRawEnvelope(t *testing.T) {
	res := &mcp.CallToolResult{Content: []mcp.Content{mcp.TextContent{Type: "text", Text: `{"ok":true,"total_drawers":5785}`}}}

	var pretty bytes.Buffer
	if err := mcpcli.PrintCallResult(&pretty, res, false); err != nil {
		t.Fatal(err)
	}
	// Re-indented, so the payload is readable and still valid JSON for jq.
	if !strings.Contains(pretty.String(), "\"total_drawers\": 5785") {
		t.Errorf("default output not re-indented JSON:\n%s", pretty.String())
	}
	if strings.Contains(pretty.String(), "\"content\"") {
		t.Errorf("default output leaked the MCP envelope:\n%s", pretty.String())
	}

	var raw bytes.Buffer
	if err := mcpcli.PrintCallResult(&raw, res, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw.String(), "\"content\"") {
		t.Errorf("--raw output missing the envelope:\n%s", raw.String())
	}
}

func TestPrintCallResultPassesNonJSONThrough(t *testing.T) {
	res := &mcp.CallToolResult{Content: []mcp.Content{mcp.TextContent{Type: "text", Text: "skill: not found"}}}
	var out bytes.Buffer
	if err := mcpcli.PrintCallResult(&out, res, false); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "skill: not found" {
		t.Errorf("output = %q, want the text verbatim", out.String())
	}
}
