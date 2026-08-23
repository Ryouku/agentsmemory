package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/urfave/cli/v3"
)

// doctorCommand reports whether the palace's stores still agree with each other.
//
// It is read-only and its verdict is its EXIT CODE, so it belongs in a pre-deploy
// script or a cron rather than in somebody's memory. The defect it exists for
// produced no error, no warning and no log line: a wing merge relabelled drawer
// rows and left every stored payload behind, and because a scoped search filters
// at the index on that payload, 13 memories on a live palace became unreachable
// from the wing they were filed in — while an unscoped search still returned
// them, so nothing looked broken.
func doctorCommand(def config.Config) *cli.Command {
	return &cli.Command{
		Name:  "doctor",
		Usage: "Check the palace for inconsistencies between the stores (read-only; exit 1 when something is wrong)",
		Flags: append(dataFlags(def),
			// --local mirrors serve's, because without it doctor checks a backend
			// nobody runs: `--local` is what switches the search index to chromem,
			// and a self-hosted operator who started the server with it and then
			// ran `doctor --index` was having a bare SQLite store inspected while
			// chromem served every query. The check exited 0 on a broken palace.
			&cli.BoolFlag{Name: "local", Sources: cli.EnvVars("AGENTSMEMORY_LOCAL"), Usage: "self-hosted single-workspace mode — must match how the server was started, or a different backend is checked"},
			&cli.StringFlag{Name: "project", Value: "local", Usage: "workspace slug to check"},
			&cli.BoolFlag{Name: "index", Usage: "check that every stored point's wing matches its drawer's"},
			&cli.BoolFlag{Name: "graph", Usage: "report what the derived graph WOULD hold if every drawer were run through the entity extractor now (read-only)"},
			&cli.BoolFlag{Name: "roles", Usage: "count active API keys that resolve to the read-only role because no membership row records what they may do"},
			&cli.StringFlag{Name: "windows", Usage: "report every candidate snippet window for this QUERY against --drawer, and which one search returns (read-only)"},
			&cli.StringFlag{Name: "drawer", Usage: "the memory id --windows reports on"},
		),
		Action: func(ctx context.Context, c *cli.Command) error {
			if !c.Bool("index") && !c.Bool("graph") && !c.Bool("roles") && c.String("windows") == "" {
				return fmt.Errorf("nothing to check: pass --index, --graph, --roles or --windows")
			}
			cfg := configFromCmd(c, def)
			if q := c.String("windows"); q != "" {
				if err := doctorWindows(ctx, cfg, c.String("project"), q, c.String("drawer"), os.Stdout); err != nil {
					return err
				}
			}
			if c.Bool("graph") {
				if err := doctorGraph(ctx, cfg, c.String("project"), os.Stdout); err != nil {
					return err
				}
			}
			if c.Bool("roles") {
				if err := doctorRoles(ctx, cfg, os.Stdout); err != nil {
					return err
				}
			}
			if c.Bool("index") {
				return doctorIndex(ctx, cfg, c.String("project"), os.Stdout)
			}
			return nil
		},
	}
}

// doctorIndex runs the wing-payload drift check and reports it.
//
// It prints drawer ids and wing names and never memory text: a doctor report is
// pasted into an issue, and the palace it describes is private.
func doctorIndex(ctx context.Context, cfg config.Config, slug string, out io.Writer) error {
	if err := requireExistingDB(cfg.DBPath); err != nil {
		return err
	}
	// reconcile=false: a checker that rebuilt the index first would report on a
	// palace it had just repaired, and could not fail on the fault it exists for.
	svc, err := buildServicesWith(cfg, false)
	if err != nil {
		return err
	}
	team, err := resolveProject(ctx, svc, slug)
	if err != nil {
		return err
	}

	report, err := svc.drawers.IndexDrift(ctx, team.ID)
	if err != nil {
		return err
	}
	return reportDrift(out, report)
}

