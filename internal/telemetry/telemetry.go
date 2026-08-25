// Package telemetry is the OpenTelemetry wiring for agentsmemory.
//
// It exists because SQLite search_events is a page-level relevance log and
// cannot explain which ranking stage ran, which was bypassed, or where latency
// went (ADR-025 §7). This package is the operational half: semantic spans plus
// unsampled feature counters. It must not change a served decision — export
// failure drops observability, never search.
//
// Privacy is load-bearing. Raw queries, memory content and tenant identifiers
// are not metric labels. Span attributes carry stage outcomes, counts, and the
// resolved ranking profile — not the text that was searched.
package telemetry

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"
)

// InstrumentationName is the tracer and meter name every span and counter is
// filed under, so a collector can filter this process from others.
const InstrumentationName = "github.com/atvirokodosprendimai/agentsmemory"

// StdoutEndpoint is the Config.Endpoint value that prints a compact stage tree
// to stderr instead of talking to a collector. It is how an operator watches a
// single request's path — including which file:line started each stage — without
// standing Jaeger up. Metrics stay silent on this path: a 15s metric dump
// drowns the trace a human is reading.
const StdoutEndpoint = "stdout"

// Outcome is the closed vocabulary ADR-025 requires of every semantic stage:
// ran, bypassed, failed_open, or failed_closed. Anything else is a bug in the
// instrumentation, not a new kind of result.
type Outcome string

const (
	// Ran means the stage executed and contributed to the served result.
	Ran Outcome = "ran"
	// Bypassed means the stage was eligible to consider and did not run (weight
	// zero, feature off, nothing to score). Eligible but not selected.
	Bypassed Outcome = "bypassed"
	// FailedOpen means the stage ran, errored, and the request continued on a
	// fallback path. Search must still succeed.
	FailedOpen Outcome = "failed_open"
	// FailedClosed means the stage ran, errored, and the request failed. The
	// served decision did not complete.
	FailedClosed Outcome = "failed_closed"
)

// Bypass/fail reasons. Closed vocabulary so a collector can group "why this
// stage did not contribute" without free-text cardinality. These are the join
// key between RankingProfile() and a dumped tree: a profile that says
// closet-boost=0.00 must produce am.reason=scale_zero, or the wire is a lie.
const (
	// ReasonOff is a generic "the feature is not in force".
	ReasonOff = "off"
	// ReasonScaleZero is closet scale 0 — the prior is configured off.
	ReasonScaleZero = "scale_zero"
	// ReasonWeightZero is rerank weight ≤ 0.
	ReasonWeightZero = "weight_zero"
	// ReasonBandZero is recency band 0.
	ReasonBandZero = "band_zero"
	// ReasonNoReranker means no cross-encoder client is configured.
	ReasonNoReranker = "no_reranker"
	// ReasonSkipSQLite is SkipTelemetry: OTEL still runs, search_events does not.
	ReasonSkipSQLite = "skip_sqlite"
	// ReasonEmpty is nothing to score or hydrate.
	ReasonEmpty = "empty"
	// ReasonLexical is evidence selection staying on the lexical control.
	ReasonLexical = "lexical"
	// ReasonError is an endpoint or storage failure.
	ReasonError = "error"
	// ReasonScoreCount is rerank returning the wrong number of scores.
	ReasonScoreCount = "score_count"
	// ReasonSemanticFailed is semantic evidence falling back to lexical.
	ReasonSemanticFailed = "semantic_failed"
	// ReasonEnough is retrieve stopping because it has candidateK memories.
	ReasonEnough = "enough"
	// ReasonExhausted is retrieve stopping because the backend returned a short page.
	ReasonExhausted = "exhausted"
	// ReasonMaxDistance is retrieve stopping because the prefix is already too far.
	ReasonMaxDistance = "max_distance"
	// ReasonWidenCeiling is retrieve hitting the doubling safety stop.
	ReasonWidenCeiling = "widen_ceiling"
)

// AttrReason records why a stage bypassed or failed-open. Pass it to End.
func AttrReason(reason string) attribute.KeyValue {
	return attribute.String("am.reason", reason)
}

// Semantic stage names. These are the span names a collector groups by, and the
// feature identifiers the unused-feature counters use. A test that Search emits
// these is what keeps a stage from being documented here and unreachable in
// Search — the repository's characteristic defect.
const (
	// StageSearch is the parent span of one Service.Search call.
	StageSearch = "am.search"
	// StageEmbed is query → vector.
	StageEmbed = "am.search.embed"
	// StageRetrieve is the vector prefix (including widening).
	StageRetrieve = "am.search.retrieve"
	// StageHydrate is id → drawer row.
	StageHydrate = "am.search.hydrate"
	// StageCollapse is chunk hits → logical memories before scoring.
	StageCollapse = "am.search.collapse"
	// StageCloset is the closet-prior lookup.
	StageCloset = "am.search.closet"
	// StageFusion is vector + lexical combination (rrf or linear).
	StageFusion = "am.search.fusion"
	// StageRecency is the recency-band reorder.
	StageRecency = "am.search.recency"
	// StageRerank is the cross-encoder pass.
	StageRerank = "am.search.rerank"
	// StageEvidence is lexical vs semantic rerank-document selection.
	StageEvidence = "am.search.evidence"
	// StageRecord is the search_events write.
	StageRecord = "am.search.record"
	// StageTool is one MCP tool invocation.
	StageTool = "am.tool"
	// StageKGQuery is one knowledge-graph query.
	StageKGQuery = "am.kg.query"
	// StageKGAdd is one knowledge-graph write.
	StageKGAdd = "am.kg.add"
	// StageTraverse is a room walk.
	StageTraverse = "am.graph.traverse"
	// StageRecompute is a derived-graph rebuild.
	StageRecompute = "am.graph.recompute"
	// StageAdd is filing a memory.
	StageAdd = "am.drawer.add"
	// StageGet is fetching one drawer.
	StageGet = "am.drawer.get"
	// StageGetMemory is fetching every chunk of a memory.
	StageGetMemory = "am.drawer.get_memory"
	// StageUpdate is an in-place drawer edit.
	StageUpdate = "am.drawer.update"
	// StageDelete is deleting a memory.
	StageDelete = "am.drawer.delete"
	// StageMine is mining a blob into drawers.
	StageMine = "am.mine"
	// StageEvalCase is one eval question scored across every arm.
	StageEvalCase = "am.eval.case"
	// StageEvalArm is one ranking arm of an eval case.
	StageEvalArm = "am.eval.arm"
)

