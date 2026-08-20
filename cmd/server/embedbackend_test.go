package main

import (
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/embed/ollama"
	"github.com/atvirokodosprendimai/agentsmemory/internal/embed/teiembed"
)

// TestEmbedBackendSelectorIsReachable pins that EMBED_BACKEND actually chooses
// the embedder, by asserting the CONCRETE TYPE that comes back.
//
// The teiembed package documented this exact variable in its package comment
// while nothing in the program read it: a complete, unit-tested, live-tested
// backend that could not be selected. This test is written to fail if that
// happens again — a test asserting only "an embedder came back" would have
// passed throughout the period the selector did nothing.
func TestEmbedBackendSelectorIsReachable(t *testing.T) {
	def := config.Default()

	t.Run("default is ollama", func(t *testing.T) {
		e, err := buildEmbedder(def)
		if err != nil {
			t.Fatalf("the default backend must not error: %v", err)
		}
		if _, ok := e.(*ollama.Embedder); !ok {
			t.Fatalf("the default must stay Ollama, got %T", e)
		}
	})

	t.Run("tei is selectable", func(t *testing.T) {
		cfg := def
		cfg.EmbedBackend, cfg.EmbedURL = "tei", "http://embed.invalid:8080"
		got, err := buildEmbedder(cfg)
		if err != nil {
			t.Fatalf("tei with a URL must not error: %v", err)
		}
		if _, ok := got.(*teiembed.Embedder); !ok {
			t.Fatalf("EMBED_BACKEND=tei must select the TEI client, got %T — the selector is not read", got)
		}
	})

	t.Run("case and spacing do not matter", func(t *testing.T) {
		cfg := def
		cfg.EmbedBackend, cfg.EmbedURL = "  TEI ", "http://embed.invalid:8080"
		e2, err := buildEmbedder(cfg)
		if err != nil {
			t.Fatalf("tei must be selectable: %v", err)
		}
		if _, ok := e2.(*teiembed.Embedder); !ok {
			t.Fatal("an operator's stray whitespace or capital must not silently fall back to Ollama")
		}
	})
}
