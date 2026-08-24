package palace

import (
	"context"
	"fmt"
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
)

// maxCandidateWidening caps how far past candidateK the memory arm will widen
// its vector prefix: eight TIMES it, which is three doublings. That is far more
// headroom than a clustered prefix of siblings needs, and it converts an
// unbounded corpus walk into a bounded one when a scope filter and the index
// disagree.
const maxCandidateWidening = 8

// survivorsFrom applies the scope rule to a retrieved prefix: it drops orphan
// vectors, rows outside the wing/room the caller asked for, and rows beyond the
// distance boundary, preserving the index's closest-first order. It also reports
// how many DISTINCT memories survived, which is what the widening loop needs to
// decide whether another round can help.
//
// It exists as one function because two callers need the identical predicate —
// searchCandidates while widening, and rankRetrieved while ranking. Spelling a
// scope rule twice is how one copy quietly goes stale, and a stale copy of THIS
// rule surfaces another wing's memory.
//
// The index filter is an optimization; the durable row remains the authority.
func survivorsFrom(hits []store.Hit, rows map[string]Drawer, q SearchQuery) ([]SearchHit, int) {
	survivors := make([]SearchHit, 0, len(hits))
	distinct := make(map[string]struct{}, len(hits))
	for _, h := range hits {
		d, ok := rows[h.ID]
		if !ok {
			continue // orphan vector (row deleted) — skip
		}
		if !drawerMatchesSearch(d, q) {
			continue
		}
		distance := distanceFromScore(h.Score)
		if q.MaxDistance > 0 && distance > q.MaxDistance {
			continue
		}
		memoryID := memoryOf(d)
		survivors = append(survivors, SearchHit{Drawer: d, MemoryID: memoryID, Distance: distance})
		distinct[memoryID] = struct{}{}
	}
	return survivors, len(distinct)
}

// searchCandidates resolves a vector prefix to the rows behind it. The legacy
// arm asks once for a chunk-sized pool. The memory arm widens the same ordered
// prefix until candidateK distinct logical memories survive, or the backend has
// no more results. Without this widening, a long memory can spend every slot on
// siblings before BM25 or the cross-encoder gets a chance to compare anything
// else.
//
// It returns the RAW hits and their rows rather than the survivors it filtered,
// which looks wasteful and is deliberate: rankRetrieved takes (hits, rows)
// because the eval arms share one retrieved pool across every arm, and an arm
// that retrieved for itself would confound the comparison those arms exist to
// make. Filtering here is for the widening decision only; rankRetrieved rebuilds
// the survivors from survivorsFrom, the same predicate, over an in-memory slice
// bounded by candidateK.
func (s *Service) searchCandidates(ctx context.Context, teamID string, q SearchQuery, vec []float32, candidateK int) ([]store.Hit, map[string]Drawer, error) {
	k := candidateK
	// Rows already resolved by a narrower prefix. Widening re-asks the index for
	// a SUPERSET in the same order, so every row loaded on a previous round is
	// still wanted on this one; refetching them made the doubling cost about
	// twice the final prefix in database work rather than once.
	rows := make(map[string]Drawer)
	// Separate from rows because an orphan vector resolves to no row at all;
	// without this it would be re-queried on every widening round.
	looked := make(map[string]bool)
	for {
		hits, err := s.vectors.Search(ctx, teamID, vec, k, searchFilter(q))
		if err != nil {
			return nil, nil, fmt.Errorf("vector search: %w", err)
		}

		missing := make([]string, 0, len(hits))
		for _, h := range hits {
			if !looked[h.ID] {
				looked[h.ID] = true
				missing = append(missing, h.ID)
			}
		}
		if len(missing) > 0 {
			fetched, err := s.repo.GetMany(ctx, teamID, missing)
			if err != nil {
				return nil, nil, fmt.Errorf("load drawer rows: %w", err)
			}
			for id, d := range fetched {
				rows[id] = d
			}
		}

		// Only the distinct-memory COUNT is needed here; rankRetrieved rebuilds the
		// survivors itself from the same helper. Returning raw hits+rows rather
		// than survivors is what lets rankRetrieved keep the signature the eval
		// arms depend on — they share one retrieved pool across arms, so a
		// per-arm retrieval would confound the comparison they exist to make.
		_, distinct := survivorsFrom(hits, rows, q)

		if !s.memoryLevelRanking || distinct >= candidateK || len(hits) < k {
			return hits, rows, nil
		}
		// Search results are closest-first. Once the farthest member of a full
		// prefix is outside the caller's distance boundary, every later member is
		// outside too, so widening cannot add an admissible memory.
		if q.MaxDistance > 0 && len(hits) > 0 && distanceFromScore(hits[len(hits)-1].Score) > q.MaxDistance {
			return hits, rows, nil
		}
		// Doubling bounds backend round trips logarithmically while preserving the
		// exact prefix ordering, but it needs a stop.
		//
		// Every condition above assumes the index honoured the scope filter. The
		// loop deliberately does not rely on that — the durable row is the
		// authority — and when the two disagree, nothing in the prefix survives
		// filtering, the distinct count stays put, and the widening walks the
		// whole corpus a doubling at a time. This bound is a safety stop for
		// that case, not a tuning knob: at the ceiling the arm simply ranks the
		// memories it did find, which is what it would do at any other point
		// where the backend runs out.
		if k >= candidateK*maxCandidateWidening {
			return hits, rows, nil
		}
		k *= 2
	}
}

