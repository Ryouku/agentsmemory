package palace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"

	"gorm.io/gorm"
)

// Sentinel errors the MCP layer maps to tool-level results. Keeping them here
// lets the transport stay ignorant of gorm: the service is the only place that
// knows the persistence library.
var (
	// ErrNotFound is returned when a drawer id does not exist for the team.
	ErrNotFound = errors.New("drawer not found")
	// ErrInvalidInput is returned when a required argument is missing or empty.
	ErrInvalidInput = errors.New("invalid input")
)

// Defaults and bounds for search/recall, mirroring the frozen Python contract so
// the tool surface behaves identically (search: limit 1-100 def5, max_distance
// 0-2 def1.5; check_duplicate: threshold def0.9).
const (
	DefaultSearchLimit  = 5
	MaxSearchLimit      = 100
	DefaultMaxDistance  = 1.5
	DefaultDupThreshold = 0.9

	// DefaultRerankPool is how many fused candidates a configured cross-encoder
	// scores. Widening the pool is the point of reranking: hybridCandidateMultiplier
	// alone shows the ranker only limit*3 candidates (15 for a default search), so a
	// document the vector pass ranked 40th can never reach the page no matter how
	// well it answers the query. 50 is wide enough to change the answer and small
	// enough to cross-encode within a search's latency budget.
	DefaultRerankPool = 50

	// DefaultRerankWeight is how much of the final ordering the cross-encoder
	// decides, with the rest left to the hybrid score it refines.
	//
	// It is a BLEND rather than a handover, and that is a measured choice. Letting
	// the cross-encoder's score decide alone throws away the lexical evidence in
	// the fused score: on a 12-question eval of this palace, ordering purely by
	// cross-encoder scored MRR 0.686 where the fused order scored 1.000, on the
	// queries that carry an identifier or a flag — exactly the searches a
	// developer actually types. A sweep put 0.25 and 0.50 joint-best (0.958).
	DefaultRerankWeight = 0.5
)

// Diary defaults, mirroring the frozen Python diary tools so the journal behaves
// identically: every entry is filed into the "diary" room, an untagged entry gets
// the "general" topic, and diary_read returns the last 10 entries by default and
// at most 100.
const (
	// DiaryRoom is the room every diary entry lives in; diary_read scopes by it
	// together with the agent, cleanly separating journal entries from memories.
	DiaryRoom = "diary"
	// DefaultDiaryTopic tags a diary entry written without an explicit topic.
	DefaultDiaryTopic = "general"
	// DefaultDiaryReadN is diary_read's window when last_n is unset.
	DefaultDiaryReadN = 10
	// MaxDiaryReadN caps diary_read's window so one call cannot scan unbounded.
	MaxDiaryReadN = 100

	// diaryTimeLayout stamps a diary entry's FiledAt with a FIXED-WIDTH, nine-digit
	// nanosecond fraction. diary_read orders by filed_at as a string (SQLite TEXT),
	// so the format must be lexicographically sortable: time.RFC3339Nano trims
	// trailing zeros, making its width vary and a string sort disagree with chrono
	// order. A zero-padded fraction keeps string order == time order, and the
	// nanosecond resolution also makes each entry's id-seed unique.
	diaryTimeLayout = "2006-01-02T15:04:05.000000000Z07:00"
)

// AAAKSpec is the compressed memory dialect agents use for diary and closet
// lines. It is static reference text (the get_aaak_spec tool returns it verbatim)
// so it lives as a constant rather than in storage.
const AAAKSpec = `AAAK is a compressed memory dialect MemPalace uses for efficient, human- and LLM-readable storage.

FORMAT:
  ENTITIES: 3-letter uppercase codes (ALC=Alice, JOR=Jordan).
  EMOTIONS: *markers* before text (*warm*=joy, *fierce*=determined, *raw*=vulnerable, *bloom*=tenderness).
  STRUCTURE: pipe-separated fields. FAM: family | PROJ: projects | ⚠: warnings.
  DATES: ISO (2026-03-31). COUNTS: Nx = N mentions. IMPORTANCE: ★ to ★★★★★.

Read AAAK naturally — expand codes mentally, treat *markers* as emotional context.
When writing AAAK: use entity codes, mark emotions, keep structure tight.`

// Embedder turns text into vectors. It is declared at the consumer (per Go's
// "accept interfaces" guidance) so the service depends on the capability, not on
// the concrete Ollama client — which also makes it trivial to fake in tests.
type Embedder interface {
	// Embed returns one vector per input string, in order.
	Embed(ctx context.Context, inputs []string) ([][]float32, error)
	// EmbedOne is the single-string convenience used by search and check_duplicate.
	EmbedOne(ctx context.Context, input string) ([]float32, error)
}

// Reranker scores candidate documents against a query with a cross-encoder,
// returning one score per document IN INPUT ORDER (higher is better). Like
// Embedder it is declared at the consumer, so the service depends on the
// capability rather than on the TEI client that currently provides it.
//
// A cross-encoder reads the query and the document together, which is strictly
// more evidence than the vector+BM25 blend that selects the candidates — but it
// is also far more expensive, which is why it only ever sees a shortlist.
type Reranker interface {
	Rerank(ctx context.Context, query string, docs []string) ([]float64, error)
}

// Service is the core memory loop: it files drawers (chunk -> embed -> store) and
// recalls them (embed query -> nearest-neighbour -> join metadata). It composes
// the metadata Repo, an Embedder, and the vector store seam; everything is
// tenant-scoped by the teamID argument, which is also the vector namespace.
type Service struct {
	repo    *Repo
	embed   Embedder
	vectors store.VectorStore
	dim     int // embedding dimension new namespaces are created with (bge-m3 = 1024)
	// rerank, when non-nil, cross-encodes the top rerankPool fused candidates and
	// reorders them before Search pages. nil is the default and means recall stops
	// at the vector+BM25+closet fusion — the behaviour every deployment had before
	// a reranker endpoint was configurable.
	rerank       Reranker
	rerankPool   int
	rerankWeight float64
	// bm25Auto scales the lexical fusion weight per query by its measured lexical
	// signal; bm25Base is the ceiling. See config.BM25Weight for the evidence.
	bm25Auto bool
	bm25Base float64
	// bm25IDF weights each query term by how much it discriminates instead of
	// counting it once, when bm25Auto is on.
	//
	// It is reachable from configuration rather than eval-only because a measured
	// arm nobody can run is not a finding: four tables across two unrelated
	// corpora put it ahead of the binary count (0.377 vs 0.257, 0.370 vs 0.290,
	// 0.246 vs 0.183, 0.726 vs 0.673), and every one of them measured a code path
	// production could not select. The default stays binary until the maintainer
	// of the second corpus has seen the case for moving it.
	bm25IDF bool
	// fusionRRF makes search fuse vector and lexical evidence by RANK
	// (reciprocal-rank fusion) instead of by weighted score. It exists because a
	// linear blend lets one bad signal drag a good candidate down: on a large,
	// diverse corpus BM25 measured WORSE than vector alone (MRR 0.178 vs 0.335),
	// and the linear fusion carried that damage into the page — and worse, into
	// the cross-encoder's pool, which is taken off the fused head, so the one
	// component that did work never saw the candidates fusion had buried. RRF
	// bounds any single signal's influence to a rank position, and on that same
	// corpus rrf+rerank was the best arm of seventeen.
	//
	// Off by default: the linear blend is best on the corpora we have measured
	// where lexical evidence helps, and a ranking default changes what every
	// existing palace returns. FUSION=rrf turns it on for the corpora where the
	// eval says it should be.
	fusionRRF bool

	// closetBoostScale scales every closet rank boost: 1 is the full curation
	// prior, 0 turns closets into a pure ranking no-op. It exists because the
	// prior's worth depends on what the palace holds: on a curated palace the
	// boost promotes the memories a human chose to keep; on a corpus dominated
	// by mined transcripts the eval measured the same boost DEMOTING correct
	// answers (~0.10 MRR at n=40) — the closets cover the curated 2% and lift
	// it over the mined gold. The operator knows which palace theirs is.
	closetBoostScale float64

	// mineLocks serializes concurrent mines of the same (team, source) within this
	// process, so two re-mines cannot interleave their purge-then-write and leave
	// both content versions behind. It is the in-process analogue of the frozen
	// miner's per-source mine_lock. Note: it does NOT coordinate across horizontally
	// scaled instances — a cross-instance guard would need a DB advisory lock.
	mineLocks *keyedMutex
	// graphLocks serializes a team's recompute_graph the same way: a recompute
	// replaces hallways and delete-and-rebuilds entity tunnels, so two concurrent
	// recomputes of one team could interleave and leave a stale rebuild. Same
	// in-process caveat as mineLocks.
	graphLocks *keyedMutex
}