// SearchStages is the set of child stages one Search parent must emit, in the
// order the pipeline actually runs (not the order an older ADR paragraph listed
// them). TestSearchEmitsSemanticStageSpans fails when any name here is absent
// from a real Search, which is the wiring proof ADR-025 asks for.
func SearchStages() []string {
	return []string{
		StageSearch,
		StageEmbed,
		StageRetrieve,
		StageHydrate,
		StageCollapse,
		StageCloset,
		StageFusion,
		StageRecency,
		StageRerank,
		StageRecord,
	}
}

// Config is the operator-facing OpenTelemetry setup. Empty Endpoint leaves the
// global noop provider in place, so an unset flag is silence rather than a
// collector nobody stood up.
type Config struct {
	// Endpoint is empty (off), "stdout" (compact stage tree on stderr), or an OTLP
	// HTTP collector URL such as http://localhost:4318.
	Endpoint string
	// ServiceName is the resource `service.name`. Empty becomes "agentsmemory".
	ServiceName string
}

// Setup installs the process TracerProvider and MeterProvider from Config.
// An empty Endpoint is a successful no-op: the returned shutdown is a no-op
// and the global providers stay as they were (noop unless a test replaced them).
//
// The caller must invoke shutdown on process exit so a BatchSpanProcessor can
// flush. Setup itself must not fail a serve: a collector that refuses is
// instrument-health, not a reason to refuse /mcp.
func Setup(ctx context.Context, cfg Config) (shutdown func(context.Context) error, err error) {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		return func(context.Context) error { return nil }, nil
	}
	name := strings.TrimSpace(cfg.ServiceName)
	if name == "" {
		name = "agentsmemory"
	}
	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(name)),
	)
	if err != nil {
		return nil, fmt.Errorf("otel resource: %w", err)
	}

	var (
		tp *sdktrace.TracerProvider
		mp *metric.MeterProvider
	)
	if strings.EqualFold(endpoint, StdoutEndpoint) {
		tp = sdktrace.NewTracerProvider(
			sdktrace.WithResource(res),
			sdktrace.WithSampler(sdktrace.AlwaysSample()),
			sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(newTreeExporter(os.Stderr))),
		)
	} else {
		traceExp, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(endpoint))
		if err != nil {
			return nil, fmt.Errorf("otel otlp trace exporter: %w", err)
		}
		metricExp, err := otlpmetrichttp.New(ctx, otlpmetrichttp.WithEndpointURL(endpoint))
		if err != nil {
			return nil, fmt.Errorf("otel otlp metric exporter: %w", err)
		}
		tp = sdktrace.NewTracerProvider(
			sdktrace.WithResource(res),
			sdktrace.WithSampler(sdktrace.AlwaysSample()),
			sdktrace.WithBatcher(traceExp, sdktrace.WithBatchTimeout(2*time.Second)),
		)
		mp = metric.NewMeterProvider(
			metric.WithResource(res),
			metric.WithReader(metric.NewPeriodicReader(metricExp, metric.WithInterval(15*time.Second))),
		)
	}

	otel.SetTracerProvider(tp)
	if mp != nil {
		otel.SetMeterProvider(mp)
	}
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return func(ctx context.Context) error {
		var first error
		if mp != nil {
			first = mp.Shutdown(ctx)
		}
		if err := tp.Shutdown(ctx); err != nil && first == nil {
			first = err
		}
		return first
	}, nil
}

// Tracer returns the process tracer. Tests that need recorded spans pass a
// provider through WithProvider on the context instead of mutating the global.
func Tracer(ctx context.Context) trace.Tracer {
	if tp, ok := ctx.Value(providerKey{}).(trace.TracerProvider); ok && tp != nil {
		return tp.Tracer(InstrumentationName)
	}
	return otel.Tracer(InstrumentationName)
}

// providerKey is the context key for a test TracerProvider.
type providerKey struct{}

// searchIDKey is the context key for the search_events id of the current Search.
type searchIDKey struct{}

// WithProvider attaches a TracerProvider to ctx so a test can record spans
// without calling otel.SetTracerProvider, which races against parallel tests.
func WithProvider(ctx context.Context, tp trace.TracerProvider) context.Context {
	return context.WithValue(ctx, providerKey{}, tp)
}

// WithSearchID stashes the search_events id on the context so every child stage
// of one Search carries the same correlation id without each caller threading it.
func WithSearchID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, searchIDKey{}, id)
}

// SearchIDFrom returns the id WithSearchID stored, or empty.
func SearchIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(searchIDKey{}).(string)
	return id
}
