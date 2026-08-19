package palace

import (
	"math"
	"regexp"
	"sort"
	"strings"
)

// Hybrid-ranking constants, ported verbatim from the frozen Python searcher
// (_hybrid_rank / _bm25_scores) so the Go SaaS recall ranks candidates exactly as
// the original did. search over-fetches a pool of vector neighbours, then this
// re-ranks them by a convex combination of vector similarity and lexical BM25 —
// vector finds the semantically near, BM25 rewards the literally-matching, and
// the blend beats either alone.
const (
	// bm25K1 is Okapi-BM25 term-frequency saturation (1.2–2.0 typical).
	bm25K1 = 1.5
	// bm25B is Okapi-BM25 length normalization (0=none, 1=full).
	bm25B = 0.75
	// hybridVectorWeight / hybridBM25Weight are the convex-combination weights:
	// 0.6 semantic + 0.4 lexical, matching the frozen default. They sum to 1 so the
	// fused score stays in the same [0,1]-ish range as each normalized term.
	hybridVectorWeight = 0.6
	hybridBM25Weight   = 0.4
	// hybridCandidateMultiplier is how far Search over-fetches beyond the requested
	// page so BM25 has a meaningful pool to re-rank (frozen used n_results*3). A
	// re-rank can only reorder what vector retrieval surfaced, so the pool must be
	// wider than the page or BM25 cannot promote a lexical match the page missed.
	hybridCandidateMultiplier = 3
	// closetDistanceCap is the farthest a closet hit may be (cosine distance) and
	// still lend its source a boost.
	//
	// The frozen Python used 1.5, and porting that number verbatim was a mistake
	// this palace's own eval caught: with bge-m3, UNRELATED text sits at distance
	// 0.60-0.71, so a 1.5 cap admits everything. Measured against a real closet:
	//
	//   0.114  the closet's own text
	//   0.49   a genuinely related question
	//   0.63   an unrelated technical question
	//   0.71   "how do I bake a cake"
	//
	// A cap that a cake recipe clears is not a cap. 0.6 is set just below where
	// unrelated content lands, and closetBoostStrength below fades the boost to
	// zero as a hit approaches it, so there is no cliff at the boundary.
	closetDistanceCap = 0.6
)

// closetRankBoosts is the diminishing boost a closet hit adds to its source's
// drawers by closet rank: the best-matching closet lifts its source most, the
// fifth barely (frozen CLOSET_RANK_BOOSTS). Closets are a ranking SIGNAL, never a
// gate — they only raise scores, never filter — so the boost is added to the
// fused score, scaled by closetBoostStrength.
//
// The scaling is not cosmetic. A flat +0.40 on a fused score that lives in [0,1]
// outranks every other signal combined, so a single mediocre closet match
// promotes its whole source above genuinely better answers. On this palace, with
// exactly one closet filed, that alone dropped recall@1 from 92% to 17%.
var closetRankBoosts = []float64{0.40, 0.25, 0.15, 0.08, 0.04}

// closetBoostStrength scales a closet's boost by how close it actually is: full
// strength at distance 0, fading linearly to nothing at closetDistanceCap.
//
// Rank alone is the wrong scale on its own — "best closet of the five returned"
// says nothing about whether any of them are relevant, and with one closet in the
// palace the best is also the worst.
func closetBoostStrength(distance float64) float64 {
	if distance >= closetDistanceCap {
		return 0
	}
	if distance < 0 {
		return 1
	}
	return (closetDistanceCap - distance) / closetDistanceCap
}

// tokenRE matches the frozen _TOKEN_RE: runs of two or more word characters.
// \w is widened to the Unicode letter/number/underscore classes so non-ASCII
// content tokenizes the same way Python's re.UNICODE \w did.
var tokenRE = regexp.MustCompile(`[\p{L}\p{N}_]{2,}`)

// tokenize lowercases text and returns its word tokens (length >= 2). It tolerates
// empty input (returns nil), since a candidate drawer may carry no usable text.
func tokenize(text string) []string {
	if text == "" {
		return nil
	}
	return tokenRE.FindAllString(strings.ToLower(text), -1)
}

