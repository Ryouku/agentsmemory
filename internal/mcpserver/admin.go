package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/usage"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerAnchors wires the two halves of staleness detection: list_anchors hands
// out the questions, mark_anchors takes back the answers.
//
// The split exists because the server usually cannot see the code it stores
// memories about — it runs in a container, the repository is on someone's laptop.
// Whoever CAN read the working tree (the `aiagentmemory verify` command) does the
// checking; the server only keeps score.
func registerAnchors(reg *registrar, drawers *palace.Service, usageSvc *usage.Service, scopeSearchToWing bool) {
	list := newTool("list_anchors",
		mcp.WithDescription("List code anchors — the (file, snippet) pairs memories are pinned to — so a client that can read the working tree can verify them. Filter by wing, repo label, or status (unchecked|verified|drifted|missing)."),
		mcp.WithString("wing", mcp.Description("Only anchors on drawers in this wing. Omitted, scoped to this registration's default_wing only when one is configured and SEARCH_SCOPE is not workspace; otherwise every wing. Pass \"*\" for every wing deliberately."), searchWingProperty()),
		mcp.WithString("repo", mcp.Description("Only anchors carrying this repo label.")),
		mcp.WithString("status", mcp.Description("Only anchors in this state.")),
		mcp.WithNumber("limit", mcp.Description("Max anchors to return (default 500).")),
	)
	reg.add(list, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		// Resolved through searchWingFor, not taken raw. Audited 2026-08-20 by
		// RUNNING it against two projects in one workspace: naming no wing
		// enumerated every wing, so one project's anchors — and the verbatim source lines they carry — were handed to another. am_search and
		// am_list_drawers resolve identically, and an enumeration that does not is
		// a hole in the scope those two enforce.
		wing, err := searchWingFor(ctx, req.GetString("wing", ""), scopeSearchToWing)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		anchors, err := drawers.ListAnchors(ctx, t.TeamID, palace.AnchorFilter{
			Wing:   wing,
			Repo:   req.GetString("repo", ""),
			Status: req.GetString("status", ""),
			Limit:  req.GetInt("limit", 0),
		})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		out := make([]map[string]any, 0, len(anchors))
		for _, a := range anchors {
			out = append(out, map[string]any{
				"id": a.ID, "drawer_id": a.DrawerID, "repo": a.Repo, "path": a.Path,
				"snippet": a.Snippet, "status": a.Status, "line": a.Line, "checked_at": a.CheckedAt,
			})
		}
		return jsonResult(map[string]any{"anchors": out, "count": len(out)}), nil
	})

	registerMarkAnchors(reg, drawers, usageSvc)
}

// registerMarkAnchors: take back the verification verdicts list_anchors handed
// out. Split from registerAnchors because it WRITES and its sibling reads, and a
// registration that builds both cannot carry one classification honestly.
func registerMarkAnchors(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	mark := newTool("mark_anchors",
		mcp.WithDescription("Record verification verdicts for code anchors: [{\"id\":\"<anchor id>\",\"status\":\"verified|drifted|missing\",\"line\":123}]. Writes only the verdict, never the memory, so stamping never re-embeds anything."),
		mcp.WithArray("verdicts", mcp.Required(), mcp.Description("The results, one object per anchor checked.")),
	)
	reg.addWrite(mark, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		raw, ok := req.GetArguments()["verdicts"].([]any)
		if !ok {
			return mcp.NewToolResultError("verdicts must be an array of {id, status, line} objects"), nil
		}
		verdicts := make([]palace.AnchorVerdict, 0, len(raw))
		for _, item := range raw {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			id, _ := m["id"].(string)
			status, _ := m["status"].(string)
			line := 0
			if f, ok := m["line"].(float64); ok {
				line = int(f)
			}
			if id == "" || status == "" {
				continue
			}
			verdicts = append(verdicts, palace.AnchorVerdict{ID: id, Status: status, Line: line})
		}
		n, err := drawers.MarkAnchors(ctx, t.TeamID, verdicts)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]any{"marked": n}), nil
	})
}

