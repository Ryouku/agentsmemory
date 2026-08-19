package palace

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/db"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec"

	glebarez "github.com/glebarez/sqlite"
	"github.com/pressly/goose/v3"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// fakeEmbedder turns text into a deterministic bag-of-bytes histogram vector:
// identical text yields an identical vector (cosine 1), and the more two strings
// share, the closer they sit. That is enough to assert recall ordering without a
// live Ollama, and it keeps the test hermetic.
type fakeEmbedder struct{}

const fakeDim = 32

func (fakeEmbedder) Embed(_ context.Context, inputs []string) ([][]float32, error) {
	out := make([][]float32, len(inputs))
	for i, s := range inputs {
		v := make([]float32, fakeDim)
		for _, b := range []byte(s) {
			v[int(b)%fakeDim]++
		}
		out[i] = v
	}
	return out, nil
}

func (f fakeEmbedder) EmbedOne(ctx context.Context, input string) ([]float32, error) {
	v, err := f.Embed(ctx, []string{input})
	if err != nil {
		return nil, err
	}
	return v[0], nil
}

// newTestService builds a Service over a throwaway migrated SQLite DB (so the
// real 00006 schema is exercised) using the SQLite store as both source of truth
// and search index, plus the fake embedder.
func newTestService(t *testing.T, opts ...Option) *Service {
	t.Helper()
	path := filepath.Join(t.TempDir(), "palace_test.db")
	gdb, err := gorm.Open(glebarez.Open(path), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("sql handle: %v", err)
	}
	goose.SetBaseFS(db.Migrations)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("dialect: %v", err)
	}
	if err := goose.Up(sqlDB, "migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewService(NewRepo(gdb), fakeEmbedder{}, sqlitevec.New(gdb), fakeDim, opts...)
}

// fakeReranker is a cross-encoder stand-in: it ranks by how many query words a
// document literally contains, which is enough to reorder a page deterministically
// without a model. err makes every call fail, for the degradation test.
type fakeReranker struct {
	err    error
	called int
}

func (f *fakeReranker) Rerank(_ context.Context, query string, docs []string) ([]RerankScore, error) {
	f.called++
	if f.err != nil {
		return nil, f.err
	}
	scores := make([]RerankScore, 0, len(docs))
	for i, d := range docs {
		var hits float64
		for _, term := range strings.Fields(strings.ToLower(query)) {
			if strings.Contains(strings.ToLower(d), term) {
				hits++
			}
		}
		scores = append(scores, RerankScore{Index: i, Score: hits})
	}
	sort.SliceStable(scores, func(a, b int) bool { return scores[a].Score > scores[b].Score })
	return scores, nil
}

// TestSearchRerankerPromotesFromOutsideThePage is the point of a cross-encoder:
// it must be able to pull a drawer the hybrid ranking put below the page INTO it.
// Reranking after paging would make that impossible, so this pins the ordering of
// the two steps, not just the presence of the reranker.
func TestSearchRerankerPromotesFromOutsideThePage(t *testing.T) {
	ctx := context.Background()
	rr := &fakeReranker{}
	svc := newTestService(t, WithReranker(rr, 10))
	const team = "team-rerank"

	// The fake embedder maps bytes to dimensions, so these are near-identical
	// vectors: retrieval surfaces all of them and the fused order is essentially
	// arbitrary — which is exactly when the cross-encoder should decide.
	for _, content := range []string{
		"aaa bbb ccc filler one",
		"aaa bbb ccc filler two",
		"aaa bbb ccc filler three",
		"the installer pins CLAUDE_CONFIG_DIR and the registration lands in an unread file",
	} {
		if _, err := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", Content: content}); err != nil {
			t.Fatalf("add: %v", err)
		}
	}

	hits, err := svc.Search(ctx, team, SearchQuery{Query: "installer pins claude_config_dir", Limit: 1})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if rr.called != 1 {
		t.Fatalf("reranker called %d times, want 1", rr.called)
	}
	if len(hits) != 1 || !strings.Contains(hits[0].Drawer.Content, "CLAUDE_CONFIG_DIR") {
		t.Fatalf("cross-encoder did not decide the page: %+v", hits)
	}
	if hits[0].RerankScore == 0 {
		t.Error("RerankScore not reported on the hit")
	}
}

