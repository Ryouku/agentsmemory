// Package palace holds the core memory-palace domain, ported faithfully from
// the frozen Python mempalace. The metaphor is the data model: a Wing is a
// project, a Room is an aspect inside it, a Drawer is one verbatim memory, and
// Hallways/Tunnels are the links that make the palace navigable.
//
// This file defines the domain types and invariants only. Mining (text ->
// drawers) and hybrid search (vector + BM25 + closet boost) are deliberately
// not implemented in the skeleton — they are the next phase — but the types are
// pinned now so every later package depends on a stable vocabulary, and every
// type carries a tenant (TeamID) because storage is tenant-isolated.
package palace

// Drawer is the atomic memory unit: a single VERBATIM text chunk plus locating
// metadata. The cardinal rule from the Python tool carries over — a drawer is
// never a summary; the exact source text is preserved so recall is lossless.
type Drawer struct {
	// ID is a deterministic hash of (team, wing, room, source, chunkIndex) so
	// re-mining the same source is idempotent rather than duplicative.
	ID string

	// TeamID is the owning tenant; it selects the Qdrant collection.
	TeamID string

	Wing       string // project namespace
	Room       string // aspect within the wing
	SourceFile string // provenance of the chunk
	ChunkIndex int    // position within the source file
	Content    string // verbatim text — the memory itself

	// Entities are the proper nouns extracted from Content; their co-occurrence
	// within a wing is what materialises Hallways.
	Entities []string

	// FiledAt is the RFC3339 ingestion time; ContentDate is the date the memory
	// is *about*, extracted from filename/frontmatter/body/mtime.
	FiledAt     string
	ContentDate string

	// ParentID links the chunks of one oversized add_drawer back to the first
	// chunk, so a multi-chunk write can be recognised as a single logical memory.
	// Empty for single-chunk drawers.
	ParentID string

	// Agent and Topic carry the two extra fields a diary entry needs and a normal
	// drawer leaves empty (migration 00007). Agent is whose journal the entry
	// belongs to — stored lowercased so diary_read is case-insensitive, matching
	// the frozen Python contract (#1243) — and is what diary_read scopes by; Topic
	// is a free tag grouping entries (defaulting to "general"). Keeping them as
	// columns on the same drawer keeps diary on the identical chunk/embed/store
	// machinery as add_drawer rather than forking a parallel store.
	Agent string
	Topic string
}

// Dynamics are the L7 "living connection" fields every hallway and tunnel carries:
// a Hebbian Strength that would grow on co-access, a Stability that resists decay,
// and the bookkeeping for that evolution. They are stored for wire-shape parity
// with the frozen tools; the potentiation/decay that would move them off their
// defaults is a later phase, so for now they stay at their initialized values.
type Dynamics struct {
	Strength      float64 `json:"strength"`
	Stability     float64 `json:"stability"`
	LastActivated string  `json:"last_activated"`
	AccessCount   int     `json:"access_count"`
}

// Hallway is a within-wing link between two entities that co-occur in drawers.
// It is derived (recomputed from drawers), never authored, and unordered: A↔B
// and B↔A are the same hallway, so endpoints are stored sorted for a stable id.
type Hallway struct {
	ID           string
	TeamID       string
	Wing         string
	EntityA      string
	EntityB      string
	CoOccurrence int      // how many drawers mention both
	Rooms        []string // rooms where they met
	Label        string
	CreatedAt    string
	CreatedBy    string // "auto" for derived hallways
	Dynamics
}

// TunnelKind distinguishes a human-authored cross-wing link from one the miner
// generated automatically from a shared topic.
type TunnelKind string

const (
	// TunnelExplicit is a user-created link between two wings/rooms.
	TunnelExplicit TunnelKind = "explicit"
	// TunnelEntity is auto-generated when an entity has hallways in two wings.
	TunnelEntity TunnelKind = "entity"
)