// NewService wires the collaborators. dim is the embedding width used to create a
// tenant's vector namespace on first write (the actual width of returned vectors
// is authoritative and used in Add; dim is only the seed/fallback).
func NewService(repo *Repo, embed Embedder, vectors store.VectorStore, dim int) *Service {
	return &Service{
		repo: repo, embed: embed, vectors: vectors, dim: dim,
		// Adaptive lexical weighting is the default because it is the only
		// configuration measured best in BOTH query regimes; a zero value here
		// would silently make fusion vector-only, which is a measured regression
		// on identifier queries.
		bm25Auto: true, bm25Base: hybridBM25Weight,
		// Pointers, not values: the eval's degraded path shallow-copies the
		// service to drop the reranker, and a copied sync.Map is a vet error and
		// a real hazard — the copy must SHARE these locks, it guards the same
		// palace.
		mineLocks: &keyedMutex{}, graphLocks: &keyedMutex{},
		closetBoostScale: 1,
	}
}

// WithReranker attaches a cross-encoder to Search and returns s for chaining.
// pool is how many fused candidates get cross-encoded; values below 1 fall back
// to DefaultRerankPool.
//
// It is a post-construction setter rather than a NewService parameter because
// reranking is optional deployment wiring, not a collaborator the service needs
// to exist — every call site that has no reranker configured simply never calls
// this. It must be called before the service is shared across goroutines: the
// field is read without synchronization on the search path.
func (s *Service) WithReranker(r Reranker, pool int) *Service {
	if pool < 1 {
		pool = DefaultRerankPool
	}
	s.rerank, s.rerankPool = r, pool
	if s.rerankWeight == 0 {
		s.rerankWeight = DefaultRerankWeight
	}
	return s
}

// WithFusion selects how vector and lexical evidence combine: "rrf" for
// reciprocal-rank fusion, anything else for the weighted-score blend. Same
// post-construction-setter contract as WithReranker: call it before the service
// is shared across goroutines.
func (s *Service) WithFusion(mode string) *Service {
	s.fusionRRF = strings.EqualFold(strings.TrimSpace(mode), "rrf")
	return s
}

// Clone returns a shallow copy, so a caller can configure one Service several
// ways without the configurations bleeding into each other.
//
// It exists because every With* setter MUTATES and returns the same pointer.
// That is convenient for a composition root, which configures once, and a trap
// for anything that configures repeatedly: a sweep that reused one Service would
// carry each cell's settings into the next, and every knob after the first would
// look inert — the exact conclusion such a sweep exists to draw, reached for the
// wrong reason.
//
// Shallow is correct here. The fields it copies are scalars and interface
// handles; the repo, embedder and vector store are shared on purpose, since a
// sweep varies ranking rather than storage.
func (s *Service) Clone() *Service {
	c := *s
	return &c
}

// WithClosetBoost scales the closet curation prior (1 = full, 0 = off). Same
// post-construction-setter contract as WithReranker: call before the service is
// shared across goroutines.
func (s *Service) WithClosetBoost(scale float64) *Service {
	if scale < 0 {
		scale = 0
	}
	if scale > 1 {
		scale = 1
	}
	s.closetBoostScale = scale
	return s
}

// WithBM25Weight configures the lexical half of fusion: auto scales it per query,
// otherwise base is used as a fixed weight. Out-of-range bases keep the default.
func (s *Service) WithBM25Weight(auto bool, base float64) *Service {
	s.bm25Auto = auto
	if base >= 0 && base <= 1 {
		s.bm25Base = base
	}
	return s
}

// WithLexicalIDF selects the IDF-weighted coverage feature for auto weighting.
// Same post-construction-setter contract as WithReranker: call it before the
// service is shared across goroutines.
func (s *Service) WithLexicalIDF(on bool) *Service {
	s.bm25IDF = on
	return s
}

// WithRerankWeight sets how much the cross-encoder's opinion counts against the
// hybrid score it refines: 1 hands it the whole decision, 0 ignores it. Values
// outside [0,1] are ignored, leaving DefaultRerankWeight in place.
func (s *Service) WithRerankWeight(w float64) *Service {
	if w >= 0 && w <= 1 {
		s.rerankWeight = w
	}
	return s
}

// AddInput is the add_drawer payload: where the memory goes (wing, room — both
// required), the verbatim text, and optional provenance/date metadata.
type AddInput struct {
	Wing        string
	Room        string
	Content     string
	SourceFile  string
	ContentDate string
}

// AddResult is what a filing returned: the drawers written, and whether their
// vectors are still owed.
//
// PendingEmbedding is not an error and not a detail — it is the difference
// between "this memory is findable" and "this memory exists but nothing will
// recall it yet". The caller is expected to say so out loud, because the failure
// it comes from (an embedder that is down) is invisible from the outside.
type AddResult struct {
	Drawers          []Drawer
	PendingEmbedding bool
}

