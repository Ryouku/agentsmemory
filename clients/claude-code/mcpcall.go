// mcpcall.go implements the `mcp` subcommand: a read-only shell window onto the
// remote agentsmemory MCP, so a customer can see exactly what a tool returns
// without going through an agent — `aiagentmemory mcp status`,
// `aiagentmemory mcp search "auth bug" -a limit=3`.
//
// It is the customer-side twin of the server's `agentsmemory mcp` CLI
// (cmd/server/mcp.go), and deliberately differs in the one way that matters: the
// server CLI calls the palace services against its own SQLite, while this one is
// a Streamable-HTTP MCP client authed by the workspace bearer token — the same
// endpoint, transport and credential the installer wires into the agents. What
// you see here is therefore what the agent sees.
//
// Two properties shape the design:
//
//   - Passthrough, not hand-written subcommands. The catalogue, each tool's
//     arguments, and the primary positional all come from the live tools/list, so
//     a tool added server-side is callable without shipping a new binary.
//   - Read-only by construction. The remote endpoint exposes writes too, but a
//     mistyped shell command must never mutate team memory, so calls are gated by
//     the readOnlyHint on the live tools/list entry. A missing or false hint is
//     refused, so an unclassified server tool cannot become writable by accident.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/urfave/cli/v3"
)

// toolPrefix is the namespace the server puts on every tool (am_status,
// am_search, …). The CLI accepts a name with or without it and always sends the
// prefixed form on the wire.
const toolPrefix = "am_"

// isReadOnlyTool reports whether the exact live tool definition says the call
// cannot modify its environment. Missing metadata fails closed: names are not a
// security boundary, and a future mutating list_* tool must not become callable
// merely because its verb looks harmless.
func isReadOnlyTool(tool mcp.Tool) bool {
	return tool.Annotations.ReadOnlyHint != nil && *tool.Annotations.ReadOnlyHint
}

// mcpCommand builds the `mcp` subcommand. With no tool it prints the catalogue;
// with one it calls the tool and prints what came back.
func mcpCommand() *cli.Command {
	return &cli.Command{
		Name:      "mcp",
		Usage:     "call a read-only memory tool on the remote MCP (run with no tool to list them)",
		ArgsUsage: "[tool] [primary-arg]",
		Description: "List the tools:     aiagentmemory mcp\n" +
			"Call one:           aiagentmemory mcp status\n" +
			"With an argument:   aiagentmemory mcp search \"auth bug\"\n" +
			"With more:          aiagentmemory mcp search \"auth bug\" -a limit=3 -a wing=wing_x\n" +
			"Pipe it:            aiagentmemory mcp search \"auth bug\" | jq '.hits[].room'\n\n" +
			"The bare positional fills the tool's first required argument, so `mcp search \"x\"`\n" +
			"means `-a query=x`. Writes are refused: this is a read-only window on the palace.\n\n" +
			"The workspace token is taken from --token/$AGENTSMEMORY_TOKEN, else from an\n" +
			"install already on this machine (--sandbox <name> selects one).",
		Flags: []cli.Flag{
			&cli.StringSliceFlag{
				Name:    "arg",
				Aliases: []string{"a"},
				Usage:   "tool argument as key=value (repeatable)",
			},
			&cli.StringFlag{
				Name:    "token",
				Sources: cli.EnvVars(tokenEnvVar),
				Usage:   "agentsmemory workspace API token (default: read from an install on this machine)",
			},
			&cli.StringFlag{
				Name:    "mcp-url",
				Sources: cli.EnvVars(mcpURLEnvVar),
				Value:   defaultMCPURL,
				Usage:   "agentsmemory remote MCP endpoint",
			},
			&cli.StringFlag{
				Name:  "sandbox",
				Usage: "read the token from the sandbox install at ~/.sandboxes/<name>",
			},
			&cli.StringFlag{
				Name:    "config-dir",
				Aliases: []string{"claude-dir"},
				Usage:   "read the token from an install in this config dir",
			},
			&cli.BoolFlag{
				Name:  "raw",
				Usage: "print the whole MCP envelope (content blocks, isError) instead of just the result",
			},
			&cli.DurationFlag{
				Name:  "timeout",
				Value: 60 * time.Second,
				Usage: "give up on the endpoint after this long",
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			return runRemoteMCP(ctx, c, os.Stdout)
		},
	}
}

// runRemoteMCP performs one CLI invocation: resolve the token, open the MCP
// session, then either print the catalogue or call the named tool.
//
// Only tool output goes to out (stdout); every diagnostic goes to stderr, so
// `aiagentmemory mcp search q | jq` keeps working.
func runRemoteMCP(ctx context.Context, c *cli.Command, out io.Writer) error {
	// The am_ prefix is for client disambiguation; accept it, don't require it.
	name := strings.TrimPrefix(c.Args().First(), toolPrefix)

	token, source, err := resolveWorkspaceToken(c)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "aiagentmemory: token from %s\n", source)

	ctx, cancel := context.WithTimeout(ctx, c.Duration("timeout"))
	defer cancel()

	session, err := dialMCP(ctx, c.String("mcp-url"), token, c.Duration("timeout"))
	if err != nil {
		return err
	}
	defer session.Close()

	tools, err := session.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return fmt.Errorf("list tools: %w", err)
	}

	if name == "" {
		return printRemoteTools(out, tools.Tools, c.Bool("raw"))
	}

	tool, ok := findRemoteTool(tools.Tools, name)
	if !ok {
		return fmt.Errorf("unknown tool %q; run `aiagentmemory mcp` to list the available tools", name)
	}
	if !isReadOnlyTool(tool) {
		// A write tool exists on the endpoint but is out of bounds here: the
		// shell is for looking, the agent is for writing.
		return fmt.Errorf("%q writes to the palace and is not available from the CLI, which is read-only; ask your agent to call it", name)
	}

	args := parseToolArgs(c.StringSlice("arg"), tailArgs(c), tool.InputSchema.Properties, primaryArg(tool))
	req := mcp.CallToolRequest{}
	req.Params.Name = tool.Name
	req.Params.Arguments = args

	res, err := session.CallTool(ctx, req)
	if err != nil {
		return fmt.Errorf("call %s: %w", tool.Name, err)
	}
	if err := printCallResult(out, res, c.Bool("raw")); err != nil {
		return err
	}
	if res.IsError {
		// Whatever the tool said is already on stdout; this only sets the exit
		// code so a script can tell success from failure.
		return errors.New("the tool reported an error")
	}
	return nil
}

