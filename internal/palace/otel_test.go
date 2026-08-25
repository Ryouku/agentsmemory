package palace

import (
	"context"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/telemetry"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// TestSearchEmitsSemanticStageSpans is the ADR-025 reachability gate: every
// name in telemetry.SearchStages must appear on a real Service.Search. A stage
// documented in the list and missing here is the repository's characteristic
// defect — finished instrumentation that nothing selects.
func TestSearchEmitsSemanticStageSpans(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	ctx := telemetry.WithProvider(context.Background(), tp)

	svc := newTestService(t)
	const team = "team-otel"
	mustAdd(t, svc, team, AddInput{
		Wing: "w", Room: "r", Content: "the otel wiring needle is unique here",
	})
	hits, err := svc.Search(ctx, team, SearchQuery{Query: "otel wiring needle", Limit: 3})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("search returned no hits; the fixture must be recallable")
	}

	got := map[string]bool{}
	var searchID string
	for _, s := range sr.Ended() {
		got[s.Name()] = true
		for _, a := range s.Attributes() {
			if string(a.Key) == "am.search_id" && a.Value.AsString() != "" {
				searchID = a.Value.AsString()
			}
		}
	}
	for _, name := range telemetry.SearchStages() {
		if !got[name] {
			t.Errorf("Search did not emit span %q — stage is documented and unreachable", name)
		}
	}
	if searchID == "" {
		t.Error("no span carried am.search_id; SQLite search_events cannot join this trace")
	}

	byID := map[string]string{}
	for _, s := range sr.Ended() {
		byID[s.SpanContext().SpanID().String()] = s.Name()
	}
	under := map[string]map[string]bool{}
	for _, s := range sr.Ended() {
		if !s.Parent().IsValid() {
			continue
		}
		parentName := byID[s.Parent().SpanID().String()]
		if under[parentName] == nil {
			under[parentName] = map[string]bool{}
		}
		under[parentName][s.Name()] = true
	}
	searchKids := []string{
		telemetry.StageEmbed,
		telemetry.StageRetrieve,
		telemetry.StageCollapse,
		telemetry.StageCloset,
		telemetry.StageFusion,
		telemetry.StageRecency,
		telemetry.StageRerank,
		telemetry.StageRecord,
	}
	for _, name := range searchKids {
		if !under[telemetry.StageSearch][name] {
			t.Errorf("%s is not a child of %s — the tree would dump as a forest of roots", name, telemetry.StageSearch)
		}
	}
	if !under[telemetry.StageRetrieve][telemetry.StageHydrate] {
		t.Errorf("%s is not a child of %s", telemetry.StageHydrate, telemetry.StageRetrieve)
	}
}
