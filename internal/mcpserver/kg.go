package mcpserver

import (
	"context"
	"fmt"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/usage"

	"github.com/mark3labs/mcp-go/mcp"
)

// kgQueryDefaultStatus is the endedness kg_query applies when the caller names
// none. It is ONE literal in ONE place on purpose: ADR-026 flips it from "all" to
// "current" as a separate, revertible commit, and reverting must be this line and
// nothing else. Anything that needs to branch on the default is a sign the filter
// logic leaked out of palace.KGQuery, where it belongs.
const kgQueryDefaultStatus = palace.KGStatusCurrent

// kgStatusParamDescription is BUILT from the palace constants rather than
// restating them, so a status the service accepts can never drift from the list
// the agent is told about.
var kgStatusParamDescription = fmt.Sprintf(
	"Which half of a fact's life to return: %q (open-ended, not retracted), %q (retracted — the audit direction), or %q. Default %q. This is filtered SERVER-SIDE, so what it removes never reaches your context; it selects on whether a fact was ever ended, which is a different question from as_of's \"was it in effect at that moment\", and the two compose.",
	palace.KGStatusCurrent, palace.KGStatusEnded, palace.KGStatusAll, kgQueryDefaultStatus)

// registerKG wires the temporal knowledge-graph tools: kg_add / kg_invalidate
// (write facts and end them), kg_query / kg_timeline (read, optionally as-of a
// point in time), and kg_stats. All are tenant-scoped via admit.
func registerKG(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	registerKGAdd(reg, drawers, usageSvc)
	registerKGInvalidate(reg, drawers, usageSvc)
	registerKGQuery(reg, drawers, usageSvc)
	registerKGStats(reg, drawers, usageSvc)
	registerKGTimeline(reg, drawers, usageSvc)
	registerEntryPoint(reg, drawers, usageSvc)
}

func registerKGAdd(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("kg_add",
		mcp.WithDescription("Add a fact (subject → predicate → object) to the temporal knowledge graph, optionally with a validity window. Re-adding an identical current fact is a no-op; to replace a fact, invalidate the old one first. SCOPE: graph facts are WORKSPACE-wide, not scoped to a wing — unlike drawers, anchors and search, a fact filed from one project is returned to every project in this workspace. File a fact here when it is true of the workspace; put project-specific detail in a drawer, which is wing-scoped."),
		mcp.WithString("subject", mcp.Required(), mcp.Description(fmt.Sprintf("The fact's subject entity. A SHORT LABEL (max %d characters), not a sentence — the entity is a node the graph is queried by, so put explanation in a drawer and point at it with source_drawer_id.", palace.MaxKGValueLen))),
		mcp.WithString("predicate", mcp.Required(), mcp.Description(fmt.Sprintf("The relationship (e.g. \"works_at\"). A safe name: max %d characters, and no \"/\", \"\\\\\" or \"..\" — it is validated like a name, not stored like a value, so \"uses/abuses\" is rejected.", palace.MaxNameLength))),
		mcp.WithString("object", mcp.Required(), mcp.Description(fmt.Sprintf("The fact's object entity. A SHORT LABEL (max %d characters), not a sentence — evidence, commit ids and repro steps belong in a drawer referenced by source_drawer_id, never smuggled in here.", palace.MaxKGValueLen))),
		mcp.WithString("valid_from", mcp.Description("Optional start of validity (YYYY-MM-DD or YYYY-MM-DDTHH:MM:SSZ).")),
		mcp.WithString("valid_to", mcp.Description("Optional end of validity; omit while the fact is current.")),
		mcp.WithString("source_closet", mcp.Description("Optional closet id this fact came from.")),
		mcp.WithString("source_file", mcp.Description("Optional source label.")),
		mcp.WithString("source_drawer_id", mcp.Description("Optional drawer id this fact was extracted from.")),
	)
	reg.addWrite(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		subject, err := req.RequireString("subject")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		predicate, err := req.RequireString("predicate")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		object, err := req.RequireString("object")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		res, err := drawers.KGAdd(ctx, t.TeamID, subject, predicate, object,
			req.GetString("valid_from", ""), req.GetString("valid_to", ""),
			req.GetString("source_closet", ""), req.GetString("source_file", ""), req.GetString("source_drawer_id", ""))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]any{"success": true, "triple_id": res.TripleID, "fact": res.Fact}), nil
	})
}

func registerKGInvalidate(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("kg_invalidate",
		mcp.WithDescription("Mark a current fact as no longer true by ending its validity window. The fact is kept (queryable as-of an earlier time), not deleted."),
		mcp.WithString("subject", mcp.Required(), mcp.Description(fmt.Sprintf("The fact's subject entity. A SHORT LABEL (max %d characters), not a sentence — the entity is a node the graph is queried by, so put explanation in a drawer and point at it with source_drawer_id.", palace.MaxKGValueLen))),
		mcp.WithString("predicate", mcp.Required(), mcp.Description("The relationship.")),
		mcp.WithString("object", mcp.Required(), mcp.Description(fmt.Sprintf("The fact's object entity. A SHORT LABEL (max %d characters), not a sentence — evidence, commit ids and repro steps belong in a drawer referenced by source_drawer_id, never smuggled in here.", palace.MaxKGValueLen))),
		mcp.WithString("ended", mcp.Description("When it stopped being true (YYYY-MM-DD or datetime; default today).")),
	)
	reg.addWrite(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		subject, err := req.RequireString("subject")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		predicate, err := req.RequireString("predicate")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		object, err := req.RequireString("object")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		fact, ended, err := drawers.KGInvalidate(ctx, t.TeamID, subject, predicate, object, req.GetString("ended", ""))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]any{"success": true, "fact": fact, "ended": ended}), nil
	})
}

