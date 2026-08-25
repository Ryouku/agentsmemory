package telemetry

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Counter names for the unsampled feature counters ADR-025 requires.
// "Unused" is eligible > 0 and selected/effect = 0 over a window — never the
// absence of a sampled span.
const (
	// CounterEligible counts that the feature or stage was in force for this request.
	CounterEligible = "eligible"
	// CounterSelected counts that the stage was chosen to run.
	CounterSelected = "selected"
	// CounterEffect counts that the stage changed the served result.
	CounterEffect = "effect"
	// CounterFallback counts a bypass or a fail-open.
	CounterFallback = "fallback"
)

var (
	// featureOnce creates the counter on first Inc so Setup order cannot
	// matter: a span that ends before Setup still records against the noop meter.
	featureOnce sync.Once
	// featureCounter is the process-wide unsampled am.feature counter.
	featureCounter metric.Int64Counter
)

// feature returns the process am.feature counter, creating it on first use.
func feature() metric.Int64Counter {
	featureOnce.Do(func() {
		c, err := otel.Meter(InstrumentationName).Int64Counter("am.feature",
			metric.WithDescription("Unsampled feature/stage counters: eligible, selected, effect, fallback"),
		)
		if err != nil {
			// A meter that cannot be created must not fail search. The noop
			// counter records nothing; instrument-health is the missing export,
			// not a panic.
			c, _ = otel.Meter(InstrumentationName).Int64Counter("am.feature")
		}
		featureCounter = c
	})
	return featureCounter
}

// Inc adds one to the named unsampled counter for feature. Empty names are
// ignored. Cardinality is bounded by the frozen stage list plus the four
// counter names — never by tenant or query text.
func Inc(ctx context.Context, featureName, counter string) {
	if featureName == "" || counter == "" {
		return
	}
	feature().Add(ctx, 1, metric.WithAttributes(
		attribute.String("am.feature", featureName),
		attribute.String("am.counter", counter),
	))
}
