package mcpserver

import (
	"context"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/usage"

	"github.com/mark3labs/mcp-go/mcp"
)

// registerRecallStats adds recall_stats: is the memory being used, and does it
// answer? Drawer counts say how much is remembered; this says whether remembering
// is working, which is the only question an operator can act on.
func registerRecallStats(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("recall_stats",
		mcp.WithDescription("How well memory is working, per wing: searches run, how many came back with something, drawers held, and the recent queries that found NOTHING (the memories the team looked for and does not have). Use it to see whether recall is earning its keep rather than guessing."),
		mcp.WithNumber("hours", mcp.Description("Window to report on, in hours (default 24).")),
		mcp.WithNumber("unanswered", mcp.Description("How many unanswered queries to list (default 10).")),
	)
	reg.add(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		hours := req.GetInt("hours", 24)
		if hours <= 0 {
			hours = 24
		}
		stats, err := drawers.RecallStats(ctx, t.TeamID, time.Duration(hours)*time.Hour, req.GetInt("unanswered", 10))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		wings := make([]map[string]any, 0, len(stats.Wings))
		for _, w := range stats.Wings {
			wings = append(wings, map[string]any{
				"wing":          w.Wing,
				"searches":      w.Searches,
				"answered":      w.Answered,
				"answered_pct":  w.AnsweredPct(),
				"avg_top_score": w.AvgTop,
				"drawers":       w.Drawers,
				"last_used":     w.LastUsed,
				"last_filed":    w.LastFiled,
			})
		}
		return jsonResult(map[string]any{
			"window_hours": hours,
			"since":        stats.Since,
			"searches":     stats.Searches,
			"answered":     stats.Answered,
			"answered_pct": stats.AnsweredPct(),
			"writes":       stats.Writes,
			"wings":        wings,
			"unanswered":   stats.Unanswered,
			"hint":         "answered_pct climbing over weeks means the palace is learning the questions this team actually asks; a wing with drawers and no searches is written-to and never read.",
		}), nil
	})
}

// registerAdmin wires the palace-maintenance tools: merge_wing (fold wings
// together) and memories_filed_away (a recent-activity summary). The frozen sync
// and hook_settings tools are deliberately not ported — both are single-user-local
// (on-disk source pruning / local Claude Code hook config) with no meaning on a
// multi-tenant server. All admin tools are tenant-scoped.
func registerAdmin(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	registerMergeWing(reg, drawers, usageSvc)
	registerMemoriesFiledAway(reg, drawers, usageSvc)
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