// Add files a memory: it chunks oversized content, embeds every chunk in one
// batch, writes the vectors, then writes the metadata rows. Vectors are written
// before rows so a row never exists without its embedding — search joins row to
// vector, and the inverse orphan (a vector with no row) is harmless because
// search skips ids it cannot resolve. It returns the drawers created (one per
// chunk), so the tool can report their ids.
func (s *Service) Add(ctx context.Context, teamID string, in AddInput) (AddResult, error) {
	wing := strings.TrimSpace(in.Wing)
	room := strings.TrimSpace(in.Room)
	content := strings.TrimSpace(in.Content)
	if wing == "" || room == "" || content == "" {
		return AddResult{}, fmt.Errorf("%w: wing, room and content are required", ErrInvalidInput)
	}

	chunks := ChunkText(content, ChunkSize, ChunkOverlap, ChunkMin)
	// A failed embed does not fail the write — see embedOrDefer. vectors is nil in
	// that case and the rows are absorbed onto the background queue instead.
	vectors := s.embedOrDefer(ctx, chunks)

	filedAt := time.Now().UTC().Format(time.RFC3339)
	drawers := make([]Drawer, len(chunks))
	for i, c := range chunks {
		// The first chunk is the parent the rest of a multi-chunk write point
		// back to; the first chunk itself has no parent.
		parentID := ""
		if i > 0 {
			parentID = drawers[0].ID
		}
		drawers[i] = Drawer{
			ID:          DrawerID(teamID, wing, room, in.SourceFile, c.Index, c.Content),
			TeamID:      teamID,
			Wing:        wing,
			Room:        room,
			SourceFile:  in.SourceFile,
			ChunkIndex:  c.Index,
			Content:     c.Content,
			FiledAt:     filedAt,
			ContentDate: strings.TrimSpace(in.ContentDate),
			ParentID:    parentID,
		}
	}

	// Re-filing a *named* source replaces it wholesale: purge the source's prior
	// drawers (rows + vectors) before writing the new set, so shrinking the
	// content cannot leave orphaned higher-index chunks behind. A source-less add
	// is a standalone memory (deduped by its content-hash id), so it is not purged.
	if in.SourceFile != "" {
		if err := s.purgeSource(ctx, teamID, wing, room, in.SourceFile); err != nil {
			return AddResult{}, err
		}
	}

	if vectors == nil {
		if err := s.repo.SaveUnembedded(ctx, drawers); err != nil {
			return AddResult{}, fmt.Errorf("save drawers (embedding deferred): %w", err)
		}
		return AddResult{Drawers: drawers, PendingEmbedding: true}, nil
	}
	if err := s.storeDrawers(ctx, teamID, drawers, vectors); err != nil {
		return AddResult{}, err
	}
	return AddResult{Drawers: drawers}, nil
}

// embedChunks embeds a batch of chunks, returning one vector per chunk in order.
// It is the shared embed step of every filing path (add_drawer, diary_write), so
// the chunk -> vector contract is single-sourced rather than copied per tool.
func (s *Service) embedChunks(ctx context.Context, chunks []Chunk) ([][]float32, error) {
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Content
	}
	vectors, err := s.embed.Embed(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("embed drawer: %w", err)
	}
	return vectors, nil
}

// embedOrDefer embeds chunks, returning nil when the embedder could not do it.
//
// A nil result means "write the rows without vectors and let the background
// worker finish the job" — the durable half of a memory is its text, and losing
// that because an optional-at-this-instant service is down is the worst possible
// trade. The queue this feeds (embedded_at IS NULL) already exists for migration
// imports, so a deferred row is picked up by the same worker and the same model.
//
// EVERY embed failure defers, not just a refused connection: a timeout, a 500, a
// model that was never pulled. Classifying them would mean deciding which
// failures are worth losing a memory over, and none are.
func (s *Service) embedOrDefer(ctx context.Context, chunks []Chunk) [][]float32 {
	vectors, err := s.embedChunks(ctx, chunks)
	if err == nil {
		return vectors
	}
	slog.Warn("embedder unavailable, storing for background embedding", "chunks", len(chunks), "error", err)
	return nil
}

// storeDrawers is the shared persistence tail every filing path ends in: ensure
// the tenant's vector namespace exists, write the embeddings, then write the
// metadata rows. Vectors are written before rows so a row never exists without
// its embedding — search joins row to vector, and the inverse orphan (a vector
// with no row) is harmless because search skips ids it cannot resolve. The vector
// width the model returned is authoritative for namespace creation, so a mis-set
// s.dim can never make EnsureNamespace and Upsert disagree. drawers and vectors
// must be index-aligned and the same length.
func (s *Service) storeDrawers(ctx context.Context, teamID string, drawers []Drawer, vectors [][]float32) error {
	if err := s.upsertDrawerVectors(ctx, teamID, drawers, vectors); err != nil {
		return err
	}
	if err := s.repo.Save(ctx, drawers); err != nil {
		return fmt.Errorf("save drawers: %w", err)
	}
	return nil
}

// upsertDrawerVectors ensures the tenant's vector namespace and writes the
// embeddings only — no metadata rows. It is shared by the synchronous filing tail
// (storeDrawers, which then writes rows) and the background embed worker (which
// backfills vectors for rows absorb already wrote). drawers and vectors must be
// index-aligned and the same length; the returned vector width is authoritative
// for namespace creation so a mis-set s.dim cannot make EnsureNamespace and Upsert
// disagree.
func (s *Service) upsertDrawerVectors(ctx context.Context, teamID string, drawers []Drawer, vectors [][]float32) error {
	dim := s.dim
	if len(vectors) > 0 {
		dim = len(vectors[0])
	}
	if err := s.vectors.EnsureNamespace(ctx, teamID, dim); err != nil {
		return fmt.Errorf("ensure namespace: %w", err)
	}
	points := make([]store.Point, len(drawers))
	for i, d := range drawers {
		// Payload carries only the cheap filter keys; the verbatim content stays
		// single-sourced in the drawers table, joined back by id at search time.
		points[i] = store.Point{
			ID:      d.ID,
			Vector:  vectors[i],
			Payload: map[string]any{"wing": d.Wing, "room": d.Room},
		}
	}
	if err := s.vectors.Upsert(ctx, teamID, points); err != nil {
		return fmt.Errorf("upsert vectors: %w", err)
	}
	return nil
}

// purgeSource deletes every drawer (row + vector) previously filed from a source
// within a (team, wing, room), so a re-add of that source replaces rather than
// accumulates. Vectors are dropped by the ids the rows carry, then the rows.
func (s *Service) purgeSource(ctx context.Context, teamID, wing, room, source string) error {
	ids, err := s.repo.IDsBySource(ctx, teamID, wing, room, source)
	if err != nil {
		return fmt.Errorf("list source drawers: %w", err)
	}
	if len(ids) == 0 {
		return nil
	}
	if err := s.vectors.Delete(ctx, teamID, ids); err != nil {
		return fmt.Errorf("purge source vectors: %w", err)
	}
	if err := s.repo.DeleteBySource(ctx, teamID, wing, room, source); err != nil {
		return fmt.Errorf("purge source rows: %w", err)
	}
	return nil
}

// Get returns one drawer, mapping an unknown id to ErrNotFound.
func (s *Service) Get(ctx context.Context, teamID, id string) (Drawer, error) {
	d, err := s.repo.Get(ctx, teamID, id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Drawer{}, ErrNotFound
	}
	return d, err
}