// registerRecallStats adds recall_stats: is the memory being used, and does it
// answer? Drawer counts say how much is remembered; this says whether remembering
// is working, which is the only question an operator can act on.
func registerRecallStats(reg *registrar, drawers *palace.Service, usageSvc *usage.Service, scopeSearchToWing bool) {
	tool := newTool("recall_stats",
		mcp.WithDescription("How well memory is working, per wing: searches run, how many came back with something, drawers held, and the recent queries that found NOTHING (the memories the team looked for and does not have). Use it to see whether recall is earning its keep rather than guessing."),
		mcp.WithString("wing", mcp.Description("Only report this wing. Omitted, scoped to this registration's default_wing only when one is configured and SEARCH_SCOPE is not workspace; otherwise every wing. Pass \"*\" for every wing deliberately."), searchWingProperty()),
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
		wing, err := searchWingFor(ctx, req.GetString("wing", ""), scopeSearchToWing)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		stats, err := drawers.RecallStats(ctx, t.TeamID, wing, time.Duration(hours)*time.Hour, req.GetInt("unanswered", 10))
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
				// The fused score above is a rank encoding under FUSION=rrf, so it is
				// nearly constant and cannot say whether recall is working. This one can:
				// ADR-001 measured the cross-encoder separating answerable from
				// unanswerable by ~4.7 in median where cosine distance separated them by
				// 0.022. Reported with its own count, because averaging it over searches
				// no cross-encoder touched would divide real logits by a denominator of
				// not-measured zeros.
				"avg_top_rerank_score": w.AvgTopRerank,
				"reranked":             w.Reranked,
				// WHY the cross-encoder did not order a page, per reason. Omitted
				// when there is nothing to report, so an always-present empty object
				// does not read as "measured, and it was fine". `reranked` alone is 0
				// for a disabled reranker and one failing on every query.
				"rerank_skips": w.RerankSkips,
				"drawers":      w.Drawers,
				"last_used":    w.LastUsed,
				"last_filed":   w.LastFiled,
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
			"suggestions":  stats.Suggestions,
			"hint":         "answered_pct climbing over weeks means the palace is learning the questions this team actually asks; a wing with drawers and no searches is written-to and never read. suggestions collapses the unanswered queries into a to-write list: each entry is one memory this team looked for and does not have, with how many times it was asked and which wing to file it in.",
		}), nil
	})
}

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
	reg.addWrite(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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

// wingMerger is the palace work merge_wing performs, declared here at the
// consumer so a test can drive the real handler against a recording double
// instead of a database. *palace.Service satisfies it, as does mergejob.Merger's
// implementation — the two interfaces are deliberately separate because each
// belongs to the package that calls it.
type wingMerger interface {
	MergeWing(ctx context.Context, teamID string, sources []string, target string) (palace.MergeWingResult, error)
	RecomputeGraph(ctx context.Context, teamID, wing string, prune bool) (palace.RecomputeResult, error)
}

// registerMergeWing: fold one or more source wings into a target, relabeling every
// drawer and closet, then rebuild the derived graph.
//
// Synchronous by decision (M, 2026-08-24), superseding the 2026-06-29 call that
// put merge+rebuild in a durable background job because the rebuild is slow.
// That decision still governs the DASHBOARD, which enqueues a mergejob and shows
// progress; an agent calling this tool has no such queue to watch, and a merge
// that returns before the graph is rebuilt hands back a palace whose hallways
// describe a layout that no longer holds.
func registerMergeWing(reg *registrar, drawers wingMerger, usageSvc *usage.Service) {
	tool := newTool("merge_wing",
		mcp.WithDescription("Merge one or more source wings into a target wing, relabeling every drawer and closet in place, then rebuild hallways/tunnels. If the graph rebuild fails after the relabel, re-run am_recompute_graph."),
		mcp.WithArray("sources", mcp.Required(),
			mcp.Description("The wing names to fold into the target."),
			mcp.Items(map[string]any{"type": "string"}),
		),
		mcp.WithString("target", mcp.Required(), mcp.Description("The wing to merge the sources into (created if new).")),
	)
	reg.addWrite(tool, mergeWingHandler(drawers, usageSvc))
}

// mergeWingHandler is the merge_wing body, named rather than inlined into the
// registration for the same reason writeGuard is: a test can then drive the real
// handler, and a re-implemented handler in a test proves the test.
//
// ⚠ORDER IS THE CONTRACT. MergeWing relabels the rows; RecomputeGraph derives
// hallways and tunnels FROM those rows. Rebuilding first and relabeling second
// compiles, passes a call-counting check, and leaves exactly the stale graph this
// pair exists to prevent — so the ordering is asserted behaviourally, not by
// counting calls in the source.
func mergeWingHandler(drawers wingMerger, usageSvc *usage.Service) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
		if _, err := drawers.RecomputeGraph(ctx, t.TeamID, "", true); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf(
				"relabeled %d drawers / %d closets, but graph rebuild failed: %v — re-run recompute_graph",
				res.Drawers, res.Closets, err)), nil
		}
		return jsonResult(res), nil
	}
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