// bm25Scores computes Okapi-BM25 for query against each document, with IDF taken
// over the provided corpus (the candidate set itself). Corpus-relative IDF is the
// right choice for re-ranking: it measures how discriminative each query term is
// *within the candidates*, which is exactly what reorders them. The smoothed
// Lucene/BM25+ IDF — log((N - df + 0.5)/(df + 0.5) + 1) — is always non-negative,
// so a term in every candidate cannot drive a score below zero. Returned scores
// are raw (unbounded) and in docs order; the caller normalizes.
func bm25Scores(query string, docs []string) []float64 {
	n := len(docs)
	scores := make([]float64, n)

	// Query terms are a set: a term repeated in the query still contributes once
	// to df/idf, matching the frozen set(_tokenize(query)).
	queryTerms := map[string]struct{}{}
	for _, t := range tokenize(query) {
		queryTerms[t] = struct{}{}
	}
	if len(queryTerms) == 0 || n == 0 {
		return scores
	}

	tokenized := make([][]string, n)
	totalLen := 0
	for i, d := range docs {
		tokenized[i] = tokenize(d)
		totalLen += len(tokenized[i])
	}
	if totalLen == 0 {
		return scores // every candidate is text-less; nothing to rank lexically
	}
	avgdl := float64(totalLen) / float64(n)

	// Document frequency: how many candidates contain each query term (once each).
	df := make(map[string]int, len(queryTerms))
	for _, toks := range tokenized {
		seen := map[string]struct{}{}
		for _, t := range toks {
			if _, isQuery := queryTerms[t]; isQuery {
				seen[t] = struct{}{}
			}
		}
		for t := range seen {
			df[t]++
		}
	}
	idf := make(map[string]float64, len(queryTerms))
	for t := range queryTerms {
		idf[t] = math.Log((float64(n-df[t])+0.5)/(float64(df[t])+0.5) + 1)
	}

	for i, toks := range tokenized {
		dl := len(toks)
		if dl == 0 {
			continue
		}
		// Term frequency of each query term within this document.
		tf := map[string]int{}
		for _, t := range toks {
			if _, isQuery := queryTerms[t]; isQuery {
				tf[t]++
			}
		}
		var score float64
		for term, freq := range tf {
			f := float64(freq)
			num := f * (bm25K1 + 1)
			den := f + bm25K1*(1-bm25B+bm25B*float64(dl)/avgdl)
			score += idf[term] * num / den
		}
		scores[i] = score
	}
	return scores
}

// vecSimFromDistance maps a cosine distance in [0,2] (0 = identical) to a
// similarity in [0,1] via max(0, 1-d), matching the frozen _distance_to_similarity
// for the cosine metric — the only metric agentsmemory's stores use. Absolute (not
// relative-to-max) so adding or removing a candidate cannot reshuffle the others.
func vecSimFromDistance(distance float64) float64 {
	if s := 1 - distance; s > 0 {
		return s
	}
	return 0
}

// rrfK is the smoothing constant in reciprocal rank fusion, 1/(k+rank). The
// value 60 is the one the original RRF paper used and every implementation since
// has kept; it flattens the difference between the top few ranks so that a
// document ranked 1st by one retriever and 5th by the other beats one ranked 2nd
// and 2nd only slightly, which is the behaviour that makes RRF robust.
const rrfK = 60.0

