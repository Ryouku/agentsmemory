package palace

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
)

// selectiveEvidenceEmbedder makes only the two paraphrased passages align with
// the query. Retrieval order is controlled separately by orderedVectors, so the
// test observes evidence selection rather than an incidental vector ranking.
type selectiveEvidenceEmbedder struct {
	// mu guards batches/queries: evidence batches are embedded concurrently, so
	// an unsynchronised spy is itself a data race under -race.
	mu      sync.Mutex
	batches [][]string
	queries []string
	err     error
}

func (e *selectiveEvidenceEmbedder) Embed(_ context.Context, inputs []string) ([][]float32, error) {
	e.mu.Lock()
	e.batches = append(e.batches, append([]string(nil), inputs...))
	e.mu.Unlock()
	if e.err != nil {
		return nil, e.err
	}
	out := make([][]float32, len(inputs))
	for i, input := range inputs {
		out[i] = make([]float32, fakeDim)
		if strings.Contains(input, "FIRST_SEMANTIC_EVIDENCE") || strings.Contains(input, "SECOND_SEMANTIC_EVIDENCE") {
			out[i][0] = 1
		} else {
			out[i][1] = 1
		}
	}
	return out, nil
}

func (e *selectiveEvidenceEmbedder) EmbedOne(_ context.Context, input string) ([]float32, error) {
	e.mu.Lock()
	e.queries = append(e.queries, input)
	e.mu.Unlock()
	query := make([]float32, fakeDim)
	query[0] = 1
	return query, nil
}

func semanticEvidenceFixture(t *testing.T) (*Service, string, []store.Hit) {
	t.Helper()
	ctx := context.Background()
	base := newTestService(t)
	const team = "team-semantic-evidence"
	content := "opening metadata and retrieval anchor " + strings.Repeat("neutral opening material. ", 90) +
		" FIRST_SEMANTIC_EVIDENCE explains the unstated implementation constraint " +
		strings.Repeat("middle material with no query vocabulary. ", 100) +
		" SECOND_SEMANTIC_EVIDENCE records the unresolved operational consequence " +
		strings.Repeat("neutral closing material. ", 50)
	created, err := base.Add(ctx, team, AddInput{Wing: "w", Room: "r", Content: content})
	if err != nil {
		t.Fatalf("add long memory: %v", err)
	}
	if len(created.Drawers) < 3 {
		t.Fatalf("fixture produced %d chunks, want at least 3", len(created.Drawers))
	}
	hits := make([]store.Hit, len(created.Drawers))
	for i, drawer := range created.Drawers {
		hits[i] = store.Hit{ID: drawer.ID, Score: float32(1 - float64(i)/100)}
	}
	return base, team, hits
}