// Update edits an existing drawer's content/wing/room in place (its id is
// stable). A supplied field must be non-empty — update_drawer must not be a back
// door around the non-empty invariant add_drawer enforces (a blank wing/room
// would file the drawer into an unaddressable taxonomy bucket). Any change
// re-embeds the drawer's final content and re-upserts the vector *before* the row
// is written, so a failed embed leaves the drawer fully consistent in its old
// state rather than with a row ahead of its stale vector. A no-op patch just
// returns the current drawer.
func (s *Service) Update(ctx context.Context, teamID, id string, patch DrawerPatch) (Drawer, error) {
	for _, f := range []struct {
		name string
		val  *string
	}{{"content", patch.Content}, {"wing", patch.Wing}, {"room", patch.Room}} {
		if f.val != nil && strings.TrimSpace(*f.val) == "" {
			return Drawer{}, fmt.Errorf("%w: %s cannot be set empty", ErrInvalidInput, f.name)
		}
	}

	current, err := s.Get(ctx, teamID, id) // also maps unknown id -> ErrNotFound
	if err != nil {
		return Drawer{}, err
	}

	// Nothing to change.
	if patch.Content == nil && patch.Wing == nil && patch.Room == nil {
		return current, nil
	}

	// A memory over ChunkSize is several rows sharing a parent, and this function
	// updates ONE row. Rewriting the content of one chunk leaves the others live,
	// individually embedded, and still returning the retracted claim — observed
	// in production, with the stale chunks ranking ABOVE the correction, and the
	// call reporting success throughout. Refuse instead of half-doing it.
	//
	// Refusing rather than re-chunking is deliberate for now: re-chunking changes
	// how many rows exist and which ids they carry, which silently invalidates
	// every anchor, tunnel and knowledge-graph fact pointing at the old ones.
	// That is a bigger change than a bug fix and it is recorded in the backlog.
	// Every patchable field is one the chunks of a memory must agree on, so the
	// guard covers all of them rather than content alone.
	//
	// Content was the reported case: rewriting one chunk left the others live with
	// the old text, ranking above the correction. Wing and room split the memory
	// instead — one chunk moves and the rest stay — and this release makes that
	// worse than it was, because recall now defaults to the registration's wing:
	// after a split neither wing returns the whole memory, and nothing tells the
	// reader that what they got is a fragment.
	chunks, err := s.repo.MemoryChunks(ctx, teamID, id)
	if err != nil {
		return Drawer{}, fmt.Errorf("look up the memory this drawer belongs to: %w", err)
	}
	if len(chunks) > 1 {
		what := "content"
		harm := "leave the other chunk(s) live with the old text — still embedded, still returned " +
			"by search, and with nothing marking them retracted"
		if patch.Content == nil {
			what = "wing or room"
			harm = "move this chunk away from the rest of the memory, so no single scope returns " +
				"all of it and a scoped search answers with a fragment that does not say it is one"
		}
		return Drawer{}, fmt.Errorf(
			"%w: drawer %s is chunk %d of a %d-chunk memory, and changing its %s would %s. "+
				"Delete the memory and file it again as one piece",
			ErrInvalidInput, short12(id), current.ChunkIndex, len(chunks), what, harm)
	}

	// Compute the post-patch state and refresh the derived index first.
	finalContent, finalWing, finalRoom := current.Content, current.Wing, current.Room
	if patch.Content != nil {
		finalContent = *patch.Content
	}
	if patch.Wing != nil {
		finalWing = *patch.Wing
	}
	if patch.Room != nil {
		finalRoom = *patch.Room
	}
	vec, err := s.embed.EmbedOne(ctx, finalContent)
	if err != nil {
		return Drawer{}, fmt.Errorf("re-embed updated drawer: %w", err)
	}
	point := store.Point{
		ID:      id,
		Vector:  vec,
		Payload: map[string]any{"wing": finalWing, "room": finalRoom},
	}
	if err := s.vectors.Upsert(ctx, teamID, []store.Point{point}); err != nil {
		return Drawer{}, fmt.Errorf("re-upsert updated vector: %w", err)
	}

	// Index is current; now commit the authoritative row.
	updated, err := s.repo.Update(ctx, teamID, id, patch)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Drawer{}, ErrNotFound
	}
	if err != nil {
		return Drawer{}, err
	}
	return updated, nil
}

// Delete removes a drawer's metadata row and its vector. The row goes first so
// the authoritative record is gone before the derived index; a failed vector
// delete leaves an orphan the next search harmlessly skips.
func (s *Service) Delete(ctx context.Context, teamID, id string) (int, error) {
	// The memory is the unit, not the row. A memory over ChunkSize is several
	// rows sharing a parent, and deleting one of them left the rest orphaned —
	// still embedded, still returned by search, and now pointing at a parent that
	// no longer exists. Reproduced: deleting the parent of a two-chunk memory
	// left chunk 1 live.
	//
	// Unlike an update, a delete has no reference ambiguity to weigh: the caller
	// is removing the memory, so removing all of it is what they asked for. The
	// count is returned so the caller can say how much went, rather than
	// reporting the one id it was given.
	chunks, err := s.repo.MemoryChunks(ctx, teamID, id)
	if err != nil {
		return 0, fmt.Errorf("look up the memory this drawer belongs to: %w", err)
	}
	ids := make([]string, 0, len(chunks))
	for _, c := range chunks {
		ids = append(ids, c.ID)
	}
	if len(ids) == 0 {
		ids = []string{id} // no row to resolve; delete what we were given
	}
	for _, cid := range ids {
		if err := s.repo.Delete(ctx, teamID, cid); err != nil {
			return 0, fmt.Errorf("delete drawer row: %w", err)
		}
	}
	if err := s.vectors.Delete(ctx, teamID, ids); err != nil {
		return 0, fmt.Errorf("delete drawer vectors: %w", err)
	}
	return len(ids), nil
}

// List paginates a team's drawers, optionally narrowed to a wing and/or room.
func (s *Service) List(ctx context.Context, teamID, wing, room string, limit, offset int) ([]Drawer, error) {
	return s.repo.List(ctx, teamID, wing, room, limit, offset)
}

// SearchQuery is the mempalace_search input.
type SearchQuery struct {
	Query       string
	Wing        string  // optional filter
	Room        string  // optional filter
	Limit       int     // 1..100, defaults to DefaultSearchLimit
	MaxDistance float64 // drop hits farther than this; <=0 disables the filter
	// SkipTelemetry keeps this search out of the recall statistics. Set by the
	// eval, whose thousands of synthetic queries would otherwise drown the real
	// usage signal the statistics exist to show.
	SkipTelemetry bool
	// Context is optional background the caller can supply to sharpen reranking —
	// what it is working on, so an ambiguous query lands in the right sense. It
	// feeds the cross-encoder ONLY (see rerankQuery); it deliberately does not
	// touch the embedding, because widening the query vector would quietly change
	// which candidates are retrieved rather than how they are ordered.
	Context string
}

