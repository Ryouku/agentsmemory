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
	// hybridBM25Weight is the lexical half of the convex combination: 0.4 lexical
	// against 0.6 semantic, matching the frozen default. The two sum to 1 so the
	// fused score stays in the same [0,1]-ish range as each normalized term.
	//
	// Only the lexical half is a constant. The semantic half is DERIVED —
	// rankFused takes a vectorWeight parameter and every caller passes
	// 1 - bm25Weight — so a `hybridVectorWeight = 0.6` constant sat here for a long
	// time with zero references, redundant while bm25Base was 0.4 and quietly wrong
	// the moment an operator set --bm25-weight to anything else.
	hybridBM25Weight = 0.4
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
	scores, _ := bm25ScoresAndCeiling(query, docs)
	return scores
}

// bm25ScoresAndCeiling returns each candidate's raw BM25 score and C, the least
// upper bound on what this query could score against any document at all.
//
// C is a supremum and not a maximum: the per-term factor f·(k1+1)/(f + k1·L)
// rises toward (k1+1) as term frequency grows but never reaches it, and the
// smoothed IDF is strictly positive even for a term in every candidate. So every
// real document scores strictly below C, and the anchored normalisers built on
// it return values strictly below 1 rather than reaching it.
//
// The two are computed together because they share the same smoothed IDF: a
// candidate's score is a sum of idf(t)·tf-saturation over the query terms, and
// that saturation approaches (k1+1) as term frequency grows without bound, so
// C = (k1+1)·Σ idf(t). Document length drops out: the length factor
// L = 1 − b + b·dl/avgdl is bounded below by 1 − b = 0.25 and never zero, so the
// supremum over frequency and length jointly is (k1+1) whatever dl is — which is
// why dl does not appear in the formula. Computing the ceiling anywhere else would mean a second
// copy of the IDF formula, and the identity the anchored normalisers rest on is
// exact only while both use the same one.
//
// C is a property of the query AND the candidate set, not of the query alone:
// the smoothed IDF reads N and df from the page. Anchoring therefore removes the
// dependence on WHICH candidate won, not the dependence on which candidates are
// present.
func bm25ScoresAndCeiling(query string, docs []string) ([]float64, float64) {
	n := len(docs)
	scores := make([]float64, n)

	// Query terms are a set: a term repeated in the query still contributes once
	// to df/idf, matching the frozen set(_tokenize(query)).
	queryTerms := map[string]struct{}{}
	for _, t := range tokenize(query) {
		queryTerms[t] = struct{}{}
	}
	if len(queryTerms) == 0 || n == 0 {
		return scores, 0
	}

	tokenized := make([][]string, n)
	totalLen := 0
	for i, d := range docs {
		tokenized[i] = tokenize(d)
		totalLen += len(tokenized[i])
	}
	if totalLen == 0 {
		// Every candidate is text-less: nothing to rank lexically, and no
		// candidate set to compute df over. The ceiling is reported as 0 rather
		// than as the IDF sum the formula would give, because there is nothing
		// to anchor AGAINST — every raw score is zero, so both the 0 and the
		// formula's value produce the same all-zero lexical term through every
		// normaliser's guard. Reported as 0 so the caller sees "no lexical
		// signal here" rather than a large number describing a page that has
		// none.
		return scores, 0
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
	var idfSum float64
	for t := range queryTerms {
		idf[t] = math.Log((float64(n-df[t])+0.5)/(float64(df[t])+0.5) + 1)
		idfSum += idf[t]
	}
	// The per-term factor f·(k1+1)/(f + k1·(…)) rises toward (k1+1) as f grows,
	// so no candidate can exceed this however often it repeats the query.
	ceiling := (bm25K1 + 1) * idfSum

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
	return scores, ceiling
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
	// Reranked says whether a cross-encoder scored this candidate, which the
	// value alone cannot: zero is a legitimate logit. Callers deciding whether
	// they HAVE a score must read this, not compare Rerank against zero.
	Reranked bool

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
	return rankHybridWeightedNorm(query, docs, distances, boosts, bm25Weight, lexNormPageMax)
}

// rankHybridWeightedNorm is rankHybridWeighted with the lexical normaliser named
// too, so an eval arm can hold the weight fixed and vary only the divisor.
//
// Weight and normaliser are not independent knobs, which is the reason both are
// swept together rather than one being folded into the other: with no boost an
// anchored normaliser at weight w orders a page exactly as page-max does at
// w·a/(1 − w + w·a), so comparing them at the SAME w compares two points on one
// curve. What separates them is everything that breaks the convex blend — an
// additive boost, or a weight that moves per query.
func rankHybridWeightedNorm(query string, docs []string, distances, boosts []float64, bm25Weight float64, norm lexNorm) []HybridScore {
	if bm25Weight < 0 {
		bm25Weight = 0
	}
	if bm25Weight > 1 {
		bm25Weight = 1
	}
	vectorWeight := 1 - bm25Weight
	return rankFused(query, docs, distances, boosts, vectorWeight, bm25Weight, norm)
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
	return rankHybridAdaptiveNorm(query, docs, distances, boosts, base, lexNormPageMax)
}

// rankHybridAdaptiveNorm is rankHybridAdaptive with the normaliser named.
//
// The adaptive arms are where the two normalisers can genuinely diverge without
// a boost in play: the identity that makes anchoring a rescaling of the weight
// holds at a FIXED weight, and here the weight moves per query, so the rescaling
// would have to move with it.
func rankHybridAdaptiveNorm(query string, docs []string, distances, boosts []float64, base float64, norm lexNorm) []HybridScore {
	return rankHybridWeightedNorm(query, docs, distances, boosts, adaptiveBM25Weight(query, docs, base), norm)
}

// rankHybridAdaptiveIDF is rankHybridAdaptive with the IDF-weighted coverage.
// It exists as a separate eval arm rather than a replacement: the binary
// coverage is the shipping default until the IDF variant beats it on a table.
func rankHybridAdaptiveIDF(query string, docs []string, distances, boosts []float64, base float64) []HybridScore {
	return rankHybridAdaptiveIDFNorm(query, docs, distances, boosts, base, lexNormPageMax)
}

// rankHybridAdaptiveIDFNorm is rankHybridAdaptiveIDF with the normaliser named.
func rankHybridAdaptiveIDFNorm(query string, docs []string, distances, boosts []float64, base float64, norm lexNorm) []HybridScore {
	return rankHybridWeightedNorm(query, docs, distances, boosts, base*LexicalCoverageIDF(query, docs), norm)
}

// lexNorm maps a page's raw BM25 scores to the [0,1] lexical term the fusion
// adds, given ceiling — the largest score this query could attain (see
// bm25ScoresAndCeiling). Implementations must return a slice as long as raw, and
// must yield zero rather than NaN when there is no lexical signal to normalise.
//
// It is a named type because the choice of divisor is a ranking decision worth
// measuring, not an implementation detail: page-max and the anchored transforms
// disagree about what a good lexical score IS, and that disagreement is what
// ADR-002 is about.
type lexNorm func(raw []float64, ceiling float64) []float64

// lexNormPageMax divides by the best raw score on the page. It is what this
// code has always done and stays the default.
//
// Its weakness is structural rather than a matter of tuning: the winner scores
// 1.0 by definition, so the lexical term says where a candidate stands relative
// to its neighbours and nothing about whether any of them matched well. A page
// of uniformly poor matches and a page with one excellent match produce the same
// top-candidate contribution.
func lexNormPageMax(raw []float64, _ float64) []float64 {
	out := make([]float64, len(raw))
	var max float64
	for _, s := range raw {
		if s > max {
			max = s
		}
	}
	if max <= 0 {
		return out
	}
	for i, s := range raw {
		out[i] = s / max
	}
	return out
}

// lexNormCeiling divides by C, the score the query could have attained, so a
// weak match reports as weak however weak its neighbours are.
//
// What it buys is winner-independence: which candidate happens to top the page
// no longer moves everyone else's lexical contribution. What it does NOT buy is
// candidate-set independence — N, df, idf and avgdl are all pool quantities, so
// dropping or adding a sibling still moves both raw and C. Anything stronger
// needs corpus-wide term statistics, which this does not have.
//
// Because C is the supremum over all documents and no real candidate reaches it,
// the result is strictly below 1 whenever any query term carries IDF; the
// anchored lexical weight is therefore always smaller than the nominal one.
func lexNormCeiling(raw []float64, ceiling float64) []float64 {
	out := make([]float64, len(raw))
	if ceiling <= 0 {
		return out
	}
	for i, s := range raw {
		out[i] = s / ceiling
	}
	return out
}

// lexNormSaturatingKappa places the half-way point of the saturating transform
// at kappa·C: a candidate scoring that much of the query's ceiling contributes
// 0.5. It is a second free constant, which is exactly what anchoring set out to
// remove, so it stays at one value until an eval says otherwise.
const lexNormSaturatingKappa = 0.5

// lexNormSaturating maps raw to raw/(raw + kappa·C), a soft version of the
// ceiling transform that compresses the top of the range instead of clipping it.
//
// It exists as a separate arm because the two anchored transforms disagree about
// strong matches, not weak ones: the ceiling transform keeps them proportional
// while this one flattens the difference between very good and excellent, on the
// argument that past some point more lexical overlap stops meaning more
// relevance.
func lexNormSaturating(raw []float64, ceiling float64) []float64 {
	out := make([]float64, len(raw))
	if ceiling <= 0 {
		return out
	}
	half := lexNormSaturatingKappa * ceiling
	for i, s := range raw {
		if s <= 0 {
			continue
		}
		out[i] = s / (s + half)
	}
	return out
}

// rankFused is the shared implementation. norm decides how raw BM25 becomes the
// lexical term; pass lexNormPageMax for the shipping behaviour.
func rankFused(query string, docs []string, distances, boosts []float64, vectorWeight, bm25Weight float64, norm lexNorm) []HybridScore {
	raw, ceiling := bm25ScoresAndCeiling(query, docs)
	lexical := norm(raw, ceiling)

	out := make([]HybridScore, len(docs))
	for i := range docs {
		boost := 0.0
		if boosts != nil {
			boost = boosts[i]
		}
		fused := vectorWeight*vecSimFromDistance(distances[i]) + bm25Weight*lexical[i] + boost
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
// SnippetHeadChars is how much of a memory's opening is always kept when the
// snippet window would otherwise start past it.
//
// The first line of a memory is what it IS — the date, the project, the subject.
// Measured 2026-08-21 against real queries: three pages returned a snippet
// beginning mid-sentence somewhere in the middle of a memory, and the agent read
// a fragment with no way to tell what it belonged to. The window is chosen to
// centre on the match, which is right; discarding the identity to do it is not.
const SnippetHeadChars = 120

// SnippetWithHead is Snippet, keeping the memory's opening when the chosen window
// starts past it. isHead says this content is the START of a memory — chunk 0, or
// a memory that was never split — because for any later chunk there is no
// identity at offset zero to preserve.
func SnippetWithHead(content, query string, maxChars int, isHead bool) string {
	if !isHead {
		return Snippet(content, query, maxChars)
	}
	runes := []rune(content)
	if maxChars <= 0 {
		maxChars = DefaultSnippetChars
	}
	if len(runes) <= maxChars {
		return content
	}
	head := SnippetHeadChars
	if head > maxChars/2 {
		head = maxChars / 2 // never let the head crowd out the match itself
	}
	body := Snippet(content, query, maxChars-head)
	if strings.HasPrefix(body, string(runes[:1])) && !strings.HasPrefix(body, "…") {
		return body // the window already starts at the head; nothing to prepend
	}
	return strings.TrimSuffix(string(runes[:head]), " ") + " … " + strings.TrimPrefix(body, "…")
}

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
	// The loop must reach the END of the content. Its first form advanced while
	// start+maxChars <= len(runes), so for a 433-rune memory at a 50-rune window it
	// stopped after 360-410 and NEVER scored the final window — any match in the
	// last maxChars runes was invisible to the chooser, and the snippet fell back
	// to the opening. Measured 2026-08-21: this was the mechanism behind the
	// largest failure mode against real queries, "the right drawer at rank 1 and
	// the answer not in the text", because a memory's conclusions live at its end.
	for start := 0; ; start += stride {
		if start+maxChars > len(runes) {
			start = len(runes) - maxChars // the final window, flush with the end
			if start < 0 {
				start = 0
			}
		}
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
		if end >= len(runes) {
			break
		}
	}

	end := best + maxChars
	if end > len(runes) {
		end = len(runes)
	}

	// Do not end the window INSIDE a matched term. The window is chosen by how
	// many terms start inside it, so a term beginning two characters before the
	// boundary counts as found and is then delivered as a fragment: measured
	// 2026-08-21, a real page returned "…a budget must be shor…" and the sentence
	// the agent needed continued past the cut. Retrieval had already put the right
	// drawer at rank 1; the aperture threw the answer away.
	//
	// Shift the window right so the clipped term completes, bounded by the content
	// and never past the point where the window would start after it.
	if end < len(runes) {
		for _, t := range terms {
			i := strings.Index(strings.ToLower(string(runes[best:end])), t)
			if i < 0 {
				continue
			}
			termEnd := best + len([]rune(string(runes[best:end])[:i])) + len([]rune(t))
			if termEnd > end {
				shift := termEnd - end
				if best+shift+maxChars <= len(runes) {
					best += shift
					end += shift
				} else {
					end = len(runes)
					if end-maxChars > 0 {
						best = end - maxChars
					}
				}
			}
		}
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

// reorderByRecency is a stable tie-break, not a ranker: within a band of fused
// score it prefers the memory with the newer content date, and outside that band
// it changes nothing.
//
// The band is the whole design. A recency prior applied across large score gaps
// promotes a recent irrelevance over an older exact answer, which is a different
// ranking function wearing a tie-break's name. Bounding it to near-ties means the
// arm can only decide cases the fused score already called close.
//
// Absence of a date is not evidence of being old. An undated or unparseable
// candidate is never promoted and never demoted — most memories in a real palace
// carry no content date, and treating "" as very old would push all of them down
// on no evidence.
//
// dates[i] belongs to the candidate at index i, matching docs/distances
// elsewhere in this file. It lives here rather than in Search on purpose: ADR-004
// puts a production recency prior explicitly out of scope, and a helper in the
// ranking file is one import away from being inherited by accident.
func reorderByRecency(page []HybridScore, dates []string, band float64) []HybridScore {
	if band <= 0 || len(page) < 2 {
		return page
	}
	out := make([]HybridScore, len(page))
	copy(out, page)

	dateOf := func(h HybridScore) string {
		if h.Index < 0 || h.Index >= len(dates) {
			return ""
		}
		return findDate(dates[h.Index])
	}

	// A stable bubble over adjacent pairs: swapping only neighbours keeps the
	// reorder inside the band by construction, because a candidate can never
	// overtake one it is not within band of.
	for pass := 0; pass < len(out); pass++ {
		moved := false
		for i := 0; i+1 < len(out); i++ {
			a, b := out[i], out[i+1]
			if a.Fused-b.Fused > band {
				continue // outside the band: the score decides, not the date
			}
			da, db := dateOf(a), dateOf(b)
			if da == "" || db == "" {
				continue // nothing to compare; neither is evidence about the other
			}
			if db > da { // ISO dates compare lexicographically
				out[i], out[i+1] = b, a
				moved = true
			}
		}
		if !moved {
			break
		}
	}
	return out
}

// DefaultLexNorm is the normaliser production has always used. It is named here
// so the default has one spelling rather than being implied by a wrapper.
const DefaultLexNorm = "page-max"

// lexNormByName resolves an operator-facing normaliser name, reporting whether it
// is one this build knows. The names are the ones the eval's tables already
// print, so a row that wins names the value that deploys it.
func lexNormByName(name string) (lexNorm, bool) {
	switch name {
	case DefaultLexNorm:
		return lexNormPageMax, true
	case "ceiling":
		return lexNormCeiling, true
	case "saturating":
		return lexNormSaturating, true
	}
	return nil, false
}

// LexNormNames are the selectable normalisers, for a flag's help text and for the
// gate that keeps that help text honest.
func LexNormNames() []string { return []string{DefaultLexNorm, "ceiling", "saturating"} }
