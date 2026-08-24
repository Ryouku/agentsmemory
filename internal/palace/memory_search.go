package palace

import (
	"context"
	"fmt"
	"strings"
)

// maxCandidateWidening caps how far past candidateK the memory arm will widen
// its vector prefix: eight TIMES it, which is three doublings. That is far more
// headroom than a clustered prefix of siblings needs, and it converts an
// unbounded corpus walk into a bounded one when a scope filter and the index
// disagree.
const maxCandidateWidening = 8

// searchCandidates resolves a vector prefix to in-scope drawer rows. The legacy
// arm asks once for a chunk-sized pool. The memory arm widens the same ordered
// prefix until candidateK distinct logical memories survive, or the backend has
// no more results. Without this widening, a long memory can spend every slot on
// siblings before BM25 or the cross-encoder gets a chance to compare anything
// else.
func (s *Service) searchCandidates(ctx context.Context, teamID string, q SearchQuery, vec []float32, candidateK int) ([]SearchHit, int, error) {
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
			return nil, 0, fmt.Errorf("vector search: %w", err)
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
				return nil, 0, fmt.Errorf("load drawer rows: %w", err)
			}
			for id, d := range fetched {
				rows[id] = d
			}
		}

		survivors := make([]SearchHit, 0, len(hits))
		distinct := make(map[string]struct{}, candidateK)
		for _, h := range hits {
			d, ok := rows[h.ID]
			if !ok {
				continue // orphan vector (row deleted) — skip
			}
			// The index filter is an optimization; the durable row remains the
			// authority and prevents a stale index from leaking another scope.
			if !drawerMatchesSearch(d, q) {
				continue
			}
			distance := distanceFromScore(h.Score)
			if q.MaxDistance > 0 && distance > q.MaxDistance {
				continue
			}
			memoryID := memoryOf(d)
			survivors = append(survivors, SearchHit{
				Drawer: d, MemoryID: memoryID, Distance: distance,
			})
			distinct[memoryID] = struct{}{}
		}

		if !s.memoryLevelRanking || len(distinct) >= candidateK || len(hits) < k {
			return survivors, len(hits), nil
		}
		// Search results are closest-first. Once the farthest member of a full
		// prefix is outside the caller's distance boundary, every later member is
		// outside too, so widening cannot add an admissible memory.
		if q.MaxDistance > 0 && len(hits) > 0 && distanceFromScore(hits[len(hits)-1].Score) > q.MaxDistance {
			return survivors, len(hits), nil
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
			return survivors, len(hits), nil
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
// prose: the result consists only of stored content in chunk order.
func reassembleMemory(chunks []Drawer) string {
	if len(chunks) == 0 {
		return ""
	}
	if len(chunks) == 1 {
		return chunks[0].Content
	}
	var b strings.Builder
	b.WriteString(chunks[0].Content)
	for i := 1; i < len(chunks); i++ {
		next := chunks[i].Content
		if storedWithoutOverlap(chunks[i]) {
			b.WriteString(next)
			continue
		}
		current := b.String()
		overlap := exactOverlap(current, next, ChunkOverlap)
		if overlap == 0 && current != "" && next != "" {
			left, right := []rune(current), []rune(next)
			if isWordRune(left[len(left)-1]) && isWordRune(right[0]) {
				b.WriteByte(' ')
			}
		}
		b.WriteString(string([]rune(next)[overlap:]))
	}
	return b.String()
}

func exactOverlap(left, right string, maxRunes int) int {
	lr, rr := []rune(left), []rune(right)
	maxRunes = min(maxRunes, len(lr), len(rr))
	for n := maxRunes; n > 0; n-- {
		if string(lr[len(lr)-n:]) == string(rr[:n]) {
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