// rerankQuery returns the text the cross-encoder scores against: the (already
// capped) query, with Context appended when the caller supplied any. A blank
// Context leaves the query exactly as the vector pass saw it.
func (q SearchQuery) rerankQuery(query string) string {
	if c := strings.TrimSpace(q.Context); c != "" {
		return query + "\n\n" + c
	}
	return query
}

// searchFilter renders a query's wing/room scope as the backend filter, matching
// the payload keys written at upsert time. An unscoped query yields nil, which
// every driver reads as "search everything".
func searchFilter(q SearchQuery) store.Filter {
	if q.Wing == "" && q.Room == "" {
		return nil
	}
	f := store.Filter{}
	if q.Wing != "" {
		f["wing"] = q.Wing
	}
	if q.Room != "" {
		f["room"] = q.Room
	}
	return f
}

// Search recalls drawers by hybrid relevance to a query. It embeds the query and
// over-fetches a pool of nearest vector neighbours, applies the wing/room and
// max-distance filters, then RE-RANKS the survivors by a convex blend of vector
// similarity and lexical Okapi-BM25 (rankHybrid) before returning the top page.
// The blend matches the frozen searcher — vector finds the semantically near,
// BM25 rewards literal term overlap — and beats either alone. Closet boost (the
// third frozen signal) arrives with the mining phase that builds closets; until
// then Score is the vector+BM25 fusion and Distance the raw cosine distance.
func (s *Service) Search(ctx context.Context, teamID string, q SearchQuery) ([]SearchHit, error) {
	query := strings.TrimSpace(q.Query)
	if query == "" {
		return nil, fmt.Errorf("%w: query is required", ErrInvalidInput)
	}
	// Cap by runes, not bytes: the contract caps queries at 250 characters, and a
	// byte slice could split a multibyte rune into invalid UTF-8 before it reaches
	// the embedder and tokenizer.
	if r := []rune(query); len(r) > 250 {
		query = string(r[:250])
	}
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultSearchLimit
	}
	if limit > MaxSearchLimit {
		limit = MaxSearchLimit
	}

	vec, err := s.embed.EmbedOne(ctx, query)
	if err != nil {
		// Recall genuinely cannot proceed — a query has to become a vector — so
		// unlike filing this fails. Name the cause, because the same outage lets
		// writes succeed (queued), and an agent seeing one work and the other not
		// will otherwise conclude the memory itself is broken.
		return nil, fmt.Errorf("embed query (the embedder is unreachable; writes are still being stored and queued, but recall needs it): %w", err)
	}

	// Over-fetch a re-rank pool: BM25 can only reorder what vector retrieval
	// surfaced, so the pool must be wider than the page (limit*multiplier) for a
	// lexical match outside the top-N to be promoted into it.
	//
	// The wing/room scope goes to the BACKEND rather than being applied to the
	// results, so every candidate the index returns is already in scope and the
	// pool stays the size the re-rank was designed for however narrow the filter
	// is. (This used to over-fetch 10 000 candidates and drop the non-matching
	// ones here — a cost that grew with the palace and was paid on every scoped
	// search, which is every search once wings are per-project.)
	candidateK := candidateKFor(limit, s.rerank != nil, s.rerankPool, s.rerankWeight)
	hits, err := s.vectors.Search(ctx, teamID, vec, candidateK, searchFilter(q))
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}

	// Resolve candidate ids to rows in one query.
	ids := make([]string, len(hits))
	for i, h := range hits {
		ids[i] = h.ID
	}
	rows, err := s.repo.GetMany(ctx, teamID, ids)
	if err != nil {
		return nil, fmt.Errorf("load drawer rows: %w", err)
	}

	// Keep the survivors that pass the wing/room/max-distance filters, in vector
	// order, carrying content (for BM25) and distance (for vector similarity).
	// The wing/room comparisons are redundant when the index honoured the filter
	// above, and deliberately kept: the drawer row is the truth about where a
	// drawer lives, and a stale index must never surface another wing's memory.
	survivors := make([]SearchHit, 0, len(hits))
	for _, h := range hits {
		d, ok := rows[h.ID]
		if !ok {
			continue // orphan vector (row deleted) — skip
		}
		if q.Wing != "" && d.Wing != q.Wing {
			continue
		}
		if q.Room != "" && d.Room != q.Room {
			continue
		}
		distance := distanceFromScore(h.Score)
		if q.MaxDistance > 0 && distance > q.MaxDistance {
			continue
		}
		survivors = append(survivors, SearchHit{Drawer: d, Distance: distance})
	}

	// Closet boost: search the team's closets with the same query and let the
	// best-matching closets lift the rank of the drawers from their source. Closets
	// are a SIGNAL, never a gate — a team that has never mined has no closets, so a
	// failed or empty closet search simply yields no boosts and search proceeds.
	closetBoostBySource := s.closetBoosts(ctx, teamID, vec)

	// Hybrid re-rank the survivors by content + distance + closet boost, then page.
	docs := make([]string, len(survivors))
	dists := make([]float64, len(survivors))
	boosts := make([]float64, len(survivors))
	for i, h := range survivors {
		docs[i] = h.Drawer.Content
		dists[i] = h.Distance
		boosts[i] = closetBoostBySource[h.Drawer.SourceFile]
	}
	var ranked []HybridScore
	switch {
	case s.fusionRRF:
		// Rank fusion ignores bm25Base entirely — the weight question does not
		// arise when neither signal contributes a magnitude, only a position.
		ranked = rankRRF(query, docs, dists, boosts)
	case s.bm25Auto && s.bm25IDF:
		ranked = rankHybridAdaptiveIDF(query, docs, dists, boosts, s.bm25Base)
	case s.bm25Auto:
		ranked = rankHybridAdaptive(query, docs, dists, boosts, s.bm25Base)
	default:
		ranked = rankHybridWeighted(query, docs, dists, boosts, s.bm25Base)
	}

	// Stage 4: cross-encode the shortlist. The fusion above is a cheap proxy built
	// from a query vector and term overlap; a cross-encoder reads the query and
	// the document together and is the better judge of MEANING — but the fused
	// score is the better judge of VOCABULARY, and a query naming an identifier
	// leans on exactly that. So the two are blended rather than one replacing the
	// other, and both are reported.
	ranked, reranked := s.applyRerank(ctx, q.rerankQuery(query), survivors, ranked)

	results := make([]SearchHit, 0, limit)
	for _, r := range ranked {
		if len(results) >= limit {
			break
		}
		hit := survivors[r.Index]
		hit.Score = r.Fused
		hit.BM25 = r.BM25
		hit.ClosetBoost = r.Boost
		hit.RerankScore, hit.Reranked = r.Rerank, r.Reranked
		results = append(results, hit)
	}

	// Record what this recall found. Best-effort by construction: measurement must
	// never be able to fail the thing it measures.
	if q.SkipTelemetry {
		return results, nil
	}
	ev := searchEventRow{
		TeamID: teamID, Wing: q.Wing, Room: q.Room, Query: query,
		// Whether reranking HAPPENED, not whether a reranker exists. The previous
		// value was boolToInt(s.rerank != nil), so at weight 0 — where
		// applyRerankWith returns before scoring anything — every event claimed a
		// cross-encoder pass that never ran. ADR-001 calibrates its abstention
		// threshold from these rows.
		Candidates: len(hits), Hits: len(results), Reranked: boolToInt(reranked),
	}
	if len(results) > 0 {
		ev.TopScore = results[0].Score
	}
	s.repo.recordSearch(ctx, ev)

	return results, nil
}

