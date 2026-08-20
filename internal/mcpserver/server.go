// Package mcpserver wires the agentsmemory tools onto a mark3labs/mcp-go server
// exposed over Streamable HTTP, so remote agents connect to it as their memory
// MCP server. Every tool is tenant-scoped: it reads the tenant the auth layer
// placed on the context, meters the call against the workspace's monthly request
// cap, and fails closed when there is no tenant or the cap is exhausted.
//
// Registered so far: status (liveness + tenant echo), load_skill (the
// centralised-skill read path), the core memory loop (drawer CRUD, semantic
// recall, taxonomy), and the agent diary (diary_write/diary_read). The remaining
// Python-contract tools (mine and the graph/KG families) slot in here the same
// way as later phases land.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/internal/auth"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/skill"
	"github.com/atvirokodosprendimai/agentsmemory/internal/skillset"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/atvirokodosprendimai/agentsmemory/internal/usage"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// toolPrefix namespaces every agentsmemory MCP tool (am_status, am_search,
// am_list_wings, …). It exists so this server can run alongside other memory
// MCPs — notably mempalace, which exposes same-named tools (search, add_drawer,
// list_wings, diary_write, kg_add) — without the client seeing two tools of the
// same name. The prefix is applied in exactly one place (newTool), so every
// registration site keeps the bare, readable name.
const toolPrefix = "am_"

// newTool builds a tool with the agentsmemory prefix applied to its name: callers
// pass the bare name and the wire name becomes am_<name>. This is the single
// chokepoint that guarantees every registered tool is prefixed.
func newTool(name string, opts ...mcp.ToolOption) mcp.Tool {
	return mcp.NewTool(toolPrefix+name, opts...)
}

// CatalogEntry is one registered tool's wire metadata: its prefixed name and the
// one-line description an agent reads to decide whether to call it. It is the unit
// am_skillset returns so a waking agent sees the live tool surface.
type CatalogEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// registrar wraps the MCP server and accumulates the tool catalogue as tools are
// registered. Every register* funnels its AddTool through registrar.add, so the
// catalogue is, by construction, EXACTLY the set of registered tools — never a
// hand-maintained copy that drifts when a tool is added, renamed, or re-described.
// This is what lets am_skillset advertise the real surface with zero upkeep.
type registrar struct {
	srv     *server.MCPServer
	catalog []CatalogEntry
}

// add registers a tool and records its catalogue entry in one step, so a tool can
// never be exposed without also being advertised (and vice versa). Description is
// read off the built tool, so it stays in sync with the WithDescription text.
func (r *registrar) add(tool mcp.Tool, handler server.ToolHandlerFunc) {
	r.srv.AddTool(tool, handler)
	r.catalog = append(r.catalog, CatalogEntry{Name: tool.Name, Description: tool.Description})
}

// WorkspaceLookup resolves the workspace a session is scoped to. It is declared
// here, at the consumer, so the MCP layer depends on the one method it needs
// rather than on the whole tenant repository — and so a test can name a workspace
// without a database.
type WorkspaceLookup interface {
	// TeamByID returns the workspace with this id.
	TeamByID(ctx context.Context, id string) (tenant.Team, error)
}

// Deps are the collaborators the tools need. Passing them in (rather than
// reaching for globals) keeps the server testable and the wiring explicit.
type Deps struct {
	Skills   *skill.Service
	Skillset *skillset.Service // the global wakeup playbook am_skillset serves
	Usage    *usage.Service
	Drawers  *palace.Service

	// Workspaces names the workspace am_status reports. Optional: a nil lookup
	// simply omits the workspace block, so the wake-up call never depends on it.
	Workspaces WorkspaceLookup

	// ScopeSearchToWing narrows a recall that names no wing to the wing this
	// registration was created for. See config.SearchScope for why the default
	// binds reads as well as writes.
	ScopeSearchToWing bool

	// Local is true when this process serves the single self-hosted workspace
	// (server --local). am_status reports it as the session's mode, which is what
	// lets an agent tell "my own machine" from "the hosted server" without
	// inspecting its own config — the check a protocol gate actually needs. It also
	// widens the tool surface to operations that are only safe when the agent, the
	// operator and the workspace are one person on one machine — see registerAdmin.
	Local bool
}

// New builds the MCP server and registers all tools. Registration funnels through
// a registrar so the live tool catalogue is captured as a side effect — see
// registrar. am_skillset is registered LAST so its handler advertises the full
// surface (every tool above it, plus itself).
func New(deps Deps) *server.MCPServer {
	srv := server.NewMCPServer(
		"agentsmemory",
		"0.1.0",
		server.WithToolCapabilities(true), // advertise the tools/list capability
	)
	reg := &registrar{srv: srv}
	registerAll(reg, deps)
	return srv
}

