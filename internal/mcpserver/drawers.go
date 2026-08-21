package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/usage"

	"github.com/mark3labs/mcp-go/mcp"
)

// registerDrawers wires the core memory-loop tools — the WRITE/FILE, SEARCH/RECALL
// and the remaining STATUS/ADMIN families of the Python contract — onto the MCP
// server. Every handler shares the admit() preamble (auth + monthly metering) and
// is scoped to the resolved tenant's TeamID, so a token can only ever touch its
// own workspace's memories.
func registerDrawers(reg *registrar, drawers *palace.Service, usageSvc *usage.Service, scopeSearchToWing bool) {
	registerAddDrawer(reg, drawers, usageSvc)
	registerGetDrawer(reg, drawers, usageSvc)
	registerUpdateDrawer(reg, drawers, usageSvc)
	registerDeleteDrawer(reg, drawers, usageSvc)
	registerListDrawers(reg, drawers, usageSvc, scopeSearchToWing)
	registerSearch(reg, drawers, usageSvc, scopeSearchToWing)
	registerCheckDuplicate(reg, drawers, usageSvc)
	registerListWings(reg, drawers, usageSvc)
	registerListRooms(reg, drawers, usageSvc, scopeSearchToWing)
	registerGetTaxonomy(reg, drawers, usageSvc)
	registerGetAAAKSpec(reg, drawers, usageSvc)
	registerReconnect(reg, drawers, usageSvc)
}

// drawerView is the agent-facing JSON shape of a drawer. It omits TeamID (the
// caller already knows its own scope) and gives every field an explicit snake_case
// tag so the wire format is stable regardless of Go field names.
type drawerView struct {
	ID          string   `json:"id"`
	Wing        string   `json:"wing"`
	Room        string   `json:"room"`
	SourceFile  string   `json:"source_file"`
	ChunkIndex  int      `json:"chunk_index"`
	Content     string   `json:"content"`
	Entities    []string `json:"entities,omitempty"`
	ParentID    string   `json:"parent_id,omitempty"`
	FiledAt     string   `json:"filed_at"`
	ContentDate string   `json:"content_date,omitempty"`
}

// toView projects a domain Drawer onto its wire shape.
func toView(d palace.Drawer) drawerView {
	return drawerView{
		ID:          d.ID,
		Wing:        d.Wing,
		Room:        d.Room,
		SourceFile:  d.SourceFile,
		ChunkIndex:  d.ChunkIndex,
		Content:     d.Content,
		Entities:    d.Entities,
		ParentID:    d.ParentID,
		FiledAt:     d.FiledAt,
		ContentDate: d.ContentDate,
	}
}

// jsonResult marshals v into a text tool result. A marshal failure is an internal
// bug, not an agent error, so it is surfaced as a tool error rather than panicking.
func jsonResult(v any) *mcp.CallToolResult {
	out, err := json.Marshal(v)
	if err != nil {
		return mcp.NewToolResultError("internal: failed to encode result")
	}
	return mcp.NewToolResultText(string(out))
}

