package main

import (
	"context"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/urfave/cli/v3"
)

// evalProject parses args through the real eval flag set and returns the
// workspace slug runEval would resolve against, so this pins the actual flag
// wiring rather than a re-implementation of it.
func evalProject(t *testing.T, args ...string) string {
	t.Helper()
	cmd := evalCommand(config.Default())
	var got string
	cmd.Action = func(_ context.Context, c *cli.Command) error {
		got = c.String("project")
		return nil
	}
	if err := cmd.Run(context.Background(), append([]string{"eval"}, args...)); err != nil {
		t.Fatalf("run: %v", err)
	}
	return got
}

// TestEvalNamesItsWorkspace is a regression test for eval being the one
// subcommand that could not measure a multi-tenant palace. It resolved through
// EnsureLocalWorkspace, whose contract is "exactly one workspace exists and it is
// slugged local" — so a database holding any other workspace (the seeded demo
// team is enough) failed with ErrForeignWorkspace before a single drawer was
// read, and no flag or environment variable could reach the real corpus.
//
// That guard exists to stop an UNAUTHENTICATED /mcp serving a workspace it did
// not provision. eval is a read-only CLI against a database file the caller
// already possesses, which is the same trust model as `wing export` and
// `inspect` — both of which name their workspace by slug.
func TestEvalNamesItsWorkspace(t *testing.T) {
	if got := evalProject(t, "--project", "acme"); got != "acme" {
		t.Errorf("--project acme resolved to %q, want acme", got)
	}
}

// TestEvalDefaultsToTheLocalWorkspace keeps the self-hoster's zero-typing path:
// naming a workspace must be what a multi-tenant operator does, never a new step
// for someone running `--local`.
func TestEvalDefaultsToTheLocalWorkspace(t *testing.T) {
	if got := evalProject(t); got != tenant.LocalSlug {
		t.Errorf("default project = %q, want %q", got, tenant.LocalSlug)
	}
}
