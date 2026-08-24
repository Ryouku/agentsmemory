package teiembed

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// vectorFor produces a deterministic, input-derived vector so a test can assert
// that a specific input's vector came back in that input's slot. Order bugs are
// the failure mode batching introduces, and they are invisible if every vector
// looks alike.
func vectorFor(input string) []float32 {
	return []float32{float32(len(input)), float32(input[0])}
}

// echoServer answers TEI's /embed contract: a bare array with one vector per
// input, derived from the input so order is checkable. It records each batch it
// was asked for.
func echoServer(t *testing.T, batches *[][]string) *httptest.Server {
	t.Helper()
	return batchingServer(t, batches, http.StatusOK, maxBatch, nil)
}

func batchingServer(
	t *testing.T,
	batches *[][]string,
	infoStatus int,
	reportedLimit int,
	infoCalls *int,
) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/info" {
			if infoCalls != nil {
				*infoCalls = *infoCalls + 1
			}
			if infoStatus != http.StatusOK {
				w.WriteHeader(infoStatus)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]int{"max_client_batch_size": reportedLimit})
			return
		}
		if r.URL.Path != "/embed" {
			t.Errorf("path = %q, want /embed", r.URL.Path)
		}
		var req embedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if !req.Truncate {
			t.Error("truncate = false, want true so an over-long input cannot 413 a whole batch")
		}
		*batches = append(*batches, req.Inputs)

		out := make([][]float32, len(req.Inputs))
		for i, in := range req.Inputs {
			out[i] = vectorFor(in)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}))
}

// TestEmbedUsesTheServerClientBatchLimit makes batching follow the actual TEI
// deployment instead of the public default. Production advertises 128 through
// /info; keeping the client fixed at 32 turns one accepted request into four
// sequential HTTP round trips without changing a single vector.
func TestEmbedUsesTheServerClientBatchLimit(t *testing.T) {
	const serverLimit = 128
	var batches [][]string
	infoCalls := 0
	srv := batchingServer(t, &batches, http.StatusOK, serverLimit, &infoCalls)
	defer srv.Close()

	inputs := make([]string, 260)
	for i := range inputs {
		inputs[i] = fmt.Sprintf("input-%03d", i)
	}
	embedder := New(srv.URL, 5*time.Second)
	for run := 0; run < 2; run++ {
		got, err := embedder.Embed(context.Background(), inputs)
		if err != nil {
			t.Fatalf("Embed run %d: %v", run, err)
		}
		for i, input := range inputs {
			want := vectorFor(input)
			if got[i][0] != want[0] || got[i][1] != want[1] {
				t.Fatalf("run %d vector %d = %v, want %v", run, i, got[i], want)
			}
		}
	}
	if infoCalls != 1 {
		t.Fatalf("/info calls = %d, want 1 cached discovery", infoCalls)
	}
	wantSizes := []int{serverLimit, serverLimit, 4, serverLimit, serverLimit, 4}
	if len(batches) != len(wantSizes) {
		t.Fatalf("made %d requests, want %d: %#v", len(batches), len(wantSizes), batches)
	}
	for i, want := range wantSizes {
		if len(batches[i]) != want {
			t.Errorf("batch %d carried %d inputs, want %d", i, len(batches[i]), want)
		}
	}
}

func TestEmbedFallsBackToTheTEIDefaultWhenInfoIsUnavailable(t *testing.T) {
	var batches [][]string
	srv := batchingServer(t, &batches, http.StatusNotFound, 0, nil)
	defer srv.Close()

	inputs := make([]string, 70)
	for i := range inputs {
		inputs[i] = fmt.Sprintf("input-%02d", i)
	}
	if _, err := New(srv.URL, 5*time.Second).Embed(context.Background(), inputs); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	wantSizes := []int{maxBatch, maxBatch, 70 - 2*maxBatch}
	if len(batches) != len(wantSizes) {
		t.Fatalf("made %d requests, want %d", len(batches), len(wantSizes))
	}
	for i, want := range wantSizes {
		if len(batches[i]) != want {
			t.Errorf("batch %d carried %d inputs, want fallback size %d", i, len(batches[i]), want)
		}
	}
}

func TestEmbedCapsAnExcessiveServerClientBatchLimit(t *testing.T) {
	var batches [][]string
	srv := batchingServer(t, &batches, http.StatusOK, 512, nil)
	defer srv.Close()

	inputs := make([]string, 260)
	for i := range inputs {
		inputs[i] = fmt.Sprintf("input-%03d", i)
	}
	if _, err := New(srv.URL, 5*time.Second).Embed(context.Background(), inputs); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	wantSizes := []int{maxDiscoveredBatch, maxDiscoveredBatch, 260 - 2*maxDiscoveredBatch}
	if len(batches) != len(wantSizes) {
		t.Fatalf("made %d requests, want %d", len(batches), len(wantSizes))
	}
	for i, want := range wantSizes {
		if len(batches[i]) != want {
			t.Errorf("batch %d carried %d inputs, want capped size %d", i, len(batches[i]), want)
		}
	}
}

