// inspect.go implements the two operator introspection subcommands: `projects`
// (list every workspace in the database) and `inspect` (report what one
// workspace, named by slug, actually holds). They answer the question you get
// before every support or migration task — "is there anything in this project's
// slug, and where is it?" — without opening a sqlite shell and remembering which
// of the fifteen tables is keyed by team_id and which by namespace.
//
// Like `share` and `set-plan`, these are superadmin CLIs rather than HTTP routes:
// they run against the same local SQLite the server uses, so possessing that
// database (shell access on the host) IS the authorization. They are also
// strictly read-only — every query is a COUNT or a SELECT, so running them
// against production data cannot change it.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"

	"github.com/urfave/cli/v3"
	"gorm.io/gorm"
)

// scopedTable describes one tenant-partitioned table: the label to print, the
// table to count, and the column that carries the workspace id.
//
// The column is NOT always team_id — `vectors` partitions by `namespace` (the
// team id under a different name, because the vector store seam is tenant-
// agnostic) and `share_requests` names two different workspaces per row. Encoding
// that per table is what lets one count function serve all of them.
// memory marks a table as the workspace's actual recallable content, as opposed
// to the account scaffolding (a member row, an API key) that every workspace has
// simply by existing. The two are reported separately because they answer
// different questions: a workspace is never truly "empty" — creating it writes a
// membership — so a bare row total would say "yes, something is here" about a
// project that in fact remembers nothing.
type scopedTable struct {
	label  string
	table  string
	column string
	memory bool
}

// teamScopedTables is the full inventory of per-workspace data, ordered the way
// an operator reads it: identity and access first, then the memory itself, then
// the operational rows. Adding a tenant-partitioned table to the schema means
// adding one line here — that single list is the reason this file has no
// per-table code.
//
// Deliberately absent: `teams` (the workspace row itself, reported in the
// header), `users` (global, joined to a workspace only through memberships) and
// `skillset` (a single global row, not tenant data).
var teamScopedTables = []scopedTable{
	{"drawers", "drawers", "team_id", true},
	{"closets", "closets", "team_id", true},
	{"hallways", "hallways", "team_id", true},
	{"tunnels", "tunnels", "team_id", true},
	{"kg entities", "kg_entities", "team_id", true},
	{"kg triples", "kg_triples", "team_id", true},
	{"skills", "skills", "team_id", true},
	{"vectors", "vectors", "namespace", true},
	{"members", "memberships", "team_id", false},
	{"api keys", "api_keys", "team_id", false},
	{"usage counters", "usage_counters", "team_id", false},
	{"merge jobs", "merge_jobs", "team_id", false},
	{"subscriptions", "subscriptions", "team_id", false},
	{"share requests (outgoing)", "share_requests", "from_team_id", false},
	{"share requests (incoming)", "share_requests", "to_team_id", false},
}

// projectsCommand lists every workspace in the database. It is the companion to
// `inspect`: you cannot inspect a slug you cannot spell, and the slug is a
// url-safe handle the operator rarely has to hand.
func projectsCommand(def config.Config) *cli.Command {
	return &cli.Command{
		Name:  "projects",
		Usage: "List every workspace in the database (slug, name, drawer count)",
		Flags: dataFlags(def),
		Action: func(ctx context.Context, c *cli.Command) error {
			return listProjects(ctx, configFromCmd(c, def))
		},
	}
}

// listProjects prints one row per workspace with its drawer count, so an empty
// project is visible at a glance before anyone bothers to inspect it.
func listProjects(ctx context.Context, cfg config.Config) error {
	svc, err := buildServices(cfg)
	if err != nil {
		return err
	}

	var teams []tenant.Team
	if err := svc.gdb.WithContext(ctx).Order("created_at").Find(&teams).Error; err != nil {
		return fmt.Errorf("list workspaces: %w", err)
	}
	if len(teams) == 0 {
		fmt.Println("no workspaces in this database")
		return nil
	}

	drawers, err := drawerCounts(ctx, svc.gdb)
	if err != nil {
		return err
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SLUG\tNAME\tKIND\tDRAWERS\tCREATED\tID")
	for _, t := range teams {
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\n",
			t.Slug, t.Name, t.Kind, drawers[t.ID], t.CreatedAt, t.ID)
	}
	return w.Flush()
}

// drawerCounts tallies drawers per workspace in one grouped query rather than a
// count per team, so listing stays a fixed two queries no matter how many
// workspaces the database holds.
func drawerCounts(ctx context.Context, gdb *gorm.DB) (map[string]int64, error) {
	var rows []struct {
		TeamID string
		N      int64
	}
	if err := gdb.WithContext(ctx).
		Table("drawers").
		Select("team_id, count(*) as n").
		Group("team_id").
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("count drawers: %w", err)
	}
	counts := make(map[string]int64, len(rows))
	for _, r := range rows {
		counts[r.TeamID] = r.N
	}
	return counts, nil
}

