package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
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
			&cli.StringFlag{Name: "project", Value: "local", Usage: "workspace slug to check"},
			&cli.BoolFlag{Name: "index", Usage: "check that every stored point's wing matches its drawer's"},
		),
		Action: func(ctx context.Context, c *cli.Command) error {
			if !c.Bool("index") {
				return fmt.Errorf("nothing to check: pass --index (the only check so far)")
			}
			return doctorIndex(ctx, configFromCmd(c, def), c.String("project"), os.Stdout)
		},
	}
}

// doctorIndex runs the wing-payload drift check and reports it.
//
// It prints drawer ids and wing names and never memory text: a doctor report is
// pasted into an issue, and the palace it describes is private.
func doctorIndex(ctx context.Context, cfg config.Config, slug string, out io.Writer) error {
	svc, err := buildServices(cfg)
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
	if report.Clean() {
		fmt.Fprintf(out, "index: %d drawer(s) checked, every stored point agrees with its row\n", report.Checked)
		return nil
	}

	fmt.Fprintf(out, "index: %d drawer(s) checked, %d stored point(s) disagree with their row\n\n",
		report.Checked, len(report.Drifted))
	for _, d := range report.Drifted {
		fmt.Fprintf(out, "  %-16s %s  indexed as %q, filed in %q\n", d.Store, d.DrawerID, d.Indexed, d.Actual)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "A scoped recall filters at the index, on the payload above — so each of these memories is")
	fmt.Fprintln(out, "UNREACHABLE from the wing it is filed in, and answers only an unscoped search. This is what a")
	fmt.Fprintln(out, "wing merge left behind before merges corrected the payloads they invalidate.")
	return fmt.Errorf("%d stored point(s) disagree with their drawer", len(report.Drifted))
}
