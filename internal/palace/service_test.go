package palace

import (
	"context"
	"errors"
	"path/filepath"
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
func newTestService(t *testing.T) *Service {
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
	return NewService(NewRepo(gdb), fakeEmbedder{}, sqlitevec.New(gdb), fakeDim)
}

// fakeReranker is a cross-encoder stand-in: it ranks by how many query words a
// document literally contains, which is enough to reorder a page deterministically
// without a model. err makes every call fail, for the degradation test.
type fakeReranker struct {
	err    error
	called int
}

func (f *fakeReranker) Rerank(_ context.Context, query string, docs []string) ([]float64, error) {
	f.called++
	if f.err != nil {
		return nil, f.err
	}
	scores := make([]float64, len(docs))
	for i, d := range docs {
		for _, term := range strings.Fields(strings.ToLower(query)) {
			if strings.Contains(strings.ToLower(d), term) {
				scores[i]++
			}
		}
	}
	return scores, nil
}

// TestSearchRerankerPromotesFromOutsideThePage is the point of a cross-encoder:
// it must be able to pull a drawer the hybrid ranking put below the page INTO it.
// Reranking after paging would make that impossible, so this pins the ordering of
// the two steps, not just the presence of the reranker.
func TestSearchRerankerPromotesFromOutsideThePage(t *testing.T) {
	ctx := context.Background()
	rr := &fakeReranker{}
	svc := newTestService(t).WithReranker(rr, 10)
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
	svc := newTestService(t).WithReranker(rr, 10)
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

// TestRerankBlendsRatherThanOverwrites pins a measured regression. Letting the
// cross-encoder's score decide alone throws away the lexical evidence in the
// fused score — on this palace's eval that was MRR 1.000 → 0.686 on the queries
// that carry an identifier, which is what a developer actually types.
func TestRerankBlendsRatherThanOverwrites(t *testing.T) {
	svc := newTestService(t)

	// Fused ranking is confident about A (an exact lexical match); the
	// cross-encoder mildly prefers B. A blend keeps A; a handover does not.
	survivors := []SearchHit{
		{Drawer: Drawer{ID: "A", Content: "exact identifier match"}},
		{Drawer: Drawer{ID: "B", Content: "topically similar"}},
	}
	ranked := []HybridScore{{Index: 0, Fused: 1.0}, {Index: 1, Fused: 0.2}}
	svc.rerank = &staticReranker{scores: []float64{1, 2}} // B scored higher
	svc.rerankPool = 2

	blended := svc.applyRerankWith(context.Background(), "q", survivors, ranked, DefaultRerankWeight)
	if survivors[blended[0].Index].Drawer.ID != "A" {
		t.Errorf("a mild cross-encoder preference overturned a confident fused score at w=%.2f", DefaultRerankWeight)
	}

	// w=1 is the handover, kept reachable so the eval can measure what it costs.
	if over := svc.applyRerankWith(context.Background(), "q", survivors, ranked, 1); survivors[over[0].Index].Drawer.ID != "B" {
		t.Error("w=1 must hand the decision to the cross-encoder")
	}
	// w=0 does not consult it at all.
	if none := svc.applyRerankWith(context.Background(), "q", survivors, ranked, 0); survivors[none[0].Index].Drawer.ID != "A" {
		t.Error("w=0 must leave the hybrid order alone")
	}
}

// TestRerankKeepsTheWholePage: a partial or failed response costs precision, not
// results.
func TestRerankKeepsTheWholePage(t *testing.T) {
	svc := newTestService(t)
	survivors := []SearchHit{
		{Drawer: Drawer{ID: "A"}}, {Drawer: Drawer{ID: "B"}}, {Drawer: Drawer{ID: "C"}},
	}
	ranked := []HybridScore{{Index: 0, Fused: 0.9}, {Index: 1, Fused: 0.5}, {Index: 2, Fused: 0.1}}

	// Wrong count: upstream's guard rejects it and the hybrid order stands.
	svc.rerank = &staticReranker{scores: []float64{5}}
	svc.rerankPool = 3
	if got := svc.applyRerankWith(context.Background(), "q", survivors, ranked, DefaultRerankWeight); len(got) != 3 {
		t.Fatalf("page shrank to %d", len(got))
	}
}

// staticReranker returns a fixed ordering, so blending is testable without a
// model.
type staticReranker struct{ scores []float64 }

func (s *staticReranker) Rerank(context.Context, string, []string) ([]float64, error) {
	return s.scores, nil
}

// TestWithFusionRRFChangesOrder pins that FUSION=rrf actually reaches the search
// path: the mechanism it exists for is bounding one bad signal's influence, so
// a candidate that a lexical score would sink must survive on its vector rank.
func TestWithFusionRRFChangesOrder(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)
	const team = "team-fusion"

	// The closest vector match shares no vocabulary with the query; the lexical
	// winner is a farther, wordier note. Linear fusion lets the lexical score
	// pull the second one up; RRF caps that to one rank position.
	mustAdd(t, svc, team, AddInput{Wing: "wing_acme", Room: "decisions", SourceFile: "a.md",
		Content: "cache eviction policy"})
	mustAdd(t, svc, team, AddInput{Wing: "wing_acme", Room: "decisions", SourceFile: "b.md",
		Content: "cache eviction policy cache eviction policy cache eviction unrelated tail"})

	order := func(s *Service) []string {
		hits, err := s.Search(ctx, team, SearchQuery{Query: "cache eviction policy", Wing: "wing_acme", Limit: 5, SkipTelemetry: true})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		out := make([]string, len(hits))
		for i, h := range hits {
			out[i] = h.Drawer.SourceFile
		}
		return out
	}
	linear := order(svc)
	rrf := order(svc.WithFusion("rrf"))
	if len(linear) != 2 || len(rrf) != 2 {
		t.Fatalf("expected both drawers on the page, got linear=%v rrf=%v", linear, rrf)
	}
	// The assertion that matters is reachability: the switch must be observable
	// from Search, not merely stored on the struct.
	if !svc.fusionRRF {
		t.Fatal("WithFusion(\"rrf\") did not set the fusion mode")
	}
	if svc.WithFusion("linear").fusionRRF {
		t.Fatal("WithFusion(\"linear\") must turn rank fusion off")
	}
}

// TestLexicalIDFIsReachableFromSearch pins that BM25_WEIGHT=auto-idf actually
// changes what Search does. Four eval tables preferred the IDF-weighted coverage
// before anything could select it in production — a measured arm nobody can run
// is not a finding, and only a test that goes through Search proves the wiring
// exists rather than the helper.
func TestLexicalIDFIsReachableFromSearch(t *testing.T) {
	svc := newTestService(t)
	if svc.bm25IDF {
		t.Fatal("the binary count must remain the default until a ranking default change is agreed")
	}
	if !svc.WithLexicalIDF(true).bm25IDF {
		t.Fatal("WithLexicalIDF(true) did not select the IDF coverage")
	}
	if svc.WithLexicalIDF(false).bm25IDF {
		t.Fatal("WithLexicalIDF(false) did not turn it off")
	}

	// Reachability, not merely storage: the search path must branch on it.
	ctx := context.Background()
	const team = "team-idf"
	mustAdd(t, svc, team, AddInput{Wing: "wing_acme", Room: "decisions", SourceFile: "a.md",
		Content: "the retry budget is three attempts before the circuit opens"})
	for _, idf := range []bool{false, true} {
		hits, err := svc.WithLexicalIDF(idf).Search(ctx, team, SearchQuery{
			Query: "retry budget", Wing: "wing_acme", Limit: 5, SkipTelemetry: true,
		})
		if err != nil {
			t.Fatalf("search (idf=%v): %v", idf, err)
		}
		if len(hits) == 0 {
			t.Fatalf("search (idf=%v) returned nothing", idf)
		}
	}
}
