package tei

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestRerankAlignsScoresToInputOrder is the contract that matters: TEI answers
// best-first, callers index by their own input, so the scatter must undo TEI's
// sort. A client that returned the wire order would silently score the wrong
// documents — the kind of bug that looks like "the reranker is bad" instead of
// "the client is wrong".
func TestRerankAlignsScoresToInputOrder(t *testing.T) {
	var gotPath string
	var gotBody rerankRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &gotBody); err != nil {
			t.Errorf("server: bad request body: %v", err)
		}
		// Best-first, deliberately NOT the input order: input 2 wins.
		_, _ = w.Write([]byte(`[{"index":2,"score":0.9},{"index":0,"score":0.5},{"index":1,"score":0.1}]`))
	}))
	defer srv.Close()

	scores, err := New(srv.URL, time.Second).Rerank(context.Background(), "q", []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if want := []float64{0.5, 0.1, 0.9}; !equal(scores, want) {
		t.Errorf("scores = %v, want %v (input order, not wire order)", scores, want)
	}
	if gotPath != "/rerank" {
		t.Errorf("path = %q, want /rerank", gotPath)
	}
	if gotBody.Query != "q" || len(gotBody.Texts) != 3 {
		t.Errorf("request = %+v, want query q with 3 texts", gotBody)
	}
}

// TestRerankTrailingSlashBaseURL guards the config path: an operator pasting a
// URL with a trailing slash must not produce a //rerank that 404s.
func TestRerankTrailingSlashBaseURL(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`[{"index":0,"score":1}]`))
	}))
	defer srv.Close()

	if _, err := New(srv.URL+"/", time.Second).Rerank(context.Background(), "q", []string{"a"}); err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if gotPath != "/rerank" {
		t.Errorf("path = %q, want /rerank", gotPath)
	}
}

// TestRerankSplitsOversizedInput pins the fix for a bug that was invisible in
// unit tests and silent in production: TEI rejects an array larger than its
// --max-client-batch-size (32) with a 422, and because search fails open, a pool
// of 50 meant a configured reranker quietly never reranked anything. The batches
// must also stay index-aligned — each request's indices are batch-relative, so a
// missing offset would scatter later batches' scores onto the wrong documents.
func TestRerankSplitsOversizedInput(t *testing.T) {
	const n = maxBatch*2 + 5 // three batches, last one partial

	var batchSizes []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rerankRequest
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("server: bad request body: %v", err)
		}
		if len(req.Texts) > maxBatch {
			t.Errorf("server received %d texts, over TEI's limit of %d", len(req.Texts), maxBatch)
		}
		batchSizes = append(batchSizes, len(req.Texts))

		// Score each text by the number it carries, so a misaligned scatter shows
		// up as a score landing on the wrong document.
		results := make([]rerankResult, len(req.Texts))
		for i, txt := range req.Texts {
			var num int
			if _, err := fmt.Sscanf(txt, "doc-%d", &num); err != nil {
				t.Errorf("server: unexpected text %q", txt)
			}
			score := float64(num)
			results[i] = rerankResult{Index: i, Score: &score}
		}
		_ = json.NewEncoder(w).Encode(results)
	}))
	defer srv.Close()

	texts := make([]string, n)
	for i := range texts {
		texts[i] = fmt.Sprintf("doc-%d", i)
	}
	scores, err := New(srv.URL, 5*time.Second).Rerank(context.Background(), "q", texts)
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if len(scores) != n {
		t.Fatalf("got %d scores, want %d", len(scores), n)
	}
	for i, got := range scores {
		if got != float64(i) {
			t.Errorf("scores[%d] = %v, want %v — batch offset lost", i, got, float64(i))
		}
	}
	if want := []int{maxBatch, maxBatch, 5}; !equalInt(batchSizes, want) {
		t.Errorf("batch sizes = %v, want %v", batchSizes, want)
	}
}

func TestRerankEmptyTextsSkipsTheCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("server called for an empty texts slice")
	}))
	defer srv.Close()

	scores, err := New(srv.URL, time.Second).Rerank(context.Background(), "q", nil)
	if err != nil || scores != nil {
		t.Errorf("Rerank(nil) = %v, %v; want nil, nil", scores, err)
	}
}

// TestRerankRejectsMalformedResponses covers the untrusted-input surface: every
// one of these must be an error the caller can fail open on, never a panic.
func TestRerankRejectsMalformedResponses(t *testing.T) {
	for name, body := range map[string]string{
		"index out of range":   `[{"index":7,"score":0.9}]`,
		"negative index":       `[{"index":-1,"score":0.9}]`,
		"fewer scores":         `[]`,
		"more scores":          `[{"index":0,"score":1},{"index":0,"score":1}]`,
		"empty llama wrapper":  `{"results":[]}`,
		"neither array nor {}": `"a string"`,
		"truncated":            `[{"index":0,`,
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()

			if _, err := New(srv.URL, time.Second).Rerank(context.Background(), "q", []string{"a"}); err == nil {
				t.Errorf("Rerank accepted %s: %s", name, body)
			}
		})
	}
}

