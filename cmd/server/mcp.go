// mcp.go implements the `mcp` subcommand: a read-only in-process client of the
// production MCP server. Operators and scripts can inspect memory from the
// shell without an HTTP round-trip — e.g. `agentsmemory mcp search "auth bug"`
// — while exercising the exact handlers, schemas, projections, wing rules, and
// admission code remote agents use.
//
// Read-only is derived from each live tools/list entry. A missing or false
// readOnlyHint fails closed, so this adapter has no parallel tool registry to
// drift. --token resolves a real tenant and meters in the production handler;
// --team is trusted local-operator access to the operator's own database and is
// deliberately unmetered.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/internal/auth"
	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpcli"
	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpserver"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	mcptransport "github.com/mark3labs/mcp-go/server"
	"github.com/urfave/cli/v3"
)

const mcpToolPrefix = "am_"

// mcpCommand builds the `mcp` subcommand. It reuses dataFlags (the storage/embed
// flags) so it opens the same database as serve, and adds the auth selectors
// (--token / --team) and the repeatable -a/--arg tool-argument flag.
func mcpCommand(def config.Config) *cli.Command {
	return &cli.Command{
		Name:      "mcp",
		Usage:     "Invoke a read-only memory tool from the CLI (run with no tool to list them)",
		ArgsUsage: "[tool] [primary-arg]",
		Flags: append(dataFlags(def),
			&cli.StringFlag{Name: "token", Sources: cli.EnvVars("AGENTSMEMORY_TOKEN"), Usage: "API key: resolves the tenant and meters the call (HTTP parity)"},
			&cli.StringFlag{Name: "team", Usage: "team id: trusted local admin read, no metering (alternative to --token)"},
			&cli.StringSliceFlag{Name: "arg", Aliases: []string{"a"}, Usage: "tool argument as key=value (repeatable)"},
			&cli.StringFlag{Name: "wing", Usage: "default wing for this call, like a per-project MCP registration; explicit -a wing= wins and \"*\" searches every wing"},
		),
		Action: func(ctx context.Context, c *cli.Command) error {
			return runMCP(ctx, c, def)
		},
	}
}

// runMCP performs one direct CLI invocation through the production MCP server.
// Listing the catalogue is registration-only and does not open the database;
// an actual call wires services, authenticates, and invokes the live handler.
func runMCP(ctx context.Context, c *cli.Command, def config.Config) error {
	cfg := configFromCmd(c, def)
	local := c.String("team") != "" && c.String("token") == ""
	definitions, err := listMCPTools(ctx, productionMCPServer(nil, cfg, local))
	if err != nil {
		return err
	}

	name := strings.TrimPrefix(c.Args().First(), mcpToolPrefix)
	if name == "" {
		return printMCPTools(c.Writer, definitions)
	}
	tool, ok := findMCPTool(definitions, name)
	if !ok {
		return fmt.Errorf("unknown tool %q; run `agentsmemory mcp` to list the available read-only tools", name)
	}
	if !mcpcli.IsReadOnly(tool) {
		return fmt.Errorf("%q writes to the palace and is not available from the CLI, which is read-only; ask your agent to call it", name)
	}

	svc, err := buildServices(cfg)
	if err != nil {
		return err
	}
	sqlDB, err := svc.gdb.DB()
	if err != nil {
		return fmt.Errorf("open SQL handle: %w", err)
	}
	defer sqlDB.Close()
	resolved, unmetered, err := resolveTenant(ctx, svc, c)
	if err != nil {
		return err
	}

	callCtx := auth.WithTenant(ctx, resolved)
	if wing := c.String("wing"); wing != "" {
		callCtx = auth.WithDefaultWing(callCtx, wing)
	}
	if unmetered {
		callCtx = mcpserver.WithUnmeteredLocalOperator(callCtx)
	}

	session, err := newInProcessMCPClient(ctx, productionMCPServer(svc, cfg, local))
	if err != nil {
		return err
	}
	defer session.Close()

	req := mcp.CallToolRequest{}
	req.Params.Name = tool.Name
	req.Params.Arguments = mcpcli.ParseArgs(
		c.StringSlice("arg"), tailArgs(c), tool.InputSchema.Properties, mcpcli.PrimaryArg(tool),
	)
	res, err := session.CallTool(callCtx, req)
	if err != nil {
		return fmt.Errorf("call %s: %w", tool.Name, err)
	}
	if err := mcpcli.PrintResult(c.Writer, res); err != nil {
		return err
	}
	if res.IsError {
		return errors.New("the tool reported an error")
	}
	return nil
}

