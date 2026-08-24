// Package teiembed embeds text with a HuggingFace text-embeddings-inference
// (TEI) server. It is an alternative to internal/embed/ollama, selected with
// EMBED_BACKEND=tei; Ollama stays the default so existing deployments are
// unaffected by this package existing.
//
// Why a second embedder: TEI is already the server that runs the re-ranking
// cross-encoder (internal/rerank/tei), so a deployment with a GPU box can embed
// and rerank there and stop keeping an Ollama alive purely for /api/embed. The
// wire shape is also plainer — one POST, a bare JSON array back.
//
// The package is named teiembed rather than tei so it does not collide with
// internal/rerank/tei at the composition root, following the same naming choice
// made for internal/store/chromemvec.
//
// WHAT TEI CANNOT DO HERE, recorded so nobody re-derives it. bge-m3's sparse
// (lexical_weights) head is unreachable through TEI. /embed_sparse requires
// SPLADE pooling — sparse weights read off an MLM head as logits over the whole
// vocabulary — whereas bge-m3 produces its sparse weights from a separate
// trained sparse_linear layer that TEI does not load. Probed 2026-08-19 against
// a live BAAI/bge-m3 (TEI 1.9.3): /embed_sparse answers
//
//	424 {"error":"Backend error: Model is not an embedding model with SPLADE pooling"}
//
// Upstream PR #899 (--pooling m3_sparse) would add it but is unmerged. Note also
// that TEI's --pooling is process-wide, so one container cannot serve dense and
// sparse for the same model even once that lands.
package teiembed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// maxBatch is how many inputs may go in ONE request to TEI. It mirrors TEI's
// --max-client-batch-size default, which rejects a larger array outright with a
// 422 rather than truncating it.
//
// It is a constant rather than config for the same reason it is one in
// internal/rerank/tei: a SMALLER batch is always accepted, so an operator who
// raises TEI's limit loses only round-trips, never correctness. Splitting is
// exact here — each input is embedded independently of the others, so how they
// are grouped into requests cannot change a vector.
const maxBatch = 32

// Embedder is a client for TEI's /embed endpoint. It satisfies the Embedder
// interface that internal/palace declares at the consumer.
type Embedder struct {
	endpoint string
	http     *http.Client
}

// New constructs an Embedder for the given TEI base URL (e.g.
// http://host:13434). A trailing slash on baseURL is tolerated. timeout bounds
// each batched call.
//
// Nothing here names a model: TEI serves exactly the one fixed by the
// container's --model-id, so a model field would be a knob the wire cannot
// carry. This is the same reason internal/config has no RERANK_MODEL.
func New(baseURL string, timeout time.Duration) *Embedder {
	return &Embedder{
		endpoint: strings.TrimRight(baseURL, "/") + "/embed",
		http:     &http.Client{Timeout: timeout},
	}
}

// embedRequest is TEI's embed payload. Truncate is set so an input longer than
// the model's context yields a (shortened) vector instead of a 413 that would
// fail a whole batch.
//
// ⚠It is a LAST resort, not a reason to relax about input size, and this comment
// used to say otherwise. It claimed truncation "should never actually trigger"
// because chunking bounds our inputs below bge-m3's 8192 tokens — true of the add
// path, which chunks at 1600 characters, and false of the update path, which
// re-embeds a whole memory with EmbedOne and never chunks it. Nothing on that path
// was bounded, so an oversized update got a prefix vector and a 200, and the tail
// of the memory became unfindable while still reading back whole. The caller is
// what fixes this: palace.MaxEmbedRunes refuses before the request is built, and
// it is deliberately set from the SMALLEST backend agentsmemory can be pointed at
// rather than from bge-m3 — an operator may be running ollama instead, and a
// limit only this client satisfies would just move the silent truncation there.
// Truncation here remains the batch's protection against the one input nobody
// bounded, and if it ever fires, something upstream stopped checking.
type embedRequest struct {
	Inputs   []string `json:"inputs"`
	Truncate bool     `json:"truncate"`
}

// Embed returns one vector per input string, in order. An empty input slice
// short-circuits to nil so callers need not special-case it.
//
// Inputs beyond maxBatch are split across several requests and reassembled in
// the caller's order.
func (e *Embedder) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	out := make([][]float32, 0, len(inputs))
	for start := 0; start < len(inputs); start += maxBatch {
		end := min(start+maxBatch, len(inputs))
		batch, err := e.embedBatch(ctx, inputs[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, batch...)
	}
	return out, nil
}

// embedBatch embeds one request's worth of inputs — at most maxBatch of them —
// and returns the vectors in request order.
func (e *Embedder) embedBatch(ctx context.Context, inputs []string) ([][]float32, error) {
	raw, err := json.Marshal(embedRequest{Inputs: inputs, Truncate: true})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// TEI puts the reason in the body ({"error":...,"error_type":...}) and it is
		// the only thing that distinguishes "this model cannot do that" (424) from
		// "batch too large" (422), so it is worth carrying into the error.
		return nil, fmt.Errorf("tei: embed -> %d: %s", resp.StatusCode, string(data))
	}

	// TEI answers with a BARE array of vectors, not an object with a field —
	// unlike Ollama's {"embeddings":[...]}.
	var vectors [][]float32
	if err := json.Unmarshal(data, &vectors); err != nil {
		return nil, fmt.Errorf("tei: decode embed response: %w", err)
	}
	// Guard the invariant the rest of the system relies on: one vector per input.
	if len(vectors) != len(inputs) {
		return nil, fmt.Errorf("tei: expected %d embeddings, got %d", len(inputs), len(vectors))
	}
	return vectors, nil
}

// EmbedOne is a convenience for the common single-string case (e.g. a search
// query), returning just that one vector.
func (e *Embedder) EmbedOne(ctx context.Context, input string) ([]float32, error) {
	vecs, err := e.Embed(ctx, []string{input})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}
