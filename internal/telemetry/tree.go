package telemetry

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// treeExporter prints one compact stage tree per root span to w. It exists
// because the JSON pretty-printer is a dump, not a debug tool: a Search is
// unreadable as 200 objects, and a human comparing RankingProfile() to what
// ran needs file:line, outcome, reason, and the child order in one block.
//
// Children End before their parent, so the exporter buffers by trace id and
// flushes when a root (invalid parent) arrives — at that moment every child
// has already been recorded.
type treeExporter struct {
	w  io.Writer
	mu sync.Mutex
	// byTrace holds ended spans until their root flushes.
	byTrace map[string][]sdktrace.ReadOnlySpan
}

func newTreeExporter(w io.Writer) *treeExporter {
	return &treeExporter{w: w, byTrace: map[string][]sdktrace.ReadOnlySpan{}}
}

// ExportSpans implements sdktrace.SpanExporter.
func (e *treeExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, s := range spans {
		tid := s.SpanContext().TraceID().String()
		e.byTrace[tid] = append(e.byTrace[tid], s)
		if !s.Parent().IsValid() {
			e.flushLocked(tid)
		}
	}
	return nil
}

// Shutdown implements sdktrace.SpanExporter.
func (e *treeExporter) Shutdown(context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	ids := make([]string, 0, len(e.byTrace))
	for tid := range e.byTrace {
		ids = append(ids, tid)
	}
	sort.Strings(ids)
	for _, tid := range ids {
		e.flushLocked(tid)
	}
	return nil
}

func (e *treeExporter) flushLocked(tid string) {
	spans := e.byTrace[tid]
	delete(e.byTrace, tid)
	if len(spans) == 0 {
		return
	}
	children := map[string][]sdktrace.ReadOnlySpan{}
	var roots []sdktrace.ReadOnlySpan
	for _, s := range spans {
		if !s.Parent().IsValid() {
			roots = append(roots, s)
			continue
		}
		pid := s.Parent().SpanID().String()
		children[pid] = append(children[pid], s)
	}
	sort.SliceStable(roots, func(i, j int) bool {
		return roots[i].StartTime().Before(roots[j].StartTime())
	})
	for _, r := range roots {
		writeTree(e.w, r, children, 0)
	}
}

func writeTree(w io.Writer, s sdktrace.ReadOnlySpan, children map[string][]sdktrace.ReadOnlySpan, depth int) {
	fmt.Fprintf(w, "%s%s\n", strings.Repeat("  ", depth), formatSpan(s))
	for _, ev := range s.Events() {
		fmt.Fprintf(w, "%s· %s %s\n", strings.Repeat("  ", depth+1), ev.Name, formatAttrs(attrsMap(ev.Attributes), eventKeys))
	}
	kids := children[s.SpanContext().SpanID().String()]
	sort.SliceStable(kids, func(i, j int) bool {
		return kids[i].StartTime().Before(kids[j].StartTime())
	})
	for _, k := range kids {
		writeTree(w, k, children, depth+1)
	}
}

func formatSpan(s sdktrace.ReadOnlySpan) string {
	ms := s.EndTime().Sub(s.StartTime()).Milliseconds()
	got := attrsMap(s.Attributes())
	parts := []string{s.Name(), fmt.Sprintf("%dms", ms)}
	if o := got["am.outcome"]; o != "" {
		parts = append(parts, o)
	}
	if r := got["am.reason"]; r != "" {
		parts = append(parts, "reason="+r)
	}
	if extra := formatAttrs(got, debugKeys); extra != "" {
		parts = append(parts, extra)
	}
	if file := got["am.code.file"]; file != "" {
		site := file
		if line := got["am.code.line"]; line != "" {
			site += ":" + line
		}
		parts = append(parts, site)
	}
	return strings.Join(parts, "  ")
}

func attrsMap(attrs []attribute.KeyValue) map[string]string {
	got := map[string]string{}
	for _, a := range attrs {
		got[string(a.Key)] = a.Value.Emit()
	}
	return got
}

func formatAttrs(got map[string]string, keys []string) string {
	var parts []string
	for _, k := range keys {
		v := got[k]
		if v == "" {
			continue
		}
		name := strings.TrimPrefix(k, "am.")
		if k == "http.response.status_code" {
			name = "status"
		}
		parts = append(parts, name+"="+v)
	}
	return strings.Join(parts, " ")
}

// debugKeys are the attributes a human comparing code to a run needs on one
// line. Order is the pipeline's, not alphabetical.
var debugKeys = []string{
	"am.profile_id",
	"am.fusion",
	"am.closet_scale",
	"am.rerank_configured",
	"am.rerank_weight",
	"am.rerank_pool",
	"am.recency_band",
	"am.evidence",
	"am.bm25_auto",
	"am.bm25_idf",
	"am.lex_norm",
	"am.arm",
	"am.dim",
	"am.k",
	"am.count",
	"am.rounds",
	"am.weight",
	"am.pool",
	"am.scale",
	"am.band",
	"am.limit",
	"am.search_id",
	"http.response.status_code",
}

var eventKeys = []string{
	"am.k",
	"am.hits",
	"am.distinct",
	"am.stop",
}