// resolveTenant picks the tenant the call acts as. --token resolves the full
// production identity and role; --team constructs a local admin identity. The
// boolean reports whether production admission should skip hosted metering.
func resolveTenant(ctx context.Context, svc *services, c *cli.Command) (tenant.Tenant, bool, error) {
	token, team := c.String("token"), c.String("team")
	if token != "" && team != "" {
		return tenant.Tenant{}, false, errors.New("provide exactly one of --token and --team, not both")
	}
	if token != "" {
		t, err := svc.tenants.ResolveToken(ctx, token)
		if err != nil {
			return tenant.Tenant{}, false, fmt.Errorf("resolve --token: %w", err)
		}
		return t, false, nil
	}
	if team != "" {
		return tenant.Tenant{TeamID: team, Role: tenant.RoleAdmin}, true, nil
	}
	return tenant.Tenant{}, false, errors.New("provide --token (or AGENTSMEMORY_TOKEN) for a metered, auth-parity read, or --team <id> for a trusted local admin read")
}

// newInProcessMCPClient starts and initializes an MCP client against srv. This
// is a protocol client, not a handler lookup: calls still cross the mcp-go
// dispatch boundary and consume the same tool definitions as HTTP clients.
func newInProcessMCPClient(ctx context.Context, srv *mcptransport.MCPServer) (*client.Client, error) {
	cli, err := client.NewInProcessClient(srv)
	if err != nil {
		return nil, fmt.Errorf("create in-process MCP client: %w", err)
	}
	if err := cli.Start(ctx); err != nil {
		_ = cli.Close()
		return nil, fmt.Errorf("start in-process MCP client: %w", err)
	}
	if _, err := cli.Initialize(ctx, mcp.InitializeRequest{}); err != nil {
		_ = cli.Close()
		return nil, fmt.Errorf("initialize in-process MCP client: %w", err)
	}
	return cli, nil
}

// listMCPTools returns the definitions a protocol client receives from
// tools/list, rather than a package-private registration slice.
func listMCPTools(ctx context.Context, srv *mcptransport.MCPServer) ([]mcp.Tool, error) {
	cli, err := newInProcessMCPClient(ctx, srv)
	if err != nil {
		return nil, err
	}
	defer cli.Close()
	res, err := cli.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("list MCP tools: %w", err)
	}
	return res.Tools, nil
}

// tailArgs returns the positional tokens after the tool name. parseArgs scans
// them so the hybrid syntax (`mcp search "q" -a limit=5`) works regardless of
// whether urfave/cli consumed the interspersed -a into its flag slice.
func tailArgs(c *cli.Command) []string {
	all := c.Args().Slice()
	if len(all) <= 1 {
		return nil
	}
	return all[1:]
}

func findMCPTool(tools []mcp.Tool, bareName string) (mcp.Tool, bool) {
	wireName := mcpToolPrefix + bareName
	for _, tool := range tools {
		if tool.Name == wireName {
			return tool, true
		}
	}
	return mcp.Tool{}, false
}

// printMCPTools renders only the read tools the CLI can call. Descriptions and
// primary arguments come from the actual wire definitions.
func printMCPTools(out io.Writer, tools []mcp.Tool) error {
	readable := make([]mcp.Tool, 0, len(tools))
	for _, tool := range tools {
		if mcpcli.IsReadOnly(tool) {
			readable = append(readable, tool)
		}
	}
	sort.Slice(readable, func(i, j int) bool { return readable[i].Name < readable[j].Name })

	fmt.Fprintf(out, "%d read-only memory tools (of %d on the production MCP surface):\n\n", len(readable), len(tools))
	for _, tool := range readable {
		usage := strings.TrimPrefix(tool.Name, mcpToolPrefix)
		if primary := mcpcli.PrimaryArg(tool); primary != "" {
			usage += " <" + primary + ">"
		}
		fmt.Fprintf(out, "  %s\n      %s\n", usage, firstMCPLine(tool.Description, 96))
	}
	fmt.Fprintln(out, "\nCalls: agentsmemory mcp <tool> [primary-arg] -a key=value")
	fmt.Fprintln(out, "Auth: --token meters in the production handler; --team <id> is trusted local operator access.")
	return nil
}

func firstMCPLine(text string, max int) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	if len(text) <= max {
		return text
	}
	return strings.TrimSpace(text[:max]) + "…"
}
