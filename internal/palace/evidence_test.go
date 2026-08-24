package palace

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
)

// selectiveEvidenceEmbedder makes only the two paraphrased passages align with
// the query. Retrieval order is controlled separately by orderedVectors, so the
// test observes evidence selection rather than an incidental vector ranking.
type selectiveEvidenceEmbedder struct {
	batches [][]string
	queries []string
	err     error
}

func (e *selectiveEvidenceEmbedder) Embed(_ context.Context, inputs []string) ([][]float32, error) {
	e.batches = append(e.batches, append([]string(nil), inputs...))
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
	e.queries = append(e.queries, input)
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