// Endpoint is one side of a Tunnel: a location in the palace, optionally pinned
// to a specific drawer.
type Endpoint struct {
	Wing     string
	Room     string
	DrawerID string // optional
}

// Tunnel links two locations across wings. Explicit tunnels are validated
// against existing rooms; entity tunnels are synthesised from hallways. A tunnel
// is symmetric — its id is a hash of its sorted endpoints — so creating A↔B and
// B↔A resolves to one record.
type Tunnel struct {
	ID        string
	TeamID    string
	Source    Endpoint
	Target    Endpoint
	Label     string
	Kind      TunnelKind
	CreatedAt string
	UpdatedAt string
	Dynamics
}

// SearchResult is one page of recall together with the identity it was recorded
// under.
//
// SearchID is the same value `search_events` holds as that row's primary key,
// so a page and its durable record join on it with no extra state anywhere. It
// is present even when Hits is empty: a recall that found nothing still ran,
// still wrote a row, and is the page an operator most often wants to trace.
type SearchResult struct {
	SearchID string
	Hits     []SearchHit
}

// SearchHit is one ranked result from hybrid search. Score is the fused rank — a
// convex blend of vector similarity and lexical BM25, as the Python searcher did
// (closet boost joins once mining builds closets). BM25 is the raw lexical score
// that fed the blend, surfaced for transparency; Distance is the raw cosine
// distance from the query.
type SearchHit struct {
	Drawer Drawer
	// MemoryID is the stable identity shared by the root and every child chunk.
	// Drawer.ID remains the best matching passage for compatibility; callers that
	// reason about, fetch, or annotate the whole memory use this field instead.
	MemoryID string
	// MemoryContent is the whole logical memory, reconstructed from its stored
	// chunks. Ranking uses it only in the memory-level A/B arm, while the MCP wire
	// uses it in both arms so snippets, regions, identity, and staleness all describe
	// the same unit.
	MemoryContent string
	// ChunksMatched is how many chunks of this memory were in the ranked pool: 1
	// for a memory that was never split, N when N of its chunks matched. It exists
	// because collapsing a page to one hit per memory would otherwise destroy the
	// signal — a memory that matched in four places is stronger evidence than one
	// that matched in one, and a silent collapse throws that away.
	ChunksMatched int
	Score         float64 // fused rank score, higher is better
	BM25          float64 // raw Okapi-BM25 lexical score (pre-normalization)
	ClosetBoost   float64 // closet rank boost folded into Score (0 when none)
	Distance      float64 // raw cosine distance, lower is closer
	// RerankScore is the cross-encoder's relevance for this hit, or 0 when no
	// reranker is configured or it did not score this one. It is reported
	// alongside Score rather than replacing it: the two are not on the same scale
	// (a cross-encoder logit against a fused [0,1] score), and the final order is
	// a blend of both, so an agent reading results can see which signal moved a
	// hit and by how much.
	RerankScore float64
	// Reranked says whether a cross-encoder actually scored this hit. It exists
	// because RerankScore's zero is ambiguous: TEI is asked for sigmoid scores
	// in (0,1), but llama.cpp's server returns bare logits, where zero is a
	// perfectly ordinary value. Anything deciding whether a score is PRESENT —
	// an abstention gate, or the eval calibrating one — must read this.
	Reranked bool
}

// memoryOf returns the id of the MEMORY a drawer belongs to: its parent when it
// is a chunk of a larger one, otherwise itself.
//
// One definition, because there were two. The eval folded hits onto ParentID in
// two places before scoring, so it measured memories, while Search returned
// chunks — and a page of ten could hold as few as six distinct memories while the
// eval reported the gold at rank 1. An eval cannot report a regression it does
// not measure the unit of, and the unit was written down twice in the harness and
// nowhere in the pipeline.
func memoryOf(d Drawer) string {
	if d.ParentID != "" {
		return d.ParentID
	}
	return d.ID
}