func TestEmbedReturnsOneVectorPerInputInOrder(t *testing.T) {
	var batches [][]string
	srv := echoServer(t, &batches)
	defer srv.Close()

	inputs := []string{"alpha", "b", "gamma-longer"}
	got, err := New(srv.URL, 5*time.Second).Embed(context.Background(), inputs)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(got) != len(inputs) {
		t.Fatalf("got %d vectors, want %d", len(got), len(inputs))
	}
	for i, in := range inputs {
		want := vectorFor(in)
		if got[i][0] != want[0] || got[i][1] != want[1] {
			t.Errorf("vector %d = %v, want %v (input %q landed in the wrong slot)", i, got[i], want, in)
		}
	}
	if len(batches) != 1 {
		t.Errorf("made %d requests, want 1 — three inputs fit in one batch", len(batches))
	}
}

// TestEmbedSplitsOverMaxBatchPreservingOrder is the test that matters: the
// v0.0.84 rerank fix existed because a batch over TEI's limit fails outright, and
// the bug that fix nearly introduced was scattering later batches' results onto
// the wrong inputs.
func TestEmbedSplitsOverMaxBatchPreservingOrder(t *testing.T) {
	var batches [][]string
	srv := echoServer(t, &batches)
	defer srv.Close()

	// 70 inputs over a 32 limit => 32 + 32 + 6.
	inputs := make([]string, 70)
	for i := range inputs {
		inputs[i] = fmt.Sprintf("input-%02d", i)
	}
	got, err := New(srv.URL, 5*time.Second).Embed(context.Background(), inputs)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(got) != len(inputs) {
		t.Fatalf("got %d vectors, want %d", len(got), len(inputs))
	}
	for i, in := range inputs {
		want := vectorFor(in)
		if got[i][0] != want[0] || got[i][1] != want[1] {
			t.Fatalf("vector %d = %v, want %v (input %q landed in the wrong slot)", i, got[i], want, in)
		}
	}
	wantSizes := []int{maxBatch, maxBatch, 70 - 2*maxBatch}
	if len(batches) != len(wantSizes) {
		t.Fatalf("made %d requests, want %d", len(batches), len(wantSizes))
	}
	for i, want := range wantSizes {
		if len(batches[i]) != want {
			t.Errorf("batch %d carried %d inputs, want %d", i, len(batches[i]), want)
		}
		if len(batches[i]) > maxBatch {
			t.Errorf("batch %d exceeds TEI's limit with %d inputs", i, len(batches[i]))
		}
	}
}

func TestEmbedEmptyInputMakesNoRequest(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	got, err := New(srv.URL, 5*time.Second).Embed(context.Background(), nil)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
	if called {
		t.Error("called TEI for an empty input slice")
	}
}

func TestNewToleratesTrailingSlash(t *testing.T) {
	var batches [][]string
	srv := echoServer(t, &batches)
	defer srv.Close()

	if _, err := New(srv.URL+"/", 5*time.Second).Embed(context.Background(), []string{"x"}); err != nil {
		t.Fatalf("Embed with trailing-slash base URL: %v", err)
	}
}

// TestEmbedSurfacesErrorBody pins the behaviour that made the sparse limitation
// diagnosable at all: TEI's status code alone does not say why, the body does.
func TestEmbedSurfacesErrorBody(t *testing.T) {
	const body = `{"error":"Backend error: Model is not an embedding model with SPLADE pooling","error_type":"Backend"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(424)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	_, err := New(srv.URL, 5*time.Second).Embed(context.Background(), []string{"x"})
	if err == nil {
		t.Fatal("want an error for a 424 response")
	}
	if !strings.Contains(err.Error(), "424") || !strings.Contains(err.Error(), "SPLADE") {
		t.Errorf("error = %q, want it to carry both the status and TEI's reason", err)
	}
}

func TestEmbedRejectsWrongVectorCount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Two inputs asked for, one vector returned: silently accepting this would
		// misalign every downstream vector with its drawer.
		_, _ = w.Write([]byte(`[[1.0,2.0]]`))
	}))
	defer srv.Close()

	_, err := New(srv.URL, 5*time.Second).Embed(context.Background(), []string{"a", "b"})
	if err == nil {
		t.Fatal("want an error when TEI returns fewer vectors than inputs")
	}
	if !strings.Contains(err.Error(), "expected 2") {
		t.Errorf("error = %q, want it to name the mismatch", err)
	}
}

func TestEmbedRejectsMalformedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// An object where TEI's contract is a bare array — what a proxy error page
		// or an Ollama-shaped response would look like.
		_, _ = w.Write([]byte(`{"embeddings":[[1.0]]}`))
	}))
	defer srv.Close()

	if _, err := New(srv.URL, 5*time.Second).Embed(context.Background(), []string{"a"}); err == nil {
		t.Fatal("want an error for a non-array response body")
	}
}

func TestEmbedOneReturnsTheSingleVector(t *testing.T) {
	var batches [][]string
	srv := echoServer(t, &batches)
	defer srv.Close()

	got, err := New(srv.URL, 5*time.Second).EmbedOne(context.Background(), "query")
	if err != nil {
		t.Fatalf("EmbedOne: %v", err)
	}
	want := vectorFor("query")
	if got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %v, want %v", got, want)
	}
}