// registerAll wires every tool onto a registrar. It is split out of New so a
// test can hold the registrar afterwards and read the catalogue it built:
// registration only constructs tools and closures, so this runs with nil
// services and no database, which is what makes the tool surface itself
// assertable rather than something a reader has to count by hand.
func registerAll(reg *registrar, deps Deps) {
	registerStatus(reg, deps.Drawers, deps.Usage, deps.Workspaces, deps.Local)
	registerLoadSkill(reg, deps.Skills, deps.Usage)
	// Skill-registry management: list + update (write is role-gated).
	registerSkills(reg, deps.Skills, deps.Usage)
	// The core memory loop: drawer CRUD, semantic recall, and palace taxonomy.
	registerDrawers(reg, deps.Drawers, deps.Usage, deps.ScopeSearchToWing)
	// The agent diary: append-only journal entries (diary_write/diary_read).
	registerDiary(reg, deps.Drawers, deps.Usage)
	// Mining: text -> chunked drawers + closet index (mine).
	registerMine(reg, deps.Drawers, deps.Usage)
	// The navigable graph: hallways, tunnels, traverse, recompute_graph.
	registerGraph(reg, deps.Drawers, deps.Usage)
	// The temporal knowledge graph: kg_add/invalidate/query/stats/timeline.
	registerKG(reg, deps.Drawers, deps.Usage)
	// Palace maintenance: merge_wing, memories_filed_away, and delete_wing when local.
	registerAdmin(reg, deps.Drawers, deps.Usage, deps.Local)
	// Recall measurement: how well the memory answers, per wing.
	registerRecallStats(reg, deps.Drawers, deps.Usage)
	// Staleness: pin memories to code, and record what verification found.
	registerAnchors(reg, deps.Drawers, deps.Usage)
	// The wakeup playbook: how to use everything above. Registered last so its
	// catalogue is complete.
	registerSkillset(reg, deps.Skillset, deps.Usage)
}

// wingFor resolves the wing a write belongs to: the one the caller passed, or —
// when it passed none — the one this MCP registration was created for.
//
// The fallback is what keeps projects apart without depending on an agent
// remembering a convention. A per-project registration states its wing once (see
// auth.WingHeader) and every write from that project lands there; an agent that
// does name a wing still wins, because an explicit argument is a decision and a
// default is only a default.
//
// The error names both routes, since a caller with neither has two different
// things it could fix.
func wingFor(ctx context.Context, passed string) (string, error) {
	if strings.TrimSpace(passed) == "" {
		if def := auth.DefaultWingFrom(ctx); def != "" {
			return palace.SanitizeName(def, "wing")
		}
		return "", fmt.Errorf("wing is required: pass one, or register this MCP with a default wing "+
			"(header %s) so every write from this project files itself", auth.WingHeader)
	}
	return palace.SanitizeName(passed, "wing")
}

// searchWingFor resolves the wing a RECALL is scoped to. An explicit argument
// always wins; otherwise the registration's default applies when the deployment
// asked for wing-scoped search, and an empty string (every wing) when it did not.
//
// It is deliberately separate from wingFor, which serves writes: a write with no
// wing is an ERROR because a memory must land somewhere nameable, while a search
// with no wing is a legitimate request to look everywhere. The two questions
// only look alike.
func searchWingFor(ctx context.Context, passed string, scoped bool) (string, error) {
	if w := strings.TrimSpace(passed); w != "" {
		// "*" asks for every wing the caller can see. Scoping made the empty
		// argument mean "my project", which silently removed the only way to ask
		// a cross-project question — and those are real: an infrastructure
		// decision explains a deploy failure in the application it hosts. A
		// default is only defensible when it can be overridden per call.
		if w == "*" {
			return "", nil
		}
		return palace.SanitizeName(w, "wing")
	}
	if !scoped {
		return "", nil
	}
	if def := auth.DefaultWingFrom(ctx); def != "" {
		return palace.SanitizeName(def, "wing")
	}
	// Registered without a wing: there is nothing to narrow to, and refusing
	// would break every caller that never had one.
	return "", nil
}

// admit resolves the tenant and meters one request against the workspace's
// monthly cap. It returns the tenant on success, or a ready-to-return error
// result (and ok=false) when the caller is unauthenticated, the meter fails, or
// the cap is exhausted. Centralising this keeps every tool's preamble identical.
func admit(ctx context.Context, usageSvc *usage.Service) (tenant.Tenant, *mcp.CallToolResult, bool) {
	t, ok := auth.TenantFrom(ctx)
	if !ok {
		return tenant.Tenant{}, mcp.NewToolResultError("unauthenticated: present a valid Bearer token"), false
	}
	st, err := usageSvc.Allow(ctx, t.TeamID)
	if err != nil {
		return tenant.Tenant{}, mcp.NewToolResultError("usage metering failed"), false
	}
	if !st.Allowed {
		return tenant.Tenant{}, mcp.NewToolResultError(
			fmt.Sprintf("monthly request cap reached (%d/%d) — upgrade the project's plan", st.Used, st.Cap),
		), false
	}
	return t, nil, true
}