// registerAddDrawer: file a verbatim memory. Oversized content is chunked, each
// chunk embedded and stored; the response reports the drawers created.
func registerAddDrawer(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("add_drawer",
		mcp.WithDescription("File a verbatim memory (drawer) into a wing/room. Content over ~800 chars is chunked into multiple drawers; re-adding the same source is idempotent."),
		mcp.WithString("wing", mcp.Description("Project namespace the memory belongs to. Optional when this MCP was registered for a project — then it defaults to that project's wing.")),
		mcp.WithString("room", mcp.Required(), mcp.Description("Aspect within the wing, e.g. \"backend\" or \"decisions\".")),
		mcp.WithString("content", mcp.Required(), mcp.Description("The verbatim text to remember — stored exactly, never summarised.")),
		mcp.WithString("source_file", mcp.Description("Optional provenance of the content (a path or label).")),
		mcp.WithString("content_date", mcp.Description("Optional date the memory is about (e.g. 2026-06-26).")),
		mcp.WithArray("code_anchors", mcp.Description(
			"Optional: pin this memory to the code it is about, as [{\"path\":\"internal/x/y.go\",\"snippet\":\"<verbatim lines>\",\"repo\":\"<optional label>\"}]. "+
				"Paste the exact code, NOT a line number — line numbers move on every edit above them. When the snippet later "+
				"disappears from the file, search marks this memory STALE instead of letting the next session act on a fact "+
				"that stopped being true. Anchor whenever a memory explains a specific piece of code.")),
		mcp.WithBoolean("confirm_new_wing", mcp.Description(
			"Set true to file an inbox item into a wing that holds no memories yet. Without it that "+
				"combination is refused, because it is what an undeliverable handoff looks like: a "+
				"target wing named for the direction of travel (wing_to-x) instead of the project "+
				"(wing_x) is a wing no session will ever look in. Pass it when the project really "+
				"has no memories filed for it yet.")),
	)
	reg.addWrite(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		wing, err := wingFor(ctx, req.GetString("wing", ""))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		room, err := req.RequireString("room")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		content, err := req.RequireString("content")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		// An inbox item into a wing that holds nothing is what an undeliverable
		// handoff looks like — see handoffRefusal. Refuse here, while the filer
		// still has the context to correct the name; nobody notices later.
		if refusal := handoffRefusal(ctx, drawers, t.TeamID, wing, room,
			req.GetBool("confirm_new_wing", false)); refusal != "" {
			return mcp.NewToolResultError(refusal), nil
		}
		created, err := drawers.Add(ctx, t.TeamID, palace.AddInput{
			Wing:        wing,
			Room:        room,
			Content:     content,
			SourceFile:  req.GetString("source_file", ""),
			ContentDate: req.GetString("content_date", ""),
		})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		views := make([]drawerView, len(created.Drawers))
		for i, d := range created.Drawers {
			views[i] = toView(d)
		}
		out := map[string]any{"ok": true, "chunks": len(created.Drawers), "drawers": views}

		// Anchors pin the FIRST chunk: it is the parent handle for a multi-chunk
		// write, and the one search returns as the memory's identity.
		if anchors := parseAnchors(req.GetArguments()["code_anchors"]); len(anchors) > 0 && len(created.Drawers) > 0 {
			n, err := drawers.AddAnchors(ctx, t.TeamID, created.Drawers[0].ID, anchors)
			if err != nil {
				// The memory is already filed; an anchor failure must not present
				// as a failed write, so it is reported beside the success.
				out["anchor_error"] = err.Error()
			} else {
				out["code_anchors"] = n
			}
		}
		if created.PendingEmbedding {
			// Say it in a field the caller can branch on AND in prose it will read
			// out loud. A memory that is stored but not yet searchable looks
			// identical to a healthy one from here, and the operator is the only
			// one who can fix what caused it.
			out["pending_embedding"] = true
			out["warning"] = pendingEmbeddingWarning
		}
		return jsonResult(out), nil
	})
}

// parseAnchors reads the code_anchors argument: a list of {path, snippet, repo}
// objects. It is tolerant by design — an unparseable entry is skipped rather than
// failing the write, because the memory itself is worth more than its anchor.
func parseAnchors(raw any) []palace.AnchorInput {
	out, _, _ := parseAnchorList(raw)
	return out
}

// parseAnchorList is parseAnchors with the one distinction the tolerant version
// cannot make: whether the argument was a LIST at all.
//
// Tolerance is right where it was written — an unreadable entry means "no
// anchors added" and the memory is worth more than its anchor. It is wrong at a
// REPLACE, where the same empty result means "delete the anchors this memory
// already has". `code_anchors: {…}` instead of `[{…}]` is an ordinary mistake for
// an LLM caller, and without this the write path would treat it as a deliberate
// clear and report success — an unknown recorded as a definite negative,
// destroying what it was built to protect. This repository has fixed that exact
// shape at the read end already.
//
// A genuine `[]` still reads fine, so a deliberate clear is unaffected.
//
// It also reports how many entries were SENT, which is the other half of the
// same distinction. A non-empty list whose entries are all malformed parses to
// readable-and-empty and would otherwise read as a deliberate clear — and since
// most callers send exactly one anchor, "every entry malformed" is usually just
// "the one anchor I sent had a typo", the likeliest way to get an entry wrong at
// all. The caller asked to SET anchors, none could be read, and deleting the
// existing ones is the opposite of the intent.
func parseAnchorList(raw any) ([]palace.AnchorInput, bool, int) {
	list, ok := raw.([]any)
	if !ok {
		return nil, false, 0
	}
	out := make([]palace.AnchorInput, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		path, _ := m["path"].(string)
		snippet, _ := m["snippet"].(string)
		repo, _ := m["repo"].(string)
		if strings.TrimSpace(path) == "" || strings.TrimSpace(snippet) == "" {
			continue
		}
		out = append(out, palace.AnchorInput{Repo: repo, Path: path, Snippet: snippet})
	}
	return out, true, len(list)
}