// reportDrift renders a drift report and returns the verdict as an error, so the
// exit code carries it. Separate from the lookup because the rendering is what
// an operator reads and the lookup needs a database — a report nobody can test
// is a report that quietly stops saying anything.
func reportDrift(out io.Writer, report palace.DriftReport) error {
	pending := ""
	if report.Pending > 0 {
		pending = fmt.Sprintf(" (%d more await a first embedding, which is a queue and not a fault)", report.Pending)
	}
	if report.Clean() {
		fmt.Fprintf(out, "index: %d drawer(s) checked, every stored point agrees with its row%s\n", report.Checked, pending)
		return nil
	}

	fmt.Fprintf(out, "index: %d drawer(s) checked, %d stored point(s) disagree with their row%s\n\n",
		report.Checked, report.Total, pending)
	for _, d := range report.Drifted {
		if d.Missing {
			fmt.Fprintf(out, "  %-16s %s  ABSENT — no point at all, filed in %q\n", d.Store, d.DrawerID, d.Actual)
			continue
		}
		fmt.Fprintf(out, "  %-16s %s  indexed as %q, filed in %q\n", d.Store, d.DrawerID, d.Indexed, d.Actual)
	}
	if report.Truncated() {
		fmt.Fprintf(out, "  … and %d more, not listed. The COUNT above is exact.\n", report.Total-len(report.Drifted))
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "A scoped recall filters at the index, on the payload above — so a mislabelled memory is")
	fmt.Fprintln(out, "UNREACHABLE from the wing it is filed in and answers only an unscoped search. An ABSENT one")
	fmt.Fprintln(out, "answers nothing at all: run `agentsmemory sync` to replay it from the source of truth.")
	return fmt.Errorf("%d stored point(s) disagree with their drawer", report.Total)
}

// doctorGraph reports what the derived graph would hold if the entity extractor
// ran over every drawer now.
//
// It changes nothing. It exists because the derived graph is empty on every
// palace populated through the agent write path, the obvious fix is to extract
// on write, and whether that fix WORKS is a property of the corpus rather than
// of the code — mining feeds the extractor long repetitive transcripts and
// agents file short deliberate notes.
func doctorGraph(ctx context.Context, cfg config.Config, slug string, out io.Writer) error {
	if err := requireExistingDB(cfg.DBPath); err != nil {
		return err
	}
	svc, err := buildServicesWith(cfg, false)
	if err != nil {
		return err
	}
	team, err := resolveProject(ctx, svc, slug)
	if err != nil {
		return err
	}
	report, err := svc.drawers.GraphPotential(ctx, team.ID)
	if err != nil {
		return err
	}
	return reportGraph(out, report)
}

// reportGraph renders the projection.
//
// The BAR is printed beside the number, always, because the number alone is what
// gets quoted: "39% would carry two entities" reads as a result, and it is only a
// result against a threshold somebody committed to beforehand.
func reportGraph(out io.Writer, report palace.GraphReport) error {
	fmt.Fprintf(out, "graph: %d drawer(s) examined — nothing was written\n\n", report.Drawers)
	fmt.Fprintf(out, "  %-26s %8s %10s %10s %10s\n", "wing", "drawers", ">=1 entity", ">=2", "hallways")
	fmt.Fprintf(out, "  %s\n", strings.Repeat("-", 68))
	for _, w := range report.Wings {
		fmt.Fprintf(out, "  %-26s %8d %10d %10d %10d\n", w.Wing, w.Drawers, w.WithAny, w.WithTwo, w.Hallways)
	}
	fmt.Fprintf(out, "  %s\n", strings.Repeat("-", 68))
	fmt.Fprintf(out, "  %-26s %8d %10d %10d %10d\n\n", "TOTAL", report.Drawers, report.WithAny, report.WithTwo, report.Hallways)

	fmt.Fprintf(out, "  %.1f%% of drawers would carry two or more entities, against a pre-registered bar of %.0f%%: %s\n",
		100*report.ViableShare(), 100*palace.GraphViabilityBar, verdictWord(report.Viable()))
	fmt.Fprintln(out, "  (a hallway needs a PAIR co-occurring in one drawer, so a drawer with one entity adds nothing)")

	for _, w := range report.Wings {
		if len(w.TopEntities) == 0 {
			continue
		}
		fmt.Fprintf(out, "\n  most frequent candidates in %s: %s\n", w.Wing, strings.Join(w.TopEntities, ", "))
	}
	return nil
}

// verdictWord states the decision the bar implies, so the reader does not have
// to compare two numbers to find out what was decided.
func verdictWord(viable bool) string {
	if viable {
		return "CLEARS the bar — extracting on the write path is worth its cost"
	}
	return "BELOW the bar — extracting on the write path would leave the graph empty for a subtler reason"
}

// requireExistingDB refuses to inspect a database that is not there.
//
// openDB CREATES a missing file and the migrations then fill it, so a mistyped
// --db made doctor build an empty palace and report it clean. "The path was
// wrong" and "the palace is healthy" must not be the same output.
func requireExistingDB(path string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("no database at %q — doctor inspects an existing palace and will not "+
			"create one; check --db (or AGENTSMEMORY_DB)", path)
	}
	return nil
}

