package telemetry

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestTreeExporterPrintsNestedStagesAndReasons(t *testing.T) {
	var buf bytes.Buffer
	exp := newTreeExporter(&buf)
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(exp)))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	ctx := WithProvider(context.Background(), tp)

	ctx, parent := Start(ctx, StageSearch, attribute.String("am.profile_id", "fusion=rrf"))
	_, closet := Start(ctx, StageCloset, attribute.Float64("am.scale", 0))
	closet.Event("widen", attribute.Int("am.k", 4), attribute.String("am.stop", ReasonEnough))
	closet.End(Bypassed, AttrReason(ReasonScaleZero))
	parent.End(Ran)

	out := buf.String()
	if strings.Contains(out, "{") {
		t.Fatalf("stdout tree looks like JSON; dumps are unreadable:\n%s", out)
	}
	if !strings.Contains(out, StageSearch) {
		t.Fatalf("missing parent:\n%s", out)
	}
	if !strings.Contains(out, "  "+StageCloset) {
		t.Fatalf("closet is not nested under search:\n%s", out)
	}
	if !strings.Contains(out, "bypassed") || !strings.Contains(out, "reason="+ReasonScaleZero) {
		t.Fatalf("bypassed closet missing reason:\n%s", out)
	}
	if !strings.Contains(out, "· widen") || !strings.Contains(out, "stop="+ReasonEnough) {
		t.Fatalf("widen event missing:\n%s", out)
	}
	if !strings.Contains(out, "internal/telemetry/tree_test.go") {
		t.Fatalf("call site missing from tree:\n%s", out)
	}
}