func drawerMatchesSearch(d Drawer, q SearchQuery) bool {
	return (q.Wing == "" || d.Wing == q.Wing) && (q.Room == "" || d.Room == q.Room)
}

// collapseCandidatesToMemories turns the treatment's retrieved chunks into one
// scoring document per logical memory. The best vector distance and the number
// of retrieved matching chunks remain explicit signals; lexical and rerank text
// comes from the whole reassembled memory.
func (s *Service) collapseCandidatesToMemories(ctx context.Context, teamID string, q SearchQuery, chunks []SearchHit) ([]SearchHit, error) {
	// One pass, not one pass per root. Rescanning every chunk for each root is
	// quadratic in the candidate pool, and the pool is exactly what the memory
	// arm widens.
	roots := make([]string, 0, len(chunks))
	best := make(map[string]SearchHit, len(chunks))
	matched := make(map[string]int, len(chunks))
	for _, h := range chunks {
		if matched[h.MemoryID] == 0 {
			roots = append(roots, h.MemoryID)
			best[h.MemoryID] = h
		} else if h.Distance < best[h.MemoryID].Distance {
			best[h.MemoryID] = h
		}
		matched[h.MemoryID]++
	}
	byRoot, err := s.repo.MemoryChunksByRoots(ctx, teamID, roots)
	if err != nil {
		return nil, fmt.Errorf("load logical memories: %w", err)
	}

	out := make([]SearchHit, 0, len(roots))
	for _, root := range roots {
		representative := best[root]

		// Filter into a fresh slice rather than over byRoot[root] in place.
		// Reusing that backing array truncates the map entry the caller handed
		// us, which is only safe while nothing else reads it — a property that
		// holds today and would break silently the moment this load is shared.
		memoryChunks := byRoot[root]
		inScope := make([]Drawer, 0, len(memoryChunks))
		for _, d := range memoryChunks {
			if drawerMatchesSearch(d, q) {
				inScope = append(inScope, d)
			}
		}
		representative.MemoryID = root
		representative.MemoryContent = reassembleMemory(inScope)
		if representative.MemoryContent == "" {
			representative.MemoryContent = representative.Drawer.Content
		}
		representative.ChunksMatched = matched[root]
		out = append(out, representative)
	}
	return out, nil
}

