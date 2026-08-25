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
	if !bytes.Contains(src, []byte("WithWriter(os.Stderr)")) {
		t.Error("stdout exporter does not write to stderr; traces would drown the eval table on stdout")
	}
}
