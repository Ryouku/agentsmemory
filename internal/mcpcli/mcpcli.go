// Package mcpcli contains transport-level helpers shared by the direct and
// remote agentsmemory MCP command-line clients. Tool definitions remain owned
// by the production server; this package only consumes their live schemas and
// annotations.
package mcpcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"
	"github.com/mark3labs/mcp-go/mcp"
)

// Endpoint is the transport seam used by the shared CLI execution path. HTTP
// and in-process clients supply different implementations, but discovery and
// invocation policy remain here.
type Endpoint struct {
	ListTools func(context.Context) ([]mcp.Tool, error)
	CallTool  func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
}

// Invocation is the transport-independent input to a CLI MCP call.
type Invocation struct {
	Tool     string
	ArgFlags []string
	Tail     []string
	Raw      bool
}

// Run executes the one CLI contract shared by local and remote consumers:
// discover the live tools, fail closed on write policy, build arguments from
// the live schema, call the selected production handler, and render its result.
func Run(ctx context.Context, out io.Writer, endpoint Endpoint, invocation Invocation) error {
	tools, err := endpoint.ListTools(ctx)
	if err != nil {
		return fmt.Errorf("list MCP tools: %w", err)
	}

	name := strings.TrimPrefix(invocation.Tool, mcpprotocol.ToolPrefix)
	if name == "" {
		return PrintTools(out, tools, invocation.Raw)
	}

	tool, ok := FindTool(tools, name)
	if !ok {
		return fmt.Errorf("unknown tool %q; run the mcp command without a tool to list the available tools", name)
	}
	if !IsReadOnly(tool) {
		return fmt.Errorf("%q writes to the palace and is not available from the CLI, which is read-only; ask your agent to call it", name)
	}

	req := mcp.CallToolRequest{}
	req.Params.Name = tool.Name
	req.Params.Arguments = ParseArgs(invocation.ArgFlags, invocation.Tail, tool.InputSchema.Properties, PrimaryArg(tool))
	result, err := endpoint.CallTool(ctx, req)
	if err != nil {
		return fmt.Errorf("call %s: %w", tool.Name, err)
	}
	if err := PrintCallResult(out, result, invocation.Raw); err != nil {
		return err
	}
	if result.IsError {
		return errors.New("the tool reported an error")
	}
	return nil
}

// IsReadOnly reports whether the live tool definition explicitly promises not
// to modify its environment. Missing metadata fails closed.
func IsReadOnly(tool mcp.Tool) bool {
	return tool.Annotations.ReadOnlyHint != nil && *tool.Annotations.ReadOnlyHint
}

// PrimaryArg returns the first required input named by the live schema, or an
// empty string when a bare positional must not be folded into the call.
func PrimaryArg(tool mcp.Tool) string {
	if len(tool.InputSchema.Required) == 0 {
		return ""
	}
	return tool.InputSchema.Required[0]
}

// FindTool resolves a bare or am_-prefixed name against the live catalogue.
func FindTool(tools []mcp.Tool, name string) (mcp.Tool, bool) {
	wireName := mcpprotocol.ToolPrefix + strings.TrimPrefix(name, mcpprotocol.ToolPrefix)
	for _, tool := range tools {
		if tool.Name == wireName {
			return tool, true
		}
	}
	return mcp.Tool{}, false
}

// TailArgs returns the positional tokens after the tool name.
func TailArgs(args []string) []string {
	if len(args) <= 1 {
		return nil
	}
	return args[1:]
}

// ParseArgs folds CLI key=value arguments and one bare positional into a map
// typed according to the live JSON schema. Explicit key=value input wins over
// the positional for the same key.
func ParseArgs(argFlags, rawTail []string, properties map[string]any, primaryKey string) map[string]any {
	raw := map[string]string{}
	add := func(kv string) {
		if key, value, ok := strings.Cut(kv, "="); ok {
			raw[strings.TrimSpace(key)] = value
		}
	}
	for _, kv := range argFlags {
		add(kv)
	}

	var positional string
	for i := 0; i < len(rawTail); i++ {
		token := rawTail[i]
		switch {
		case token == "-a" || token == "--arg":
			if i+1 < len(rawTail) {
				add(rawTail[i+1])
				i++
			}
		case strings.Contains(token, "="):
			add(token)
		case positional == "":
			positional = token
		}
	}
	if positional != "" && primaryKey != "" {
		if _, exists := raw[primaryKey]; !exists {
			raw[primaryKey] = positional
		}
	}

	args := make(map[string]any, len(raw))
	for key, value := range raw {
		args[key] = coerce(properties[key], value)
	}
	return args
}

func coerce(spec any, value string) any {
	property, ok := spec.(map[string]any)
	if !ok {
		return value
	}
	switch property["type"] {
	case "number", "integer":
		if number, err := strconv.ParseFloat(value, 64); err == nil {
			return number
		}
	case "boolean":
		if boolean, err := strconv.ParseBool(value); err == nil {
			return boolean
		}
	}
	return value
}

// PrintResult writes MCP text content, pretty-printing JSON while preserving
// non-JSON tool messages verbatim. Non-text blocks remain available through a
// client's raw-envelope mode.
func PrintResult(out io.Writer, result *mcp.CallToolResult) error {
	for _, content := range result.Content {
		text, ok := mcp.AsTextContent(content)
		if !ok {
			continue
		}
		var decoded any
		if err := json.Unmarshal([]byte(text.Text), &decoded); err != nil {
			fmt.Fprintln(out, text.Text)
			continue
		}
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(decoded); err != nil {
			return fmt.Errorf("render MCP result: %w", err)
		}
	}
	return nil
}

// PrintCallResult renders either the useful tool content or the complete MCP
// envelope requested by a diagnostic caller.
func PrintCallResult(out io.Writer, result *mcp.CallToolResult, raw bool) error {
	if raw {
		return printJSON(out, result)
	}
	return PrintResult(out, result)
}

// PrintTools renders the callable read surface from the live catalogue. Raw
// mode emits the complete tools/list payload, schemas included.
func PrintTools(out io.Writer, tools []mcp.Tool, raw bool) error {
	if raw {
		return printJSON(out, tools)
	}

	sorted := append([]mcp.Tool(nil), tools...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	readable := make([]mcp.Tool, 0, len(sorted))
	for _, tool := range sorted {
		if IsReadOnly(tool) {
			readable = append(readable, tool)
		}
	}

	fmt.Fprintf(out, "%d read-only tools (of %d on the production MCP surface):\n\n", len(readable), len(sorted))
	for _, tool := range readable {
		usage := strings.TrimPrefix(tool.Name, mcpprotocol.ToolPrefix)
		if primary := PrimaryArg(tool); primary != "" {
			usage += " <" + primary + ">"
		}
		fmt.Fprintf(out, "  %s\n      %s\n", usage, firstLine(tool.Description, 96))
	}
	fmt.Fprintf(out, "\n%d write tools are not callable here — ask your agent to run those.\n", len(sorted)-len(readable))
	fmt.Fprintln(out, "Arguments: `mcp <tool> <primary-arg> -a key=value`; raw mode prints every schema.")
	return nil
}

func firstLine(text string, max int) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	if len(text) <= max {
		return text
	}
	return strings.TrimSpace(text[:max]) + "…"
}

func printJSON(out io.Writer, value any) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("render MCP value: %w", err)
	}
	return nil
}