// TestSearchSurvivesRerankerFailure: the cross-encoder is a refinement, so a
// server that is down must cost ranking quality and nothing else. Recall is the
// product; it cannot depend on an optional service.
func TestSearchSurvivesRerankerFailure(t *testing.T) {
	ctx := context.Background()
	rr := &fakeReranker{err: errors.New("connection refused")}
	svc := newTestService(t, WithReranker(rr, 10))
	const team = "team-rerank-down"

	for _, content := range []string{"alpha memory", "beta memory", "gamma memory"} {
		if _, err := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", Content: content}); err != nil {
			t.Fatalf("add: %v", err)
		}
	}

	hits, err := svc.Search(ctx, team, SearchQuery{Query: "beta memory", Limit: 3})
	if err != nil {
		t.Fatalf("search must not fail when the reranker is down: %v", err)
	}
	if len(hits) != 3 {
		t.Fatalf("want the full hybrid page, got %d hit(s)", len(hits))
	}
	// The lexical half of the hybrid ranking still works, so the literal match
	// leads — proof the results are the fused ordering rather than an empty or
	// arbitrary one.
	if !strings.Contains(hits[0].Drawer.Content, "beta") {
		t.Errorf("hybrid order lost: %q leads", hits[0].Drawer.Content)
	}
	for _, h := range hits {
		if h.RerankScore != 0 {
			t.Errorf("failed rerank must leave RerankScore unset, got %v", h.RerankScore)
		}
	}
}

