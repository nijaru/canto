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

	_, err := p.Generate(ctx, &llm.Request{Model: "m"})
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

func TestWrapProvider_RecordMessages(t *testing.T) {
	rec := setupTracer(t)

	mock := xtest.NewMockProvider(
		"test",
		xtest.Step{Content: "hello"},
		xtest.Step{Content: "hello"},
	)

	// Test without RecordMessages (default)
	p1 := obs.WrapProvider(mock)
	_, _ = p1.Generate(context.Background(), &llm.Request{
		Model:    "m",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "secret"}},
	})

	// Test with RecordMessages = true
	p2 := obs.WrapProvider(mock, obs.WithRecordMessages(true))
	_, _ = p2.Generate(context.Background(), &llm.Request{
		Model:    "m",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "secret"}},
	})

	spans := rec.Ended()
	if len(spans) != 2 {
		t.Fatalf("expected 2 spans, got %d", len(spans))
	}

	span1 := spans[0]
	span2 := spans[1]

	// Verify span1 does NOT have messages
	for _, attr := range span1.Attributes() {
		if attr.Key == "gen_ai.input.messages" || attr.Key == "gen_ai.output.messages" {
			t.Errorf("default span should not contain messages, found %s", attr.Key)
		}
	}

	// Verify span2 DOES have messages
	foundInput := false
	foundOutput := false
	for _, attr := range span2.Attributes() {
		if attr.Key == "gen_ai.input.messages" {
			foundInput = true
			val := attr.Value.AsString()
			if val == "" {
				t.Errorf("expected gen_ai.input.messages to contain JSON, got empty string")
			}
		}
		if attr.Key == "gen_ai.output.messages" {
			foundOutput = true
			val := attr.Value.AsString()
			if val == "" {
				t.Errorf("expected gen_ai.output.messages to contain JSON, got empty string")
			}
		}
	}

	if !foundInput {
		t.Error("expected gen_ai.input.messages attribute in span with RecordMessages enabled")
	}
	if !foundOutput {
		t.Error("expected gen_ai.output.messages attribute in span with RecordMessages enabled")
	}
}

func TestWrapProvider_StreamRecordMessages(t *testing.T) {
	rec := setupTracer(t)

	mock := xtest.NewMockProvider("test", xtest.Step{
		Chunks: []llm.Chunk{
			{Content: "hel"},
			{Content: "lo"},
			{Usage: &llm.Usage{InputTokens: 10, OutputTokens: 5}},
		},
	})

	p := obs.WrapProvider(mock, obs.WithRecordMessages(true))

	stream, err := p.Stream(context.Background(), &llm.Request{
		Model:    "m",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "secret"}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	for {
		_, ok := stream.Next()
		if !ok {
			break
		}
	}
	stream.Close()

	spans := rec.Ended()

	var streamSpan sdktrace.ReadOnlySpan
	for _, s := range spans {
		if s.Name() == "gen_ai.chat" {
			streamSpan = s
			break
		}
	}
	if streamSpan == nil {
		t.Fatal("gen_ai.chat span not found")
	}

	foundInput := false
	foundOutput := false
	for _, attr := range streamSpan.Attributes() {
		if attr.Key == "gen_ai.input.messages" {
			foundInput = true
		}
		if attr.Key == "gen_ai.output.messages" {
			foundOutput = true
			if attr.Value.AsString() == "" {
				t.Error("gen_ai.output.messages should contain JSON chunks")
			}
		}
	}

	if !foundInput {
		t.Error("expected gen_ai.input.messages in stream span with RecordMessages enabled")
	}
	if !foundOutput {
		t.Error("expected gen_ai.output.messages in stream span with RecordMessages enabled")
	}
}