// TestSemanticEvidenceSelectorFindsParaphrasedDistantPassages drives the served
// Search path. The lexical control has no matching query vocabulary and falls
// back to the retrieved opening chunk; the semantic arm must embed windows from
// the whole memory and give both distant passages to the cross-encoder, once per
// logical memory and inside the existing model budget.
func TestSemanticEvidenceSelectorFindsParaphrasedDistantPassages(t *testing.T) {
	ctx := context.Background()
	base, team, ordered := semanticEvidenceFixture(t)

	controlDocs := &recordingDocuments{}
	controlEmbed := &selectiveEvidenceEmbedder{}
	control := base.Clone().WithMemoryLevelRanking(true).
		WithMemoryEvidenceSelector("lexical").
		WithReranker(controlDocs, 10).WithRerankWeight(0.5)
	control.embed = controlEmbed
	control.vectors = &orderedVectors{VectorStore: base.vectors, hits: ordered}
	if _, err := control.Search(ctx, team, SearchQuery{Query: "unfinished reasoning", Limit: 1, SkipTelemetry: true}); err != nil {
		t.Fatalf("lexical control search: %v", err)
	}
	if len(controlDocs.docs) != 1 || len(controlDocs.docs[0]) != 1 {
		t.Fatalf("lexical reranker calls/docs = %#v, want one document", controlDocs.docs)
	}
	if strings.Contains(controlDocs.docs[0][0], "SEMANTIC_EVIDENCE") {
		t.Fatalf("lexical control unexpectedly selected the paraphrased passages: %q", controlDocs.docs[0][0])
	}

	semanticDocs := &recordingDocuments{}
	semanticEmbed := &selectiveEvidenceEmbedder{}
	semantic := base.Clone().WithMemoryLevelRanking(true).
		WithMemoryEvidenceSelector("semantic").
		WithReranker(semanticDocs, 10).WithRerankWeight(0.5)
	semantic.embed = semanticEmbed
	semantic.vectors = &orderedVectors{VectorStore: base.vectors, hits: ordered}
	if _, err := semantic.Search(ctx, team, SearchQuery{
		Query: "unfinished reasoning", Context: "caller context must reach only the cross-encoder",
		Limit: 1, SkipTelemetry: true,
	}); err != nil {
		t.Fatalf("semantic search: %v", err)
	}
	if len(semanticDocs.docs) != 1 || len(semanticDocs.docs[0]) != 1 {
		t.Fatalf("semantic reranker calls/docs = %#v, want one document", semanticDocs.docs)
	}
	doc := semanticDocs.docs[0][0]
	if !strings.Contains(doc, "FIRST_SEMANTIC_EVIDENCE") || !strings.Contains(doc, "SECOND_SEMANTIC_EVIDENCE") {
		t.Fatalf("semantic evidence omitted distant paraphrased passages: %q", doc)
	}
	if strings.Index(doc, "FIRST_SEMANTIC_EVIDENCE") > strings.Index(doc, "SECOND_SEMANTIC_EVIDENCE") {
		t.Fatalf("semantic evidence changed source order: %q", doc)
	}
	if got := len([]rune(doc)); got > ChunkSize {
		t.Fatalf("semantic evidence is %d runes, above the %d-rune cross-encoder budget", got, ChunkSize)
	}
	if len(semanticEmbed.batches) == 0 || len(semanticEmbed.batches[0]) < 2 {
		t.Fatalf("passage embeddings were not batched: %#v", semanticEmbed.batches)
	}
	if got := strings.Join(semanticEmbed.queries, "|"); got != "unfinished reasoning" {
		t.Fatalf("semantic selector embedded %q, want the raw query exactly once; Context belongs only to the cross-encoder", got)
	}
}

// TestSemanticEvidenceSelectorRequiresBothGates keeps the nested experiment
// honest. Selecting semantic is observable in the profile, but it must not add
// passage-embedding latency unless memory-level ranking and the cross-encoder
// are both active.
func TestSemanticEvidenceSelectorRequiresBothGates(t *testing.T) {
	for _, tc := range []struct {
		name     string
		memory   bool
		reranker bool
	}{
		{name: "chunk ranking", memory: false, reranker: true},
		{name: "no cross-encoder", memory: true, reranker: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, team, ordered := semanticEvidenceFixture(t)
			embed := &selectiveEvidenceEmbedder{}
			svc := base.Clone().WithMemoryLevelRanking(tc.memory).WithMemoryEvidenceSelector("semantic")
			if tc.reranker {
				svc.WithReranker(&recordingDocuments{}, 10).WithRerankWeight(0.5)
			}
			svc.embed = embed
			svc.vectors = &orderedVectors{VectorStore: base.vectors, hits: ordered}
			if _, err := svc.Search(context.Background(), team, SearchQuery{
				Query: "unfinished reasoning", Limit: 1, SkipTelemetry: true,
			}); err != nil {
				t.Fatalf("search: %v", err)
			}
			if len(embed.batches) != 0 {
				t.Fatalf("semantic passage embedding ran with memory=%v reranker=%v: %#v", tc.memory, tc.reranker, embed.batches)
			}
		})
	}
}

