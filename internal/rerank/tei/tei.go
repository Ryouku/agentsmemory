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
}

// New constructs a Client for the given TEI base URL (e.g. http://host:12434).
// timeout bounds each batched call: cross-encoding a full pool is the slowest
// step in search, and recall must degrade rather than hang when the box is
// loaded.
func New(baseURL string, timeout time.Duration) *Client {
	return &Client{
		endpoint: strings.TrimRight(baseURL, "/") + "/rerank",
		http:     &http.Client{Timeout: timeout},
	}
}

// rerankRequest is TEI's payload. The field is "texts" (not "documents") and the
// path is /rerank (not /v1/rerank) — TEI's shape differs from the Cohere-style
// APIs other rerank servers expose, so this is deliberately TEI-specific.
type rerankRequest struct {
	Query string   `json:"query"`
	Texts []string `json:"texts"`
	// RawScores false asks TEI for sigmoid-squashed scores in (0,1) rather than
	// bare logits. Bounded scores keep a reranked ordering comparable across
	// queries, which matters because we report the score, not just the order.
	RawScores bool `json:"raw_scores"`
}

// rerankResult is one scored pair. TEI answers with a flat array sorted
// best-first, so the index is the only link back to the caller's input order.
type rerankResult struct {
	Index int     `json:"index"`
	Score float64 `json:"score"`
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

// rerankBatch scores one batch of at most maxBatch texts, returning the scores
// in the batch's own input order.
func (c *Client) rerankBatch(ctx context.Context, query string, texts []string) ([]float64, error) {
	raw, err := json.Marshal(rerankRequest{Query: query, Texts: texts})
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

	var results []rerankResult
	if err := json.Unmarshal(data, &results); err != nil {
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
		scores[r.Index] = r.Score
	}
	return scores, nil
}