// candidateKFor is how many vector neighbours a search fetches.
//
// A cross-encoder can only promote what retrieval surfaced, so widening the pool
// it sees is where the accuracy comes from — not from the scoring alone. But it
// widens only when the cross-encoder will actually RUN: at weight 0,
// applyRerankWith returns before scoring anything, so a configured reranker
// bought a wider fetch and a bigger GetMany join on every search and cross-
// encoded none of it.
//
// It is a function rather than a branch inline so the rule can be driven by a
// test; the inline version was correct and unfalsifiable.
func candidateKFor(limit int, rerankConfigured bool, rerankPool int, rerankWeight float64) int {
	k := limit * hybridCandidateMultiplier
	if rerankConfigured && rerankWeight > 0 && k < rerankPool {
		k = rerankPool
	}
	return k
}

// boolToInt maps a flag onto the INTEGER column SQLite uses for booleans.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// applyRerank cross-encodes the best rerankPool candidates and returns ranked
// reordered by cross-encoder score, with each reordered entry's Rerank set. The
// tail beyond the pool keeps its fused order and a zero Rerank — it was never
// scored, and pretending otherwise would put an unscored drawer above a scored
// one.
//
// It fails OPEN: with no reranker configured, nothing to score, or any error
// from the endpoint, ranked is returned untouched and search proceeds on the
// hybrid order. That mirrors the closet boost's rule that a ranking input is a
// signal, never a gate — a reranker that is down or slow must degrade recall,
// never break it.
func (s *Service) applyRerank(ctx context.Context, query string, survivors []SearchHit, ranked []HybridScore) ([]HybridScore, bool) {
	return s.applyRerankWith(ctx, query, survivors, ranked, s.rerankWeight)
}

// applyRerankWith is applyRerank at an explicit blend weight, so the eval can
// measure what the weight is worth instead of the default being someone's taste.
//
// The two signals know different things: the cross-encoder reads the query and
// the document together, which the embedder never did, and the fused score
// carries the lexical evidence, which a cross-encoder logit does not
// distinguish. Blending keeps both; handing over discards one.
func (s *Service) applyRerankWith(ctx context.Context, query string, survivors []SearchHit, ranked []HybridScore, weight float64) ([]HybridScore, bool) {
	if s.rerank == nil || len(ranked) == 0 || weight <= 0 {
		return ranked, false
	}
	pool := min(s.rerankPool, len(ranked))
	docs := make([]string, pool)
	for i := range docs {
		docs[i] = survivors[ranked[i].Index].Drawer.Content
	}

	scores, err := s.rerank.Rerank(ctx, query, docs)
	if err != nil {
		// A degraded reranker returns FALSE, deliberately. It failed open and the
		// page is the fused order, so a telemetry row claiming a cross-encoder pass
		// would be exactly as wrong as the weight-0 case this fix is about.
		slog.Warn("rerank failed, falling back to hybrid order", "error", err, "candidates", pool)
		return ranked, false
	}
	if len(scores) != pool {
		slog.Warn("rerank returned the wrong number of scores", "want", pool, "got", len(scores))
		return ranked, false
	}
	return BlendRerank(ranked, scores, weight), true
}

// RerankScoresFor fetches cross-encoder scores for the head of a fused ranking,
// or nil when there is no reranker or the call fails. The caller blends them with
// BlendRerank, possibly several times at different weights, without paying for
// the inference again.
func (s *Service) RerankScoresFor(ctx context.Context, query string, survivors []SearchHit, ranked []HybridScore) []float64 {
	if s.rerank == nil || len(ranked) == 0 {
		return nil
	}
	pool := min(s.rerankPool, len(ranked))
	docs := make([]string, pool)
	for i := range docs {
		docs[i] = survivors[ranked[i].Index].Drawer.Content
	}
	scores, err := s.rerank.Rerank(ctx, query, docs)
	if err != nil {
		slog.Warn("rerank failed, falling back to hybrid order", "error", err, "candidates", pool)
		return nil
	}
	if len(scores) != pool {
		slog.Warn("rerank returned the wrong number of scores", "want", pool, "got", len(scores))
		return nil
	}
	return scores
}

// BlendRerank combines a fused ranking with cross-encoder scores already
// obtained for its head, at the given weight.
//
// It is separate from the call that fetches those scores because the scores do
// not depend on the weight: an eval comparing several weights was calling the
// cross-encoder once per weight with identical inputs, which multiplied the
// slowest step in the pipeline by the number of arms for no information at all.
func BlendRerank(ranked []HybridScore, scores []float64, weight float64) []HybridScore {
	pool := len(scores)
	if pool == 0 || pool > len(ranked) {
		return ranked
	}

	// Normalize both terms within this page before combining them: a
	// cross-encoder logit and a fused [0,1] score are not comparable numbers, and
	// adding them raw would let whichever has the wider range decide everything.
	rerankNorm := normalizeScores(scores)
	fusedRaw := make([]float64, pool)
	for i := range fusedRaw {
		fusedRaw[i] = ranked[i].Fused
	}
	fusedNorm := normalizeScores(fusedRaw)

	head := make([]HybridScore, pool)
	for i := range head {
		head[i] = ranked[i]
		head[i].Rerank, head[i].Reranked = scores[i], true
		head[i].Blended = weight*rerankNorm[i] + (1-weight)*fusedNorm[i]
	}
	// Stable so equal blended scores keep the fused order as the tie-break,
	// exactly as rankHybrid keeps the vector order.
	sort.SliceStable(head, func(a, b int) bool { return head[a].Blended > head[b].Blended })
	return append(head, ranked[pool:]...)
}