func TestServiceAddAndSearch(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	if _, err := svc.Add(ctx, team, AddInput{Wing: "proj", Room: "backend", Content: "the cache uses an LRU eviction policy"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := svc.Add(ctx, team, AddInput{Wing: "proj", Room: "frontend", Content: "the button turns blue on hover"}); err != nil {
		t.Fatalf("add: %v", err)
	}

	hits, err := svc.Search(ctx, team, SearchQuery{Query: "the cache uses an LRU eviction policy"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("expected at least one hit")
	}
	if hits[0].Drawer.Content != "the cache uses an LRU eviction policy" {
		t.Fatalf("top hit is not the exact match: %q (score %.3f)", hits[0].Drawer.Content, hits[0].Score)
	}
	if hits[0].Distance < 0 || hits[0].Distance > 2 {
		t.Fatalf("distance out of [0,2]: %f", hits[0].Distance)
	}
}

func TestServiceSearchWingFilter(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	mustAdd(t, svc, team, AddInput{Wing: "alpha", Room: "r", Content: "shared phrase here alpha"})
	mustAdd(t, svc, team, AddInput{Wing: "beta", Room: "r", Content: "shared phrase here beta"})

	hits, err := svc.Search(ctx, team, SearchQuery{Query: "shared phrase here", Wing: "beta"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	for _, h := range hits {
		if h.Drawer.Wing != "beta" {
			t.Fatalf("wing filter leaked: got wing %q", h.Drawer.Wing)
		}
	}
}

func TestServiceGetUpdateDelete(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	created := mustAdd(t, svc, team, AddInput{Wing: "w", Room: "r", Content: "original text"})
	id := created[0].ID

	got, err := svc.Get(ctx, team, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Content != "original text" {
		t.Fatalf("get returned %q", got.Content)
	}

	newContent := "rewritten text"
	if _, err := svc.Update(ctx, team, id, DrawerPatch{Content: &newContent}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ = svc.Get(ctx, team, id)
	if got.Content != newContent {
		t.Fatalf("update did not persist: %q", got.Content)
	}

	if err := svc.Delete(ctx, team, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.Get(ctx, team, id); err != ErrNotFound {
		t.Fatalf("want ErrNotFound after delete, got %v", err)
	}
}

func TestServiceGetUnknownIsNotFound(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.Get(context.Background(), "team-1", "nope"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestServiceAggregations(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	mustAdd(t, svc, team, AddInput{Wing: "proj", Room: "backend", Content: "alpha"})
	mustAdd(t, svc, team, AddInput{Wing: "proj", Room: "frontend", Content: "beta"})
	mustAdd(t, svc, team, AddInput{Wing: "notes", Room: "ideas", Content: "gamma"})

	wings, err := svc.Wings(ctx, team)
	if err != nil {
		t.Fatalf("wings: %v", err)
	}
	got := map[string]WingStat{}
	for _, w := range wings {
		got[w.Wing] = w
	}
	if got["proj"].Drawers != 2 || got["proj"].Rooms != 2 {
		t.Fatalf("proj wing stats wrong: %+v", got["proj"])
	}
	if got["notes"].Drawers != 1 || got["notes"].Rooms != 1 {
		t.Fatalf("notes wing stats wrong: %+v", got["notes"])
	}

	tax, err := svc.GetTaxonomy(ctx, team)
	if err != nil {
		t.Fatalf("taxonomy: %v", err)
	}
	if len(tax.Wings) != 2 {
		t.Fatalf("want 2 wings in taxonomy, got %d", len(tax.Wings))
	}
}

func TestServiceCheckDuplicate(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	mustAdd(t, svc, team, AddInput{Wing: "w", Room: "r", Content: "a uniquely worded memory about otters"})

	dup, err := svc.CheckDuplicate(ctx, team, "a uniquely worded memory about otters", DefaultDupThreshold)
	if err != nil {
		t.Fatalf("check duplicate: %v", err)
	}
	if !dup.IsDuplicate || dup.Drawer == nil {
		t.Fatalf("identical content should be a duplicate: %+v", dup)
	}

	none, err := svc.CheckDuplicate(ctx, team, "completely different subject matter zzz", DefaultDupThreshold)
	if err != nil {
		t.Fatalf("check duplicate: %v", err)
	}
	if none.IsDuplicate {
		t.Fatalf("unrelated content flagged as duplicate (sim %.3f)", none.Similarity)
	}
}

func TestServiceAddNoSourceKeepsDistinctMemories(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	// Two different memories, same wing/room, no source_file: both must survive
	// (the content-hashed id prevents the second from overwriting the first).
	mustAdd(t, svc, team, AddInput{Wing: "w", Room: "r", Content: "first memory about cats"})
	mustAdd(t, svc, team, AddInput{Wing: "w", Room: "r", Content: "second memory about dogs"})

	list, err := svc.List(ctx, team, "w", "r", 50, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 distinct drawers, got %d (collision overwrote one)", len(list))
	}
}

func TestServiceReAddNamedSourcePurgesStaleChunks(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"

	long := strings.Repeat("alpha ", 400)    // ~2400 chars -> several chunks
	short := "now just a single short chunk" // 1 chunk

	first := mustAdd(t, svc, team, AddInput{Wing: "w", Room: "r", SourceFile: "notes.md", Content: long})
	if len(first) < 2 {
		t.Fatalf("expected the long content to chunk into >1 drawer, got %d", len(first))
	}
	mustAdd(t, svc, team, AddInput{Wing: "w", Room: "r", SourceFile: "notes.md", Content: short})

	list, err := svc.List(ctx, team, "w", "r", 50, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("re-adding a shorter source should purge stale chunks; want 1 drawer, got %d", len(list))
	}
}

func TestServiceUpdateRejectsEmptyField(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"
	created := mustAdd(t, svc, team, AddInput{Wing: "w", Room: "r", Content: "keep me addressable"})

	empty := ""
	if _, err := svc.Update(ctx, team, created[0].ID, DrawerPatch{Wing: &empty}); err == nil {
		t.Fatal("expected an error updating wing to empty")
	}
}

func TestServiceCheckDuplicateClampsThreshold(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-1"
	mustAdd(t, svc, team, AddInput{Wing: "w", Room: "r", Content: "an exact phrase to match"})

	// threshold > 1 is nonsense; clamped to 1, an exact match (sim 1.0) still counts.
	dup, err := svc.CheckDuplicate(ctx, team, "an exact phrase to match", 2.0)
	if err != nil {
		t.Fatalf("check duplicate: %v", err)
	}
	if !dup.IsDuplicate {
		t.Fatalf("threshold>1 should clamp so an exact duplicate still matches (sim %.3f)", dup.Similarity)
	}
}

func TestServiceAddValidates(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.Add(context.Background(), "team-1", AddInput{Wing: "", Room: "r", Content: "x"}); err == nil {
		t.Fatal("expected validation error for empty wing")
	}
}

// brokenEmbedder stands in for an Ollama that is not running — the single most
// common self-hosted failure, and the one that used to lose memories.
type brokenEmbedder struct{ fakeEmbedder }

func (brokenEmbedder) Embed(context.Context, []string) ([][]float32, error) {
	return nil, errors.New("dial tcp 127.0.0.1:11434: connect: connection refused")
}

func (b brokenEmbedder) EmbedOne(ctx context.Context, input string) ([]float32, error) {
	return nil, errors.New("dial tcp 127.0.0.1:11434: connect: connection refused")
}

// TestFilingSurvivesAnEmbedderOutage is the whole point of the deferred path: a
// memory written while the embedder is down must still EXIST. Losing the text
// because the index could not be built is the worst trade this system can make —
// the text is the memory, the vector is only how it is found.
func TestFilingSurvivesAnEmbedderOutage(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	svc.embed = brokenEmbedder{}
	const team = "team-outage"

	res, err := svc.Add(ctx, team, AddInput{Wing: "w", Room: "r", Content: "the embedder was down when this was written"})
	if err != nil {
		t.Fatalf("add must not fail when the embedder is down: %v", err)
	}
	if !res.PendingEmbedding {
		t.Error("PendingEmbedding not reported; the caller cannot tell the memory is unsearchable")
	}
	if len(res.Drawers) != 1 {
		t.Fatalf("want 1 drawer, got %d", len(res.Drawers))
	}

	diary, err := svc.WriteDiary(ctx, team, DiaryWriteInput{Agent: "tester", Entry: "journal written during the outage"})
	if err != nil {
		t.Fatalf("diary_write must not fail when the embedder is down: %v", err)
	}
	if !diary.PendingEmbedding {
		t.Error("diary PendingEmbedding not reported")
	}

	// The rows are durable and queued — which is exactly the state the background
	// worker drains.
	pending, err := svc.PendingCount(ctx, team)
	if err != nil {
		t.Fatalf("pending count: %v", err)
	}
	if pending != 2 {
		t.Fatalf("want 2 rows awaiting embedding, got %d", pending)
	}
	stored, err := svc.Get(ctx, team, res.Drawers[0].ID)
	if err != nil {
		t.Fatalf("the drawer must be readable back immediately: %v", err)
	}
	if stored.Content != "the embedder was down when this was written" {
		t.Errorf("content not stored verbatim: %q", stored.Content)
	}

	// Embedder returns: the queue drains and the memories become searchable, with
	// no re-filing by anyone.
	svc.embed = fakeEmbedder{}
	n, err := svc.EmbedPendingForTeam(ctx, team, 10)
	if err != nil {
		t.Fatalf("drain queue: %v", err)
	}
	if n != 2 {
		t.Fatalf("worker embedded %d rows, want 2", n)
	}
	if pending, err = svc.PendingCount(ctx, team); err != nil || pending != 0 {
		t.Fatalf("queue not drained: %d (err %v)", pending, err)
	}
	hits, err := svc.Search(ctx, team, SearchQuery{Query: "journal written during the outage", Limit: 5})
	if err != nil {
		t.Fatalf("search after recovery: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("memories written during the outage never became searchable")
	}
}

func mustAdd(t *testing.T, svc *Service, team string, in AddInput) []Drawer {
	t.Helper()
	res, err := svc.Add(context.Background(), team, in)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if res.PendingEmbedding {
		t.Fatalf("add deferred embedding unexpectedly (is the fake embedder failing?)")
	}
	return res.Drawers
}

// TestCrossEncodeBlendsRatherThanOverwrites pins the fix for a measured
// regression. Handing the cross-encoder the whole decision throws away the
// lexical evidence in the fused score — on this palace's eval that cost MRR
// 1.000 → 0.684 in the regime where a developer searches with the identifier they
// remember. A blend keeps both opinions.
func TestCrossEncodeBlendsRatherThanOverwrites(t *testing.T) {
	svc := newTestService(t)

	// The fused ranking is confident about hit A (an exact lexical match); the
	// cross-encoder mildly prefers B. A blend must keep A on top; a handover must
	// not.
	hits := []SearchHit{
		{Drawer: Drawer{ID: "A", Content: "exact identifier match"}, Score: 1.0},
		{Drawer: Drawer{ID: "B", Content: "topically similar"}, Score: 0.2},
	}
	flip := &staticReranker{scores: []RerankScore{{Index: 1, Score: 2}, {Index: 0, Score: 1}}}

	svc.reranker = flip
	blended := svc.crossEncodeWith(context.Background(), "q", hits, DefaultRerankWeight)
	if blended[0].Drawer.ID != "A" {
		t.Errorf("blend at w=%.2f let a mild cross-encoder preference overturn a confident fused score: %s leads",
			DefaultRerankWeight, blended[0].Drawer.ID)
	}

	// w=1 is the old behaviour, kept reachable so the eval can measure it.
	overwritten := svc.crossEncodeWith(context.Background(), "q", hits, 1)
	if overwritten[0].Drawer.ID != "B" {
		t.Errorf("w=1 must hand the decision to the cross-encoder, got %s", overwritten[0].Drawer.ID)
	}

	// w=0 means the cross-encoder is not consulted at all.
	if untouched := svc.crossEncodeWith(context.Background(), "q", hits, 0); untouched[0].Drawer.ID != "A" {
		t.Errorf("w=0 must leave the hybrid order alone, got %s", untouched[0].Drawer.ID)
	}
}

// TestCrossEncodeKeepsUnscoredCandidates: a server that scores only part of the
// page must cost precision, not evict results.
func TestCrossEncodeKeepsUnscoredCandidates(t *testing.T) {
	svc := newTestService(t)
	hits := []SearchHit{
		{Drawer: Drawer{ID: "A"}, Score: 0.9},
		{Drawer: Drawer{ID: "B"}, Score: 0.5},
		{Drawer: Drawer{ID: "C"}, Score: 0.1},
	}
	svc.reranker = &staticReranker{scores: []RerankScore{{Index: 2, Score: 5}}} // only C scored

	got := svc.crossEncodeWith(context.Background(), "q", hits, DefaultRerankWeight)
	if len(got) != 3 {
		t.Fatalf("page shrank to %d hits", len(got))
	}
	seen := map[string]bool{}
	for _, h := range got {
		seen[h.Drawer.ID] = true
	}
	for _, id := range []string{"A", "B", "C"} {
		if !seen[id] {
			t.Errorf("hit %s was dropped", id)
		}
	}
}

// staticReranker returns a fixed ordering, so blending is testable without a
// model.
type staticReranker struct{ scores []RerankScore }

func (s *staticReranker) Rerank(context.Context, string, []string) ([]RerankScore, error) {
	return s.scores, nil
}
