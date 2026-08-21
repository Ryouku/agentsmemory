package palace

import (
	"context"
	"strings"
	"testing"
)

// longMemory builds content long enough that Add chunks it, with a marker term
// repeated so every chunk matches the same query — which is exactly the shape
// that makes chunks crowd a page.
func longMemory(marker string, chunks int) string {
	var b strings.Builder
	for i := 0; i < chunks; i++ {
		b.WriteString(marker + " ")
		// ChunkSize is 1600 characters; fill past it so the splitter produces a
		// new chunk, and vary the filler so the chunks are not identical rows.
		for j := 0; j < 200; j++ {
			b.WriteString("filler")
			b.WriteByte(byte('a' + (i+j)%26))
			b.WriteByte(' ')
		}
	}
	return b.String()
}

// TestSearchReturnsOneHitPerMemory: a page is a page of memories.
//
// Chunks of one memory are similar to the same query, so they cluster and crowd
// each other rather than spreading. Measured on a live palace before this
// existed: at limit 10, one query spent 2 slots on a single memory and another
// spent 4 slots on two, with the duplicate pairs adjacent.
//
// The eval could not see it. eval.go folds every hit onto its ParentID before
// scoring — including for the production arm — so it scores MEMORIES over a page
// production returned as CHUNKS. Its headline would be unchanged if production
// returned ten chunks of one memory.
func TestSearchReturnsOneHitPerMemory(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-collapse"

	if _, err := svc.Add(ctx, team, AddInput{
		Wing: "w", Room: "r", Content: longMemory("zephyrine", 4),
	}); err != nil {
		t.Fatalf("add chunked: %v", err)
	}
	for _, c := range []string{
		"zephyrine appears here too, in a memory short enough to stay whole",
		"zephyrine again, another distinct short memory",
	} {
		if _, err := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", Content: c}); err != nil {
			t.Fatalf("add short: %v", err)
		}
	}

	hits, err := svc.Search(ctx, team, SearchQuery{Query: "zephyrine", Limit: 5})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("no hits at all — the fixture cannot exhibit the defect, so this test proves nothing")
	}

	seen := map[string]int{}
	for _, h := range hits {
		seen[memoryOf(h.Drawer)]++
	}
	for id, n := range seen {
		if n > 1 {
			t.Errorf("memory %s occupies %d of %d slots on one page — chunks of one memory are "+
				"crowding out other memories, and `limit` does not mean what a caller thinks",
				id[:8], n, len(hits))
		}
	}
	if len(seen) != len(hits) {
		t.Errorf("%d slots hold only %d distinct memories", len(hits), len(seen))
	}
}

// TestSearchReportsHowManyChunksMatched: collapsing must not destroy the signal
// it collapses. A memory that matched in four places is stronger evidence than
// one that matched in one, and a silent collapse throws that away.
func TestSearchReportsHowManyChunksMatched(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-chunkcount"

	if _, err := svc.Add(ctx, team, AddInput{
		Wing: "w", Room: "r", Content: longMemory("quintessa", 4),
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	hits, err := svc.Search(ctx, team, SearchQuery{Query: "quintessa", Limit: 5})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("want one hit for one memory, got %d", len(hits))
	}
	if hits[0].ChunksMatched < 2 {
		t.Errorf("ChunksMatched = %d for a memory whose chunks all matched; the collapse threw "+
			"away how much of the memory was relevant", hits[0].ChunksMatched)
	}
}

// TestSearchKeepsTheBestChunkNotTheFirst: the surviving chunk must be the one
// that matched, because its snippet is the passage the caller asked for. Keeping
// chunk 0 would answer a different question than the one asked.
func TestSearchKeepsTheBestChunkNotTheFirst(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-bestchunk"

	// A memory whose DISTINCTIVE term lives only in a later chunk.
	var b strings.Builder
	for j := 0; j < 400; j++ {
		b.WriteString("preamble ")
	}
	b.WriteString(" ")
	for j := 0; j < 400; j++ {
		b.WriteString("nimbostratus ")
	}
	if _, err := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", Content: b.String()}); err != nil {
		t.Fatalf("add: %v", err)
	}

	hits, err := svc.Search(ctx, team, SearchQuery{Query: "nimbostratus", Limit: 3})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	if !strings.Contains(hits[0].Drawer.Content, "nimbostratus") {
		t.Errorf("the surviving chunk does not contain the term that was searched for — "+
			"the collapse kept the first chunk rather than the matching one: %.80s",
			hits[0].Drawer.Content)
	}
}

// TestGetMemoryReturnsEveryChunkInOrder: collapsing is only safe because the rest
// of the memory can be fetched. Before this, Repo.MemoryChunks was called by
// Update and Delete alone — no read path could reach a whole chunked memory.
func TestGetMemoryReturnsEveryChunkInOrder(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-whole"

	res, err := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", Content: longMemory("thalassa", 3)})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if len(res.Drawers) < 2 {
		t.Fatalf("the fixture produced %d chunk(s); it cannot show a whole-memory read", len(res.Drawers))
	}

	chunks, err := svc.GetMemory(ctx, team, res.Drawers[0].ID)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("want every chunk of a chunked memory, got %d — the fixture did not chunk, so "+
			"this test cannot show the property", len(chunks))
	}
	for i, c := range chunks {
		if c.ChunkIndex != i {
			t.Errorf("chunk %d carries ChunkIndex %d — the chunks are not in order, so a caller "+
				"reassembling the memory gets it scrambled", i, c.ChunkIndex)
		}
	}
	// Asking with a LATER chunk's id must return the same whole memory: a caller
	// holding a search hit holds whichever chunk matched, not the first.
	fromLater, err := svc.GetMemory(ctx, team, chunks[len(chunks)-1].ID)
	if err != nil {
		t.Fatalf("GetMemory from a later chunk: %v", err)
	}
	if len(fromLater) != len(chunks) {
		t.Errorf("asking with the last chunk's id returned %d chunks, want %d — a caller can only "+
			"reassemble the memory if it holds chunk 0", len(fromLater), len(chunks))
	}
}