// dialMCP opens and initialises a Streamable-HTTP MCP session against url,
// authenticated with the workspace bearer token — the same handshake the pi
// bridge extension performs (clients/claude-code/extensions/agentsmemory.ts).
func dialMCP(ctx context.Context, url, token string, timeout time.Duration) (*client.Client, error) {
	c, err := client.NewStreamableHttpClient(url,
		transport.WithHTTPHeaders(map[string]string{"Authorization": "Bearer " + token}),
		transport.WithHTTPTimeout(timeout),
	)
	if err != nil {
		return nil, fmt.Errorf("connect %s: %w", url, err)
	}
	if err := c.Start(ctx); err != nil {
		return nil, fmt.Errorf("connect %s: %w", url, err)
	}

	init := mcp.InitializeRequest{}
	init.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	init.Params.ClientInfo = mcp.Implementation{Name: "aiagentmemory-cli", Version: version}
	if _, err := c.Initialize(ctx, init); err != nil {
		c.Close()
		// A bad or revoked token surfaces here as an HTTP 401 from the endpoint.
		return nil, fmt.Errorf("initialize %s: %w", url, err)
	}
	return c, nil
}

// findRemoteTool looks a bare tool name up in the live catalogue, matching with
// or without the am_ prefix.
func findRemoteTool(tools []mcp.Tool, name string) (mcp.Tool, bool) {
	for _, t := range tools {
		if strings.TrimPrefix(t.Name, toolPrefix) == name {
			return t, true
		}
	}
	return mcp.Tool{}, false
}

// primaryArg is the argument the bare positional fills: the tool's first
// required input. Taking it from the live schema rather than a hand-kept table
// is what lets `mcp search "x"` and `mcp get_drawer <id>` work without this
// binary knowing anything about either tool. A tool with no required input (like
// status) has no primary, and a positional passed to it is simply dropped.
func primaryArg(t mcp.Tool) string {
	if len(t.InputSchema.Required) == 0 {
		return ""
	}
	return t.InputSchema.Required[0]
}