// TestSemanticEvidenceSelectorFailsOpenForTheWholeShortlist pins availability:
// passage embedding is an optional refinement, so a failed batch must preserve
// the lexical documents for every candidate instead of returning no recall or a
// half-semantic, half-lexical comparison.
func TestSemanticEvidenceSelectorFailsOpenForTheWholeShortlist(t *testing.T) {
	ctx := context.Background()
	base, team, ordered := semanticEvidenceFixture(t)
	other, err := base.Add(ctx, team, AddInput{
		Wing: "w", Room: "r",
		Content: "second memory opening " + strings.Repeat("another long candidate passage. ", 120),
	})
	if err != nil {
		t.Fatalf("add second long memory: %v", err)
	}
	for i, drawer := range other.Drawers {
		ordered = append(ordered, store.Hit{ID: drawer.ID, Score: float32(0.8 - float64(i)/100)})
	}

	lexicalDocs := &recordingDocuments{}
	lexical := base.Clone().WithMemoryLevelRanking(true).
		WithMemoryEvidenceSelector("lexical").
		WithReranker(lexicalDocs, 10).WithRerankWeight(0.5)
	lexical.embed = &selectiveEvidenceEmbedder{}
	lexical.vectors = &orderedVectors{VectorStore: base.vectors, hits: ordered}
	if _, err := lexical.Search(ctx, team, SearchQuery{Query: "unfinished reasoning", Limit: 2, SkipTelemetry: true}); err != nil {
		t.Fatalf("lexical search: %v", err)
	}

	failedDocs := &recordingDocuments{}
	failedEmbed := &selectiveEvidenceEmbedder{err: errors.New("passage embedding unavailable")}
	semantic := base.Clone().WithMemoryLevelRanking(true).
		WithMemoryEvidenceSelector("semantic").
		WithReranker(failedDocs, 10).WithRerankWeight(0.5)
	semantic.embed = failedEmbed
	semantic.vectors = &orderedVectors{VectorStore: base.vectors, hits: ordered}
	if _, err := semantic.Search(ctx, team, SearchQuery{Query: "unfinished reasoning", Limit: 2, SkipTelemetry: true}); err != nil {
		t.Fatalf("semantic search should fail open: %v", err)
	}
	if len(failedEmbed.batches) == 0 {
		t.Fatal("semantic selector never attempted passage embedding")
	}
	if len(lexicalDocs.docs) != 1 || len(lexicalDocs.docs[0]) != 2 ||
		len(failedDocs.docs) != 1 || len(failedDocs.docs[0]) != 2 ||
		strings.Join(lexicalDocs.docs[0], "\x00") != strings.Join(failedDocs.docs[0], "\x00") {
		t.Fatalf("failed semantic selector did not preserve the lexical shortlist:\nlexical=%#v\nfailed=%#v", lexicalDocs.docs, failedDocs.docs)
	}
}

// concurrencyWitnessEmbedder answers only once enough callers have arrived
// together, so the test cannot pass on a serial implementation: with batches
// end to end the first caller waits alone and the barrier times out.
//
// It records the high-water mark of simultaneous calls rather than timing
// anything, so the gate is about overlap and not about how fast the machine is.
type concurrencyWitnessEmbedder struct {
	want    int
	arrived chan struct{}
	release sync.Once

	mu       sync.Mutex
	inFlight int
	peak     int
}

func newConcurrencyWitness(want int) *concurrencyWitnessEmbedder {
	return &concurrencyWitnessEmbedder{want: want, arrived: make(chan struct{})}
}

func (e *concurrencyWitnessEmbedder) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	e.mu.Lock()
	e.inFlight++
	e.peak = max(e.peak, e.inFlight)
	reached := e.inFlight >= e.want
	e.mu.Unlock()

	if reached {
		e.release.Do(func() { close(e.arrived) })
	}
	select {
	case <-e.arrived:
	case <-time.After(2 * time.Second): // serial implementation: nobody else is coming
	case <-ctx.Done():
	}

	e.mu.Lock()
	e.inFlight--
	e.mu.Unlock()

	out := make([][]float32, len(inputs))
	for i := range out {
		out[i] = make([]float32, fakeDim)
		out[i][0] = 1
	}
	return out, nil
}

func (e *concurrencyWitnessEmbedder) EmbedOne(_ context.Context, _ string) ([]float32, error) {
	v := make([]float32, fakeDim)
	v[0] = 1
	return v, nil
}

func (e *concurrencyWitnessEmbedder) peakInFlight() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.peak
}

