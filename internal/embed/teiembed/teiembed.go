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
	"sync"
	"time"
)

// maxBatch is the safe fallback when TEI's /info capability endpoint is not
// reachable. It mirrors TEI's --max-client-batch-size default, which rejects a
// larger array outright with a 422 rather than truncating it.
//
// A deployment may advertise a larger limit through /info. The client uses it
// up to maxDiscoveredBatch so the production 128-input server needs one request
// instead of four while payloads remain bounded. Splitting is exact here: each
// input is embedded independently, so grouping cannot change a vector.
const maxBatch = 32

const maxDiscoveredBatch = 128

// Embedder is a client for TEI's /embed endpoint. It satisfies the Embedder
// interface that internal/palace declares at the consumer.
type Embedder struct {
	endpoint     string
	infoEndpoint string
	http         *http.Client
	batchOnce    sync.Once
	batchSize    int
}

// New constructs an Embedder for the given TEI base URL (e.g.
// http://host:13434). A trailing slash on baseURL is tolerated. timeout bounds
// each batched call.
//
// Nothing here names a model: TEI serves exactly the one fixed by the
// container's --model-id, so a model field would be a knob the wire cannot
// carry. This is the same reason internal/config has no RERANK_MODEL.
func New(baseURL string, timeout time.Duration) *Embedder {
	baseURL = strings.TrimRight(baseURL, "/")
	return &Embedder{
		endpoint:     baseURL + "/embed",
		infoEndpoint: baseURL + "/info",
		http:         &http.Client{Timeout: timeout},
	}
}

// embedRequest is TEI's embed payload. Truncate is set so an input longer than
// the model's context yields a (shortened) vector instead of a 413 that would
// fail a whole batch: chunking already bounds our inputs well below bge-m3's
// 8192 tokens, so truncation should never actually trigger, and asking for it
// costs nothing on a server whose auto_truncate is already on.
type embedRequest struct {
	Inputs   []string `json:"inputs"`
	Truncate bool     `json:"truncate"`
}

// Embed returns one vector per input string, in order. An empty input slice
// short-circuits to nil so callers need not special-case it.
//
// Inputs beyond the server's discovered client limit are split across several
// requests and reassembled in the caller's order.
func (e *Embedder) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	batchSize := len(inputs)
	if len(inputs) > 1 {
		batchSize = e.clientBatchSize(ctx)
	}
	out := make([][]float32, 0, len(inputs))
	for start := 0; start < len(inputs); start += batchSize {
		end := min(start+batchSize, len(inputs))
		batch, err := e.embedBatch(ctx, inputs[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, batch...)
	}
	return out, nil
}

// clientBatchSize reads the server's real client limit once. Capability
// discovery is an optimisation rather than an availability dependency: a
// proxy may expose /embed without /info, in which case the public TEI default
// remains the safe answer.
func (e *Embedder) clientBatchSize(ctx context.Context) int {
	e.batchOnce.Do(func() {
		e.batchSize = e.discoverClientBatchSize(ctx)
	})
	return e.batchSize
}

func (e *Embedder) discoverClientBatchSize(ctx context.Context) int {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.infoEndpoint, nil)
	if err != nil {
		return maxBatch
	}
	resp, err := e.http.Do(req)
	if err != nil {
		return maxBatch
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return maxBatch
	}
	var info struct {
		MaxClientBatchSize int `json:"max_client_batch_size"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil || info.MaxClientBatchSize < 1 {
		return maxBatch
	}
	return min(info.MaxClientBatchSize, maxDiscoveredBatch)
}

// embedBatch embeds one request's worth of inputs and returns the vectors in
// request order. Its caller has already bounded the slice to the server limit.
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