// hydrateResultMemories attaches stable identity and whole content after the
// legacy arm has ranked and collapsed chunks. It deliberately happens after
// ranking there, preserving the control's scores while fixing the wire-level
// mismatch where a child hit lost root metadata and sibling evidence.
func (s *Service) hydrateResultMemories(ctx context.Context, teamID string, results []SearchHit) error {
	roots := make([]string, 0, len(results))
	for i := range results {
		results[i].MemoryID = memoryOf(results[i].Drawer)
		if results[i].MemoryContent == "" {
			roots = append(roots, results[i].MemoryID)
		}
	}
	if len(roots) == 0 {
		return nil
	}
	byRoot, err := s.repo.MemoryChunksByRoots(ctx, teamID, roots)
	if err != nil {
		return fmt.Errorf("load result memories: %w", err)
	}
	for i := range results {
		if results[i].MemoryContent != "" {
			continue
		}
		results[i].MemoryContent = reassembleMemory(byRoot[results[i].MemoryID])
		if results[i].MemoryContent == "" {
			results[i].MemoryContent = results[i].Drawer.Content
		}
	}
	return nil
}

// storedWithoutOverlap reports whether a chunk came from the ONE writer that
// does not overlap adjacent chunks: the diary.
//
// The discriminator used to be "has an author", which is wrong and was
// expensive. Mine stamps an author on every drawer it writes — defaulting to
// DefaultMineAgent when the caller supplies none, so it is never empty — while
// mineChunkText overlaps by MineChunkOverlap. Every multi-chunk MINED memory
// therefore took the no-overlap branch and re-emitted its overlap at each
// boundary: measured at +4,477 runes on a 19,390-rune source, with 57 of 260
// paragraphs appearing twice.
//
// Author-without-source is exact: WriteDiary and Mine are the only writers that
// set an author, Mine requires a source (sanitizeSource rejects an empty one),
// and Add sets no author at all. So this admits diary chunks and nothing else,
// including a memory mined into the diary room.
func storedWithoutOverlap(d Drawer) bool {
	return d.Agent != "" && d.SourceFile == ""
}

// reassembleMemory removes the exact overlap chunking added while preserving
// diary chunks, which were stored without any. It never summarizes or invents
// prose: the result is stored content in chunk order.
//
// ONE character is not stored content, and it is named here rather than left for
// a reader to find: when two adjacent chunks do not overlap and the join would
// weld a word to a word, a single SPACE is inserted. Without it "…a wor" and
// "d then…" reassemble into a token that appears in no chunk, which is worse
// than a space that appears in none — a fabricated word can be searched for and
// found, and a reader cannot tell it was fabricated.
//
// evidenceFromRegions is scrupulous about declaring its ellipsis as the one
// non-copied string it emits; this is the same declaration, for the same reason.
func reassembleMemory(chunks []Drawer) string {
	if len(chunks) == 0 {
		return ""
	}
	if len(chunks) == 1 {
		return chunks[0].Content
	}
	var b strings.Builder
	b.WriteString(chunks[0].Content)

	// Carry only the TAIL of what has been written, never the whole prefix.
	//
	// The seam between two chunks can overlap by at most ChunkOverlap runes, so
	// nothing earlier than the last ChunkOverlap runes can ever match — yet the
	// previous shape re-read the entire accumulated text on every chunk
	// (b.String(), then []rune of it, TWICE on the zero-overlap branch). That is
	// quadratic in the memory's length and it is paid per distinct memory in the
	// candidate pool AND once per returned hit, in both arms, on the default read
	// path. Benchmarked before the change: 512k runes cost 105ms and 419MB
	// against 6.6ms and 12.5MB bounded — 4x the input for 15x the allocation.
	tail := tailRunes(chunks[0].Content, ChunkOverlap)
	for i := 1; i < len(chunks); i++ {
		next := chunks[i].Content
		nextRunes := []rune(next)
		if storedWithoutOverlap(chunks[i]) {
			b.WriteString(next)
			tail = appendTail(tail, nextRunes, ChunkOverlap)
			continue
		}
		overlap := exactOverlap(tail, nextRunes, ChunkOverlap)
		if overlap == 0 && len(tail) > 0 && len(nextRunes) > 0 {
			if isWordRune(tail[len(tail)-1]) && isWordRune(nextRunes[0]) {
				b.WriteByte(' ')
				tail = appendTail(tail, []rune{' '}, ChunkOverlap)
			}
		}
		written := nextRunes[overlap:]
		b.WriteString(string(written))
		tail = appendTail(tail, written, ChunkOverlap)
	}
	return b.String()
}

