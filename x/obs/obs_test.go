package obs_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/tool"
	"github.com/nijaru/canto/x/obs"
	xtest "github.com/nijaru/canto/x/testing"
)

func setupTracer(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { tp.Shutdown(context.Background()) }) //nolint
	return rec
}

func TestStartEndTurn(t *testing.T) {
	rec := setupTracer(t)

	ctx, span := obs.StartTurn(context.Background(), "agent1", "sess1", "gpt-4o")
	if ctx == nil {
		t.Fatal("nil ctx")
	}
	obs.EndTurn(span, nil)

	spans := rec.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if spans[0].Name() != "canto.turn" {
		t.Errorf("span name = %q, want canto.turn", spans[0].Name())
	}
}

func TestWrapProvider_RecordsGenAIChatSpan(t *testing.T) {
	rec := setupTracer(t)

	mock := xtest.NewMockProvider("test", xtest.Step{Content: "hello"})
	p := obs.WrapProvider(mock)

	ctx, turnSpan := obs.StartTurn(context.Background(), "a", "s", "m")

	_, err := p.Generate(ctx, &llm.LLMRequest{Model: "m"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	obs.EndTurn(turnSpan, nil)

	var names []string
	for _, s := range rec.Ended() {
		names = append(names, s.Name())
	}
	found := func(name string) bool {
		for _, n := range names {
			if n == name {
				return true
			}
		}
		return false
	}
	if !found("gen_ai.chat") {
		t.Errorf("gen_ai.chat span not recorded; got: %v", names)
	}
	if !found("canto.turn") {
		t.Errorf("canto.turn span not recorded; got: %v", names)
	}
}

func TestWrapTool_RecordsToolSpan(t *testing.T) {
	rec := setupTracer(t)

	inner := tool.Func("my_tool", "desc", nil,
		func(ctx context.Context, args string) (string, error) { return "ok", nil },
	)
	wrapped := obs.WrapTool(inner)

	ctx, span := obs.StartTurn(context.Background(), "a", "s", "m")
	out, err := wrapped.Execute(ctx, "{}")
	obs.EndTurn(span, err)

	if err != nil || out != "ok" {
		t.Fatalf("Execute: out=%q err=%v", out, err)
	}

	var names []string
	for _, s := range rec.Ended() {
		names = append(names, s.Name())
	}
	foundTool := false
	for _, n := range names {
		if n == "canto.tool.my_tool" {
			foundTool = true
		}
	}
	if !foundTool {
		t.Errorf("canto.tool.my_tool span not recorded; got: %v", names)
	}
}