// anchorReplacement decides what a code_anchors argument means at a REPLACE,
// returning either the anchors to write or a non-empty refusal to send back.
//
// It exists as a function rather than inline at the call site because the two
// refusals are the whole behaviour: a source-grep check that the call site
// "looks right" passes happily against a guard disarmed with && false, which is
// the same component-tested/selection-untested shape this file already fixed
// once. A function can be driven by a test; a call site can only be read.
//
// Both refusals protect the same thing — an argument the caller got wrong must
// not delete the anchors a memory already has. An unreadable VALUE (an object
// where a list belongs) and an unreadable LIST (every entry malformed, which is
// most often the single anchor sent having a typo) both parse to nothing, and
// nothing is indistinguishable from a deliberate []. A list with one bad row
// among several is not this case: it is readable, something survived, and the
// bad row is dropped.
func anchorReplacement(raw any) ([]palace.AnchorInput, string) {
	anchors, readable, sent := parseAnchorList(raw)
	if !readable {
		return nil, "code_anchors must be a LIST of {path, snippet, repo?} objects; the value sent could not be " +
			"read as one. Refusing rather than clearing, because an unreadable argument and a " +
			"deliberate \"remove the anchors\" look identical once parsed — send [] if you meant to clear them"
	}
	if sent > 0 && len(anchors) == 0 {
		return nil, fmt.Sprintf(
			"code_anchors carried %d entr(ies) and none could be read — each needs a non-empty "+
				"\"path\" and \"snippet\". Refusing rather than clearing: you asked to set anchors, "+
				"so deleting the ones this memory has would be the opposite of that. Send [] if you "+
				"meant to remove them", sent)
	}
	return anchors, ""
}

// pendingEmbeddingWarning is the one sentence a caller must pass on when a write
// was stored without its vector: the memory is safe, it is simply not findable
// yet, and something outside this server has to be fixed for that to change.
const pendingEmbeddingWarning = "stored, but NOT searchable yet: the embedder could not be reached, " +
	"so this memory is queued for background indexing. It will become searchable once the embedder is " +
	"running again (check the Ollama server the agentsmemory server points at). Tell the user."

// registerGetDrawer: fetch one drawer by id.
func registerGetDrawer(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("get_drawer",
		mcp.WithDescription("Fetch a drawer by its id. A memory longer than ~1600 characters is stored as several chunks and a search returns the ONE that matched; pass whole=true to get every chunk of that memory, in order, so you can read the note as it was written."),
		mcp.WithString("id", mcp.Required(), mcp.Description("The drawer id returned by am_add_drawer or am_search.")),
		mcp.WithBoolean("whole", mcp.Description("Return every chunk of the memory this drawer belongs to, in order, instead of just this one. Any chunk's id works — you do not need the first.")),
	)
	reg.add(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		id, err := req.RequireString("id")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		// A chunked memory had no read path that could return it whole: the query
		// existed (repo.MemoryChunks) and was called only by update and delete.
		// An agent handed one chunk of a long note had no second call to complete
		// it, which is why collapsing a search page to one hit per memory could
		// not ship on its own.
		if req.GetBool("whole", false) {
			chunks, err := drawers.GetMemory(ctx, t.TeamID, id)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			views := make([]drawerView, 0, len(chunks))
			for _, c := range chunks {
				views = append(views, toView(c))
			}
			return jsonResult(map[string]any{"chunks": views, "count": len(views)}), nil
		}
		d, err := drawers.Get(ctx, t.TeamID, id)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(toView(d)), nil
	})
}