// tailArgs returns the positional tokens after the tool name. parseToolArgs
// re-scans them so the hybrid syntax works regardless of whether urfave/cli
// consumed an interspersed -a into its flag slice.
func tailArgs(c *cli.Command) []string {
	all := c.Args().Slice()
	if len(all) <= 1 {
		return nil
	}
	return all[1:]
}

// parseToolArgs builds the JSON arguments for a tool call from the -a/--arg
// values plus the raw trailing tokens, coercing each value to the type the
// tool's schema declares. The first plain token (not key=value, not an -a
// marker) becomes the primary positional, folded under primaryKey unless an
// explicit -a already set it.
//
// Coercion is what makes this differ from the server CLI's string-map
// equivalent: these arguments cross the wire as JSON, so `-a limit=3` has to
// arrive as the number 3 — a tool reading limit with GetInt would silently fall
// back to its default on the string "3". Only properties the schema calls
// number/integer/boolean are converted, so a hex drawer id stays the string it
// is. A value that does not parse is passed through unchanged, letting the
// server report the type error rather than the CLI guessing.
func parseToolArgs(argFlags, rawTail []string, props map[string]any, primaryKey string) map[string]any {
	raw := map[string]string{}
	add := func(kv string) {
		if k, v, ok := strings.Cut(kv, "="); ok {
			raw[strings.TrimSpace(k)] = v
		}
	}
	for _, kv := range argFlags {
		add(kv)
	}

	var positional string
	for i := 0; i < len(rawTail); i++ {
		tok := rawTail[i]
		switch {
		case tok == "-a" || tok == "--arg":
			if i+1 < len(rawTail) {
				add(rawTail[i+1])
				i++
			}
		case strings.Contains(tok, "="):
			add(tok)
		case positional == "":
			positional = tok
		}
	}

	if positional != "" && primaryKey != "" {
		if _, exists := raw[primaryKey]; !exists {
			raw[primaryKey] = positional
		}
	}

	args := make(map[string]any, len(raw))
	for k, v := range raw {
		args[k] = coerceArg(props[k], v)
	}
	return args
}

// coerceArg converts one raw value according to its JSON Schema entry, leaving
// it a string when the schema says string, says nothing, or the value does not
// parse as the declared type.
func coerceArg(spec any, value string) any {
	obj, ok := spec.(map[string]any)
	if !ok {
		return value
	}
	switch obj["type"] {
	case "number", "integer":
		if n, err := strconv.ParseFloat(value, 64); err == nil {
			return n
		}
	case "boolean":
		if b, err := strconv.ParseBool(value); err == nil {
			return b
		}
	}
	return value
}

// printCallResult writes what the tool returned. By default that is the text
// content itself — which for every agentsmemory tool is JSON, so it pipes
// straight into jq — re-indented when it parses. --raw prints the whole MCP
// envelope instead, for when the question is what the protocol returned rather
// than what the tool did.
func printCallResult(out io.Writer, res *mcp.CallToolResult, raw bool) error {
	if raw {
		return writeJSON(out, res)
	}
	for _, content := range res.Content {
		text, ok := mcp.AsTextContent(content)
		if !ok {
			// Non-text blocks (images, embedded resources) have no shell
			// rendering; --raw is the way to see them.
			continue
		}
		var parsed any
		if err := json.Unmarshal([]byte(text.Text), &parsed); err != nil {
			fmt.Fprintln(out, text.Text)
			continue
		}
		if err := writeJSON(out, parsed); err != nil {
			return err
		}
	}
	return nil
}