// TestSemanticEvidenceBatchesOverlap gates the fix for the selector's latency.
//
// The cost was never the cross-encoder: a 5,000-rune memory yields 17 windows,
// so a full shortlist is thousands of passages, and embedding them one batch
// after another meant the reranker had not started yet. Adaptive batching
// reduced the NUMBER of round trips and left them serial, which is why it
// bought so little.
//
// Nothing about the RESULT can see this — the selected evidence is identical
// either way, deliberately, so the selector A/B stays valid. So the gate
// observes overlap directly, and removing SetLimit/errgroup makes it red.
func TestSemanticEvidenceBatchesOverlap(t *testing.T) {
	svc := newTestService(t)
	witness := newConcurrencyWitness(2)
	svc.embed = witness

	// Two memories, each long enough to produce more than one batch of windows.
	const windowsPerBatch = semanticEvidenceBatchSize
	// Sized just past one batch of windows: enough to observe overlap, not
	// enough to make the suite carry a megabyte of fixture strings.
	long := strings.Repeat("premise conclusion evidence latency corpus drawer palace signal ", 700)

	survivors := []SearchHit{
		{Drawer: Drawer{ID: "m1", Content: long}, MemoryID: "m1", MemoryContent: long},
		{Drawer: Drawer{ID: "m2", Content: long}, MemoryID: "m2", MemoryContent: long},
	}
	ranked := []HybridScore{{Index: 0}, {Index: 1}}
	lexical := []string{"one", "two"}

	// Batching is over the FLATTENED window list across documents, not per
	// memory — and one document now yields at most one batch by construction (see
	// the stride widening in semanticEvidenceWindows, which bounds a single
	// document to semanticEvidenceBatchSize passages). So what this test needs is
	// more than one batch in TOTAL, which two full documents provide.
	if total := len(semanticEvidenceWindows(long)) * len(survivors); total <= windowsPerBatch {
		t.Fatalf("fixture yields %d windows across %d memories, which is one batch; the test "+
			"cannot observe concurrency", total, len(survivors))
	}

	query := make([]float32, fakeDim)
	query[0] = 1
	if _, err := svc.semanticRerankDocuments(context.Background(), "latency", query, survivors, ranked, lexical); err != nil {
		t.Fatalf("semanticRerankDocuments: %v", err)
	}

	if peak := witness.peakInFlight(); peak < 2 {
		t.Fatalf("peak concurrent embed calls = %d; evidence batches are still serialised", peak)
	}
}

// TestSemanticWindowsCoverUnbrokenTokenRuns pins evidence coverage across the
// content this corpus actually holds: sha256 digests, image refs and URLs.
//
// Windows start at a word boundary, and a long unbroken token has none — the
// boundary walk ran to the end of the window and emitted nothing, so the run and
// everything the step covered were never eligible as evidence. Measured before
// the fix: 87% of a 6,002-rune memory reachable, plus a 98-rune stub sharing a
// start offset with a full window, embedded twice and able to occupy one of only
// four evidence slots.
func TestSemanticWindowsCoverUnbrokenTokenRuns(t *testing.T) {
	prose := strings.Repeat("ranking memory chunk rerank fusion candidate vector lexical ", 10)
	digest := strings.Repeat("a1b2c3d4e5f6", 200) // a 2,400-rune token with no boundary
	content := prose + " " + digest + " " + strings.Repeat("budget passage evidence latency corpus drawer ", 50)
	runes := []rune(content)

	windows := semanticEvidenceWindows(content)
	if len(windows) < 2 {
		t.Fatalf("fixture produced %d windows; it is not exercising the walk", len(windows))
	}

	// Every rune must be reachable through some window. Coverage is what the
	// selector can choose from; text outside it cannot be evidence at all.
	covered := make([]bool, len(runes))
	starts := make(map[int]int)
	for _, w := range windows {
		starts[w.Start]++
		if n := len([]rune(w.Text)); n < minRegionRunes {
			t.Fatalf("window at %d is %d runes, below the %d-rune floor; a stub can win an evidence slot",
				w.Start, n, minRegionRunes)
		}
		for i := w.Start; i < w.Start+len([]rune(w.Text)) && i < len(runes); i++ {
			covered[i] = true
		}
	}
	for offset, count := range starts {
		if count > 1 {
			t.Fatalf("%d windows share start offset %d; the same passage is embedded more than once", count, offset)
		}
	}
	for i, ok := range covered {
		if !ok {
			t.Fatalf("rune %d of %d is in no window, so it can never be selected as evidence (first gap of %d)",
				i, len(runes), countFalse(covered))
		}
	}
}

func countFalse(b []bool) int {
	n := 0
	for _, v := range b {
		if !v {
			n++
		}
	}
	return n
}
