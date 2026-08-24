package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
)

// TestVersionFlagPrintsTheStampedVersion runs the REAL root command with
// --version and asserts the printed string names the version variable. It fails
// when the wiring is removed — when cmd.Version is no longer set on the root
// command, urfave/cli v3 either rejects --version as an unknown flag or prints
// the default empty version, and either way this test goes red. That is the
// point: a version that exists only as a stamped linker symbol nobody can read
// is this repository's characteristic defect, "finished and unreachable".
func TestVersionFlagPrintsTheStampedVersion(t *testing.T) {
	cmd := rootCommand(config.Default())
	var buf bytes.Buffer
	cmd.Writer = &buf

	if err := cmd.Run(context.Background(), []string{"agentsmemory", "--version"}); err != nil {
		t.Fatalf("running --version: %v", err)
	}

	got := buf.String()
	want := "agentsmemory version " + version
	if !strings.Contains(got, want) {
		t.Errorf("--version printed %q, want it to contain %q", got, want)
	}
}