// printRemoteTools prints the live catalogue, listing only the tools this CLI
// can actually call and counting the rest. --raw prints tools/list verbatim,
// schemas included — the way to discover what arguments a tool takes.
func printRemoteTools(out io.Writer, tools []mcp.Tool, raw bool) error {
	if raw {
		return writeJSON(out, tools)
	}

	sorted := append([]mcp.Tool(nil), tools...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	var readable []mcp.Tool
	for _, t := range sorted {
		if isReadOnlyTool(t) {
			readable = append(readable, t)
		}
	}

	fmt.Fprintf(out, "%d read-only tools (of %d on the endpoint):\n\n", len(readable), len(sorted))
	for _, t := range readable {
		usage := strings.TrimPrefix(t.Name, toolPrefix)
		if p := primaryArg(t); p != "" {
			usage += " <" + p + ">"
		}
		fmt.Fprintf(out, "  %s\n      %s\n", usage, firstLine(t.Description, 96))
	}
	fmt.Fprintf(out, "\n%d write tools are not callable here — ask your agent to run those.\n", len(sorted)-len(readable))
	fmt.Fprintln(out, "Arguments: `mcp <tool> <primary-arg> -a key=value`; `mcp --raw` prints every schema.")
	return nil
}

// firstLine trims a tool description to one readable line for the catalogue.
func firstLine(s string, max int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) <= max {
		return s
	}
	return strings.TrimSpace(s[:max]) + "…"
}

// writeJSON prints v as indented JSON — the CLI's one output format, so every
// result is pipeable into jq.
func writeJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("render result: %w", err)
	}
	return nil
}

// resolveWorkspaceToken finds the token to authenticate with and describes where
// it came from (for the stderr note — a call made with a different workspace's
// token is otherwise a confusing empty result).
//
// The flag/env wins; otherwise an install already on this machine is read, since
// the token was typed once during `install` and asking for it again on every
// shell command would make this feature useless in practice.
func resolveWorkspaceToken(c *cli.Command) (token, source string, err error) {
	if t := c.String("token"); t != "" {
		return t, "--token/$" + tokenEnvVar, nil
	}

	dirs := tokenSearchDirs(c)
	for _, dir := range dirs {
		if t, from := tokenFromConfigDir(dir); t != "" {
			return t, from, nil
		}
	}
	return "", "", fmt.Errorf("no workspace token found: pass --token (or set %s), or point at an install with --sandbox <name>/--config-dir <dir>; looked in %s",
		tokenEnvVar, strings.Join(dirs, ", "))
}

// tokenSearchDirs lists the config dirs to look for a token in. An explicit
// --config-dir or --sandbox selects exactly one — naming a sandbox and silently
// falling back to a different workspace's token would be worse than failing.
// With neither, the global installs of the three agents are searched, plus $HOME
// itself because that is where Claude keeps user-scope MCP servers when
// CLAUDE_CONFIG_DIR is unset.
func tokenSearchDirs(c *cli.Command) []string {
	if dir := c.String("config-dir"); dir != "" {
		return []string{dir}
	}
	if name := c.String("sandbox"); name != "" {
		return []string{sandboxDir(name)}
	}
	home := homeDir()
	return []string{
		home,
		claudeKit.globalConfigDir(home),
		codexKit.globalConfigDir(home),
		piKit.globalConfigDir(home),
	}
}

// tokenFromConfigDir reads the workspace token out of one config dir, trying
// both shapes the installer produces: agentsmemory.env (written for codex and
// pi, which take the token through an env var) and the agent's own MCP
// registration in .claude.json (Claude, where the token is embedded in the
// Authorization header). It returns the token and the file it came from.
func tokenFromConfigDir(dir string) (token, source string) {
	envPath := filepath.Join(dir, tokenFile)
	if t := tokenFromEnvFile(envPath); t != "" {
		return t, envPath
	}
	jsonPath := filepath.Join(dir, ".claude.json")
	if t := tokenFromClaudeJSON(jsonPath); t != "" {
		return t, jsonPath
	}
	return "", ""
}

// tokenFromEnvFile pulls AGENTSMEMORY_TOKEN out of an agentsmemory.env file
// (KEY=value lines, 0600, written by registerCodexMCP/registerPiMCP).
func tokenFromEnvFile(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && strings.TrimSpace(key) == tokenEnvVar {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// tokenFromClaudeJSON pulls the bearer token out of a Claude config's
// agentsmemory MCP registration — mcpServers.agentsmemory.headers.Authorization,
// which registerClaudeMCP filled with "Bearer <token>".
func tokenFromClaudeJSON(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var cfg struct {
		MCPServers map[string]struct {
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return "" // a config we cannot parse is one we have no token in
	}
	auth := cfg.MCPServers[mcpName].Headers["Authorization"]
	return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
}
