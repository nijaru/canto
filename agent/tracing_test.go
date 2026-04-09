package agent

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/tool"
)

func setupTraceRecorder(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { tp.Shutdown(t.Context()) }) //nolint
	return rec
}

func spanNames(spans []sdktrace.ReadOnlySpan) map[string]int {
	names := make(map[string]int, len(spans))
	for _, span := range spans {
		names[span.Name()]++
	}
	return names
}

func TestStepEmitsContextChatAndToolSpans(t *testing.T) {
	rec := setupTraceRecorder(t)

	call := llm.Call{ID: "c1", Type: "function"}
	call.Function.Name = "echo"
	call.Function.Arguments = `{}`

	p := &mockProvider{
		responses: []*llm.Response{
			{Content: "", Calls: []llm.Call{call}},
		},
	}
	reg := tool.NewRegistry()
	reg.Register(tool.Func("echo", "echoes", nil,
		func(_ context.Context, _ string) (string, error) { return "ok", nil },
	))
	a := New("a", "sys", "m", p, reg)
	s := userSession("s-step-trace", "hi")

	if _, err := a.Step(t.Context(), s); err != nil {
		t.Fatalf("Step: %v", err)
	}

	names := spanNames(rec.Ended())
	for _, name := range []string{"canto.context", "gen_ai.chat", "canto.tool.echo"} {
		if names[name] == 0 {
			t.Fatalf("expected span %q, got %#v", name, names)
		}
	}
}

func TestTurnEmitsSessionAndTurnSpans(t *testing.T) {
	rec := setupTraceRecorder(t)

	p := &mockProvider{
		responses: []*llm.Response{
			{Content: "done"},
		},
	}
	a := New("a", "sys", "m", p, nil)
	s := userSession("s-turn-trace", "hi")

	if _, err := a.Turn(t.Context(), s); err != nil {
		t.Fatalf("Turn: %v", err)
	}

	names := spanNames(rec.Ended())
	for _, name := range []string{"canto.session", "canto.turn", "canto.context", "gen_ai.chat"} {
		if names[name] == 0 {
			t.Fatalf("expected span %q, got %#v", name, names)
		}
	}
}

func TestStreamTurnEmitsSessionAndTurnSpans(t *testing.T) {
	rec := setupTraceRecorder(t)

	p := &streamMockProvider{
		chunks: [][]llm.Chunk{
			{{Content: "streamed"}},
		},
	}
	a := New("a", "sys", "m", p, nil)
	s := userSession("s-stream-trace", "hi")

	if _, err := a.StreamTurn(t.Context(), s, nil); err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}

	names := spanNames(rec.Ended())
	for _, name := range []string{"canto.session", "canto.turn", "canto.context", "gen_ai.chat"} {
		if names[name] == 0 {
			t.Fatalf("expected span %q, got %#v", name, names)
		}
	}
}