// registerUpdateDrawer: edit a drawer's content/wing/room in place. Only the
// fields actually supplied are changed; a changed drawer is re-embedded.
func registerUpdateDrawer(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("update_drawer",
		mcp.WithDescription("Update a drawer's content, wing, or room in place (its id is unchanged). Only supplied fields are modified."),
		mcp.WithString("id", mcp.Required(), mcp.Description("The drawer id to update.")),
		mcp.WithString("content", mcp.Description("New verbatim content (re-embedded on change).")),
		mcp.WithString("wing", mcp.Description("Move the drawer to this wing.")),
		mcp.WithString("room", mcp.Description("Move the drawer to this room.")),
		mcp.WithArray("code_anchors", mcp.Description(
			"REPLACE this memory's code anchors, as [{\"path\":\"internal/x/y.go\",\"snippet\":\"<verbatim lines>\",\"repo\":\"<optional label>\"}]. "+
				"Send [] to remove them all. Omit the field to leave them untouched. "+
				"Correcting a memory without re-anchoring it leaves the old anchor pinned to text that "+
				"changed, so the staleness check meant to protect the memory is what marks the correction "+
				"out of date — that is the case this exists for.")),
	)
	reg.addWrite(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		id, err := req.RequireString("id")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		// Distinguish "field omitted" from "field set to empty" by presence in the
		// raw arguments, so update_drawer only touches what the agent actually sent.
		args := req.GetArguments()
		patch := palace.DrawerPatch{}
		if _, ok := args["content"]; ok {
			v := req.GetString("content", "")
			patch.Content = &v
		}
		if _, ok := args["wing"]; ok {
			v := req.GetString("wing", "")
			patch.Wing = &v
		}
		if _, ok := args["room"]; ok {
			v := req.GetString("room", "")
			patch.Room = &v
		}
		// Anchors are REPLACED, not merged. This exists for the case that
		// motivated it: a memory is corrected, and its old anchor still pins the
		// old text — so the staleness check meant to protect the memory is what
		// marks the correction out of date. Merging would leave both live.
		//
		// Validated BEFORE anything is written. The first version updated the
		// drawer and then checked the anchors, so a call carrying new content and
		// a malformed anchor list changed the content and returned an error
		// announcing that it had refused — a caller reading that error and
		// retrying would apply the content twice. An argument the caller got
		// wrong must leave the memory as it found it.
		raw, wantsAnchors := args["code_anchors"]
		var anchors []palace.AnchorInput
		if wantsAnchors {
			var refusal string
			if anchors, refusal = anchorReplacement(raw); refusal != "" {
				return mcp.NewToolResultError(refusal), nil
			}
		}

		d, err := drawers.Update(ctx, t.TeamID, id, patch)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if wantsAnchors {
			n, aerr := drawers.ReplaceAnchors(ctx, t.TeamID, id, anchors)
			if aerr != nil {
				return mcp.NewToolResultError(aerr.Error()), nil
			}
			return jsonResult(map[string]any{"drawer": toView(d), "code_anchors": n}), nil
		}
		return jsonResult(toView(d)), nil
	})
}

// registerDeleteDrawer: remove a drawer (row + vector) by id.
func registerDeleteDrawer(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("delete_drawer",
		mcp.WithDescription("Delete a memory by the id of any of its drawers (removes every chunk's metadata and embedding). A memory over the chunk size is several drawers sharing a parent, and deleting one of them would leave the rest live and searchable with nothing to belong to, so all of them go."),
		mcp.WithString("id", mcp.Required(), mcp.Description("The drawer id to delete.")),
	)
	reg.addWrite(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		id, err := req.RequireString("id")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		n, err := drawers.Delete(ctx, t.TeamID, id)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]any{"ok": true, "deleted": id, "chunks_deleted": n}), nil
	})
}