// tailRunes returns the last n runes of s, or all of them when it is shorter.
func tailRunes(s string, n int) []rune {
	r := []rune(s)
	if len(r) > n {
		return append([]rune(nil), r[len(r)-n:]...)
	}
	return r
}

// appendTail extends tail with add and keeps only the last n runes.
//
// It COPIES when it trims rather than resliceing, because a reslice keeps the
// whole grown backing array alive — which would reintroduce, as retained memory,
// exactly the unbounded growth this bookkeeping exists to remove.
func appendTail(tail, add []rune, n int) []rune {
	if len(add) >= n {
		return append([]rune(nil), add[len(add)-n:]...)
	}
	combined := append(tail, add...)
	if len(combined) > n {
		return append([]rune(nil), combined[len(combined)-n:]...)
	}
	return combined
}

// exactOverlap returns how many runes of right's prefix repeat left's suffix, up
// to maxRunes. left is the bounded TAIL of the accumulated text, not the whole of
// it: capping the comparison at maxRunes makes every rune before that unreachable,
// so passing more would change the cost and never the answer.
func exactOverlap(left, right []rune, maxRunes int) int {
	maxRunes = min(maxRunes, len(left), len(right))
	for n := maxRunes; n > 0; n-- {
		if string(left[len(left)-n:]) == string(right[:n]) {
			return n
		}
	}
	return 0
}

// maxMemoryEvidenceRegions keeps each cross-encoder passage at least as large as
// the measured agent-visible snippet size. More, smaller fragments cover extra
// term occurrences but hide the reasoning that follows them — the live failure
// this limit fixes presented sixteen 100-rune shards from a 1600-rune budget.
const maxMemoryEvidenceRegions = ChunkSize / DefaultSnippetChars

// memoryEvidence gives the cross-encoder a few coherent matching regions within
// one existing chunk-sized budget. At most four places share that budget, so
// each selected passage carries the same 400-rune reasoning context as a normal
// search snippet. Region text is verbatim and position ordered; the ellipsis
// only marks omitted distance between those source slices.
func memoryEvidence(content, query, fallback string) string {
	regions := snippetRegions(content, query, ChunkSize, maxMemoryEvidenceRegions, true)
	matched := false
	for _, region := range regions {
		matched = matched || region.Score > 0
	}
	if len(regions) == 0 || !matched {
		runes := []rune(fallback)
		if len(runes) > ChunkSize {
			runes = runes[:ChunkSize]
		}
		return string(runes)
	}
	return evidenceFromRegions(regions)
}

// evidenceFromRegions joins verbatim, source-ordered passages inside the
// existing cross-encoder budget. The ellipsis marks omitted source text; it is
// the only text not copied from the memory itself.
func evidenceFromRegions(regions []Region) string {
	var b strings.Builder
	remaining := ChunkSize
	for i, region := range regions {
		separator := ""
		if i > 0 {
			separator = " … "
		}
		if len([]rune(separator)) >= remaining {
			break
		}
		b.WriteString(separator)
		remaining -= len([]rune(separator))
		runes := []rune(region.Text)
		if len(runes) > remaining {
			runes = runes[:remaining]
		}
		b.WriteString(string(runes))
		remaining -= len(runes)
		if remaining == 0 {
			break
		}
	}
	return b.String()
}

func (h SearchHit) rankingContent(query string, evidence bool) string {
	if h.MemoryContent == "" {
		return h.Drawer.Content
	}
	if evidence {
		return memoryEvidence(h.MemoryContent, query, h.Drawer.Content)
	}
	return h.MemoryContent
}
