// Package rerank scores query/document pairs with a cross-encoder served over
// HTTP, the precision half of retrieval that a bi-encoder cannot do.
//
// Embedding search compares a query vector to document vectors that were
// computed without ever seeing the query — fast, and necessarily approximate. A
// cross-encoder reads the pair together and answers the question retrieval only
// estimates: does THIS document answer THIS query. It is too expensive to run
// over a whole palace, which is exactly why it belongs at the end of the funnel,
// over the handful of candidates hybrid ranking already surfaced.
//
// bge-m3 (the embedder) and bge-reranker-v2-m3 (this) are a matched pair; the
// second half was missing.
//
// One client speaks two server dialects because the servers that host these
// models on a developer's machine disagree about the response shape, not the
// request:
//
//   - text-embeddings-inference (TEI) answers POST /rerank with a bare array
//     of {index, score}.
//   - llama.cpp's server answers POST /v1/rerank (Cohere-shaped) with
//     {results: [{index, relevance_score}]}.
//
// Both are accepted, so the same configuration works whether the operator runs
// TEI on an x86 server or llama.cpp on an arm64 laptop.
package rerank

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

// Score is one candidate's cross-encoder result: its position in the documents
// slice the caller passed, and how well it answers the query. Score is the
// server's raw relevance — comparable within one response, not across queries —
// so callers use it to ORDER candidates, never to threshold them.
type Score struct {
	Index int
	Score float64
}

// Client calls a cross-encoder rerank endpoint.
type Client struct {
	endpoint string
	model    string
	http     *http.Client
}

// New builds a Client for a rerank endpoint. baseURL may be a bare host
// ("http://reranker:8080"), in which case /rerank is appended, or a full URL
// including the path when the server uses another route (llama.cpp: /v1/rerank).
// model is sent when non-empty; single-model servers like TEI ignore it, while
// OpenAI-shaped ones require it.
func New(baseURL, model string, timeout time.Duration) *Client {
	endpoint := strings.TrimRight(baseURL, "/")
	// A URL with no path at all names a server, not an endpoint. Anything with a
	// path is taken verbatim, so an operator can point at whatever route their
	// server exposes without us guessing.
	if !strings.Contains(strings.TrimPrefix(strings.TrimPrefix(endpoint, "http://"), "https://"), "/") {
		endpoint += "/rerank"
	}
	return &Client{endpoint: endpoint, model: model, http: &http.Client{Timeout: timeout}}
}

// request is the shape both dialects accept: a query and the candidate texts.
type request struct {
	Query     string   `json:"query"`
	Texts     []string `json:"texts"`
	Documents []string `json:"documents"` // llama.cpp/Cohere name for Texts
	Model     string   `json:"model,omitempty"`
	TopN      int      `json:"top_n,omitempty"`
}

// response covers both dialects: TEI returns the bare array, llama.cpp wraps it
// in {results: …} and names the field relevance_score. Decoding both here keeps
// the dialect difference inside this package.
type response struct {
	Results []scored `json:"results"`
}

type scored struct {
	Index int `json:"index"`
	// Pointers, not plain float64s, because the question is WHICH FIELD THE
	// SERVER SENT — not which one is nonzero. A cross-encoder emits logits, and a
	// logit of exactly 0.0 is a perfectly ordinary score; keying off zero would
	// silently read the absent field in that case.
	Score          *float64 `json:"score"`
	RelevanceScore *float64 `json:"relevance_score"`
}

// value returns whichever score field the server populated, and 0 when neither
// is present — a result with no score at all still holds its place in the
// server's ordering, which is the information we actually use.
func (s scored) value() float64 {
	switch {
	case s.Score != nil:
		return *s.Score
	case s.RelevanceScore != nil:
		return *s.RelevanceScore
	default:
		return 0
	}
}

// Rerank scores every document against the query and returns the results
// best-first. An empty document slice short-circuits, so callers need not
// special-case it.
//
// Indices are validated against the input length before they are returned: a
// server that answers with an index we never sent would otherwise become an
// out-of-range panic in the caller's reorder loop.
func (c *Client) Rerank(ctx context.Context, query string, documents []string) ([]Score, error) {
	if len(documents) == 0 || strings.TrimSpace(query) == "" {
		return nil, nil
	}
	raw, err := json.Marshal(request{
		Query:     query,
		Texts:     documents,
		Documents: documents,
		Model:     c.model,
	})
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
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("rerank: %s -> %d: %s", c.endpoint, resp.StatusCode, string(body))
	}
	return decode(body, len(documents))
}

// decode parses either dialect's body and drops results that do not name a
// document we sent.
func decode(body []byte, n int) ([]Score, error) {
	var wrapped response
	results := wrapped.Results
	if err := json.Unmarshal(body, &wrapped); err == nil && len(wrapped.Results) > 0 {
		results = wrapped.Results
	} else {
		var bare []scored
		if err := json.Unmarshal(body, &bare); err != nil {
			return nil, fmt.Errorf("rerank: cannot decode response: %w", err)
		}
		results = bare
	}
	out := make([]Score, 0, len(results))
	for _, r := range results {
		if r.Index < 0 || r.Index >= n {
			continue
		}
		out = append(out, Score{Index: r.Index, Score: r.value()})
	}
	return out, nil
}
