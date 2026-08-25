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
		attribute.String("am.fusion", s.fusionModeName()),
		attribute.Float64("am.closet_scale", s.closetBoostScale),
		attribute.Float64("am.recency_band", s.recencyBand),
		attribute.String("am.evidence", s.MemoryEvidenceSelectorName()),
		attribute.Bool("am.rerank_configured", s.rerank != nil),
		attribute.Float64("am.rerank_weight", s.rerankWeight),
		attribute.Int("am.rerank_pool", s.rerankPool),
	}
	if !s.fusionRRF {
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
