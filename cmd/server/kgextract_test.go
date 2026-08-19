package main

import (
	"context"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/urfave/cli/v3"
)

// TestParseTriplesSplitsOnFirstTwoPipes pins the defensive split: only the first
// two pipes are structural, so an object that itself contains a pipe keeps its
// tail instead of being clipped into a different (wrong) fact.
func TestParseTriplesSplitsOnFirstTwoPipes(t *testing.T) {
	triples, malformed := parseTriples("CQRS | separates | reads | writes")
	if malformed != 0 {
		t.Fatalf("a 3+-pipe line is well-formed, got %d malformed", malformed)
	}
	if len(triples) != 1 {
		t.Fatalf("expected 1 triple, got %d", len(triples))
	}
	got := triples[0]
	if got.Subject != "CQRS" || got.Predicate != "separates" || got.Object != "reads | writes" {
		t.Fatalf("first-two-pipes split broken: %+v", got)
	}
}

// TestParseTriplesCountsMalformed pins the fail-loud half of parsing: junk lines
// are skipped but each one is counted, while blank lines are structure rather
// than failure and count for nothing.
func TestParseTriplesCountsMalformed(t *testing.T) {
	raw := strings.Join([]string{
		"Here are the extracted facts:", // preamble prose — malformed
		"",                              // blank — not counted
		"api gateway | routes to | billing service",
		"no pipes at all",                 // malformed
		"only one | pipe",                 // malformed
		" | routes to | billing",          // empty subject — malformed
		"- worker | consumes | job queue", // bulleted but parseable
		"   ",                             // whitespace-only — not counted
	}, "\n")
	triples, malformed := parseTriples(raw)
	if len(triples) != 2 {
		t.Fatalf("expected 2 triples, got %d: %+v", len(triples), triples)
	}
	if malformed != 4 {
		t.Fatalf("expected 4 malformed lines, got %d", malformed)
	}
	if triples[1].Subject != "worker" {
		t.Fatalf("bulleted triple should parse, got %+v", triples[1])
	}
}

// TestWindowRunesCoversEverything is the no-silent-truncation guarantee: a text
// longer than one window becomes several windows whose concatenation is the
// whole text, each within the cap.
func TestWindowRunesCoversEverything(t *testing.T) {
	text := strings.Repeat("ą", 2500) // multibyte on purpose: the cap is runes, not bytes
	windows := windowRunes(text, 1000)
	if len(windows) != 3 {
		t.Fatalf("2500 runes at 1000/window should be 3 windows, got %d", len(windows))
	}
	var rebuilt strings.Builder
	for _, w := range windows {
		if n := len([]rune(w)); n > 1000 {
			t.Fatalf("window exceeds the cap: %d runes", n)
		}
		rebuilt.WriteString(w)
	}
	if rebuilt.String() != text {
		t.Fatal("windows do not reassemble into the original text — content was dropped")
	}
	if windowRunes("   ", 1000) != nil {
		t.Fatal("whitespace-only text should yield no windows")
	}
}

// kgExtract parses args through the real kg-extract flag set and hands the
// parsed command to inspect, so these tests pin the actual flag wiring rather
// than a re-implementation of it.
func kgExtract(t *testing.T, inspect func(c *cli.Command), args ...string) error {
	t.Helper()
	cmd := kgExtractCommand(config.Default())
	cmd.Action = func(_ context.Context, c *cli.Command) error {
		inspect(c)
		return nil
	}
	return cmd.Run(context.Background(), append([]string{"kg-extract"}, args...))
}

// TestKGExtractDefaultsToTheLocalWorkspace keeps the self-hoster's zero-typing
// path, matching eval: naming a workspace is a multi-tenant operator's step,
// never a new requirement for `--local`.
func TestKGExtractDefaultsToTheLocalWorkspace(t *testing.T) {
	var project string
	var limit int
	if err := kgExtract(t, func(c *cli.Command) { project, limit = c.String("project"), c.Int("limit") }, "--wing", "wing_acme"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if project != tenant.LocalSlug {
		t.Errorf("default project = %q, want %q", project, tenant.LocalSlug)
	}
	if limit != 10 {
		t.Errorf("default limit = %d, want 10", limit)
	}
}

// TestKGExtractGeneratorFollowsTheEmbedderByDefault pins the single-machine
// path shared with eval: one Ollama, nothing to configure; --gen-url exists to
// SEPARATE the two, so leaving it unset must not require setting it.
func TestKGExtractGeneratorFollowsTheEmbedderByDefault(t *testing.T) {
	var url string
	if err := kgExtract(t, func(c *cli.Command) { url = genURL(c) }, "--wing", "wing_acme", "--ollama-url", "http://box:11434"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if url != "http://box:11434" {
		t.Errorf("gen url = %q, want it to follow --ollama-url", url)
	}
}

// TestKGExtractRequiresAWing pins --wing as required: extraction fans LLM time
// out over a corpus, and "the whole palace by default" is a footgun, not a
// convenience.
func TestKGExtractRequiresAWing(t *testing.T) {
	if err := kgExtract(t, func(*cli.Command) {}); err == nil {
		t.Fatal("running without --wing should fail")
	}
}
