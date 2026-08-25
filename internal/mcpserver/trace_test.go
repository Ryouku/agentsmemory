package mcpserver

import (
	"context"
	"errors"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/telemetry"

	"github.com/mark3labs/mcp-go/mcp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestTraceToolEmitsRanAndFailedClosed(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	ctx := telemetry.WithProvider(context.Background(), tp)

	ok := traceTool("am_status", func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})
	if _, err := ok(ctx, mcp.CallToolRequest{}); err != nil {
		t.Fatal(err)
	}

	boom := traceTool("am_add_drawer", func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultError("role refused"), nil
	})
	res, err := boom(ctx, mcp.CallToolRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if res == nil || !res.IsError {
		t.Fatal("expected IsError result")
	}

	var ran, failed bool
	for _, s := range sr.Ended() {
		if s.Name() != telemetry.StageTool {
			continue
		}
		for _, a := range s.Attributes() {
			if string(a.Key) != "am.outcome" {
				continue
			}
			switch a.Value.AsString() {
			case string(telemetry.Ran):
				ran = true
			case string(telemetry.FailedClosed):
				failed = true
			}
		}
	}
	if !ran {
		t.Error("successful tool call did not record outcome=ran")
	}
	if !failed {
		t.Error("IsError tool call did not record outcome=failed_closed")
	}
}

func TestTraceToolFailedClosedOnHandlerError(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	ctx := telemetry.WithProvider(context.Background(), tp)

	h := traceTool("am_search", func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return nil, errors.New("transport")
	})
	if _, err := h(ctx, mcp.CallToolRequest{}); err == nil {
		t.Fatal("expected handler error")
	}
	found := false
	for _, s := range sr.Ended() {
		for _, a := range s.Attributes() {
			if string(a.Key) == "am.outcome" && a.Value.AsString() == string(telemetry.FailedClosed) {
				found = true
			}
		}
	}
	if !found {
		t.Error("handler error did not record failed_closed")
	}
}
