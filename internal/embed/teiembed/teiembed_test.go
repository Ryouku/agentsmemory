package teiembed

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

// TestCapabilityDiscoveryRetriesAfterAFailedProbe is the gate on a permanent,
// silent downgrade.
//
// Discovery used sync.Once, which fires whether the probe succeeded or failed.
// One transient error — TEI still loading its model, a proxy 503, or a caller
// who hangs up, since the probe also inherited the FIRST caller's context —
// pinned the 32-input default for the lifetime of the process: four times the
// round trips, no log, no retry, and the adaptive batching this package exists
// to provide silently gone.
//
// The existing 128-input test cannot fail for this reason, because its first
// call succeeds. That is the gap this closes.
func TestCapabilityDiscoveryRetriesAfterAFailedProbe(t *testing.T) {
	var infoHits, infoFailures int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/info" {
			infoHits++
			// Fail the first probe the way a still-warming TEI does.
			if infoFailures == 0 {
				infoFailures++
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte(`{"max_client_batch_size":128}`))
			return
		}
		var req embedRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		out := make([][]float32, len(req.Inputs))
		for i := range out {
			out[i] = []float32{1, 0}
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer srv.Close()

	e := New(srv.URL, 5*time.Second)
	e.retryAfter = 0 // this test is about retrying at all, not about the backoff
	if _, err := e.Embed(context.Background(), []string{"a", "b"}); err != nil {
		t.Fatalf("first embed: %v", err)
	}
	if got := e.clientBatchSize(context.Background()); got != maxDiscoveredBatch {
		t.Fatalf("after a failed probe the client is stuck at batch %d; want it to retry and discover %d", got, maxDiscoveredBatch)
	}
	if infoHits < 2 {
		t.Fatalf("/info was asked %d times; a failed probe must not be cached as the answer", infoHits)
	}

	// And once discovered, it is not re-probed on every call.
	before := infoHits
	if _, err := e.Embed(context.Background(), []string{"c", "d"}); err != nil {
		t.Fatalf("third embed: %v", err)
	}
	if infoHits != before {
		t.Fatalf("/info re-probed after a successful discovery (%d -> %d); the success must be cached", before, infoHits)
	}
}

// TestCallerCancellationDoesNotDecideTheProcessBatchSize pins the second half:
// the probe must not inherit the cancellation of whichever embed happened to be
// first, or one disconnecting client downgrades every later batch.
func TestCallerCancellationDoesNotDecideTheProcessBatchSize(t *testing.T) {
	var infoHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/info" {
			infoHits++
			_, _ = w.Write([]byte(`{"max_client_batch_size":128}`))
			return
		}
		_, _ = w.Write([]byte(`[[1,0]]`))
	}))
	defer srv.Close()

	e := New(srv.URL, 5*time.Second)
	dead, cancel := context.WithCancel(context.Background())
	cancel()
	// The embed itself is expected to fail. What must not happen is the
	// capability probe being cancelled along with it.
	_, _ = e.Embed(dead, []string{"a", "b"})

	// Assert on the probe, not on a later retry: with the retry in place a
	// second call would rediscover 128 either way, so only whether /info was
	// REACHED separates a detached probe from an inherited cancellation.
	if infoHits == 0 {
		t.Fatal("a cancelled caller cancelled the capability probe with it; /info was never reached")
	}
	if e.batchSize != maxDiscoveredBatch {
		t.Fatalf("probe under a cancelled caller left batchSize=%d; want %d discovered on that same call", e.batchSize, maxDiscoveredBatch)
	}
}

// TestCapabilityProbeDoesNotSerialiseEmbedding is the regression gate for a fix
// that was worse than the defect it replaced.
//
// Caching only a successful probe is right, but the first version did the probe
// while holding the mutex that guards the cached value. Every concurrent embed
// in the process then queued behind one /info, so a proxy that black-holes the
// path — or a TEI still loading — turned the entire write path into a queue one
// timeout wide, shared across tenants. Measured at 8 callers x 300ms = 2.4s with
// a maximum of ONE concurrent /embed.
//
// Discovery is an optimisation, so no caller should ever wait for it.
func TestCapabilityProbeDoesNotSerialiseEmbedding(t *testing.T) {
	var mu sync.Mutex
	var inFlight, peakEmbed int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/info" {
			time.Sleep(200 * time.Millisecond) // a slow, failing probe
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		mu.Lock()
		inFlight++
		peakEmbed = max(peakEmbed, inFlight)
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		mu.Lock()
		inFlight--
		mu.Unlock()

		var req embedRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		out := make([][]float32, len(req.Inputs))
		for i := range out {
			out[i] = []float32{1, 0}
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer srv.Close()

	e := New(srv.URL, 5*time.Second)
	const callers = 8
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = e.Embed(context.Background(), []string{"a", "b"})
		}()
	}
	wg.Wait()

	mu.Lock()
	peak := peakEmbed
	mu.Unlock()
	if peak < 2 {
		t.Fatalf("peak concurrent /embed = %d; capability discovery is serialising the embed path", peak)
	}
}

