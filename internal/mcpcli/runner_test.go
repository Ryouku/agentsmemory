package mcpcli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestRunOwnsDiscoveryPolicyArgumentsCallAndRendering(t *testing.T) {
	read := mcp.NewTool("am_search",
		mcp.WithString("query", mcp.Required()),
		mcp.WithReadOnlyHintAnnotation(true),
	)
	write := mcp.NewTool("am_add_drawer",
		mcp.WithString("content", mcp.Required()),
		mcp.WithReadOnlyHintAnnotation(false),
	)
	listCalls, toolCalls := 0, 0
	endpoint := Endpoint{
		ListTools: func(context.Context) ([]mcp.Tool, error) {
			listCalls++
			return []mcp.Tool{read, write}, nil
		},
		CallTool: func(_ context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			toolCalls++
			arguments, ok := request.Params.Arguments.(map[string]any)
			if !ok || request.Params.Name != "am_search" || arguments["query"] != "needle" {
				t.Fatalf("request = %#v", request.Params)
			}
			return &mcp.CallToolResult{Content: []mcp.Content{
				mcp.TextContent{Type: "text", Text: `{"ok":true}`},
			}}, nil
		},
	}

	var out bytes.Buffer
	if err := Run(t.Context(), &out, endpoint, Invocation{Tool: "search", Tail: []string{"needle"}}); err != nil {
		t.Fatal(err)
	}
	if listCalls != 1 || toolCalls != 1 {
		t.Fatalf("calls list=%d tool=%d, want exactly one each", listCalls, toolCalls)
	}
	if !strings.Contains(out.String(), `"ok": true`) {
		t.Fatalf("result not rendered by shared path: %s", out.String())
	}

	if err := Run(t.Context(), &out, endpoint, Invocation{Tool: "add_drawer"}); err == nil || !strings.Contains(err.Error(), "writes to the palace") {
		t.Fatalf("write refusal = %v", err)
	}
	if toolCalls != 1 {
		t.Fatalf("refused write reached transport; tool calls=%d", toolCalls)
	}
}

func TestPrintToolsAndResultsAreSharedAcrossTransports(t *testing.T) {
	tools := []mcp.Tool{
		mcp.NewTool("am_search", mcp.WithDescription("Semantically recall drawers."), mcp.WithString("query", mcp.Required()), mcp.WithReadOnlyHintAnnotation(true)),
		mcp.NewTool("am_status", mcp.WithDescription("Wake-up call."), mcp.WithReadOnlyHintAnnotation(true)),
		mcp.NewTool("am_add_drawer", mcp.WithDescription("File a verbatim memory."), mcp.WithReadOnlyHintAnnotation(false)),
	}
	var catalogue bytes.Buffer
	if err := PrintTools(&catalogue, tools, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"search <query>",
		"2 read-only tools (of 3 on the production MCP surface)",
		"1 write tools are not callable here",
	} {
		if !strings.Contains(catalogue.String(), want) {
			t.Errorf("catalogue missing %q:\n%s", want, catalogue.String())
		}
	}
	if strings.Contains(catalogue.String(), "add_drawer") {
		t.Errorf("catalogue exposed write tool:\n%s", catalogue.String())
	}

	result := &mcp.CallToolResult{Content: []mcp.Content{
		mcp.TextContent{Type: "text", Text: `{"ok":true,"total_drawers":5785}`},
	}}
	var pretty, raw bytes.Buffer
	if err := PrintCallResult(&pretty, result, false); err != nil {
		t.Fatal(err)
	}
	if err := PrintCallResult(&raw, result, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pretty.String(), `"total_drawers": 5785`) || strings.Contains(pretty.String(), `"content"`) {
		t.Errorf("pretty result = %s", pretty.String())
	}
	if !strings.Contains(raw.String(), `"content"`) {
		t.Errorf("raw result = %s", raw.String())
	}
}