// registerStatus adds the status tool: the wake-up call. Beyond liveness and the
// session's team/role/quota, it returns the team's memory overview — total
// drawers and the wing -> rooms taxonomy with counts — so an agent grounds itself
// in the shape of its memory before searching, mirroring mempalace's status. The
// taxonomy read is best-effort: a status call still succeeds (with an empty
// overview) if the aggregation fails, so liveness never depends on it.
func registerStatus(reg *registrar, drawers *palace.Service, usageSvc *usage.Service, workspaces WorkspaceLookup, local bool) {
	tool := newTool("status",
		mcp.WithDescription("Wake-up call: the workspace this MCP session is scoped to (name, slug, and whether the server is self-hosted or hosted) plus your role, the memory overview (total drawers + the wing→rooms taxonomy with counts), and remaining monthly quota. Check the workspace to confirm you are talking to the palace you think you are — an empty wing list means nothing has been written yet, NOT that you are in the wrong place."),
	)
	reg.add(tool, func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		st, _ := usageSvc.Snapshot(ctx, t.TeamID)

		// Memory overview. Best-effort: an aggregation error leaves an empty
		// overview rather than failing the wake-up call.
		tax, _ := drawers.GetTaxonomy(ctx, t.TeamID)
		total := 0
		for _, w := range tax.Wings {
			total += w.Drawers
		}

		// Workspace identity. An agent's protocol gate needs to know WHICH palace
		// it is talking to before it recalls or writes — a token from another
		// project answers every probe happily, and the wing list cannot tell that
		// apart from a wing nobody has written to yet. Naming the workspace and
		// the mode here is what makes that check possible without guessing.
		// Best-effort, like the taxonomy above: a lookup failure omits the block
		// rather than failing the wake-up call.
		mode := "hosted"
		if local {
			mode = "local"
		}
		// The wing this registration files into, if it was registered for a
		// project. An agent that can see it does not have to guess whether its
		// writes are landing in the right place.
		defaultWing := auth.DefaultWingFrom(ctx)

		// What is waiting in this session's own wing. Best-effort like the blocks
		// around it: a counting failure reports as unknown rather than as a zero,
		// and never fails the wake-up call.
		inboxCount, inboxErr := 0, error(nil)
		if defaultWing != "" {
			inboxCount, inboxErr = drawers.InboxCount(ctx, t.TeamID, defaultWing, inboxRoom)
		}
		inbox := inboxStatus(defaultWing, inboxCount, inboxErr)

		var workspace map[string]any
		if workspaces != nil {
			if team, err := workspaces.TeamByID(ctx, t.TeamID); err == nil {
				workspace = map[string]any{
					"id":   team.ID,
					"slug": team.Slug,
					"name": team.Name,
					"kind": team.Kind,
				}
			}
		}

		out, _ := json.Marshal(map[string]any{
			"ok":            true,
			"team_id":       t.TeamID,
			"role":          string(t.Role),
			"mode":          mode,
			"workspace":     workspace,
			"default_wing":  defaultWing,
			"total_drawers": total,
			"wings":         tax.Wings, // [{wing, drawers, rooms:[{wing, room, drawers}]}]
			"inbox":         inbox,
			"usage": map[string]any{
				"used_this_month": st.Used,
				"monthly_cap":     st.Cap,
				"remaining":       st.Remaining(),
			},
			// Point the agent at the rest of the wake-up loop — and, when something
			// is waiting, at that first. The hint changes with the inbox because a
			// line that is always there is a line nobody reads.
			"hint": statusHint(inbox),
		})
		return mcp.NewToolResultText(string(out)), nil
	})
}

// registerLoadSkill adds the load_skill tool: an agent passes a skill name and
// receives the centralised, team-shared skill body. Read access for any member.
func registerLoadSkill(reg *registrar, skills *skill.Service, usageSvc *usage.Service) {
	tool := newTool("load_skill",
		mcp.WithDescription("Load a centralised, team-shared skill by name. Returns the skill body and version so the calling agent can use it directly."),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("The unique skill name within the team, e.g. \"effective-go\"."),
		),
	)
	reg.add(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, errResult, ok := admit(ctx, usageSvc)
		if !ok {
			return errResult, nil
		}
		name, err := req.RequireString("name")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		res, err := skills.Load(ctx, t.TeamID, name)
		if err != nil {
			// A missing skill is a normal outcome for the agent — surface it as
			// a tool error, not a transport failure.
			return mcp.NewToolResultError(err.Error()), nil
		}
		out, _ := json.Marshal(res)
		return mcp.NewToolResultText(string(out)), nil
	})
}
