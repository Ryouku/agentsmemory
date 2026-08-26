package palace

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/rerank/tei"
	"github.com/atvirokodosprendimai/agentsmemory/internal/telemetry"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// The production reranker must be able to state its budget, or am.rerank_timeout_ms
// is emitted in tests and absent in production — rung 3, the shape this corpus
// keeps shipping. A behavioural test cannot see this: everything works for a
// reranker that DOES describe itself, and the palace's fake does.
var _ RerankDescriber = (*tei.Client)(nil)

// rerankSpanReason runs one Search against svc and returns the am.search.rerank
// span's status and reason. It reads the CHILD span deliberately: searchSpanAttrs
// reads the parent, where a failed-open rerank leaves no trace at all.
func rerankSpanReason(t *testing.T, svc *Service, team string, q SearchQuery) (status, reason string) {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	if _, err := svc.Search(telemetry.WithProvider(context.Background(), tp), team, q); err != nil {
		t.Fatalf("search: %v", err)
	}
	for _, s := range sr.Ended() {
		if s.Name() != telemetry.StageRerank {
			continue
		}
		attrs := spanAttrs(s)
		return attrs["am.outcome"], attrs["am.reason"]
	}
	t.Fatalf("no %s span was recorded", telemetry.StageRerank)
	return "", ""
}

// TestABlownBudgetIsNotReportedAsAnEndpointError pins the distinction that made
// this worth building. Both cases fail open to the fused order and both used to
// emit `reason=error`, so an operator watching recall quality drop could not
// tell "the pool is too large for the budget" from "the reranker is unwell" —
// opposite fixes, identical span.
//
// It drives the REAL tei client against a real HTTP server, because that is the
// only way the fixture can exhibit the defect. A fake reranker returning
// context.DeadlineExceeded would pass this test green while production reported
// every timeout as an error: tei arms two timeout paths and only one of them
// produces that sentinel.
func TestABlownBudgetIsNotReportedAsAnEndpointError(t *testing.T) {
	const team = "team-budget"

	t.Run("a call that overruns its budget says timeout", func(t *testing.T) {
		slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-time.After(2 * time.Second):
			case <-r.Context().Done():
			}
		}))
		t.Cleanup(slow.Close)

		svc := newTestService(t).WithReranker(tei.New(slow.URL, 50*time.Millisecond), 5).WithRerankWeight(0.5)
		mustAdd(t, svc, team, AddInput{Wing: "w", Room: "r", Content: "a memory about budgets"})

		status, reason := rerankSpanReason(t, svc, team, SearchQuery{Query: "budgets", Limit: 3, MaxDistance: 1.5})
		if status != string(telemetry.FailedOpen) {
			t.Errorf("status = %q, want %q — an overrun must still serve the fused order", status, string(telemetry.FailedOpen))
		}
		if reason != telemetry.ReasonTimeout {
			t.Errorf("reason = %q, want %q. A rerank that ran out of budget is a capacity signal: "+
				"lower the pool or raise the budget. Reported as %q it reads as an outage and sends "+
				"the operator to look at a healthy endpoint.", reason, telemetry.ReasonTimeout, telemetry.ReasonError)
		}
	})

	t.Run("an endpoint that is not there says error, not timeout", func(t *testing.T) {
		// The case a review on 2026-08-26 showed the first version got wrong.
		// http.Client.Timeout covers DNS, connect and TLS, so an unroutable endpoint
		// answers net.Error.Timeout() true and used to be reported as a capacity
		// signal — telling an operator to lower the pool on a reranker that is
		// simply absent. 192.0.2.1 is TEST-NET-1 and routes nowhere by definition.
		//
		// Whether the platform hangs until the budget expires or refuses
		// immediately, the verdict must be the same: no connection was ever
		// obtained, so this is an outage and not a budget problem.
		svc := newTestService(t).WithReranker(tei.New("http://192.0.2.1:9", 300*time.Millisecond), 5).WithRerankWeight(0.5)
		mustAdd(t, svc, team, AddInput{Wing: "w", Room: "r", Content: "a memory about budgets"})

		status, reason := rerankSpanReason(t, svc, team, SearchQuery{Query: "budgets", Limit: 3, MaxDistance: 1.5})
		if status != string(telemetry.FailedOpen) {
			t.Errorf("status = %q, want %q", status, string(telemetry.FailedOpen))
		}
		if reason != telemetry.ReasonError {
			t.Errorf("reason = %q, want %q. The call never reached the model, so its budget was "+
				"not the binding constraint — reporting capacity here sends the operator to tune "+
				"a pool on an endpoint that is not answering at all.", reason, telemetry.ReasonError)
		}
	})

	t.Run("a sick endpoint still says error", func(t *testing.T) {
		sick := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}))
		t.Cleanup(sick.Close)

		svc := newTestService(t).WithReranker(tei.New(sick.URL, 30*time.Second), 5).WithRerankWeight(0.5)
		mustAdd(t, svc, team, AddInput{Wing: "w", Room: "r", Content: "a memory about budgets"})

		status, reason := rerankSpanReason(t, svc, team, SearchQuery{Query: "budgets", Limit: 3, MaxDistance: 1.5})
		if status != string(telemetry.FailedOpen) {
			t.Errorf("status = %q, want %q", status, string(telemetry.FailedOpen))
		}
		if reason != telemetry.ReasonError {
			t.Errorf("reason = %q, want %q — widening the timeout check must not swallow real failures",
				reason, telemetry.ReasonError)
		}
	})
}

// TestTheBudgetInForceIsOnTheSearchSpan: a trace that records how long a rerank
// took, but never what it was allowed, cannot answer the only question that
// matters when recall degrades — was this call close to its ceiling?
func TestTheBudgetInForceIsOnTheSearchSpan(t *testing.T) {
	const team = "team-budget-attr"
	svc := newTestService(t).WithReranker(tei.New("http://127.0.0.1:1", 7*time.Second), 5).WithRerankWeight(0.5)
	mustAdd(t, svc, team, AddInput{Wing: "w", Room: "r", Content: "a memory about budgets"})

	got := searchSpanAttrs(t, svc, team, SearchQuery{Query: "budgets", Limit: 3, MaxDistance: 1.5})
	if got["am.rerank_timeout_ms"] != "7000" {
		t.Errorf("am.rerank_timeout_ms = %q, want %q", got["am.rerank_timeout_ms"], "7000")
	}
}