// normalizeScores min-max scales a slice into [0,1]. An all-equal slice maps to
// 1 everywhere: there is nothing to choose between, so this term should not
// reorder anything.
func normalizeScores(in []float64) []float64 {
	out := make([]float64, len(in))
	if len(in) == 0 {
		return out
	}
	min, max := in[0], in[0]
	for _, v := range in {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	span := max - min
	for i, v := range in {
		if span == 0 {
			out[i] = 1
			continue
		}
		out[i] = (v - min) / span
	}
	return out
}

// DuplicateResult is the check_duplicate verdict: whether the most similar
// existing drawer crosses the threshold, that similarity, and the match (nil
// when nothing is similar enough).
type DuplicateResult struct {
	IsDuplicate bool
	Similarity  float64
	Drawer      *Drawer
}

// CheckDuplicate reports whether content is near-identical to an existing drawer.
// It embeds the content, takes the single nearest neighbour, and compares its
// cosine similarity to threshold (callers pass DefaultDupThreshold when unset).
func (s *Service) CheckDuplicate(ctx context.Context, teamID, content string, threshold float64) (DuplicateResult, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return DuplicateResult{}, fmt.Errorf("%w: content is required", ErrInvalidInput)
	}
	// Cosine similarity lives in [-1, 1]; a duplicate threshold outside [0, 1] is
	// nonsense (>1 can never match an exact duplicate, <0 marks everything a
	// duplicate), so clamp it rather than trust a stray argument.
	if threshold < 0 {
		threshold = 0
	}
	if threshold > 1 {
		threshold = 1
	}
	vec, err := s.embed.EmbedOne(ctx, content)
	if err != nil {
		return DuplicateResult{}, fmt.Errorf("embed content: %w", err)
	}
	hits, err := s.vectors.Search(ctx, teamID, vec, 1, nil)
	if err != nil {
		return DuplicateResult{}, fmt.Errorf("vector search: %w", err)
	}
	if len(hits) == 0 {
		return DuplicateResult{IsDuplicate: false}, nil
	}
	top := hits[0]
	sim := float64(top.Score)
	res := DuplicateResult{IsDuplicate: sim >= threshold, Similarity: sim}
	if res.IsDuplicate {
		if d, err := s.repo.Get(ctx, teamID, top.ID); err == nil {
			res.Drawer = &d
		}
	}
	return res, nil
}

// Taxonomy is the get_taxonomy view: every wing with its rooms and counts.
type Taxonomy struct {
	Wings []TaxonomyWing `json:"wings"`
}

// TaxonomyWing is one wing in the taxonomy: its totals and the rooms inside it.
type TaxonomyWing struct {
	Wing    string     `json:"wing"`
	Drawers int        `json:"drawers"`
	Rooms   []RoomStat `json:"rooms"`
}

// GetTaxonomy assembles the wing -> rooms tree from the two indexed
// aggregations, so an agent can see the shape of a team's memory before searching.
func (s *Service) GetTaxonomy(ctx context.Context, teamID string) (Taxonomy, error) {
	wings, err := s.repo.Wings(ctx, teamID)
	if err != nil {
		return Taxonomy{}, err
	}
	rooms, err := s.repo.Rooms(ctx, teamID, "")
	if err != nil {
		return Taxonomy{}, err
	}
	byWing := make(map[string][]RoomStat, len(wings))
	for _, r := range rooms {
		byWing[r.Wing] = append(byWing[r.Wing], r)
	}
	tax := Taxonomy{Wings: make([]TaxonomyWing, 0, len(wings))}
	for _, w := range wings {
		tax.Wings = append(tax.Wings, TaxonomyWing{
			Wing:    w.Wing,
			Drawers: w.Drawers,
			Rooms:   byWing[w.Wing],
		})
	}
	return tax, nil
}

// Wings and Rooms expose the list_wings / list_rooms aggregations directly.
func (s *Service) Wings(ctx context.Context, teamID string) ([]WingStat, error) {
	return s.repo.Wings(ctx, teamID)
}

// Rooms lists a team's rooms, optionally within one wing.
func (s *Service) Rooms(ctx context.Context, teamID, wing string) ([]RoomStat, error) {
	return s.repo.Rooms(ctx, teamID, wing)
}

// ClosetsByWing lists one wing's closets — the pointer index built by mining.
// It completes the read surface a wing export needs (drawers, closets, tunnels,
// wing stats), so one *Service satisfies both halves of a wing transfer rather
// than callers having to hold a separate repository handle for this one query.
func (s *Service) ClosetsByWing(ctx context.Context, teamID, wing string) ([]Closet, error) {
	return s.repo.ClosetsByWing(ctx, teamID, wing)
}

// Reconnect re-readies a tenant's vector namespace and confirms the store is
// reachable. The Python tool invalidated a cached Qdrant client; this server is
// stateless (no per-session cache), so reconnect has no client to drop — it is
// instead a cheap liveness probe agents can call to verify the backend before a
// burst of writes. EnsureNamespace is idempotent, so re-running it is safe.
func (s *Service) Reconnect(ctx context.Context, teamID string) error {
	if err := s.vectors.EnsureNamespace(ctx, teamID, s.dim); err != nil {
		return fmt.Errorf("reconnect: vector store unreachable: %w", err)
	}
	return nil
}

// DiaryWriteInput is the diary_write payload: whose journal (Agent), the AAAK
// entry text, an optional Topic (defaulting to DefaultDiaryTopic), and an
// optional Wing (defaulting to the agent's own wing).
type DiaryWriteInput struct {
	Agent string
	Entry string
	Topic string
	Wing  string
}

// DiaryWriteResult reports what diary_write filed: the logical entry id (the
// first chunk's id), the normalized agent and topic, the entry's timestamp, how
// many chunks it became, and — only when it chunked — every physical chunk id.
type DiaryWriteResult struct {
	EntryID   string
	Agent     string
	Topic     string
	Timestamp string
	Chunks    int
	ChunkIDs  []string
	// PendingEmbedding is true when the entry is durable but not yet searchable
	// because the embedder could not be reached; the background worker will index
	// it. See AddResult for why this is surfaced rather than swallowed.
	PendingEmbedding bool
}

