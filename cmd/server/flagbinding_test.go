package main

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"

	cli "github.com/urfave/cli/v3"
)

// flagFieldAliases are the flags whose spelling deliberately differs from the
// config field they fill. Each entry is a place where the operator-facing name
// and the internal name genuinely diverge, not an excuse.
//
// The list cannot be used to hide a mis-binding: TestFlagAliasesAreNecessary
// rejects any alias whose flag name already normalises to a real Config field,
// so the only way in is a flag that has no mechanical counterpart at all.
var flagFieldAliases = map[string]string{
	"socket":       "SocketPath",
	"db":           "DBPath",
	"ollama-model": "OllamaEmbedModel",
	"token":        "LocalToken",
}

// TestEveryFlagFillsTheFieldItNamesRunsTheRealCLI parses each flag through the
// actual urfave command and checks the value lands in the field the flag names.
//
// Every other wiring gate in this package reads the SOURCE — that a flag is
// declared, that a field is assigned, that a knob's closure moves what it says.
// All of them stay green when two assignments are swapped:
//
//	BM25Weight: c.String("fusion"),
//	Fusion:     c.String("bm25-weight"),
//
// Both flags are still declared, both are still read, both fields are still
// assigned, and the sweep's hand-written closures still move the fields they
// intend — because they are not the production binding. Measured 2026-08-20:
// that swap passed `go test ./...` in full while `--fusion=rrf` no longer
// selected RRF. A reviewer found it; sixteen reachability gates did not.
//
// So this one executes the binding instead of reading it.
func TestEveryFlagFillsTheFieldItNamesRunsTheRealCLI(t *testing.T) {
	def := config.Default()
	fields := map[string]bool{}
	rt := reflect.TypeOf(config.Config{})
	for i := 0; i < rt.NumField(); i++ {
		fields[rt.Field(i).Name] = true
	}

	base := parseThroughCLI(t, def)
	checked := 0
	for _, f := range serveFlags(def) {
		name := f.Names()[0]
		arg, ok := probeFor(f, name)
		if !ok {
			continue // no probe shape for this flag kind; counted below
		}
		want, err := fieldFor(name, fields)
		if err != nil {
			t.Errorf("--%s: %v", name, err)
			continue
		}
		got := parseThroughCLI(t, def, arg)
		moved := movedFields(base, got)
		if len(moved) == 0 {
			t.Errorf("--%s changed no config field at all: the flag parses and the value goes nowhere", name)
			continue
		}
		if !contains(moved, want) {
			t.Errorf("--%s should fill Config.%s; parsing %q moved %v instead.\n"+
				"  The flag is declared, the field is assigned, and they are not connected to each other.",
				name, want, arg, moved)
		}
		checked++
	}
	// Coverage is asserted against the real flag count, not a magic number. A
	// constant floor is satisfied by the flags that happen to have probes, which
	// is exactly how the bool kind sat outside this check while the guard read
	// "only 21 probed, floor is 15, fine".
	if want := len(serveFlags(def)); checked != want {
		t.Errorf("%d of %d flags were probed; %d flag(s) have no probe for their kind, so they are "+
			"outside this check entirely. Add the kind to probeFor.", checked, want, want-checked)
	}
}

// TestMemoryEvidenceSelectorEnvironmentReachesConfig executes the production
// .env path for the nested A/B arm rather than proving only the CLI spelling.
func TestMemoryEvidenceSelectorEnvironmentReachesConfig(t *testing.T) {
	t.Setenv("MEMORY_EVIDENCE_SELECTOR", "semantic")
	got := parseThroughCLI(t, config.Default())
	if got.MemoryEvidenceSelector != "semantic" {
		t.Fatalf("MEMORY_EVIDENCE_SELECTOR=semantic reached Config.MemoryEvidenceSelector as %q", got.MemoryEvidenceSelector)
	}
}

// TestFlagAliasesAreNecessary keeps the alias table from becoming the hole. An
// alias is admissible only for a flag with no mechanical counterpart: if the
// flag name already normalises onto a real Config field, an alias pointing it
// somewhere else is precisely the mis-binding this file exists to catch.
func TestFlagAliasesAreNecessary(t *testing.T) {
	rt := reflect.TypeOf(config.Config{})
	for flag, field := range flagFieldAliases {
		for i := 0; i < rt.NumField(); i++ {
			if normalize(rt.Field(i).Name) == normalize(flag) && rt.Field(i).Name != field {
				t.Errorf("--%s is aliased to %s, but it already names Config.%s; an alias may not "+
					"redirect a flag that has a counterpart", flag, field, rt.Field(i).Name)
			}
		}
	}
}

// fieldFor resolves a flag name to the Config field it must fill.
func fieldFor(flag string, fields map[string]bool) (string, error) {
	if f, ok := flagFieldAliases[flag]; ok {
		if !fields[f] {
			return "", fmt.Errorf("aliased to %q, which is not a Config field", f)
		}
		return f, nil
	}
	for f := range fields {
		if normalize(f) == normalize(flag) {
			return f, nil
		}
	}
	return "", fmt.Errorf("no Config field matches this flag name, and no alias declares where it goes")
}

// normalize folds a flag name and a field name onto the same spelling, so that
// bm25-weight meets BM25Weight and rerank-url meets RerankURL.
func normalize(s string) string {
	return strings.ToLower(strings.ReplaceAll(s, "-", ""))
}

// probeFor returns a command-line argument that sets the flag to a value it
// cannot already hold, and whether such a probe exists for this flag's kind.
//
// Every kind in the command must appear here. The first version omitted bool and
// string-slice, which silently excused --local and --debug from the whole check:
// swapping their two bindings was caught only by --local's own behavioural tests,
// and --debug had no such backstop. A probe table that quietly skips a kind is
// the same defect one level up — the gate is declared over "every flag" and
// selects a subset.
func probeFor(f cli.Flag, name string) (string, bool) {
	switch v := f.(type) {
	case *cli.StringFlag:
		return "--" + name + "=probe-" + name, true
	case *cli.IntFlag:
		return "--" + name + "=4242", true
	case *cli.FloatFlag:
		return "--" + name + "=0.4242", true
	case *cli.DurationFlag:
		return "--" + name + "=4242s", true
	case *cli.BoolFlag:
		// The negation of the default, so the probe always moves the field.
		return fmt.Sprintf("--%s=%v", name, !v.Value), true
	case *cli.StringSliceFlag:
		return "--" + name + "=probe-" + name, true
	}
	return "", false
}

// parseThroughCLI runs the real command over args and returns the Config it produced.
func parseThroughCLI(t *testing.T, def config.Config, args ...string) config.Config {
	t.Helper()
	var out config.Config
	cmd := &cli.Command{
		Name:  "probe",
		Flags: serveFlags(def),
		Action: func(_ context.Context, c *cli.Command) error {
			out = configFromCmd(c, def)
			return nil
		},
	}
	if err := cmd.Run(context.Background(), append([]string{"probe"}, args...)); err != nil {
		t.Fatalf("running the command with %v: %v", args, err)
	}
	return out
}

func movedFields(a, b config.Config) []string {
	ra, rb := reflect.ValueOf(a), reflect.ValueOf(b)
	var out []string
	for i := 0; i < ra.NumField(); i++ {
		if !reflect.DeepEqual(ra.Field(i).Interface(), rb.Field(i).Interface()) {
			out = append(out, ra.Type().Field(i).Name)
		}
	}
	return out
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}