// rankRRF fuses the vector and BM25 orderings by RANK rather than by score.
//
// It exists because our weighted fusion has a known weakness: BM25 is unbounded
// and cosine similarity is not, so combining them means normalizing two
// distributions that do not share a shape, and the 0.6/0.4 split that governs it
// was inherited rather than measured. RRF sidesteps the problem entirely — it
// never looks at a score, only at position — which is why it needs no tuning and
// why the published comparisons keep finding it competitive with tuned weights.
//
// Whether that holds HERE is exactly what the eval's rrf arm is for; this
// function is the candidate, not the decision.
func rankRRF(query string, docs []string, distances, boosts []float64) []HybridScore {
	n := len(docs)
	out := make([]HybridScore, n)

	// Position of each candidate in the vector ordering (closest first).
	byVector := make([]int, n)
	for i := range byVector {
		byVector[i] = i
	}
	sort.SliceStable(byVector, func(a, b int) bool { return distances[byVector[a]] < distances[byVector[b]] })

	bm25 := bm25Scores(query, docs)
	byLexical := make([]int, n)
	for i := range byLexical {
		byLexical[i] = i
	}
	sort.SliceStable(byLexical, func(a, b int) bool { return bm25[byLexical[a]] > bm25[byLexical[b]] })

	fused := make([]float64, n)
	for rank, idx := range byVector {
		fused[idx] += 1 / (rrfK + float64(rank+1))
	}
	for rank, idx := range byLexical {
		fused[idx] += 1 / (rrfK + float64(rank+1))
	}

	for i := range out {
		boost := 0.0
		if boosts != nil {
			// The closet boost is a score, not a ranking, so it cannot join the
			// fusion as a third list. Scaling it into RRF's range keeps it the
			// nudge it is meant to be: a full-strength closet is worth about one
			// rank position here, not the whole ordering.
			boost = boosts[i] / rrfK
		}
		out[i] = HybridScore{Index: i, Fused: fused[i] + boost, BM25: bm25[i], Boost: boost}
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Fused > out[b].Fused })
	return out
}

// HybridScore is one candidate's fused ranking: its position in the input slice
// plus the component and combined scores, exposed so the search tool can report
// the lexical and closet contributions alongside the final order.
type HybridScore struct {
	Index int     // position in the docs/distances input
	Fused float64 // 0.6*vecSim + 0.4*bm25Norm + closetBoost, higher is better
	BM25  float64 // raw Okapi-BM25 score (pre-normalization)
	Boost float64 // closet boost added to this candidate (0 when none)
	// Rerank is the cross-encoder's raw relevance score for this candidate, set
	// only for the ones a configured reranker actually scored.
	//
	// Its SCALE depends on the backend and must not be assumed. TEI is asked for
	// sigmoid-squashed scores in (0,1); llama.cpp's server returns bare logits,
	// which are routinely negative — a measured absent-query median came back at
	// −3.8. Any threshold read off this number is therefore specific to one
	// backend, model and version, and a value comparable across two deployments
	// does not exist.
	//
	// Zero is used as "not scored" (no reranker wired, the call failed, or this
	// candidate fell outside the pool). That sentinel was justified by the
	// sigmoid range and is only ALMOST safe on a logit backend, where a genuine
	// score can land arbitrarily close to zero — a candidate scoring exactly 0.0
	// reads as unscored. Prefer an explicit signal where the distinction
	// matters; this is documented rather than silently relied upon.
	Rerank float64

	// Blended is the weighted combination of the normalized fused and rerank
	// scores that actually decided this hit's position. It is what sorts the page
	// when a reranker is configured; without one it stays zero and Fused sorts.
	Blended float64
}

// rankHybrid fuses vector similarity, BM25 and an optional closet boost over a
// candidate set and returns the candidates' indices ordered best-first. docs[i]
// is candidate i's verbatim text, distances[i] its cosine distance, and boosts[i]
// a closet rank boost to add to its score (pass nil for no boosts). BM25 is
// min-max normalized within the set so it is commensurable with the [0,1] vector
// similarity before the weighted sum; the closet boost is added on top because it
// is a signal, not a competing term. A stable sort keeps the vector order as the
// tie-breaker when two candidates fuse equal. docs, distances and (when non-nil)
// boosts must be the same length.
func rankHybrid(query string, docs []string, distances, boosts []float64) []HybridScore {
	return rankHybridWeighted(query, docs, distances, boosts, hybridBM25Weight)
}

