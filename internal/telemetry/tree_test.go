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

// TestEveryEmittedAttributeReachesTheDump: an attribute that is computed must be
// readable, or it was paid for and thrown away.
//
// formatAttrs used to iterate a WHITELIST (debugKeys) rather than the span's own
// attributes, so anything not on that list was set, carried, and dropped at
// render. Measured on this tree 2026-08-25: SIXTEEN attributes were in that
// state, and the worst was am.tool — the span is NAMED "am.tool" for every tool,
// so the attribute carrying WHICH tool was the only thing distinguishing them. A
// live dump showed `am.tool 13ms ran` and `am.tool 1520ms ran` for two different
// tools and named neither; a tool span with no children was fully anonymous.
//
// The deeper problem with a whitelist is that it fails silently FORWARD: every
// attribute added after it is dropped until somebody remembers to extend the
// list, and nothing goes red. This test is what makes that impossible, so it
// deliberately uses a key no production code sets — a test that only checked the
// sixteen known names would pass again the moment a seventeenth was added.
func TestEveryEmittedAttributeReachesTheDump(t *testing.T) {
	var buf bytes.Buffer
	exp := newTreeExporter(&buf)
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(exp)))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	ctx := WithProvider(context.Background(), tp)

	_, sp := Start(ctx, StageTool,
		attribute.String("am.tool", "am_get_drawer"),
		attribute.Int("am.withheld", 3),
		// A key nothing in production sets: the point is that the renderer does not
		// need to have heard of an attribute to print it.
		attribute.String("am.not_on_any_list", "visible"),
	)
	sp.End(Ran)

	out := buf.String()
	for _, want := range []string{"tool=am_get_drawer", "withheld=3", "not_on_any_list=visible"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q is absent from the dump — the attribute was computed and discarded at "+
				"render, which is indistinguishable from never having been set:\n%s", want, out)
		}
	}
	// Order still matters: debugKeys places the attributes a reader wants first.
	if i, j := strings.Index(out, "am.tool"), strings.Index(out, "not_on_any_list"); i < 0 || j < i {
		t.Errorf("span name should precede the overflow attributes:\n%s", out)
	}
	// And nothing formatSpan already renders should appear twice.
	if n := strings.Count(out, "ran"); n != 1 {
		t.Errorf("outcome rendered %d times, want once — the overflow is repeating a handled key:\n%s", n, out)
	}
}
