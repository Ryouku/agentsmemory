package palace

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"golang.org/x/sync/errgroup"
)

// semanticEvidenceBatchSize bounds each request without falling back to one
// network round trip per passage. It matches the largest TEI client batch this
// service will discover; adapters with a lower limit split it further. A pool
// of long memories can produce thousands of windows, which should not become
// one unbounded embedding payload.
const semanticEvidenceBatchSize = 128

// semanticEvidenceConcurrency bounds how many of those batches are in flight at
// once. The embedder is one shared server doing real inference for every tenant,
// so the goal is to stop paying latency serially, not to hand it the whole
// shortlist at once and turn a client-side wait into a server-side queue.
const semanticEvidenceConcurrency = 4

// evidenceEmbedSlots bounds evidence batches in flight PROCESS-WIDE, across every
// concurrent search.
//
// semanticEvidenceConcurrency is per request, which stops one search serialising
// itself and does nothing about C of them: admit() enforces a monthly quota, not
// a rate limit, so ten concurrent searches meant forty batches — 5,120 passages —
// on one shared inference server that is also serving every other tenant. The
// per-request limit made the client feel fast by moving the queue to the server.
//
// Twice the per-request limit rather than equal to it: equal would serialise
// searches behind each other entirely, and the goal is to bound the total, not to
// make concurrency pointless. Past this the extra work would queue on the
// embedder regardless — this just makes the wait visible on our side, where it is
// bounded and cancellable, instead of as unexplained latency on theirs.
var evidenceEmbedSlots = make(chan struct{}, semanticEvidenceConcurrency*2)