// rankHybridWeighted is rankHybrid with the lexical weight named, so how much
// BM25 should count can be MEASURED rather than inherited.
//
// The 0.6/0.4 split came from the frozen Python and was never tested on a real
// corpus. It should not be assumed to travel: BM25 helps when a query repeats the
// vocabulary of the memory it wants, and hurts when it does not — a paraphrase, a
// different language, or simply a large corpus where many memories share the
// query's words without answering it. A weight that is right for one palace can
// be actively wrong for another, which is an argument for a knob and an eval, not
// for a better constant.
func rankHybridWeighted(query string, docs []string, distances, boosts []float64, bm25Weight float64) []HybridScore {
	if bm25Weight < 0 {
		bm25Weight = 0
	}
	if bm25Weight > 1 {
		bm25Weight = 1
	}
	vectorWeight := 1 - bm25Weight
	return rankFused(query, docs, distances, boosts, vectorWeight, bm25Weight)
}

// LexicalCoverage reports what share of a query's terms carry usable lexical
// signal against a candidate set: terms that appear in at least one candidate but
// not in all of them.
//
// Both exclusions matter. A term in NO candidate cannot match anything — a
// Lithuanian question against English memories is mostly these, which is why
// lexical fusion measured WORSE than vector alone there. A term in EVERY
// candidate cannot discriminate between them; it is the vocabulary of the corpus
// rather than of the answer.
//
// This is the quantity that actually decides whether BM25 helps, and it is
// knowable per query without any tuning, labels, or corpus statistics gathered in
// advance.
func LexicalCoverage(query string, docs []string) float64 {
	terms := map[string]struct{}{}
	for _, t := range tokenize(query) {
		terms[t] = struct{}{}
	}
	if len(terms) == 0 || len(docs) == 0 {
		return 0
	}
	df := make(map[string]int, len(terms))
	for _, d := range docs {
		seen := map[string]struct{}{}
		for _, t := range tokenize(d) {
			if _, ok := terms[t]; ok {
				seen[t] = struct{}{}
			}
		}
		for t := range seen {
			df[t]++
		}
	}
	informative := 0
	for t := range terms {
		if n := df[t]; n > 0 && n < len(docs) {
			informative++
		}
	}
	return float64(informative) / float64(len(terms))
}

// LexicalCoverageIDF is LexicalCoverage with each term weighted by how much it
// discriminates, instead of counted as one vote.
//
// The binary count has a measured failure mode: a term appearing in N-1 of N
// candidates counts exactly as much as a rare identifier appearing in one, so a
// paraphrase query made of ordinary words reads as lexically informative and the
// adaptive weight stays up precisely when BM25 is noise — the n=40 paraphrase
// eval scored binary-coverage auto worst of every fusion arm. Here a term
// contributes its BM25-style IDF, normalized so a df=1 term is worth 1 and a
// term in every candidate is worth ~0; terms in no candidate still contribute 0
// signal while diluting the denominator, which is what keeps cross-language
// queries at weight ~0.
func LexicalCoverageIDF(query string, docs []string) float64 {
	terms := map[string]struct{}{}
	for _, t := range tokenize(query) {
		terms[t] = struct{}{}
	}
	if len(terms) == 0 || len(docs) == 0 {
		return 0
	}
	df := make(map[string]int, len(terms))
	for _, d := range docs {
		seen := map[string]struct{}{}
		for _, t := range tokenize(d) {
			if _, ok := terms[t]; ok {
				seen[t] = struct{}{}
			}
		}
		for t := range seen {
			df[t]++
		}
	}
	n := float64(len(docs))
	idf := func(d float64) float64 { return math.Log(1 + (n-d+0.5)/(d+0.5)) }
	max := idf(1) // the most a usable term can discriminate: present in one doc
	if max <= 0 { // single-doc pool: no term can discriminate between candidates
		return 0
	}
	sum := 0.0
	for t := range terms {
		if d := df[t]; d > 0 {
			sum += idf(float64(d)) / max
		}
	}
	return sum / float64(len(terms))
}

