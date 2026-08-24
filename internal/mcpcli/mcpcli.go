// Package mcpcli contains transport-level helpers shared by the direct and
// remote agentsmemory MCP command-line clients. Tool definitions remain owned
// by the production server; this package only consumes their live schemas and
// annotations.
package mcpcli

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

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
