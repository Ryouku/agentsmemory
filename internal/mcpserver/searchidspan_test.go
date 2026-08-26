package mcpserver

import (
	"context"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/telemetry"

	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// TestGetDrawerSpanCarriesTheSearchIDItWasGiven: the join is observable.
//
// ADR-028 T1 accepts a search_id on am_get_drawer and deliberately does not
// STORE it — the durable recording is a separate task with its own trigger. The
// first version of that decision threw the id away entirely, which shipped a
// signal whose adoption could not be observed: an agent sent the id, the server
// took it, and the trace showed `am.tool ... ran` with nothing tying it to any
// recall. Verified against the running server on 2026-08-25 before this test
// existed.
//
// The trigger for the recording task is "the first week a non-test client sends
// one". Nothing could answer that question. One span attribute can.
func TestGetDrawerSpanCarriesTheSearchIDItWasGiven(t *testing.T) {
	recorded := func(t *testing.T, args map[string]any) map[string]string {
		t.Helper()
		sr := tracetest.NewSpanRecorder()
		tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
		t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

		ctx, span := telemetry.Start(telemetry.WithProvider(context.Background(), tp), telemetry.StageTool)
		annotateSearchID(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{
			Name: "am_get_drawer", Arguments: args,
		}})
		span.End(telemetry.Ran)

		out := map[string]string{}
		for _, s := range sr.Ended() {
			for _, kv := range s.Attributes() {
				out[string(kv.Key)] = kv.Value.Emit()
			}
		}
		return out
	}

	got := recorded(t, map[string]any{"id": "d-1", "search_id": "cbbf4b5f8aacdf20"})
	if got["am.search_id"] != "cbbf4b5f8aacdf20" {
		t.Errorf("the fetch quoted a recall and the span does not say so: am.search_id=%q. "+
			"A search followed by a fetch is the relevance signal this ADR exists to create, and "+
			"an unobservable signal cannot answer its own adoption trigger", got["am.search_id"])
	}

	// Absent and blank must not put an empty attribute on the span: a key that is
	// always present says nothing, and "0 fetches carried an id" must stay
	// distinguishable from "every fetch carried an empty one".
	for name, args := range map[string]map[string]any{
		"absent": {"id": "d-1"},
		"blank":  {"id": "d-1", "search_id": "   "},
	} {
		if _, ok := recorded(t, args)["am.search_id"]; ok {
			t.Errorf("%s search_id still set the attribute; an always-present key cannot measure adoption", name)
		}
	}
}

// TestGetDrawerHandlerReachesTheAnnotation is the rung-2 half, and it exists
// because the rung-1 half was not enough.
//
// The first version of this file called annotateSearchID directly. A mutant that
// deleted the CALL from the handler — leaving the function intact and its unit
// test green — SURVIVED: the component worked and nothing selected it, which is
// the defect this repository is named for, reproduced inside the fix for it.
//
// This drives the registered handler instead. No palace is needed: the
// annotation runs before admit(), so an unauthenticated call still records what
// the caller asked for, which is the behaviour worth having anyway — a refused
// call is exactly when you want the trace to say what was attempted.
func TestGetDrawerHandlerReachesTheAnnotation(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	srv := server.NewMCPServer("test", "0.0.0", server.WithToolCapabilities(true))
	registerAll(&registrar{srv: srv}, Deps{})

	const tool = mcpprotocol.ToolPrefix + "get_drawer"
	st := srv.GetTool(tool)
	if st.Handler == nil {
		t.Fatalf("%s is not registered — this check has stopped checking anything", tool)
	}

	// Drive the PRODUCTION wrapper, not a span this test opened itself. The first
	// version started its own span and handed that context to the handler — which
	// is not how the server runs it, and it passed while the annotation was inert
	// in production because traceTool discarded the context its span lived on. A
	// test that constructs the environment differently from production can only
	// prove things about the test.
	ctx := telemetry.WithProvider(context.Background(), tp)
	if _, err := traceTool(tool, st.Handler)(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: tool, Arguments: map[string]any{"id": "d-1", "search_id": "deadbeefcafe0123"},
	}}); err != nil {
		t.Fatalf("handler returned a transport error: %v", err)
	}

	for _, s := range sr.Ended() {
		for _, kv := range s.Attributes() {
			if string(kv.Key) == "am.search_id" && kv.Value.Emit() == "deadbeefcafe0123" {
				return
			}
		}
	}
	t.Error("the registered am_get_drawer handler did not put the caller's search_id on the span; " +
		"annotateSearchID can be correct and called by nothing, which is a capability that ships unreachable")
}

// TestARejectedSearchIDIsCountedNotDropped is the rejection branch's rung-2
// half. validSearchID being correct is worth nothing if the production wrapper
// never reaches the rejecting path, and a client's malformed id must leave a
// mark: ADR-028 defers on "the first week a non-test client sends one", so an
// id thrown away in silence reads as no adoption rather than as a client bug.
func TestARejectedSearchIDIsCountedNotDropped(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	srv := server.NewMCPServer("test", "0.0.0", server.WithToolCapabilities(true))
	registerAll(&registrar{srv: srv}, Deps{})

	const tool = mcpprotocol.ToolPrefix + "get_drawer"
	st := srv.GetTool(tool)
	if st.Handler == nil {
		t.Fatalf("%s is not registered — this check has stopped checking anything", tool)
	}

	ctx := telemetry.WithProvider(context.Background(), tp)
	if _, err := traceTool(tool, st.Handler)(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{
		// Query text in the search_id field is the leak this check exists for:
		// ADR-025 keeps query text off spans, and this is the one am.* string a
		// caller supplies.
		Name: tool, Arguments: map[string]any{"id": "d-1", "search_id": "how do I configure the reranker"},
	}}); err != nil {
		t.Fatalf("handler returned a transport error: %v", err)
	}

	var rejected bool
	for _, s := range sr.Ended() {
		for _, kv := range s.Attributes() {
			if string(kv.Key) == "am.search_id" {
				t.Errorf("a caller-supplied %q reached the span as am.search_id=%q", "search_id", kv.Value.Emit())
			}
			if string(kv.Key) == "am.search_id_rejected" {
				rejected = true
			}
		}
	}
	if !rejected {
		t.Error("a malformed search_id left no mark at all; an id dropped in silence is " +
			"indistinguishable from a client that never sent one, which is the opposite conclusion")
	}
}
