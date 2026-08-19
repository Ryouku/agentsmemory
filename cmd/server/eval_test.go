package main

import (
	"context"
	"net/http"
	"net/http/httptest"
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

// evalGen parses args through the real eval flag set and reports where the
// question generator would point, plus the model it would ask for.
func evalGen(t *testing.T, args ...string) (url, model string) {
	t.Helper()
	cmd := evalCommand(config.Default())
	cmd.Action = func(_ context.Context, c *cli.Command) error {
		url, model = genURL(c), c.String("gen-model")
		return nil
	}
	if err := cmd.Run(context.Background(), append([]string{"eval"}, args...)); err != nil {
		t.Fatalf("run: %v", err)
	}
	return url, model
}

// TestEvalGeneratorFollowsTheEmbedderByDefault pins the single-machine path: one
// Ollama, nothing to configure. --gen-url exists to SEPARATE the two, so leaving
// it unset must not require setting it.
func TestEvalGeneratorFollowsTheEmbedderByDefault(t *testing.T) {
	url, model := evalGen(t, "--ollama-url", "http://box:11434")
	if url != "http://box:11434" {
		t.Errorf("gen url = %q, want it to follow --ollama-url", url)
	}
	if model != "qwen2.5-coder:7b" {
		t.Errorf("gen model = %q, want the documented default", model)
	}
}

// TestEvalGeneratorCanLeaveTheEmbedderBehind is the point of --gen-url: sending a
// one-off burst of question generation to a bigger or hosted model must not drag
// the embedder along with it, because the vectors stay where the data is.
func TestEvalGeneratorCanLeaveTheEmbedderBehind(t *testing.T) {
	url, model := evalGen(t,
		"--ollama-url", "http://localhost:11434",
		"--gen-url", "https://ollama.com",
		"--gen-model", "qwen3-coder:480b-cloud",
	)
	if url != "https://ollama.com" {
		t.Errorf("gen url = %q, want the override to win over --ollama-url", url)
	}
	if model != "qwen3-coder:480b-cloud" {
		t.Errorf("gen model = %q, want the override", model)
	}
}

// TestEvalGeneratorSendsItsBearerToken covers the reason --gen-api-key exists:
// hosted Ollama rejects an unauthenticated call, and a local one ignores the
// header, so sending it whenever it is set is both necessary and harmless.
func TestEvalGeneratorSendsItsBearerToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"response":"why did the deploy fail?"}`))
	}))
	defer srv.Close()

	gen := &questionGen{
		url:    srv.URL,
		model:  "m",
		apiKey: "secret-token",
		prompt: "%s",
		http:   srv.Client(),
	}
	if _, err := gen.ask(context.Background(), "a note"); err != nil {
		t.Fatalf("ask: %v", err)
	}
	if gotAuth != "Bearer secret-token" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer secret-token")
	}

	// And absent when unset: a local Ollama needs no credential, and inventing an
	// empty Bearer header is the kind of thing a strict proxy rejects.
	gen.apiKey = ""
	if _, err := gen.ask(context.Background(), "a note"); err != nil {
		t.Fatalf("ask without key: %v", err)
	}
	if gotAuth != "" {
		t.Errorf("Authorization = %q with no key set, want it absent", gotAuth)
	}
}