// adaptiveBM25Weight scales the lexical half by how much lexical signal this
// query actually has against these candidates.
//
// It replaces a constant that cannot be right for everyone: the same palace
// measured BM25 as decisively helpful on questions that keep an identifier and
// decisively harmful on questions asked in another language. Corpus SIZE looked
// like the variable in early runs and is not — the same 114 memories produce both
// verdicts. Coverage is the variable, and it is a per-query property.
func adaptiveBM25Weight(query string, docs []string, base float64) float64 {
	return base * LexicalCoverage(query, docs)
}

// rankHybridAdaptive fuses with the lexical weight chosen per query.
func rankHybridAdaptive(query string, docs []string, distances, boosts []float64, base float64) []HybridScore {
	return rankHybridWeighted(query, docs, distances, boosts, adaptiveBM25Weight(query, docs, base))
}

// rankHybridAdaptiveIDF is rankHybridAdaptive with the IDF-weighted coverage.
// It exists as a separate eval arm rather than a replacement: the binary
// coverage is the shipping default until the IDF variant beats it on a table.
func rankHybridAdaptiveIDF(query string, docs []string, distances, boosts []float64, base float64) []HybridScore {
	return rankHybridWeighted(query, docs, distances, boosts, base*LexicalCoverageIDF(query, docs))
}

// rankFused is the shared implementation.
func rankFused(query string, docs []string, distances, boosts []float64, vectorWeight, bm25Weight float64) []HybridScore {
	raw := bm25Scores(query, docs)
	var maxBM25 float64
	for _, s := range raw {
		if s > maxBM25 {
			maxBM25 = s
		}
	}

	out := make([]HybridScore, len(docs))
	for i := range docs {
		norm := 0.0
		if maxBM25 > 0 {
			norm = raw[i] / maxBM25
		}
		boost := 0.0
		if boosts != nil {
			boost = boosts[i]
		}
		fused := vectorWeight*vecSimFromDistance(distances[i]) + bm25Weight*norm + boost
		out[i] = HybridScore{Index: i, Fused: fused, BM25: raw[i], Boost: boost}
	}
	// Stable so equal-fused candidates keep their incoming (vector) order.
	sort.SliceStable(out, func(a, b int) bool { return out[a].Fused > out[b].Fused })
	return out
}

// DefaultSnippetChars is how much of a hit's content search returns by default.
//
// Recall is paid for in an agent's context window, and the price decides whether
// it gets called twice. A 5-hit page of full 1600-character drawers is ~2,500
// tokens; the same page as snippets is a few hundred, and the agent can pull any
// one of them in full by id. What it must never do is return so little that the
// agent cannot tell whether the memory is the one it wanted — so the window is
// centred on the query's own terms rather than cut from the front.
const DefaultSnippetChars = 400

// Snippet returns the window of content most relevant to query, with an ellipsis
// where text was removed. It returns content unchanged when it already fits.
//
// The window is chosen by term density, not position: the first paragraph of a
// memory is usually its heading, and the sentence that answers the query is
// usually not there.
func Snippet(content, query string, maxChars int) string {
	if maxChars <= 0 {
		maxChars = DefaultSnippetChars
	}
	runes := []rune(content)
	if len(runes) <= maxChars {
		return content
	}

	terms := tokenize(query)
	if len(terms) == 0 {
		return string(runes[:maxChars]) + "…"
	}
	lower := []rune(strings.ToLower(content))

	// Score each candidate window by how many query terms start inside it. A
	// coarse stride keeps this linear-ish on long content while still landing
	// within a sentence of the best match.
	const stride = 40
	best, bestScore := 0, -1
	for start := 0; start+maxChars <= len(runes) || start == 0; start += stride {
		end := start + maxChars
		if end > len(runes) {
			end = len(runes)
		}
		window := string(lower[start:end])
		score := 0
		for _, t := range terms {
			if strings.Contains(window, t) {
				score++
			}
		}
		if score > bestScore {
			best, bestScore = start, score
		}
		if end == len(runes) {
			break
		}
	}

	end := best + maxChars
	if end > len(runes) {
		end = len(runes)
	}
	out := string(runes[best:end])
	if best > 0 {
		out = "…" + out
	}
	if end < len(runes) {
		out += "…"
	}
	return out
}