func registerKGQuery(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("kg_query",
		mcp.WithDescription("Query the knowledge graph by entity, by predicate, or both — optionally as of a point in time, in a chosen direction, and restricted to facts that are still current. Give at least one of entity/predicate. Facts are workspace-wide: this returns facts filed by any project in the workspace, not only this registration's."),
		mcp.WithString("entity", mcp.Description("The entity to look up. Optional when predicate is given.")),
		mcp.WithString("predicate", mcp.Description("Only facts with this relation. Given WITHOUT an entity it is an entry point in its own right, answering \"every fact of this relation\" — how you audit a whole relation type, e.g. every retracts edge. Given WITH an entity it narrows that entity's facts.")),
		mcp.WithString("as_of", mcp.Description("Only facts in effect at this instant (YYYY-MM-DD or datetime).")),
		mcp.WithString("direction", mcp.Description("\"outgoing\", \"incoming\", or \"both\" (default). Ignored without an entity: with predicate alone there is no queried endpoint for a fact to be incoming or outgoing of.")),
		mcp.WithString("status", mcp.Description(kgStatusParamDescription)),
	)
	reg.add(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		asOf := req.GetString("as_of", "")
		res, err := drawers.KGQuery(ctx, t.TeamID, palace.KGQueryInput{
			Entity:    req.GetString("entity", ""),
			Predicate: req.GetString("predicate", ""),
			AsOf:      asOf,
			Direction: req.GetString("direction", "both"),
			Status:    req.GetString("status", kgQueryDefaultStatus),
		})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		out := map[string]any{
			"facts": res.Facts, "count": len(res.Facts), "status": res.Status,
			// resolution is what separates "nothing is filed about this" from
			// "you asked about something this graph has never heard of". Both
			// used to arrive as count:0 with no error, so a caller could not act
			// on the difference and a pointer built on the second pointed nowhere.
			// It is rendered here, not merely set on the Go struct: a field no
			// handler emits is invisible to every agent, and no behavioural test
			// can see that.
			"resolution": string(res.Resolution),
		}
		// Named only when something actually failed to resolve, so the key's
		// presence is itself the signal rather than an empty string every caller
		// has to compare against.
		if res.Unresolved != "" {
			out["unresolved"] = res.Unresolved
		}
		// Each entry point is echoed only when it was used, so the response says
		// which question was asked rather than carrying an empty key for the one
		// that was not.
		if res.Entity != "" {
			out["entity"] = res.Entity
		}
		if res.Predicate != "" {
			out["predicate"] = res.Predicate
		}
		if asOf != "" {
			out["as_of"] = asOf
		}
		// A filtered page must say what it filtered rather than presenting itself
		// as the whole. ADR-010's argument is the reason this is never silent: a
		// session about to redo a rejected thing does not know to ask for history
		// — that is precisely what it does not know. So the keys appear only when
		// something was actually removed, which makes their presence informative,
		// and the hint names the parameter that brings it back.
		if res.Withheld > 0 {
			out["withheld"] = map[string]int64{res.WithheldStatus: res.Withheld}
			out["hint"] = fmt.Sprintf(
				"%d %s fact(s) not shown — pass status:%q to see them, or status:%q for both.",
				res.Withheld, res.WithheldStatus, res.WithheldStatus, palace.KGStatusAll)
		}
		return jsonResult(out), nil
	})
}

func registerKGStats(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("kg_stats",
		mcp.WithDescription("Knowledge-graph overview: entity and fact totals, current vs expired facts, and the relationship types in use."),
	)
	reg.add(tool, func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		stats, err := drawers.KGStats(ctx, t.TeamID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(stats), nil
	})
}

func registerKGTimeline(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("kg_timeline",
		mcp.WithDescription("Chronological timeline of facts (validity start order), for one entity or — with no entity — across the whole graph. Facts are workspace-wide, so a timeline may include facts filed by another project in this workspace."),
		mcp.WithString("entity", mcp.Description("Restrict to facts touching this entity (default: all).")),
	)
	reg.add(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		facts, label, err := drawers.KGTimeline(ctx, t.TeamID, req.GetString("entity", ""))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]any{"entity": label, "timeline": facts, "count": len(facts)}), nil
	})
}

// registerEntryPoint exposes a wing's front door.
//
// The reg.add call is the line that makes it REACHABLE, and the catalogue entry
// that call produces is what makes it DISCOVERABLE — an agent consults the
// catalogue, and a tool the handler serves but the catalogue omits is one nobody
// will ever call.
func registerEntryPoint(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("entry_point",
		mcp.WithDescription("Where to START in a wing. Returns the wing's entry node and what it points at, so a session needs no id from a skill file and no multi-hop walk to begin. Edges whose target is not readable from this wing are dropped and counted in refused, never listed. A wing with no entry point says so, distinguishably from an error."),
		mcp.WithString("wing", mcp.Required(), mcp.Description("The wing whose entry point to resolve.")),
	)
	reg.add(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		res, err := drawers.EntryPoint(ctx, t.TeamID, req.GetString("wing", ""))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		out := map[string]any{
			"wing": res.Wing, "node": res.Node, "edges": res.Edges,
			"resolution": string(res.Resolution),
		}
		// A refusal count of zero is the normal case and stays out of the
		// response; when present it says "the front door holds more than you
		// were shown", which is the one fact a filtered listing owes its reader.
		if res.Refused > 0 {
			out["refused"] = res.Refused
		}
		return jsonResult(out), nil
	})
}
