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
	"log/slog"
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

// infoTimeout bounds the capability probe. It is short because /info is a
// static read served before any inference, and because the probe is on the
// critical path of the embed that triggered it.
const infoTimeout = 5 * time.Second

// defaultProbeRetry is how long a failed capability probe is left alone. Long
// enough that a permanently /info-less deployment costs one request a minute
// rather than one per embed, short enough that a TEI still loading its model is
// picked up well within a warm-up.
const defaultProbeRetry = 30 * time.Second

// Embedder is a client for TEI's /embed endpoint. It satisfies the Embedder
// interface that internal/palace declares at the consumer.
type Embedder struct {
	endpoint     string
	infoEndpoint string
	http         *http.Client

	// retryAfter is how long a failed probe suppresses the next one. Unexported
	// and injectable so a test can drive the policy without sleeping; it is not
	// an operator knob.
	retryAfter time.Duration

	// batchMu guards the fields below. It is a mutex and not a sync.Once because
	// Once fires whether the probe succeeded or FAILED, which pinned a transient
	// error to the whole process lifetime. The mutex is NEVER held across the
	// probe itself — see clientBatchSize.
	batchMu     sync.Mutex
	batchSize   int
	probing     bool
	nextProbe   time.Time
	batchWarned bool
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
		retryAfter:   defaultProbeRetry,
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

// clientBatchSize reports the server's real client limit, discovering it on
// first use and CACHING ONLY A SUCCESSFUL ANSWER. Capability discovery is an
// optimisation rather than an availability dependency: a proxy may expose
// /embed without /info, in which case the public TEI default is the safe
// answer — but it must be a safe answer for this call, not a verdict on the
// process.
//
// Memoizing failure is how the optimisation disappears in production. TEI is
// commonly still loading its model when the first embed arrives, and one 503,
// one dial error, or one caller who hangs up is otherwise enough to pin 32 for
// the lifetime of the server, four times the round trips, with nothing said.
func (e *Embedder) clientBatchSize(ctx context.Context) int {
	e.batchMu.Lock()
	if e.batchSize > 0 {
		size := e.batchSize
		e.batchMu.Unlock()
		return size
	}
	// Do not queue behind someone else's probe, and do not re-ask a server that
	// just refused. Either way the safe default is the right answer for THIS
	// call: discovery is an optimisation, so waiting for it is always worse than
	// proceeding without it.
	if e.probing || time.Now().Before(e.nextProbe) {
		e.batchMu.Unlock()
		return maxBatch
	}
	e.probing = true
	e.batchMu.Unlock()

	// Deliberately OUTSIDE the lock. Holding it across a network call made every
	// embed in the process wait on one /info, so a proxy that black-holes the
	// path turned the whole write path — add_drawer chunking, mining, the
	// backfill worker, every tenant — into a queue one timeout wide.
	size, err := e.discoverClientBatchSize(ctx)

	e.batchMu.Lock()
	e.probing = false
	if err != nil {
		e.nextProbe = time.Now().Add(e.retryAfter)
		warn := !e.batchWarned
		e.batchWarned = true
		e.batchMu.Unlock()
		// Said once, not per call: a warning on every embed would bury the log
		// of a server whose /info is permanently absent, which is a supported
		// deployment rather than a fault.
		if warn {
			slog.Warn("TEI capability discovery failed; using the default client batch size and will retry",
				"endpoint", e.infoEndpoint, "batch", maxBatch, "error", err)
		}
		return maxBatch
	}
	e.batchSize = size
	e.batchMu.Unlock()
	return size
}

// discoverClientBatchSize asks TEI what it will accept.
//
// The probe deliberately does NOT inherit the caller's cancellation. Whichever
// embed happens to be first would otherwise decide a process-wide setting, so a
// client that disconnects mid-request downgrades every later batch. It keeps
// the caller's values (deadline aside) so tracing and auth survive.
func (e *Embedder) discoverClientBatchSize(ctx context.Context) (int, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), infoTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.infoEndpoint, nil)
	if err != nil {
		return 0, err
	}
	resp, err := e.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("GET %s: %s", e.infoEndpoint, resp.Status)
	}
	var info struct {
		MaxClientBatchSize int `json:"max_client_batch_size"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return 0, fmt.Errorf("decode %s: %w", e.infoEndpoint, err)
	}
	if info.MaxClientBatchSize < 1 {
		return 0, fmt.Errorf("%s advertised max_client_batch_size=%d", e.infoEndpoint, info.MaxClientBatchSize)
	}
	return min(info.MaxClientBatchSize, maxDiscoveredBatch), nil
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
