package teiembed

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLiveEmbed exercises the client against a REAL TEI server, skipping unless
// TEI_EMBED_URL is set so the ordinary suite stays hermetic and offline.
//
//	TEI_EMBED_URL=http://host:13434 go test ./internal/embed/teiembed/ -run TestLive -v
//
// It exists because the fake-server tests cannot catch a contract drift, and this
// project has already been burned by exactly that: the v0.0.83 re-ranking release
// passed a green unit suite and was inert in production, because TEI rejected a
// batch larger than its limit and the failure was invisible. A live check over the
// batching boundary is the cheap insurance against a repeat.
func TestLiveEmbed(t *testing.T) {
	baseURL := os.Getenv("TEI_EMBED_URL")
	if baseURL == "" {
		t.Skip("TEI_EMBED_URL not set; skipping live TEI check")
	}
	e := New(baseURL, 60*time.Second)
	ctx := context.Background()

	one, err := e.EmbedOne(ctx, "memory palace sparse vectors")
	if err != nil {
		t.Fatalf("EmbedOne against %s: %v", baseURL, err)
	}
	if len(one) == 0 {
		t.Fatal("EmbedOne returned an empty vector")
	}
	t.Logf("EmbedOne: %d dims", len(one))

	// 130 inputs cross production's discovered 128-input boundary. Distinct
	// texts are what make a cross-batch misalignment detectable at all.
	inputs := make([]string, 130)
	for i := range inputs {
		inputs[i] = "drawer number " + string(rune('a'+i%26)) + " about wing agentmemories"
	}
	vecs, err := e.Embed(ctx, inputs)
	if err != nil {
		t.Fatalf("Embed(70): %v", err)
	}
	if len(vecs) != len(inputs) {
		t.Fatalf("got %d vectors, want %d", len(vecs), len(inputs))
	}
	for i, v := range vecs {
		if len(v) != len(one) {
			t.Fatalf("vector %d has %d dims, want %d", i, len(v), len(one))
		}
	}

	// The order assertion only a live server can falsify: re-embed the last input
	// on its own and require it to match the slot the batched call put it in.
	solo, err := e.EmbedOne(ctx, inputs[129])
	if err != nil {
		t.Fatalf("EmbedOne(inputs[129]): %v", err)
	}
	var maxDiff float32
	for i := range solo {
		d := solo[i] - vecs[129][i]
		if d < 0 {
			d = -d
		}
		if d > maxDiff {
			maxDiff = d
		}
	}
	// float16 weights on the server make this not bit-exact; a scattered result
	// would be off by whole units, not by rounding.
	if maxDiff > 1e-3 {
		t.Errorf("batched vector 129 differs from a solo embed by %g — batching scattered results", maxDiff)
	}
	t.Logf("order check: max diff %g across the discovered batch boundary", maxDiff)
}
