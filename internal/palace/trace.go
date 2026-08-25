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

// searchAttrs are the parent-span attributes one Search carries. Wing and room
// are booleans, never names: a tenant or project identifier as a metric label
// is the cardinality ADR-025 forbids.
func searchAttrs(s *Service, q SearchQuery, limit int) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("am.profile_id", s.RankingProfile()),
		attribute.Bool("am.has_wing", strings.TrimSpace(q.Wing) != ""),
		attribute.Bool("am.has_room", strings.TrimSpace(q.Room) != ""),
		attribute.Int("am.limit", limit),
	}
}

// endStage records Ran on success and FailedClosed on error. Nil-safe so a
// forgotten Start cannot panic the served path.
func endStage(sp *telemetry.Span, err error, attrs ...attribute.KeyValue) {
	if sp == nil {
		return
	}
	if err != nil {
		sp.End(telemetry.FailedClosed, attrs...)
		return
	}
	sp.End(telemetry.Ran, attrs...)
}
