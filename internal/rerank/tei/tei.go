// Package tei scores query/document pairs with a cross-encoder served by
// HuggingFace text-embeddings-inference (TEI). It is the second half of recall:
// internal/embed/ollama finds candidates that are semantically near, this ranks
// the ones that actually answer the query, having read both sides of the pair.
//
// TEI rather than Ollama because Ollama cannot rerank at all — it exposes only a
// model's embedding layer, never the cross-encoder classification head, so there
// is no /api/rerank to call (ollama/ollama#10467). TEI serves the reference
// BAAI/bge-reranker-v2-m3 weights and is the maintained option; the model is
// fixed by the container's --model-id, which is why nothing here names a model.
//
// One client speaks two server dialects, because the servers that host these
// weights disagree about field names but not about meaning:
//
//   - TEI answers POST /rerank with a bare array of {index, score}.
//   - llama.cpp answers the same route (and /v1/rerank) Cohere-shaped, with
//     {"results": [{index, relevance_score}]}.
//
// Both are accepted so one RERANK_URL keeps working either way. That is not
// theoretical: TEI publishes no arm64 image, so on Apple Silicon llama.cpp is
// the only way to run a cross-encoder at all, and a TEI-only client would leave
// those operators with search silently falling back to hybrid.
package tei

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/telemetry"
)

// maxBatch is how many texts may go in ONE request to TEI. It mirrors TEI's
// --max-client-batch-size default, which rejects a larger array outright:
//
//	HTTP 422 {"error":"batch size 50 > maximum allowed batch size 32"}
//
// The rerank pool is a search-quality decision and must not be held hostage to a
// server-side request limit, so Rerank splits the pool into batches rather than
// forcing the pool down to 32. Batching is exact, not an approximation: a
// cross-encoder scores each (query, document) pair independently, so how the
// pairs are grouped into requests cannot change any score.
//
// It is a constant rather than config because a SMALLER batch is always accepted
// — an operator who raises TEI's limit loses nothing here, and one who lowers it
// below 32 gets a 422 that search fails open on, with the reason in the log.
const maxBatch = 32

// Client is a client for TEI's /rerank endpoint.
type Client struct {
	endpoint string
	http     *http.Client
	// budget bounds the COMPLETE Rerank call. http.Client.Timeout bounds each
	// request, and a pool larger than maxBatch is several requests: at pool 100
	// and a 10s timeout, four batches taking nine seconds each is 36 seconds
	// without a single one timing out, while every caller has already given up.
	budget time.Duration
}

// New constructs a Client for the given base URL (e.g. http://host:12434).
// timeout bounds the WHOLE call — every batch a pool is split into, not each one
// — because cross-encoding a full pool is the slowest step in search and recall
// must degrade rather than hang when the box is loaded.
//
// baseURL may name the server ("http://host:12434"), a version prefix
// ("http://host:8080/v1"), or the full route ("http://host:8080/v1/rerank"):
// /rerank is appended unless the URL already ends in the route. All three appear
// in this repo's own shipped configuration — docker-compose.full.yml sets the /v1
// prefix while .env.docker.example spells out the whole path — so a rule that
// only ever appended, or only ever took the URL verbatim, would break one of
// them. Accepting all three costs one suffix check and keeps every documented
// value working, including llama.cpp's /v1/rerank and /reranking aliases.
func New(baseURL string, timeout time.Duration) *Client {
	endpoint := strings.TrimRight(baseURL, "/")
	if last := endpoint[strings.LastIndex(endpoint, "/")+1:]; last != "rerank" && last != "reranking" {
		endpoint += "/rerank"
	}
	return &Client{
		endpoint: endpoint,
		http:     telemetry.HTTPClient(timeout),
		budget:   timeout,
	}
}

// rerankRequest is the payload both dialects accept. TEI reads "texts" and
// llama.cpp reads the Cohere-style "documents", so the same slice is sent under
// both names: each server ignores the field it does not know, which is cheaper
// and far less brittle than sniffing which server is on the other end.
type rerankRequest struct {
	Query     string   `json:"query"`
	Texts     []string `json:"texts"`
	Documents []string `json:"documents"`
	// RawScores false asks TEI for sigmoid-squashed scores in (0,1) rather than
	// bare logits. Bounded scores keep a reranked ordering comparable across
	// queries, which matters because we report the score, not just the order.
	RawScores bool `json:"raw_scores"`
}