// registerListDrawers: paginate a team's drawers, optionally filtered by wing/room.
func registerListDrawers(reg *registrar, drawers *palace.Service, usageSvc *usage.Service, scopeSearchToWing bool) {
	tool := newTool("list_drawers",
		mcp.WithDescription("List drawers (newest first), optionally narrowed to a wing and/or room, with limit/offset paging. Naming no wing lists the wing this MCP registration was created for; pass \"*\" to list every wing."),
		mcp.WithString("wing", mcp.Description("Only drawers in this wing. Omitted, the listing is scoped to the wing this registration was created for, exactly as a recall is — enumeration and recall must agree, or one of them leaks. Pass \"*\" to list every wing.")),
		mcp.WithString("room", mcp.Description("Only drawers in this room.")),
		mcp.WithNumber("limit", mcp.Description("Max drawers to return (default 50).")),
		mcp.WithNumber("offset", mcp.Description("Number of drawers to skip (default 0).")),
	)
	reg.add(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		// Resolved exactly as a recall is. am_search has scoped to the
		// registration's wing since scoping landed and this did not, so a listing
		// that named no wing enumerated EVERY wing — including other projects'
		// inboxes, which is the call am_status recommends to a waking agent. A
		// scope one enumeration route ignores is not a scope.
		wing, err := searchWingFor(ctx, req.GetString("wing", ""), scopeSearchToWing)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		list, err := drawers.List(ctx, t.TeamID,
			wing, req.GetString("room", ""),
			req.GetInt("limit", 50), req.GetInt("offset", 0))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		views := make([]drawerView, len(list))
		for i, d := range list {
			views[i] = toView(d)
		}
		return jsonResult(map[string]any{"drawers": views, "count": len(views)}), nil
	})
}

// searchHitView is one ranked search result: the drawer plus its scores.
type searchHitView struct {
	drawerView
	Score       float64 `json:"score"`        // fused hybrid rank (vector + BM25 + closet boost), higher is better
	BM25        float64 `json:"bm25_score"`   // raw lexical BM25 component, for transparency
	ClosetBoost float64 `json:"closet_boost"` // closet rank boost folded into score, for transparency
	Distance    float64 `json:"distance"`     // raw cosine distance in [0,2], lower is closer
	// RerankScore is the cross-encoder's relevance for this hit, present only when
	// a reranker is configured (omitted otherwise so an unconfigured deployment's
	// results are byte-identical to before). It is reported rather than folded
	// into score because the two are not on the same scale — an agent reading the
	// page should be able to see which signal moved a hit.
	RerankScore float64 `json:"rerank_score,omitempty"`
	// Reranked says a cross-encoder actually SCORED this hit. It exists because
	// rerank_score's absence was ambiguous four ways: no reranker configured, a
	// reranker at weight 0, a hit below the pool cutoff that was never scored, and
	// a cross-encoder that genuinely returned 0.0 all produced the same missing
	// key. The domain has carried this bool since ADR-006 T4 made the telemetry
	// honest; the agent-facing surface discarded it, so the one reader who acts on
	// the answer could not see it.
	Reranked bool `json:"reranked,omitempty"`
	// ChunksMatched is how many chunks of this memory were in the ranked pool.
	// A memory that matched in four places is stronger evidence than one that
	// matched in one, and ADR-013's collapse would otherwise destroy that signal
	// silently — which it did, for exactly as long as this field existed in the
	// domain and not on the wire.
	ChunksMatched int `json:"chunks_matched,omitempty"`
	// Truncated says the content above is a snippet around the match, not the
	// whole memory — fetch it with am_get_drawer when the snippet is not enough.
	Truncated  bool `json:"content_truncated,omitempty"`
	FullLength int  `json:"content_length,omitempty"`
	// Anchors are the code this memory was written about, with the verdict of the
	// last verification pass. Stale is the summary an agent should branch on.
	Anchors []anchorView `json:"code_anchors,omitempty"`
	Stale   bool         `json:"stale,omitempty"`
}

// anchorView is one code anchor as search reports it.
type anchorView struct {
	Path      string `json:"path"`
	Status    string `json:"status"` // unchecked | verified | drifted | missing
	Line      int    `json:"line,omitempty"`
	CheckedAt string `json:"checked_at,omitempty"`
}