// acquireEvidenceSlot takes a process-wide embedding slot, returning ctx.Err() if
// the caller gives up first. Releasing is the caller's job.
func acquireEvidenceSlot(ctx context.Context) error {
	select {
	case evidenceEmbedSlots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func releaseEvidenceSlot() { <-evidenceEmbedSlots }

type semanticEvidenceWindow struct {
	doc        int
	region     Region
	similarity float64
}

// semanticRerankDocuments replaces lexical evidence only after every required
// passage embedding succeeds. Returning an error leaves the caller's whole
// shortlist on one comparable lexical protocol rather than mixing selectors.
func (s *Service) semanticRerankDocuments(
	ctx context.Context,
	evidenceQuery string,
	queryVector []float32,
	survivors []SearchHit,
	ranked []HybridScore,
	lexical []string,
) ([]string, error) {
	docs := append([]string(nil), lexical...)
	var windows []semanticEvidenceWindow
	for doc := range docs {
		hit := survivors[ranked[doc].Index]
		if len([]rune(hit.MemoryContent)) <= ChunkSize {
			continue
		}
		for _, region := range semanticEvidenceWindows(hit.MemoryContent) {
			windows = append(windows, semanticEvidenceWindow{doc: doc, region: region})
		}
	}
	if len(windows) == 0 {
		return docs, nil
	}

	// Batches run CONCURRENTLY. They were serial, and that is where the
	// selector's latency lived rather than in the cross-encoder: a five
	// thousand rune memory yields seventeen windows, so a full shortlist is
	// thousands of passages, and at one round trip after another the reranker
	// had not started yet. Adaptive batching cut the NUMBER of trips and left
	// them end to end, which is why it bought so little.
	//
	// Each goroutine writes only windows[from:to], a disjoint span, so no lock
	// is needed and the selected evidence is byte-identical to the serial
	// version — the A/B comparing selectors stays valid.
	group, ctx := errgroup.WithContext(ctx)
	group.SetLimit(semanticEvidenceConcurrency)
	for from := 0; from < len(windows); from += semanticEvidenceBatchSize {
		to := min(from+semanticEvidenceBatchSize, len(windows))
		group.Go(func() error {
			inputs := make([]string, to-from)
			for i := range inputs {
				inputs[i] = windows[from+i].region.Text
			}
			// Held across the embed call only — see evidenceEmbedSlots.
			if err := acquireEvidenceSlot(ctx); err != nil {
				return err
			}
			vectors, err := s.embed.Embed(ctx, inputs)
			releaseEvidenceSlot()
			if err != nil {
				return fmt.Errorf("embed semantic evidence passages: %w", err)
			}
			if len(vectors) != len(inputs) {
				return fmt.Errorf("embed semantic evidence passages: got %d vectors for %d passages", len(vectors), len(inputs))
			}
			for i, vector := range vectors {
				// A GLOBAL fault stays fatal; a LOCAL one costs one passage.
				//
				// Dimension mismatch is a property of the model or the stored
				// index, not of this passage: every other vector has it too, so
				// failing fast names the real problem instead of silently
				// scoring a whole shortlist at zero.
				if len(vector) != len(queryVector) {
					return fmt.Errorf("embed semantic evidence passages: vector %d has %d dimensions, "+
						"query has %d — the embedding model and the query vector disagree",
						from+i, len(vector), len(queryVector))
				}
				// Anything else cosineSimilarity rejects is a property of THIS
				// passage: a zero-magnitude vector, or a non-finite component.
				// semanticEvidenceWindows genuinely emits whitespace-only windows
				// (a long blank run in a memory yields a whole batch of them), and
				// aborting the errgroup for one of those dropped every document in
				// the shortlist back to lexical evidence — ten documents' semantic
				// ranking lost to one blank passage. It scores 0 and competes
				// honestly instead, which is what an uninformative passage deserves.
				similarity, ok := cosineSimilarity(queryVector, vector)
				if !ok {
					similarity = 0
				}
				windows[from+i].similarity = similarity
			}
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}

	byDocument := make(map[int][]semanticEvidenceWindow)
	for _, window := range windows {
		byDocument[window.doc] = append(byDocument[window.doc], window)
	}
	for doc, candidates := range byDocument {
		selected := selectSemanticEvidence(candidates, evidenceQuery)
		if len(selected) > 0 {
			docs[doc] = evidenceFromRegions(selected)
		}
	}
	return docs, nil
}

// semanticEvidenceWindows covers the reassembled text with coherent overlapping
// passages. It operates after chunk reassembly, so a window may cross an
// original storage boundary without inheriting chunk 0 as a privileged region.
func semanticEvidenceWindows(content string) []Region {
	runes := []rune(content)
	if len(runes) == 0 {
		return nil
	}
	if len(runes) <= DefaultSnippetChars {
		return []Region{{Text: content}}
	}

	step := DefaultSnippetChars - minRegionRunes
	// Bound the passages ONE document may put on the shared embedder.
	//
	// The natural stride yields about one window per 300 runes, so a 100k-rune
	// memory produces ~334 of them and a shortlist of ten produces ~3,340 — to
	// keep four each. That is ~1% utilisation of a server doing real inference
	// for every tenant, and nothing capped it.
	//
	// The cap is one batch per document, which is why it is expressed as
	// semanticEvidenceBatchSize rather than a new number: past it, a single
	// document would need more round trips than the batching was designed to
	// hide. WIDENING the stride rather than truncating the list is the whole
	// point — the windows still span the entire memory, so evidence can still be
	// selected from its end. A truncating cap would quietly make the tail of a
	// long memory unreachable, which is the failure this path exists to avoid.
	//
	// ⚠It changes which passages exist for memories past ~48k runes, and so can
	// change selected evidence for them. Note it if ADR-024's arms are re-run.
	if spread := (len(runes) + semanticEvidenceBatchSize - 1) / semanticEvidenceBatchSize; spread > step {
		step = spread
	}
	windows := make([]Region, 0, (len(runes)+step-1)/step)
	for start := 0; start < len(runes); start += step {
		end := min(start+DefaultSnippetChars, len(runes))
		// A small word-tail extension is worth a few embedding tokens and avoids
		// turning an identifier at the boundary into two meaningless fragments.
		if end < len(runes) {
			if grow := wordTail(runes, end); grow > 0 && grow <= minRegionRunes/2 {
				end += grow
			}
		}
		// Skip a word the previous window already carries in full, so a window
		// begins at a word rather than inside one.
		windowStart := start
		if windowStart > 0 && isWordRune(runes[windowStart-1]) && isWordRune(runes[windowStart]) {
			for windowStart < end && isWordRune(runes[windowStart]) {
				windowStart++
			}
		}
		for windowStart < end && !isWordRune(runes[windowStart]) {
			windowStart++
		}
		// A long unbroken token — a digest, a URL, a base64 blob, all of which
		// this corpus is full of — has no boundary to skip to, so the advance
		// above walks to `end` and the window collapses. Left alone that emits
		// nothing for this step and leaves the run itself uncovered: measured at
		// 87% of a memory reachable, with a 98-rune stub competing for one of
		// only four evidence slots. Fall back to the raw span, which is what a
		// passage inside a long token has to be.
		if end-windowStart < minRegionRunes {
			windowStart = start
		}
		if windowStart < end {
			windows = append(windows, Region{Text: string(runes[windowStart:end]), Start: windowStart})
		}
		if end == len(runes) {
			break
		}
	}
	return windows
}

// selectSemanticEvidence takes the strongest semantic passage first. Later
// slots prefer uncovered literal query clauses, then semantic score, while the
// neighbourhood exclusion prevents overlapping views of one paragraph from
// consuming the whole budget.
func selectSemanticEvidence(candidates []semanticEvidenceWindow, query string) []Region {
	if len(candidates) == 0 {
		return nil
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].similarity != candidates[j].similarity {
			return candidates[i].similarity > candidates[j].similarity
		}
		return candidates[i].region.Start < candidates[j].region.Start
	})

	terms := distinctTerms(tokenize(query))
	picked := []semanticEvidenceWindow{candidates[0]}
	covered := coveredEvidenceTerms(candidates[0].region.Text, terms)
	for len(picked) < maxMemoryEvidenceRegions {
		best := -1
		bestFresh := -1
		for i := range candidates {
			if nearSemanticEvidence(candidates[i], picked) {
				continue
			}
			fresh := 0
			window := strings.ToLower(candidates[i].region.Text)
			for _, term := range terms {
				if !covered[term] && strings.Contains(window, term) {
					fresh++
				}
			}
			// Candidates are already semantic-score first and position second,
			// so retaining the first fresh-term tie preserves deterministic order.
			if fresh > bestFresh {
				best, bestFresh = i, fresh
			}
		}
		if best < 0 {
			break
		}
		chosen := candidates[best]
		picked = append(picked, chosen)
		for term := range coveredEvidenceTerms(chosen.region.Text, terms) {
			covered[term] = true
		}
	}

	regions := make([]Region, len(picked))
	for i := range picked {
		regions[i] = picked[i].region
	}
	sort.SliceStable(regions, func(i, j int) bool { return regions[i].Start < regions[j].Start })
	return regions
}

func coveredEvidenceTerms(content string, terms []string) map[string]bool {
	covered := make(map[string]bool, len(terms))
	lower := strings.ToLower(content)
	for _, term := range terms {
		if strings.Contains(lower, term) {
			covered[term] = true
		}
	}
	return covered
}

func nearSemanticEvidence(candidate semanticEvidenceWindow, picked []semanticEvidenceWindow) bool {
	start := candidate.region.Start
	end := start + len([]rune(candidate.region.Text))
	for _, selected := range picked {
		selectedStart := selected.region.Start
		selectedEnd := selectedStart + len([]rune(selected.region.Text))
		if start < selectedEnd+DefaultSnippetChars && selectedStart < end+DefaultSnippetChars {
			return true
		}
	}
	return false
}

func cosineSimilarity(a, b []float32) (float64, bool) {
	if len(a) == 0 || len(a) != len(b) {
		return 0, false
	}
	var dot, normA, normB float64
	for i := range a {
		av, bv := float64(a[i]), float64(b[i])
		if math.IsNaN(av) || math.IsInf(av, 0) || math.IsNaN(bv) || math.IsInf(bv, 0) {
			return 0, false
		}
		dot += av * bv
		normA += av * av
		normB += bv * bv
	}
	if normA == 0 || normB == 0 {
		return 0, false
	}
	similarity := dot / (math.Sqrt(normA) * math.Sqrt(normB))
	if math.IsNaN(similarity) || math.IsInf(similarity, 0) {
		return 0, false
	}
	return similarity, true
}
