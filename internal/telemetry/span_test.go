package telemetry

import (
	"bytes"
	"context"
	"os"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func recorder(t *testing.T) (context.Context, *tracetest.SpanRecorder) {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	return WithProvider(context.Background(), tp), sr
}

func TestStartRecordsOutcomeAndSearchID(t *testing.T) {
	ctx, sr := recorder(t)
	ctx = WithSearchID(ctx, "search-1")
	_, sp := Start(ctx, StageEmbed, attribute.Int("am.dim", 4))
	sp.Set(attribute.String("am.extra", "yes"))
	sp.End(Ran)

	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(spans))
	}
	s := spans[0]
	if s.Name() != StageEmbed {
		t.Errorf("name = %q, want %q", s.Name(), StageEmbed)
	}
	got := map[string]string{}
	for _, a := range s.Attributes() {
		got[string(a.Key)] = a.Value.Emit()
	}
	if got["am.search_id"] != "search-1" {
		t.Errorf("search_id = %q", got["am.search_id"])
	}
	if got["am.outcome"] != string(Ran) {
		t.Errorf("outcome = %q", got["am.outcome"])
	}
	if got["am.extra"] != "yes" {
		t.Errorf("extra = %q", got["am.extra"])
	}
}

func TestEndIsIdempotentAndNilSafe(t *testing.T) {
	ctx, sr := recorder(t)
	_, sp := Start(ctx, StageRerank)
	sp.End(FailedOpen)
	sp.End(Ran) // must not double-close or flip the outcome
	var none *Span
	none.End(Ran)

	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(spans))
	}
	for _, a := range spans[0].Attributes() {
		if a.Key == "am.outcome" && a.Value.AsString() != string(FailedOpen) {
			t.Errorf("second End mutated outcome to %s", a.Value.AsString())
		}
	}
}

func TestSearchStagesIsTheWiringList(t *testing.T) {
	if len(SearchStages()) < 8 {
		t.Fatal("SearchStages shrank — a removed name is an unreachable stage")
	}
}

func TestStdoutExporterWritesToStderr(t *testing.T) {
	src, err := os.ReadFile("telemetry.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(src, []byte("newTreeExporter(os.Stderr)")) {
		t.Error("stdout exporter does not write to stderr; traces would drown the eval table on stdout")
	}
}

func TestStartRecordsCallSite(t *testing.T) {
	ctx, sr := recorder(t)
	_, sp := Start(ctx, StageEmbed)
	sp.End(Ran)

	got := map[string]string{}
	for _, a := range sr.Ended()[0].Attributes() {
		got[string(a.Key)] = a.Value.Emit()
	}
	if got["am.code.file"] != "internal/telemetry/span_test.go" {
		t.Errorf("call site file = %q, want the test — Caller(2) must skip Start", got["am.code.file"])
	}
	if got["am.code.line"] == "" || got["am.code.line"] == "0" {
		t.Errorf("call site line = %q", got["am.code.line"])
	}
}

func TestEventRecordsInsideStage(t *testing.T) {
	ctx, sr := recorder(t)
	_, sp := Start(ctx, StageRetrieve)
	sp.Event("widen", attribute.Int("am.k", 8), attribute.String("am.stop", ReasonEnough))
	sp.End(Ran)

	evs := sr.Ended()[0].Events()
	if len(evs) != 1 || evs[0].Name != "widen" {
		t.Fatalf("events = %v, want one widen", evs)
	}
	found := false
	for _, a := range evs[0].Attributes {
		if a.Key == "am.stop" && a.Value.AsString() == ReasonEnough {
			found = true
		}
	}
	if !found {
		t.Error("widen event missing am.stop")
	}
}

func TestSearchStagesDoesNotIncludeEval(t *testing.T) {
	for _, name := range SearchStages() {
		if name == StageEvalCase || name == StageEvalArm {
			t.Errorf("%s is an eval wrapper, not a Search stage — putting it in SearchStages would fail every non-eval Search", name)
		}
	}
}