// registerSearch: hybrid recall over a team's drawers — vector candidates
// re-ranked by a vector+BM25 blend (closet boost joins with the mining phase).
func registerSearch(reg *registrar, drawers *palace.Service, usageSvc *usage.Service, scopeSearchToWing bool) {
	tool := newTool("search",
		mcp.WithDescription("Semantically recall drawers most similar to a query. Optionally filter by wing/room and a max cosine distance."),
		mcp.WithString("query", mcp.Required(), mcp.Description("What to recall (max 250 chars).")),
		mcp.WithNumber("limit", mcp.Description("Max results, 1-100 (default 5).")),
		mcp.WithString("wing", mcp.Description("Restrict to this wing. Omitted, a recall is scoped to the wing this MCP registration was created for, so one project's memories do not answer another's. Pass another wing to look there instead, or \"*\" to search EVERY wing — worth doing when the question is about something shared, such as an infrastructure decision that explains an application's behaviour. SEARCH_SCOPE=workspace makes searching everything the default.")),
		mcp.WithString("room", mcp.Description("Restrict to this room.")),
		mcp.WithNumber("max_distance", mcp.Description("Drop results farther than this cosine distance (0-2, default 1.5; 0 disables).")),
		mcp.WithNumber("snippet_chars", mcp.Description(
			"How much of each hit's text to return, as a window centred on the match (default 400). "+
				"Recall is paid for in your context window: a page of full-length memories costs several thousand tokens, "+
				"and most of it is text you did not need. Pass 0 for whole memories, or fetch any single one in full with am_get_drawer.")),
		mcp.WithString("context", mcp.Description("Optional background context — what you are working on. Sharpens re-ranking when a reranker is configured; ignored otherwise. It does not change which drawers are retrieved, only how they are ordered.")),
	)
	reg.add(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		query, err := req.RequireString("query")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		wing, err := searchWingFor(ctx, req.GetString("wing", ""), scopeSearchToWing)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		hits, err := drawers.Search(ctx, t.TeamID, palace.SearchQuery{
			Query:       query,
			Wing:        wing,
			Room:        req.GetString("room", ""),
			Limit:       req.GetInt("limit", palace.DefaultSearchLimit),
			MaxDistance: req.GetFloat("max_distance", palace.DefaultMaxDistance),
			Context:     req.GetString("context", ""),
		})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		snippetChars := req.GetInt("snippet_chars", palace.DefaultSnippetChars)
		views := make([]searchHitView, len(hits))
		ids := make([]string, len(hits))
		for i, h := range hits {
			views[i] = searchHitView{drawerView: toView(h.Drawer), Score: h.Score, BM25: h.BM25, ClosetBoost: h.ClosetBoost, Distance: h.Distance, RerankScore: h.RerankScore, Reranked: h.Reranked, ChunksMatched: h.ChunksMatched}
			ids[i] = h.Drawer.ID
			if snippetChars > 0 {
				// The window is centred on the query's own terms, so what comes
				// back is the part that matched rather than the memory's heading.
				// chunk 0 (and an unsplit memory) carries the identity line, so its
				// opening is preserved even when the match is further down.
				isHead := h.Drawer.ChunkIndex == 0
				if snippet := palace.SnippetWithHead(h.Drawer.Content, query, snippetChars, isHead); snippet != h.Drawer.Content {
					views[i].Content = snippet
					views[i].Truncated = true
					views[i].FullLength = len([]rune(h.Drawer.Content))
				}
			}
		}
		// Staleness travels WITH the memory. A recalled sentence about code that
		// has since changed is the one failure mode a confident agent cannot catch
		// on its own — it reads as knowledge either way.
		stale := 0
		if anchors, err := drawers.AnchorsForDrawers(ctx, t.TeamID, ids); err == nil {
			for i := range views {
				for _, a := range anchors[ids[i]] {
					views[i].Anchors = append(views[i].Anchors, anchorView{
						Path: a.Path, Status: a.Status, Line: a.Line, CheckedAt: a.CheckedAt,
					})
					if a.Stale() {
						views[i].Stale = true
					}
				}
				if views[i].Stale {
					stale++
				}
			}
		}
		out := map[string]any{"hits": views, "count": len(views)}
		if stale > 0 {
			out["stale_hits"] = stale
			out["warning"] = "some hits are marked STALE: the code they were written about has changed since. " +
				"Re-read that code before acting on the memory, and re-file the memory if it is now wrong."
		}
		return jsonResult(out), nil
	})
}

