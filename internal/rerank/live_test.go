package rerank_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/rerank"
)

// TestLiveRerank exercises the client against a REAL cross-encoder, because the
// two dialects this package straddles are only worth trusting if they have been
// spoken to. It is skipped unless RERANK_LIVE_URL points at one, so the normal
// test run stays hermetic.
//
// Bring one up locally (arm64-native, no GPU needed):
//
//	docker run --rm -p 8090:8080 ghcr.io/ggml-org/llama.cpp:server \
//	  -hf gpustack/bge-reranker-v2-m3-GGUF:Q4_K_M --reranking --host 0.0.0.0 --port 8080
//	RERANK_LIVE_URL=http://localhost:8090/v1/rerank go test ./internal/rerank/ -run Live
func TestLiveRerank(t *testing.T) {
	url := os.Getenv("RERANK_LIVE_URL")
	if url == "" {
		t.Skip("RERANK_LIVE_URL unset; skipping the live cross-encoder check")
	}

	// The middle document is the only one that answers the query. A working
	// cross-encoder must rank it first even though all three share vocabulary
	// with each other — which is exactly the case vector similarity gets wrong.
	docs := []string{
		"The cat sat on the mat and refused to move all afternoon.",
		"A global install must not pin CLAUDE_CONFIG_DIR, or the MCP registration lands in a file the CLI never reads.",
		"Qdrant payload filters narrow a search to points whose payload matches.",
	}
	scores, err := rerank.New(url, "bge-reranker-v2-m3", 60*time.Second).
		Rerank(context.Background(), "why did the installer register the MCP where claude cannot see it?", docs)
	if err != nil {
		t.Fatalf("live rerank: %v", err)
	}
	if len(scores) != len(docs) {
		t.Fatalf("scored %d of %d documents", len(scores), len(docs))
	}
	if scores[0].Index != 1 {
		t.Errorf("best document = %d (%q), want 1", scores[0].Index, docs[scores[0].Index])
	}
	for i := 1; i < len(scores); i++ {
		if scores[i].Score > scores[i-1].Score {
			t.Errorf("results not ordered best-first: %+v", scores)
			break
		}
	}
}
