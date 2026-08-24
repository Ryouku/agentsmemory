package palace

import (
	"context"
	"fmt"
	"strings"
)

// searchCandidates resolves a vector prefix to in-scope drawer rows. The legacy
// arm asks once for a chunk-sized pool. The memory arm widens the same ordered
// prefix until candidateK distinct logical memories survive, or the backend has
// no more results. Without this widening, a long memory can spend every slot on
// siblings before BM25 or the cross-encoder gets a chance to compare anything
// else.
func (s *Service) searchCandidates(ctx context.Context, teamID string, q SearchQuery, vec []float32, candidateK int) ([]SearchHit, int, error) {
	k := candidateK
	for {
		hits, err := s.vectors.Search(ctx, teamID, vec, k, searchFilter(q))
		if err != nil {
			return nil, 0, fmt.Errorf("vector search: %w", err)
		}

		ids := make([]string, len(hits))
		for i, h := range hits {
			ids[i] = h.ID
		}
		rows, err := s.repo.GetMany(ctx, teamID, ids)
		if err != nil {
			return nil, 0, fmt.Errorf("load drawer rows: %w", err)
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
		// exact prefix ordering. The overflow guard is reachable only at the int
		// limit, where no backend can accept a wider request anyway.
		if k > int(^uint(0)>>1)/2 {
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
	roots := make([]string, 0, len(chunks))
	seen := make(map[string]bool, len(chunks))
	for _, h := range chunks {
		if !seen[h.MemoryID] {
			seen[h.MemoryID] = true
			roots = append(roots, h.MemoryID)
		}
	}
	byRoot, err := s.repo.MemoryChunksByRoots(ctx, teamID, roots)
	if err != nil {
		return nil, fmt.Errorf("load logical memories: %w", err)
	}

	out := make([]SearchHit, 0, len(roots))
	for _, root := range roots {
		var representative SearchHit
		matched := 0
		for _, h := range chunks {
			if h.MemoryID != root {
				continue
			}
			if matched == 0 || h.Distance < representative.Distance {
				representative = h
			}
			matched++
		}
		if matched == 0 {
			continue
		}

		memoryChunks := byRoot[root]
		inScope := memoryChunks[:0]
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
		representative.ChunksMatched = matched
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

// reassembleMemory removes ChunkText's exact overlap while preserving diary
// chunks, which were stored without overlap. It never summarizes or invents
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
		if chunks[i].Agent != "" || chunks[i-1].Agent != "" {
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

// memoryEvidence gives the cross-encoder several matching regions within one
// existing chunk-sized budget. Region text is verbatim and position ordered;
// the ellipsis only marks omitted distance between those source slices.
func memoryEvidence(content, query, fallback string) string {
	regions := SnippetRegions(content, query, ChunkSize)
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