// TestFailedCapabilityProbeBacksOff pins the other half. Not caching a failure
// must not mean re-asking on every call: a deployment that exposes /embed
// without /info is supported, and it should cost one probe per backoff window
// rather than one per embed — each of which can burn the full info timeout.
func TestFailedCapabilityProbeBacksOff(t *testing.T) {
	var mu sync.Mutex
	infoHits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/info" {
			mu.Lock()
			infoHits++
			mu.Unlock()
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`[[1,0]]`))
	}))
	defer srv.Close()

	e := New(srv.URL, 5*time.Second)
	for range 5 {
		if got := e.clientBatchSize(context.Background()); got != maxBatch {
			t.Fatalf("batch size without /info = %d; want the %d fallback", got, maxBatch)
		}
	}

	mu.Lock()
	hits := infoHits
	mu.Unlock()
	if hits != 1 {
		t.Fatalf("/info probed %d times across 5 calls inside one backoff window; want 1", hits)
	}
}

// TestProbeKeepsTheModelWindowItAlreadyFetched: /info's max_input_length and
// model_id reach DescribeEmbedder.
//
// The probe has always requested /info and decoded exactly ONE field from the
// response, max_client_batch_size, discarding the other two. The consequence was
// not a missing feature but a missing FACT: nothing in this repository could
// state any model's input window, so ChunkSize sits at 1600 characters — about
// 5% of bge-m3's 8192 tokens — on the authority of a source comment, and
// MaxEmbedRunes is documented as "CONSERVATIVE HEADROOM, NOT A MEASURED
// CEILING". The number that would have grounded both was one struct field away
// in a response the code was already parsing.
//
// The window is asserted as REPORTED rather than as a constant: the point is
// that it comes from the server, so a test hard-coding 8192 would pass against
// an implementation that hard-coded it too.
func TestProbeKeepsTheModelWindowItAlreadyFetched(t *testing.T) {
	const wantWindow, wantModel = 8192, "BAAI/bge-m3"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/info") {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"max_client_batch_size":32,"max_input_length":%d,"model_id":%q}`, wantWindow, wantModel)
			return
		}
		var req struct {
			Inputs []string `json:"inputs"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		vecs := make([]string, len(req.Inputs))
		for i := range vecs {
			vecs[i] = "[0.1,0.2]"
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, "[%s]", strings.Join(vecs, ","))
	}))
	defer srv.Close()

	e := New(srv.URL, 5*time.Second)

	// Before any probe the honest answer is "not measured", not a guess.
	if _, model, window := e.DescribeEmbedder(); model != "" || window != 0 {
		t.Errorf("before probing: model=%q window=%d, want empty/0 — reporting an unmeasured "+
			"window is exactly the folklore this change exists to replace", model, window)
	}

	// TWO inputs, not one, and that is forced rather than stylistic. Embed only
	// consults clientBatchSize when len(inputs) > 1 (teiembed.go:213), so a single
	// EmbedOne never probes — which is what the first version of this test used
	// and why it failed. The limitation is real and is recorded on
	// DescribeEmbedder: a SEARCH only ever calls EmbedOne, so on a search-only
	// server the window stays unknown until some write embeds a batch.
	if _, err := e.Embed(context.Background(), []string{"drive", "the probe"}); err != nil {
		t.Fatalf("Embed: %v", err)
	}

	backend, model, window := e.DescribeEmbedder()
	if backend != "tei" {
		t.Errorf("backend = %q, want tei", backend)
	}
	if window != wantWindow {
		t.Errorf("window = %d, want %d — /info reported it and the decoder dropped it, which is "+
			"the whole defect", window, wantWindow)
	}
	if model != wantModel {
		t.Errorf("model = %q, want %q — am.dim cannot identify a model, so two different "+
			"1024-dimension models are indistinguishable on a trace without this", model, wantModel)
	}
}

// TestDescribeEmbedderReportsNoWindowWhenTheServerOmitsIt: absent beats guessed.
//
// A TEI build that does not advertise max_input_length must leave the window at
// 0 so the span omits it entirely. The alternative — defaulting to 8192 — would
// put an unverified number on a trace and reintroduce, as data, the folklore
// this replaced.
func TestDescribeEmbedderReportsNoWindowWhenTheServerOmitsIt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/info") {
			fmt.Fprint(w, `{"max_client_batch_size":32}`) // no window, no model
			return
		}
		var req struct {
			Inputs []string `json:"inputs"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		vecs := make([]string, len(req.Inputs))
		for i := range vecs {
			vecs[i] = "[0.1,0.2]"
		}
		fmt.Fprintf(w, "[%s]", strings.Join(vecs, ","))
	}))
	defer srv.Close()

	e := New(srv.URL, 5*time.Second)
	if _, err := e.Embed(context.Background(), []string{"drive", "the probe"}); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if _, _, window := e.DescribeEmbedder(); window != 0 {
		t.Errorf("window = %d for a server that reported none, want 0", window)
	}
}
