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
	endpoint := mcpcli.Endpoint{
		ListTools: func(callCtx context.Context) ([]mcp.Tool, error) {
			return listMCPTools(callCtx, productionMCPServer(nil, cfg, local))
		},
		CallTool: func(callCtx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			svc, err := buildServices(cfg)
			if err != nil {
				return nil, err
			}
			sqlDB, err := svc.gdb.DB()
			if err != nil {
				return nil, fmt.Errorf("open SQL handle: %w", err)
			}
			defer sqlDB.Close()

			resolved, unmetered, err := resolveTenant(callCtx, svc, c)
			if err != nil {
				return nil, err
			}
			callCtx = auth.WithTenant(callCtx, resolved)
			if wing := c.String("wing"); wing != "" {
				callCtx = auth.WithDefaultWing(callCtx, wing)
			}
			if unmetered {
				callCtx = mcpserver.WithUnmeteredLocalOperator(callCtx)
			}

			session, err := newInProcessMCPClient(callCtx, productionMCPServer(svc, cfg, local))
			if err != nil {
				return nil, err
			}
			defer session.Close()
			return session.CallTool(callCtx, req)
		},
	}
	return mcpcli.Run(ctx, c.Writer, endpoint, mcpcli.Invocation{
		Tool:     c.Args().First(),
		ArgFlags: c.StringSlice("arg"),
		Tail:     mcpcli.TailArgs(c.Args().Slice()),
	})
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