// WriteDiary files an agent's journal entry. It mirrors the frozen tool: the
// agent name is lowercased (so reads are case-insensitive, #1243), the topic
// defaults to "general", and the wing defaults to the agent's own wing
// (wing_<agent>) unless one is supplied. The entry rides the same chunk -> embed
// -> store machinery as add_drawer, but — unlike add_drawer's content-hashed,
// idempotent ids — each diary id folds in the write timestamp, so journaling the
// *same* reflection twice keeps both entries instead of overwriting one: a
// journal is append-only. (The frozen tool used a non-idempotent add for exactly
// this reason; the timestamp seed makes a same-id collision effectively
// impossible, so reusing the idempotent upsert store path is safe.)
func (s *Service) WriteDiary(ctx context.Context, teamID string, in DiaryWriteInput) (DiaryWriteResult, error) {
	agent, err := SanitizeName(in.Agent, "agent_name")
	if err != nil {
		return DiaryWriteResult{}, err
	}
	agent = strings.ToLower(agent)

	entry, err := SanitizeContent(in.Entry)
	if err != nil {
		return DiaryWriteResult{}, err
	}

	topic := in.Topic
	if strings.TrimSpace(topic) == "" {
		topic = DefaultDiaryTopic
	}
	if topic, err = SanitizeName(topic, "topic"); err != nil {
		return DiaryWriteResult{}, err
	}

	wing := strings.TrimSpace(in.Wing)
	if wing == "" {
		// Default to the agent's own wing. The agent is already sanitized and
		// lowercased; spaces become underscores so the result still satisfies the
		// safe-name pattern (underscores are legal in a name's interior).
		wing = "wing_" + strings.ReplaceAll(agent, " ", "_")
	} else if wing, err = SanitizeName(wing, "wing"); err != nil {
		return DiaryWriteResult{}, err
	}

	// One timestamp per write: it stamps every chunk's FiledAt (so diary_read can
	// order entries newest-first) and seeds the id (so the entry is unique).
	// RFC3339Nano gives enough resolution that two successive writes never collide.
	now := time.Now().UTC()
	filedAt := now.Format(diaryTimeLayout)
	date := now.Format("2006-01-02")
	// seed makes the id unique per write: the timestamp orders entries, the random
	// nonce guarantees uniqueness even if two writes (e.g. on two scaled instances)
	// land on the same nanosecond — without it a same-ns, same-content write would
	// collide and the idempotent store upsert would silently overwrite a prior
	// journal entry. The clean filedAt (no nonce) is what stamps FiledAt for sorting.
	seed := diarySeed(filedAt)

	// SanitizeContent guarantees a non-empty entry, so diaryChunks yields >= 1
	// chunk and drawers[0] below is always present. diaryChunks (not ChunkText)
	// keeps the journal entry verbatim — no overlap, no trim — matching the frozen
	// tool. EntryID is the first chunk's id (our ParentID model makes chunk 0 the
	// canonical, fetchable handle); the frozen tool's logical handle was opaque and
	// un-fetchable, but for the common single-chunk AAAK entry the two coincide.
	chunks := diaryChunks(entry, ChunkSize)
	vectors := s.embedOrDefer(ctx, chunks)

	drawers := make([]Drawer, len(chunks))
	for i, c := range chunks {
		parentID := ""
		if i > 0 {
			parentID = drawers[0].ID
		}
		drawers[i] = Drawer{
			ID:          diaryEntryID(teamID, wing, agent, topic, c.Index, c.Content, seed),
			TeamID:      teamID,
			Wing:        wing,
			Room:        DiaryRoom,
			ChunkIndex:  c.Index,
			Content:     c.Content,
			FiledAt:     filedAt,
			ContentDate: date,
			ParentID:    parentID,
			Agent:       agent,
			Topic:       topic,
		}
	}
	pending := vectors == nil
	if pending {
		if err := s.repo.SaveUnembedded(ctx, drawers); err != nil {
			return DiaryWriteResult{}, fmt.Errorf("save diary entry (embedding deferred): %w", err)
		}
	} else if err := s.storeDrawers(ctx, teamID, drawers, vectors); err != nil {
		return DiaryWriteResult{}, err
	}

	res := DiaryWriteResult{
		PendingEmbedding: pending,
		EntryID:          drawers[0].ID,
		Agent:            agent,
		Topic:            topic,
		Timestamp:        filedAt,
		Chunks:           len(drawers),
	}
	// A single-chunk entry's id is already EntryID; only a chunked entry needs its
	// physical ids enumerated so a caller can fetch each piece by id.
	if len(drawers) > 1 {
		res.ChunkIDs = make([]string, len(drawers))
		for i, d := range drawers {
			res.ChunkIDs[i] = d.ID
		}
	}
	return res, nil
}

// diarySeed combines the write timestamp with a random nonce to seed a diary
// id, so the id is unique even when two writes share a nanosecond. crypto/rand is
// the source; on the near-impossible event it fails, we fall back to the
// timestamp alone rather than block a journal write — at worst reintroducing the
// vanishingly small same-nanosecond collision the nonce exists to remove.
func diarySeed(filedAt string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return filedAt
	}
	return filedAt + "|" + hex.EncodeToString(b[:])
}

// DiaryEntry is one entry diary_read returns: when it was written, its topic, and
// the verbatim text — the read projection of a diary Drawer.
type DiaryEntry struct {
	Date      string `json:"date"`
	Timestamp string `json:"timestamp"`
	Topic     string `json:"topic"`
	Content   string `json:"content"`
}

// DiaryReadResult is the diary_read response: the normalized agent, the page of
// entries (newest first), the total entries in scope, and how many are shown.
type DiaryReadResult struct {
	Agent   string       `json:"agent"`
	Entries []DiaryEntry `json:"entries"`
	Total   int64        `json:"total"`
	Showing int          `json:"showing"`
}

// ReadDiary returns an agent's most recent diary entries, newest first. Like the
// frozen tool it lowercases the agent (case-insensitive reads), clamps lastN to
// [1, MaxDiaryReadN], and treats an empty wing as "every wing this agent has
// journaled in" — hook-written entries land in project wings, so a wingless read
// must still see them. Total is the full count in scope, so a caller can tell its
// journal is larger than the returned window.
func (s *Service) ReadDiary(ctx context.Context, teamID, agent, wing string, lastN int) (DiaryReadResult, error) {
	cleanAgent, err := SanitizeName(agent, "agent_name")
	if err != nil {
		return DiaryReadResult{}, err
	}
	cleanAgent = strings.ToLower(cleanAgent)

	if wing = strings.TrimSpace(wing); wing != "" {
		if wing, err = SanitizeName(wing, "wing"); err != nil {
			return DiaryReadResult{}, err
		}
	}

	if lastN <= 0 {
		lastN = DefaultDiaryReadN
	}
	if lastN > MaxDiaryReadN {
		lastN = MaxDiaryReadN
	}

	rows, err := s.repo.Diary(ctx, teamID, cleanAgent, wing, lastN)
	if err != nil {
		return DiaryReadResult{}, fmt.Errorf("read diary: %w", err)
	}
	total, err := s.repo.DiaryCount(ctx, teamID, cleanAgent, wing)
	if err != nil {
		return DiaryReadResult{}, fmt.Errorf("count diary: %w", err)
	}

	entries := make([]DiaryEntry, len(rows))
	for i, d := range rows {
		entries[i] = DiaryEntry{
			Date:      d.ContentDate,
			Timestamp: d.FiledAt,
			Topic:     d.Topic,
			Content:   d.Content,
		}
	}
	return DiaryReadResult{
		Agent:   cleanAgent,
		Entries: entries,
		Total:   total,
		Showing: len(entries),
	}, nil
}

// distanceFromScore converts a cosine similarity in [-1, 1] into a distance in
// [0, 2] (0 = identical), matching the Python contract's max_distance scale.
func distanceFromScore(score float32) float64 {
	d := 1 - float64(score)
	if d < 0 {
		return 0
	}
	if d > 2 {
		return 2
	}
	return d
}

// short12 trims an id for an error message.
func short12(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// WingIsEmpty reports whether a wing holds no drawers yet.
func (s *Service) WingIsEmpty(ctx context.Context, teamID, wing string) (bool, error) {
	return s.repo.WingIsEmpty(ctx, teamID, wing)
}

// WingNames lists the wings a team has written to.
func (s *Service) WingNames(ctx context.Context, teamID string) ([]string, error) {
	return s.repo.WingNames(ctx, teamID)
}

// InboxCount counts the drawers in one wing's room.
func (s *Service) InboxCount(ctx context.Context, teamID, wing, room string) (int, error) {
	return s.repo.InboxCount(ctx, teamID, wing, room)
}
