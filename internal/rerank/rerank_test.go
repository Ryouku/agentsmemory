package rerank

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestRerankAcceptsBothDialects is the reason this client exists: the two servers
// an operator realistically runs return the same ranking in different shapes, and
// the caller must not have to care which one is deployed.
func TestRerankAcceptsBothDialects(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"tei bare array", `[{"index":1,"score":-3.2},{"index":0,"score":-11.0}]`},
		{"llama.cpp wrapped", `{"object":"list","results":[{"index":1,"relevance_score":-3.2},{"index":0,"relevance_score":-11.0}]}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var got request
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Errorf("decode request: %v", err)
				}
				// Both field names carry the candidates, so either server reads them.
				if len(got.Texts) != 2 || len(got.Documents) != 2 {
					t.Errorf("request carried texts=%d documents=%d, want 2 and 2", len(got.Texts), len(got.Documents))
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(c.body))
			}))
			defer srv.Close()

			scores, err := New(srv.URL+"/rerank", "", time.Second).Rerank(
				context.Background(), "installer config dir", []string{"unrelated", "the installer pins CLAUDE_CONFIG_DIR"})
			if err != nil {
				t.Fatalf("rerank: %v", err)
			}
			if len(scores) != 2 || scores[0].Index != 1 {
				t.Fatalf("want best-first starting at index 1, got %+v", scores)
			}
			// Negative relevance is normal for a cross-encoder logit; the client
			// must order by it, never treat it as absent.
			if scores[0].Score >= 0 || scores[0].Score <= scores[1].Score {
				t.Errorf("scores not carried through: %+v", scores)
			}
		})
	}
}

// TestRerankReadsAZeroScore guards the field-presence rule: a cross-encoder logit
// of exactly 0.0 is an ordinary score, so "which field did the server send" must
// not be decided by whether the value is nonzero.
func TestRerankReadsAZeroScore(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"index":0,"score":0.0},{"index":1,"score":-2.5}]`))
	}))
	defer srv.Close()

	scores, err := New(srv.URL, "", time.Second).Rerank(context.Background(), "q", []string{"a", "b"})
	if err != nil {
		t.Fatalf("rerank: %v", err)
	}
	if len(scores) != 2 || scores[0].Index != 0 || scores[0].Score != 0 || scores[1].Score != -2.5 {
		t.Fatalf("zero score not carried through: %+v", scores)
	}
}

// TestNewAppendsDefaultPath covers the one bit of URL guessing this client does:
// a bare host means "the usual endpoint", while any path is taken verbatim so an
// operator can point at llama.cpp's /v1/rerank without a second setting.
func TestNewAppendsDefaultPath(t *testing.T) {
	for base, want := range map[string]string{
		"http://reranker:8080":           "http://reranker:8080/rerank",
		"http://reranker:8080/":          "http://reranker:8080/rerank",
		"http://reranker:8080/v1/rerank": "http://reranker:8080/v1/rerank",
	} {
		if got := New(base, "", time.Second).endpoint; got != want {
			t.Errorf("New(%q).endpoint = %q, want %q", base, got, want)
		}
	}
}

// TestRerankIgnoresOutOfRangeIndex guards the caller's reorder loop: a server
// that names a document we never sent must not become a panic downstream.
func TestRerankIgnoresOutOfRangeIndex(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"index":7,"score":1},{"index":0,"score":0.5}]`))
	}))
	defer srv.Close()

	scores, err := New(srv.URL, "", time.Second).Rerank(context.Background(), "q", []string{"only one"})
	if err != nil {
		t.Fatalf("rerank: %v", err)
	}
	if len(scores) != 1 || scores[0].Index != 0 {
		t.Fatalf("want just index 0, got %+v", scores)
	}
}

// TestRerankSurfacesServerError keeps a broken reranker loud at this layer; the
// palace is where it is downgraded to "keep the hybrid order".
func TestRerankSurfacesServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "model not loaded", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	_, err := New(srv.URL, "", time.Second).Rerank(context.Background(), "q", []string{"a"})
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("want a 503 error, got %v", err)
	}
}

// TestRerankShortCircuits: no documents (or no query) is not an error, so callers
// need no special case.
func TestRerankShortCircuits(t *testing.T) {
	c := New("http://127.0.0.1:1", "", time.Second) // would fail if it dialled
	if scores, err := c.Rerank(context.Background(), "q", nil); err != nil || scores != nil {
		t.Errorf("empty documents: got %v, %v", scores, err)
	}
	if scores, err := c.Rerank(context.Background(), "  ", []string{"a"}); err != nil || scores != nil {
		t.Errorf("blank query: got %v, %v", scores, err)
	}
}
