package telemetry

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Span is one semantic stage. End is idempotent so a defer can always fire.
type Span struct {
	// span is the OpenTelemetry span being closed.
	span trace.Span
	// name is the feature identifier counters are keyed by.
	name string
	// ctx is the context Inc uses; it must outlive End.
	ctx context.Context
	// once makes a second End a no-op.
	once sync.Once
	// attrs accumulates Set calls until End.
	attrs []attribute.KeyValue
	// outcome is the last End's vocabulary; stored so a second End cannot flip it.
	outcome Outcome
}

// Start begins a named stage span. When a search_id is on the context it is
// copied onto the span so a collector can join children of one Search without
// each call site repeating it.
func Start(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, *Span) {
	if id := SearchIDFrom(ctx); id != "" {
		attrs = append([]attribute.KeyValue{attribute.String("am.search_id", id)}, attrs...)
	}
	ctx, span := Tracer(ctx).Start(ctx, name, trace.WithAttributes(attrs...))
	Inc(ctx, name, CounterEligible)
	return ctx, &Span{span: span, name: name, ctx: ctx, outcome: Ran}
}

// Set records attributes that become known after the stage starts (hit counts,
// fusion mode, widening rounds). They are applied at End so a panic between
// Set and End still has a closed span; the attributes themselves survive if
// End runs.
func (s *Span) Set(attrs ...attribute.KeyValue) {
	if s == nil {
		return
	}
	s.attrs = append(s.attrs, attrs...)
}

// End records the outcome, increments the matching unsampled counters, and
// closes the span. A second call is a no-op. Nil-safe so a forgotten Start
// cannot panic the served path.
func (s *Span) End(outcome Outcome, attrs ...attribute.KeyValue) {
	if s == nil {
		return
	}
	s.once.Do(func() {
		s.outcome = outcome
		all := append(s.attrs, attrs...)
		all = append(all, attribute.String("am.outcome", string(outcome)))
		s.span.SetAttributes(all...)
		switch outcome {
		case Ran:
			Inc(s.ctx, s.name, CounterSelected)
			Inc(s.ctx, s.name, CounterEffect)
		case Bypassed:
			Inc(s.ctx, s.name, CounterFallback)
		case FailedOpen:
			Inc(s.ctx, s.name, CounterSelected)
			Inc(s.ctx, s.name, CounterFallback)
			s.span.SetStatus(codes.Error, string(FailedOpen))
		case FailedClosed:
			Inc(s.ctx, s.name, CounterSelected)
			s.span.SetStatus(codes.Error, string(FailedClosed))
		}
		s.span.End()
	})
}
