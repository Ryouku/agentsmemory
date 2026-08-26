package palace

import (
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/internal/telemetry"

	"go.opentelemetry.io/otel/attribute"
)

// fusionModeName is the span attribute for which fusion function Search ran:
// "rrf" or "linear". Rank fusion ignores lexical weights, so the name is the
// thing a collector groups by, not the unused bm25Base.
func (s *Service) fusionModeName() string {
	if s.fusionRRF {
		return "rrf"
	}
	return "linear"
}

// searchAttrs are the parent-span attributes one Search carries. They are the
// join between RankingProfile() and a dumped tree: structured knobs plus the
// profile line, so a bypassed closet with closet_scale=0 is a wiring proof and
// a bypassed closet with closet_scale=1 is a bug. Wing and room are booleans,
// never names: a tenant or project identifier as a metric label is the
// cardinality ADR-025 forbids.
func searchAttrs(s *Service, q SearchQuery, limit int) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("am.profile_id", s.RankingProfile()),
		attribute.Bool("am.has_wing", strings.TrimSpace(q.Wing) != ""),
		attribute.Bool("am.has_room", strings.TrimSpace(q.Room) != ""),
		attribute.Int("am.limit", limit),
		// The RAW request beside the served value. am.limit is post-clamp, so a
		// caller asking for 5000 and one asking for 100 emitted an identical
		// am.limit=100 and the delta — the only thing that says a request was
		// altered — existed nowhere.
		attribute.Int("am.limit_requested", q.Limit),
		// The boundary that removes candidates outright, and the one retrieval knob
		// this set omitted. retrieveStop can already end the widening loop with
		// reason=max_distance, so the trace named the STOP without ever naming the
		// threshold it compared against.
		attribute.Float64("am.max_distance", q.MaxDistance),
		attribute.String("am.fusion", s.fusionModeName()),
		attribute.Float64("am.closet_scale", s.closetBoostScale),
		attribute.Float64("am.recency_band", s.recencyBand),
		attribute.String("am.evidence", s.MemoryEvidenceSelectorName()),
		attribute.Bool("am.rerank_configured", s.rerank != nil),
		attribute.Float64("am.rerank_weight", s.rerankWeight),
		attribute.Int("am.rerank_pool", s.rerankPool),
		// The knob shipped on 2026-08-25 and was not observable until 2026-08-26 —
		// rung 3 missed in the very change that added it. It decides whether the
		// blend preserves the cross-encoder's magnitude or min-max stretches it, so
		// a trace without it cannot say which ordering rule produced the page.
		attribute.String("am.rerank_norm", s.RerankNormName()),
		// The budget the cross-encoder's evidence shares, and how many places may
		// share it. These decide WHAT TEXT is scored, which decides the order as
		// surely as the weight does: the live failure maxMemoryEvidenceRegions
		// fixes presented sixteen 100-rune shards from this same budget, each too
		// small to carry the reasoning that followed its match. Constants today —
		// which is the argument for emitting them, not against it, since a trace
		// taken after someone tunes them would otherwise be silently incomparable
		// with one taken before.
		attribute.Int("am.evidence_chars", ChunkSize),
		attribute.Int("am.evidence_regions_max", maxMemoryEvidenceRegions),
	}
	if backend := s.VectorBackendName(); backend != "" {
		attrs = append(attrs, attribute.String("am.vector_backend", backend))
	}
	if budget := s.RerankBudget(); budget > 0 {
		// The ceiling that decides whether the cross-encoder's order survives at
		// all. Without it a reader sees `am.search.rerank 11427ms failed_open` and
		// cannot tell a slow box from a pool too large for its budget — the trace
		// records how long the call took and, until now, never what it was allowed.
		attrs = append(attrs, attribute.Int64("am.rerank_timeout_ms", budget.Milliseconds()))
	}
	if floor := withRetrieveFloors(0, q.RetrieveK, s.retrieveK); floor > 0 {
		attrs = append(attrs, attribute.Int("am.retrieve_k", floor))
	}
	if s.fusionRRF {
		// Under rrf this constant IS the fusion: fused = sum over arms of
		// 1/(rrfK+rank). It is the only fusion parameter that applies, and the
		// lexical knobs above are inert, so a reader with am.fusion=rrf and no
		// am.rrf_k cannot reconstruct a single fused score.
		attrs = append(attrs, attribute.Int("am.rrf_k", int(rrfK)))
	} else {
		attrs = append(attrs, linearFusionAttrs(s)...)
	}
	return attrs
}

// fusionAttrs is what the fusion span carries: the mode, plus the linear knobs
// only when they are actually consulted. Rank fusion ignores them, so printing
// them there would claim Search used weights it never read.
func (s *Service) fusionAttrs() []attribute.KeyValue {
	attrs := []attribute.KeyValue{attribute.String("am.fusion", s.fusionModeName())}
	if s.fusionRRF {
		return attrs
	}
	return append(attrs, linearFusionAttrs(s)...)
}

func linearFusionAttrs(s *Service) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.Bool("am.bm25_auto", s.bm25Auto),
		attribute.Bool("am.bm25_idf", s.bm25IDF),
		attribute.String("am.lex_norm", s.lexNormName),
		attribute.Float64("am.bm25_base", s.bm25Base),
	}
}

// endStage records Ran on success and FailedClosed on error. Nil-safe so a
// forgotten Start cannot panic the served path.
func endStage(sp *telemetry.Span, err error, attrs ...attribute.KeyValue) {
	if sp == nil {
		return
	}
	if err != nil {
		sp.End(telemetry.FailedClosed, append(attrs, telemetry.AttrReason(telemetry.ReasonError))...)
		return
	}
	sp.End(telemetry.Ran, attrs...)
}