// doctorWindows reports every candidate snippet window for a query against one
// memory, and which one search returns.
//
// It answers the question ADR-019 turns on, with data rather than intuition: when
// an agent gets the right memory and not the answer, is the answer in a window
// the chooser scored and threw away, or in no window at all? The first is fixable
// by showing more of the memory; the second is synthesis, and showing more buys
// nothing.
func doctorWindows(ctx context.Context, cfg config.Config, slug, query, drawerID string, out io.Writer) error {
	if err := requireExistingDB(cfg.DBPath); err != nil {
		return err
	}
	if drawerID == "" {
		return fmt.Errorf("--windows needs --drawer: a window report is about ONE memory, and picking " +
			"one for you would report on a memory you did not choose")
	}
	svc, err := buildServicesWith(cfg, false)
	if err != nil {
		return err
	}
	team, err := resolveProject(ctx, svc, slug)
	if err != nil {
		return err
	}
	d, err := svc.drawers.Get(ctx, team.ID, drawerID)
	if err != nil {
		return err
	}
	rep := palace.WindowReport(d.Content, query, palace.DefaultSnippetChars)

	fmt.Fprintf(out, "memory %s — %d runes, %d-rune window, %d candidate(s)\n\n",
		drawerID[:12], rep.Memory, rep.Window, len(rep.Windows))
	for _, w := range rep.Windows {
		mark := "  "
		if w.Chosen {
			mark = "->" // the one search actually returns
		}
		fmt.Fprintf(out, "%s [%5d,%5d) %d term(s)  %s\n", mark, w.Start, w.End, w.Terms, firstLineOf(w.Text, 88))
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "The marked window is what an agent receives. Read the others: if the answer to the")
	fmt.Fprintln(out, "query is in one of them, showing more of the memory fixes it. If it is in none of")
	fmt.Fprintln(out, "them, the answer is not in this memory and more windows would buy nothing.")
	return nil
}

// doctorRoles reports API keys that authenticate but may not write.
//
// The write guard refuses the least-privileged role, and tenantFromKey resolves
// to it whenever a key's membership row is absent or carries an empty role. That
// resolution predates the guard and was harmless under it: such a key wrote
// normally and am_status merely called it a member. Arming the guard turns it
// into a refusal at write time, per call, which is the worst place to find out.
//
// No current code path produces the condition, so this reports historical rows —
// and only an operator holding the database can see them. It takes no --project
// because the fault belongs to the deployment rather than to one workspace.
func doctorRoles(ctx context.Context, cfg config.Config, out io.Writer) error {
	if err := requireExistingDB(cfg.DBPath); err != nil {
		return err
	}
	svc, err := buildServicesWith(cfg, false)
	if err != nil {
		return err
	}
	gaps, err := svc.tenants.RoleGaps(ctx)
	if err != nil {
		return err
	}
	return reportRoleGaps(out, gaps)
}

// reportRoleGaps renders the role report and returns the verdict as an error so
// the exit code carries it. Split from the lookup for the same reason reportDrift
// is: the rendering is what an operator reads, and a report that needs a database
// to exercise is a report that quietly stops saying anything.
//
// It prints team slugs and counts, never key material and never a token prefix:
// a doctor report is pasted into an issue.
func reportRoleGaps(out io.Writer, gaps []tenant.RoleGap) error {
	if len(gaps) == 0 {
		fmt.Fprintln(out, "roles: every active API key has a membership row naming what it may do")
		return nil
	}

	total := 0
	for _, g := range gaps {
		total += g.Total()
	}
	fmt.Fprintf(out, "roles: %d active key(s) across %d workspace(s) resolve to the read-only role\n\n", total, len(gaps))
	fmt.Fprintf(out, "  %-28s %9s %9s\n", "workspace", "no row", "empty role")
	fmt.Fprintf(out, "  %s\n", strings.Repeat("-", 48))
	for _, g := range gaps {
		slug := g.Slug
		if slug == "" {
			slug = g.TeamID // a key whose team row is gone still counts
		}
		fmt.Fprintf(out, "  %-28s %9d %9d\n", slug, g.Missing, g.Empty)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Each of these authenticates, reads normally, and is REFUSED on every write tool —")
	fmt.Fprintln(out, "per call, at write time. Give each one the role it should have had (writer for an")
	fmt.Fprintln(out, "agent that files memories) before this reaches the people holding them.")
	return fmt.Errorf("%d active key(s) resolve to the read-only role for want of a membership row", total)
}