// rerankResponse is llama.cpp's wrapper. TEI sends the bare array instead, so
// decodeResults accepts either and the dialect difference stops here.
type rerankResponse struct {
	Results []rerankResult `json:"results"`
}

// rerankResult is one scored pair. Both dialects answer sorted best-first, so
// the index is the only link back to the caller's input order.
type rerankResult struct {
	Index int `json:"index"`
	// Pointers, not plain float64s, because the question is WHICH FIELD THE SERVER
	// SENT — not which one happens to be nonzero. A cross-encoder emits logits, and
	// a logit of exactly 0.0 is an ordinary score; keying off zero would read the
	// absent field precisely when the real one said "neutral".
	Score          *float64 `json:"score"`           // TEI
	RelevanceScore *float64 `json:"relevance_score"` // llama.cpp / Cohere
}

// value returns whichever score field the server populated, and 0 when neither
// is present — a result with no score still holds its place in the server's
// ordering, which is the information the caller actually uses.
func (r rerankResult) value() float64 {
	switch {
	case r.Score != nil:
		return *r.Score
	case r.RelevanceScore != nil:
		return *r.RelevanceScore
	default:
		return 0
	}
}

// decodeResults parses either dialect's body. The first non-space byte decides,
// so a malformed body reports the dialect it actually looked like rather than a
// confusing second error from a speculative retry.
func decodeResults(data []byte) ([]rerankResult, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var wrapped rerankResponse
		if err := json.Unmarshal(trimmed, &wrapped); err != nil {
			return nil, err
		}
		return wrapped.Results, nil
	}
	var bare []rerankResult
	if err := json.Unmarshal(trimmed, &bare); err != nil {
		return nil, err
	}
	return bare, nil
}

// Rerank scores each text against query and returns the scores in INPUT order,
// not TEI's best-first order. Aligning to the input mirrors the Embedder's
// "one per input, in order" contract and leaves the ordering decision with the
// caller, which is the only place that knows what else feeds the rank.
//
// An empty texts slice short-circuits to nil so callers need not special-case it.
// Inputs longer than maxBatch are sent as several sequential requests; the
// batches are joined back into one score per input.
func (c *Client) Rerank(ctx context.Context, query string, texts []string) ([]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	// One deadline over every batch. Without it the timeout is per REQUEST and a
	// pool of 100 is four of them, so the promised budget was silently multiplied
	// by the number of batches — and the fail-open path, whose entire purpose is
	// to give up before the caller does, could not be reached.
	if c.budget > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.budget)
		defer cancel()
	}
	scores := make([]float64, len(texts))
	for start := 0; start < len(texts); start += maxBatch {
		end := min(start+maxBatch, len(texts))
		batch, err := c.rerankBatch(ctx, query, texts[start:end])
		if err != nil {
			// One failed batch means an incomplete ranking, which would order the
			// page on a mix of scored and unscored candidates. Fail the whole call
			// so the caller falls back to a ranking that is at least coherent.
			return nil, err
		}
		copy(scores[start:end], batch)
	}
	return scores, nil
}

// RerankBudget returns the ceiling this client enforces on a complete Rerank
// call, satisfying palace.RerankDescriber so the search span can record the
// budget beside the duration it bounds.
func (c *Client) RerankBudget() time.Duration { return c.budget }

// rerankBatch scores one batch of at most maxBatch texts, returning the scores
// in the batch's own input order.
func (c *Client) rerankBatch(ctx context.Context, query string, texts []string) ([]float64, error) {
	raw, err := json.Marshal(rerankRequest{Query: query, Texts: texts, Documents: texts})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("tei: rerank -> %d: %s", resp.StatusCode, string(data))
	}

	results, err := decodeResults(data)
	if err != nil {
		return nil, err
	}
	if len(results) != len(texts) {
		return nil, fmt.Errorf("tei: expected %d scores, got %d", len(texts), len(results))
	}

	// Scatter back into input order. The indices come off the wire, so they are
	// bounds-checked before use — a malformed response must be an error, never a
	// panic in the middle of a search.
	scores := make([]float64, len(texts))
	for _, r := range results {
		if r.Index < 0 || r.Index >= len(texts) {
			return nil, fmt.Errorf("tei: score index %d out of range for %d texts", r.Index, len(texts))
		}
		scores[r.Index] = r.value()
	}
	return scores, nil
}
