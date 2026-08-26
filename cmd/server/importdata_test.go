package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
)

// TestImportCommandIsRegistered: a producer nothing can select is this
// repository's characteristic defect, and it has shipped that shape more than
// once. The check reads the CLI's own command list rather than the source, so it
// fails for the reason a user would notice — `agentsmemory import` not existing.
//
// Proved by mutation: removing importCommand() from rootCommand's Commands turns
// this red, and restoring it turns it green.
func TestImportCommandIsRegistered(t *testing.T) {
	root := rootCommand(config.Default())
	var names []string
	for _, c := range root.Commands {
		names = append(names, c.Name)
		if c.Name == "import" {
			return
		}
	}
	t.Errorf("the CLI registers %v and not \"import\" — the producer exists and cannot be run", names)
}

// TestImportWritesABundleTheImporterWouldAccept drives the real Action path end
// to end over a temporary project, because the unit tests prove the format and
// this proves the COMMAND reaches it: config resolution, relative dataset paths,
// and the file that lands on disk.
func TestImportWritesABundleTheImporterWouldAccept(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "data", "currencies.jsonl"),
		"{\"code\":\"EUR\",\"minor_units\":2}\n{\"code\":\"JPY\",\"minor_units\":0}\n")
	write(t, filepath.Join(dir, "import.toml"), `
wing = "wing_acme"

[[dataset]]
file  = "data/currencies.jsonl"
room  = "refdata"
title = "Currencies we actually support"
why   = "Only these reach the pricing UI; the rest are rejected at the boundary."
show_values = ["code"]
`)
	out := filepath.Join(dir, "bundle.ndjson")

	if err := runImport(context.Background(), importOptions{
		configPath: filepath.Join(dir, "import.toml"),
		out:        out,
		outSet:     true,
	}); err != nil {
		t.Fatalf("runImport: %v", err)
	}

	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	got := string(body)
	for _, want := range []string{
		`"kind":"manifest"`,
		`"kind":"drawer"`,
		`"room":"refdata"`,
		`"source_file":"data/currencies.jsonl"`,
		"pricing UI",  // the human half
		"minor_units", // a measured field name
		"EUR, JPY",    // the value set of the field show_values names
	} {
		if !strings.Contains(got, want) {
			t.Errorf("bundle is missing %q:\n%s", want, got)
		}
	}

	// show_values has to survive the whole CLI path — parsed from the committed
	// file, carried into the profiler, honoured in the emitted drawer. The unit
	// tests prove the filter; this proves the mapping key reaches it. Dropping the
	// field from Dataset leaves "EUR, JPY" missing above, and dropping the
	// per-field count leaves this line red.
	if !strings.Contains(got, "2 distinct value(s), not listed") {
		t.Errorf("minor_units was not named in show_values, so its values must be counted and "+
			"withheld — and the count must still be there, or the omission is silent:\n%s", got)
	}
}

// TestDatasetPathsResolveAgainstTheConfigNotTheWorkingDirectory: the mapping
// file is committed beside the data, so its own directory is the only stable
// base. Resolving against the cwd would make the same committed config work or
// fail depending on where someone ran it from.
func TestDatasetPathsResolveAgainstTheConfigNotTheWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "nested", "data", "x.jsonl"), "{\"a\":1}\n")
	write(t, filepath.Join(dir, "nested", "import.toml"), `
[[dataset]]
file  = "data/x.jsonl"
room  = "schema"
title = "X"
why   = "because"
`)
	// Run from the PARENT, so a cwd-relative resolution would look for
	// data/x.jsonl here and miss.
	t.Chdir(dir)

	if err := runImport(context.Background(), importOptions{
		configPath: filepath.Join("nested", "import.toml"),
		out:        filepath.Join(dir, "b.ndjson"),
		outSet:     true,
	}); err != nil {
		t.Fatalf("runImport resolved the dataset against the working directory: %v", err)
	}
}

// TestPushFlagsRefuseTheCombinationsThatWouldSilentlyDoNothing. --as with no
// --push discards the wing (ADR-006: a knob that does nothing must say when),
// and --push with no token would hit the Bearer gate and report a 401 that reads
// like a server problem rather than a missing flag.
func TestPushFlagsRefuseTheCombinationsThatWouldSilentlyDoNothing(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "import.toml"), `
[[dataset]]
file  = "x.jsonl"
room  = "schema"
title = "X"
why   = "because"
`)
	cfg := filepath.Join(dir, "import.toml")

	if err := runImport(context.Background(), importOptions{configPath: cfg, as: "wing_x"}); err == nil {
		t.Error("--as without --push was accepted; the wing would be silently discarded")
	}
	if err := runImport(context.Background(), importOptions{configPath: cfg, push: "http://x/import"}); err == nil {
		t.Error("--push without a token was accepted; it would fail at the Bearer gate")
	}
}

// write creates a file and every directory above it.
func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