// registerCheckDuplicate: is content near-identical to an existing drawer?
func registerCheckDuplicate(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("check_duplicate",
		mcp.WithDescription("Check whether content is near-identical to an existing drawer before filing it."),
		mcp.WithString("content", mcp.Required(), mcp.Description("The candidate content to test.")),
		mcp.WithNumber("threshold", mcp.Description("Cosine-similarity threshold for a duplicate (default 0.9).")),
	)
	reg.add(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		content, err := req.RequireString("content")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		res, err := drawers.CheckDuplicate(ctx, t.TeamID, content, req.GetFloat("threshold", palace.DefaultDupThreshold))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		out := map[string]any{"is_duplicate": res.IsDuplicate, "similarity": res.Similarity}
		if res.Drawer != nil {
			out["drawer"] = toView(*res.Drawer)
		}
		return jsonResult(out), nil
	})
}

// registerListWings: per-wing drawer/room counts.
func registerListWings(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("list_wings",
		mcp.WithDescription("List the team's wings with how many drawers and distinct rooms each holds."),
	)
	reg.add(tool, func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		wings, err := drawers.Wings(ctx, t.TeamID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]any{"wings": wings, "count": len(wings)}), nil
	})
}

// registerListRooms: per-room drawer counts, optionally within one wing.
func registerListRooms(reg *registrar, drawers *palace.Service, usageSvc *usage.Service, scopeSearchToWing bool) {
	tool := newTool("list_rooms",
		mcp.WithDescription("List the team's rooms with drawer counts, optionally restricted to one wing."),
		mcp.WithString("wing", mcp.Description("Only rooms within this wing.")),
	)
	reg.add(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		// Resolved through searchWingFor, not taken raw. Audited 2026-08-20 by
		// RUNNING it against two projects in one workspace: naming no wing
		// enumerated every wing, so one project's room names and drawer counts were disclosed to another. am_search and
		// am_list_drawers resolve identically, and an enumeration that does not is
		// a hole in the scope those two enforce.
		wing, err := searchWingFor(ctx, req.GetString("wing", ""), scopeSearchToWing)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		rooms, err := drawers.Rooms(ctx, t.TeamID, wing)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]any{"rooms": rooms, "count": len(rooms)}), nil
	})
}

// registerGetTaxonomy: the wing -> rooms tree with counts.
func registerGetTaxonomy(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("get_taxonomy",
		mcp.WithDescription("Return the team's memory taxonomy: every wing with its rooms and counts."),
	)
	reg.add(tool, func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		tax, err := drawers.GetTaxonomy(ctx, t.TeamID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(tax), nil
	})
}

// registerGetAAAKSpec: the static AAAK dialect reference. It needs no storage, so
// it still meters (to keep tool behaviour uniform) but reads nothing per-team.
func registerGetAAAKSpec(reg *registrar, _ *palace.Service, usageSvc *usage.Service) {
	tool := newTool("get_aaak_spec",
		mcp.WithDescription("Return the AAAK compressed-memory dialect spec agents use for diary and closet lines."),
	)
	reg.add(tool, func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if _, errResult, ok := admit(ctx, usageSvc); !ok {
			return errResult, nil
		}
		return jsonResult(map[string]any{"spec": palace.AAAKSpec}), nil
	})
}

// registerReconnect: a liveness probe over the tenant's vector namespace. In this
// stateless server it has no cached client to drop (unlike the Python tool); it
// re-readies the namespace and confirms the backend is reachable.
func registerReconnect(reg *registrar, drawers *palace.Service, usageSvc *usage.Service) {
	tool := newTool("reconnect",
		mcp.WithDescription("Re-ready the workspace's vector store and confirm the backend is reachable (a stateless liveness probe)."),
	)
	reg.addWrite(tool, func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		if err := drawers.Reconnect(ctx, t.TeamID); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return jsonResult(map[string]any{"ok": true, "note": "stateless server: namespace re-readied, backend reachable"}), nil
	})
}