// TestRerankAcceptsLlamaCppDialect is the regression test for a capability that
// was lost by deletion rather than by decision: PR #17 replaced a dual-dialect
// client with this TEI-only one, and llama.cpp — the ONLY way to run a
// cross-encoder on Apple Silicon, since TEI ships no arm64 image — answers
// Cohere-shaped. Decoding only TEI's bare array meant those operators got a
// decode error, a fail-open, and hybrid-only search with nothing but a log line
// to say the reranker they configured never ran.
func TestRerankAcceptsLlamaCppDialect(t *testing.T) {
	var gotBody rerankRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &gotBody); err != nil {
			t.Errorf("server: bad request body: %v", err)
		}
		_, _ = w.Write([]byte(`{"object":"list","results":[{"index":2,"relevance_score":0.9},{"index":0,"relevance_score":0.5},{"index":1,"relevance_score":0.1}]}`))
	}))
	defer srv.Close()

	scores, err := New(srv.URL, time.Second).Rerank(context.Background(), "q", []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if want := []float64{0.5, 0.1, 0.9}; !equal(scores, want) {
		t.Errorf("scores = %v, want %v (input order, relevance_score read)", scores, want)
	}
	// llama.cpp reads "documents"; TEI reads "texts". Both must be on the wire,
	// or one of the two servers sees an empty candidate list.
	if len(gotBody.Documents) != 3 || len(gotBody.Texts) != 3 {
		t.Errorf("request carried texts=%d documents=%d, want 3 of each", len(gotBody.Texts), len(gotBody.Documents))
	}
}

// TestRerankReadsZeroScore pins the field-by-presence rule. A cross-encoder emits
// logits and a logit of exactly 0.0 is an ordinary "neutral" score, so choosing
// the field by nonzero-ness would fall through to the absent dialect's field
// precisely when the real one had something to say.
func TestRerankReadsZeroScore(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"index":0,"score":0.0},{"index":1,"score":-2.5}]`))
	}))
	defer srv.Close()

	scores, err := New(srv.URL, time.Second).Rerank(context.Background(), "q", []string{"a", "b"})
	if err != nil {
		t.Fatalf("Rerank: %v", err)
	}
	if want := []float64{0, -2.5}; !equal(scores, want) {
		t.Errorf("scores = %v, want %v", scores, want)
	}
}

// TestRerankEndpointFromBaseURL pins the three forms this repo's own configs
// actually ship. docker-compose.full.yml sets RERANK_URL to a /v1 prefix and
// .env.docker.example spells out /v1/rerank, so a rule that only appended would
// break the second and one that only took the URL verbatim would break the first
// — either way a configured reranker silently 404s and search fails open.
func TestRerankEndpointFromBaseURL(t *testing.T) {
	for _, tc := range []struct{ suffix, want string }{
		{"", "/rerank"},                    // README: http://localhost:12434
		{"/", "/rerank"},                   // pasted trailing slash
		{"/v1", "/v1/rerank"},              // docker-compose.full.yml
		{"/v1/rerank", "/v1/rerank"},       // .env.docker.example
		{"/rerank", "/rerank"},             // no /rerank/rerank
		{"/v1/reranking", "/v1/reranking"}, // llama.cpp alias
	} {
		t.Run(tc.suffix+"->"+tc.want, func(t *testing.T) {
			var gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				_, _ = w.Write([]byte(`[{"index":0,"score":1}]`))
			}))
			defer srv.Close()

			if _, err := New(srv.URL+tc.suffix, time.Second).Rerank(context.Background(), "q", []string{"a"}); err != nil {
				t.Fatalf("Rerank: %v", err)
			}
			if gotPath != tc.want {
				t.Errorf("base %q -> path %q, want %q", srv.URL+tc.suffix, gotPath, tc.want)
			}
		})
	}
}

func TestRerankSurfacesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"model not loaded"}`))
	}))
	defer srv.Close()

	_, err := New(srv.URL, time.Second).Rerank(context.Background(), "q", []string{"a"})
	if err == nil {
		t.Fatal("Rerank succeeded on a 500")
	}
	if got := err.Error(); !strings.Contains(got, "model not loaded") {
		t.Errorf("error %q does not carry the server's body", got)
	}
}

func equal(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalInt(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
