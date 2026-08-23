---
date: 2026-08-21
category: security
severity: critical
files_changed:
  - internal/mcpserver/server.go
  - internal/mcpserver/skills.go
  - internal/mcpserver/admin.go
  - internal/mcpserver/drawers.go
  - internal/mcptest/harness.go
tags: [authorization, privilege-escalation, mcp, role, declared-vs-selecting]
---

## Symptom

No symptom. Nothing failed, nothing logged, no test went red. A workspace member holding the
least-privileged role could call `am_delete_drawer`, `am_merge_wing`, `am_kg_invalidate` and eleven
other mutating tools over MCP and every call succeeded, while the dashboard showed them a role
described as read-only. The only observable trace was `am_status` cheerfully reporting
`"role": "member"` in the same response as a successful write.

## Context

`internal/mcpserver` registers 41 MCP tools, each funnelling through `registrar.add`
(`server.go:67`) and calling a shared admission function `admit` (`server.go:210`) before doing any
work. `internal/tenant` resolves the caller from their API key. `internal/web` is the dashboard,
where the same three roles govern about twenty actions.

The MCP surface is the one agents actually use; the dashboard is the one humans occasionally visit.
Authorization was enforced on the second and not the first.

## Root Cause

The role was resolved and never consumed. `tenantFromKey` (`internal/tenant/tenant.go:305`) does the
work correctly, including the safe default:

```go
func (r *Repo) tenantFromKey(ctx context.Context, key APIKey) Tenant {
	role := RoleMember
	var m Membership
	if err := r.db.WithContext(ctx).
		Where("team_id = ? AND user_id = ?", key.TeamID, key.UserID).
		First(&m).Error; err == nil && m.Role != "" {
		role = Role(m.Role)
	}
	return Tenant{TeamID: key.TeamID, UserID: key.UserID, Role: role}
}
```

And the shared admission every tool calls never looked at it:

```go
func admit(ctx context.Context, usageSvc *usage.Service) (tenant.Tenant, *mcp.CallToolResult, bool) {
	t, ok := auth.TenantFrom(ctx)          // authentication: yes
	if !ok { /* … */ }
	st, err := usageSvc.Allow(ctx, t.TeamID) // metering: yes
	// authorization: nothing. t.Role is returned to the caller and never tested.
	return t, nil, true
}
```

Exactly one tool checked the role — `am_update_skill`, through `skillCaller.CanWrite`
(`skills.go:27`) — and its predicate was correct. The predicate was never the problem. It had one
consumer out of the fourteen that needed it.

## Investigation

The finding did not come from a bug report, a test, or a review of this code. It came from asking a
structural question about the whole repository.

Twelve defects fixed in this codebase over one week were sorted by root cause rather than by subject
line. Twelve different subjects — a tool schema, a privacy leak, telemetry, a config field, docs, an
eval arm — resolved to one cause: *a set of DECLARED things and a set of SELECTING/READING/RECEIVING
things drift apart, and nothing compares the two sets.* Sixteen bespoke gates existed, each written
after its own instance shipped, and no one could say which axes had no gate at all, because the
inventory of axes was nowhere written down.

So the axes were enumerated — seven areas in parallel, plus one agent whose only task was to find
what the others missed. That last agent asked: which MCP tools mutate, and which consult
`tenant.Role`? It reported `grep -rn "CanWrite\|RoleWriter\|RoleAdmin" internal/mcpserver/` returning
two lines, both in `skills.go`.

Three checks turned a plausible claim into a confirmed one, and the second and third mattered:

1. **Is the role real on this path, or a leftover field?** `ResolveToken` → `tenantFromKey` reads a
   membership row per call. Real, and least-privileged by default.
2. **Can a member actually obtain such a key?** `postRotateKey` (`internal/web/keys.go:72`) is gated
   on membership alone, with a comment saying so deliberately — correct for a key whose privileges
   match the member's, which is precisely the assumption that had stopped holding.
3. **Is the gap uniform, or did someone reason about it?** `am_delete_wing` is registered only in
   local mode (`admin.go:162`), under a comment about who is on the far end of a shared deployment.
   That single exception is what distinguishes a considered design from an oversight: someone
   thought it through for one tool and did not generalise. Had every mutating tool been ungated with
   no such comment anywhere, "roles are advisory on MCP" would have been a live reading.

The adversary agent also overstated the blast radius — it listed `am_delete_wing` among the exposed
tools without noticing the local-mode registration. Verifying each claim against the source rather
than accepting the report is what kept the ADR's Context accurate.

## Fix

### Before

```go
// Every tool, mutating or not, registered the same way:
func (r *registrar) add(tool mcp.Tool, handler server.ToolHandlerFunc) {
	r.srv.AddTool(tool, handler)
	r.catalog = append(r.catalog, CatalogEntry{Name: tool.Name, Description: tool.Description})
}
```

### After

```go
// addWrite registers a tool that CHANGES state, refusing the call when the
// caller's role does not permit writing.
func (r *registrar) addWrite(tool mcp.Tool, handler server.ToolHandlerFunc) {
	r.srv.AddTool(tool, writeGuard(tool.Name, handler))
	r.catalog = append(r.catalog, CatalogEntry{Name: tool.Name, Description: tool.Description, Write: true})
}

func writeGuard(name string, handler server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t, ok := auth.TenantFrom(ctx)
		if !ok {
			return mcp.NewToolResultError("unauthenticated: present a valid Bearer token"), nil
		}
		if !canWrite(t.Role) {
			return mcp.NewToolResultError(/* names the role and the remedy */), nil
		}
		return handler(ctx, req)
	}
}

// canWrite is the one definition of "may change stored memory", so the MCP
// surface and the dashboard cannot drift into two different policies.
func canWrite(role tenant.Role) bool {
	return role == tenant.RoleWriter || role == tenant.RoleAdmin
}
```

The guard is placed at the registration rather than in each handler because a per-handler check is a
thing every future handler must remember — which is how this arose: one handler remembered and
thirteen did not. Now the classification and the enforcement are the same act. Registering a
mutating tool with `add` IS forgetting the check, and `TestEveryMutatingToolIsRegisteredAsAWrite`
walks the AST of every `register*` function and fails on it.

`skillCaller.CanWrite` was rewritten to delegate to `canWrite` rather than restate the predicate, so
the two surfaces cannot come to different conclusions about the same word.

One mutant is worth recording because it initially survived: deleting the unauthenticated branch
entirely. A missing tenant yields a zero `Tenant`, whose empty role fails `canWrite`, so the call is
still refused — correctly, but with a message telling an anonymous caller their *role* is
insufficient. The test now asserts the refusal names the right reason, because failing closed and
explaining correctly are two properties and only one of them is free.

## Lesson

When a request carries an identity attribute — a role, a scope, a tenant, a plan tier — count its
consumers, not its resolvers. Resolution is visible and gets reviewed; consumption is diffuse and
one call site is indistinguishable from all of them. If the value is returned to the caller (here,
in `am_status`) while exactly one code path tests it, that asymmetry IS the bug, and no behavioural
test will show it because every test runs as the privileged role that makes the check pass.

And enforce at the registration, not in the handler: a check each new handler must remember is a
check that will eventually be forgotten, and the forgetting produces no symptom at all.