// inspectCommand reports what a single workspace holds, addressed by the slug
// the dashboard shows. It is the "does this project have anything in the
// database?" check — run it before a migration, a wing share, or a deletion.
func inspectCommand(def config.Config) *cli.Command {
	return &cli.Command{
		Name:  "inspect",
		Usage: "Report what a workspace holds in the database, by slug (read-only)",
		Flags: append(dataFlags(def),
			&cli.StringFlag{Name: "slug", Required: true, Usage: "workspace slug to inspect"},
		),
		Action: func(ctx context.Context, c *cli.Command) error {
			return inspectProject(ctx, configFromCmd(c, def), c.String("slug"))
		},
	}
}

// inspectProject resolves the slug and counts every tenant-partitioned table for
// it. An unknown slug is an error, not an empty report: the caller asked whether
// something exists, and "no such workspace" and "workspace with no data" are
// different answers, so they get different exit codes (main log.Fatals on error).
func inspectProject(ctx context.Context, cfg config.Config, slug string) error {
	svc, err := buildServices(cfg)
	if err != nil {
		return err
	}

	team, err := svc.tenants.TeamBySlug(ctx, slug)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Name the recovery path rather than only the failure — the slug is
			// easy to mistype and `projects` is how you find the right one.
			return fmt.Errorf("no workspace with slug %q (run `agentsmemory projects` to list them)", slug)
		}
		return fmt.Errorf("workspace %q: %w", slug, err)
	}

	// The plan is part of "what this workspace is", and a workspace with no plan
	// attached reads as "none" rather than failing the whole report.
	plan := "none"
	if p, err := svc.tenants.PlanForTeam(ctx, team.ID); err == nil {
		plan = fmt.Sprintf("%s (cap %s)", p.Name, capText(p.MonthlyRequestCap))
	}

	fmt.Printf("workspace  %s\n", team.Slug)
	fmt.Printf("name       %s\n", team.Name)
	fmt.Printf("id         %s\n", team.ID)
	fmt.Printf("kind       %s\n", team.Kind)
	fmt.Printf("plan       %s\n", plan)
	fmt.Printf("created    %s\n\n", team.CreatedAt)

	counts := make(map[string]int64, len(teamScopedTables))
	var memoryRows, otherRows int64
	for _, t := range teamScopedTables {
		n, err := countScoped(ctx, svc.gdb, t, team.ID)
		if err != nil {
			return err
		}
		counts[t.label] = n
		if t.memory {
			memoryRows += n
		} else {
			otherRows += n
		}
	}

	// Two blocks, memory first, because that is what the question is about. A
	// blank line between them ends the tabwriter column block, so each section's
	// columns align to its own widest label instead of to the widest overall.
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	printSection(w, "MEMORY", memoryRows, counts, true)
	fmt.Fprintln(w)
	printSection(w, "ACCOUNT / OPS", otherRows, counts, false)
	if err := w.Flush(); err != nil {
		return err
	}

	// The headline answer, spelled out. The per-table zeros above already say it,
	// but the operator ran this to learn one thing, so state it in one sentence —
	// and "no memory" is the answer they came for, so it must not read as "empty
	// database" when the account rows are still there.
	fmt.Println()
	if memoryRows == 0 {
		fmt.Printf("workspace %q exists but holds NO memory — only %d account/ops row(s)\n", slug, otherRows)
		return nil
	}
	fmt.Printf("workspace %q holds %d memory row(s) (+%d account/ops)\n", slug, memoryRows, otherRows)
	return nil
}

// printSection writes one titled block of the report: the tables whose memory
// flag matches, then the block's subtotal. It exists so the two blocks cannot
// drift apart in formatting.
func printSection(w *tabwriter.Writer, title string, subtotal int64, counts map[string]int64, memory bool) {
	fmt.Fprintf(w, "%s\tROWS\n", title)
	for _, t := range teamScopedTables {
		if t.memory == memory {
			fmt.Fprintf(w, "  %s\t%d\n", t.label, counts[t.label])
		}
	}
	fmt.Fprintf(w, "  subtotal\t%d\n", subtotal)
}

// countScoped counts one table's rows for one workspace. The table and column
// names come only from the teamScopedTables literal above — never from user
// input — so interpolating them into the query is safe; the workspace id, which
// does come from the command line, stays a bound parameter.
func countScoped(ctx context.Context, gdb *gorm.DB, t scopedTable, teamID string) (int64, error) {
	var n int64
	if err := gdb.WithContext(ctx).
		Table(t.table).
		Where(t.column+" = ?", teamID).
		Count(&n).Error; err != nil {
		return 0, fmt.Errorf("count %s: %w", t.table, err)
	}
	return n, nil
}
