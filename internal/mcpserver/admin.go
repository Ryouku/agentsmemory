package mcpserver

import (
	"context"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/usage"

	"github.com/mark3labs/mcp-go/mcp"
)

// registerAdmin wires the palace-maintenance tools: merge_wing (fold wings
// together), memories_filed_away (a recent-activity summary), and — in local mode
// only — delete_wing. The frozen sync and hook_settings tools are deliberately not
// ported — both are single-user-local (on-disk source pruning / local Claude Code
// hook config) with no meaning on a multi-tenant server. All admin tools are
// tenant-scoped.
//
// local gates delete_wing because the two deployments differ in who is on the far
// end of the connection. Self-hosted, the agent and the operator are one person on
// one machine with one workspace, and the alternative to an agent deleting a wing
// is nobody deleting it — there is no dashboard. On the multi-tenant server a
// workspace is shared, an unrecoverable mass delete is not a tool an agent should
// be able to reach for on a colleague's memory, and the dashboard is right there.
func registerAdmin(reg *registrar, drawers *palace.Service, usageSvc *usage.Service, local bool) {
	registerMergeWing(reg, drawers, usageSvc)
	registerMemoriesFiledAway(reg, drawers, usageSvc)
	if local {
		registerDeleteWing(reg, drawers, usageSvc)
	}
}

// registerDeleteWing: permanently remove one wing. Registered only in local mode.
func registerDeleteWing(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("delete_wing",
		mcp.WithDescription("Permanently delete one wing and everything filed in it: its drawers, closets, hallways, and every tunnel with an endpoint in it. This cannot be undone and knowledge-graph facts are left untouched. Set confirm to the wing's own name; any other value is refused and reports what the delete would have removed."),
		mcp.WithString("wing", mcp.Required(), mcp.Description("The wing to delete.")),
		mcp.WithString("confirm", mcp.Required(), mcp.Description("Repeat the wing name exactly. This is a deliberate second spelling, so do not derive it from the wing argument — take it from what the user actually asked you to delete.")),
	)
	reg.add(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		wing, err := req.RequireString("wing")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		// Absent rather than defaulted: a missing confirmation must fail the guard,
		// and "" would fail it anyway, but requiring the field makes the refusal say
		// so rather than reporting a mismatch against an argument nobody sent.
		confirm, err := req.RequireString("confirm")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		res, err := drawers.DeleteWing(ctx, t.TeamID, wing, confirm)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(res), nil
	})
}

// registerMergeWing: fold one or more source wings into a target, relabeling every
// drawer and closet. Run am_recompute_graph afterwards to rebuild the derived graph.
func registerMergeWing(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("merge_wing",
		mcp.WithDescription("Merge one or more source wings into a target wing, relabeling every drawer and closet in place. Run am_recompute_graph afterwards to rebuild hallways/tunnels."),
		mcp.WithArray("sources", mcp.Required(),
			mcp.Description("The wing names to fold into the target."),
			mcp.Items(map[string]any{"type": "string"}),
		),
		mcp.WithString("target", mcp.Required(), mcp.Description("The wing to merge the sources into (created if new).")),
	)
	reg.add(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		target, err := req.RequireString("target")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		sources, ok := stringSlice(req.GetArguments()["sources"])
		if !ok || len(sources) == 0 {
			return mcp.NewToolResultError("sources must be a non-empty array of wing-name strings"), nil
		}
		res, err := drawers.MergeWing(ctx, t.TeamID, sources, target)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(res), nil
	})
}

// registerMemoriesFiledAway: a quick summary of what the team has filed.
func registerMemoriesFiledAway(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("memories_filed_away",
		mcp.WithDescription("Summarise what the team has filed: total memories, distinct wings and rooms, and the most recent filing."),
	)
	reg.add(tool, func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		res, err := drawers.MemoriesFiledAway(ctx, t.TeamID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(res), nil
	})
}

// stringSlice coerces an MCP argument (a JSON array decoded to []any) into a
// []string. It returns ok=false if the value is not an array or any element is
// not a plain string, so a malformed `sources` is rejected outright rather than
// silently partially applied.
func stringSlice(v any) ([]string, bool) {
	raw, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		s, ok := item.(string)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}
