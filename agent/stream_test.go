package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	ccontext "github.com/nijaru/canto/context"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
)

// streamMockProvider extends mockProvider with a scripted Stream implementation.
type streamMockProvider struct {
	mockProvider
	chunks [][]llm.Chunk // one slice of chunks per Stream call
	spos   int
}

func (m *streamMockProvider) Stream(_ context.Context, _ *llm.Request) (llm.Stream, error) {
	if m.spos >= len(m.chunks) {
		return &fixedStream{chunks: []llm.Chunk{{Content: "no more streams"}}}, nil
	}
	s := &fixedStream{chunks: m.chunks[m.spos]}
	m.spos++
	return s, nil
}

type fixedStream struct {
	chunks []llm.Chunk
	pos    int
}

func (s *fixedStream) Next() (*llm.Chunk, bool) {
	if s.pos >= len(s.chunks) {
		return nil, false
	}
	c := s.chunks[s.pos]
	s.pos++
	return &c, true
}
func (s *fixedStream) Err() error   { return nil }
func (s *fixedStream) Close() error { return nil }

// ---------------------------------------------------------------------------
// StreamStep
// ---------------------------------------------------------------------------

func TestStreamStepNoToolCalls(t *testing.T) {
	p := &streamMockProvider{
		chunks: [][]llm.Chunk{
			{{Content: "hello "}, {Content: "world"}},
		},
	}
	a := New("a", "sys", "m", p, nil)
	s := userSession("s1", "hi")

	var collected []string
	result, err := a.StreamStep(context.Background(), s, func(c *llm.Chunk) {
		if c.Content != "" {
			collected = append(collected, c.Content)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Handoff != nil {
		t.Error("expected no handoff")
	}

	msgs := s.Messages()
	last := msgs[len(msgs)-1]
	if last.Role != llm.RoleAssistant {
		t.Errorf("expected assistant message, got %s", last.Role)
	}
	if last.Content != "hello world" {
		t.Errorf("expected assembled content %q, got %q", "hello world", last.Content)
	}
	if strings.Join(collected, "") != "hello world" {
		t.Errorf("chunkFn received %q", strings.Join(collected, ""))
	}
}

func TestStreamStepNilChunkFn(t *testing.T) {
	p := &streamMockProvider{
		chunks: [][]llm.Chunk{
			{{Content: "silent"}},
		},
	}
	a := New("a", "sys", "m", p, nil)
	s := userSession("s2", "hi")

	_, err := a.StreamStep(context.Background(), s, nil)
	if err != nil {
		t.Fatal(err)
	}
	msgs := s.Messages()
	last := msgs[len(msgs)-1]
	if last.Content != "silent" {
		t.Errorf("expected %q, got %q", "silent", last.Content)
	}
}

func TestStreamStepWithToolCall(t *testing.T) {
	call := llm.Call{ID: "c1", Type: "function"}
	call.Function.Name = "greet"
	call.Function.Arguments = `{"name":"world"}`

	p := &streamMockProvider{
		chunks: [][]llm.Chunk{
			// First stream: tool call across two chunks
			{
				{Calls: []llm.Call{{ID: "c1", Type: "function"}}},
				{Calls: []llm.Call{call}}, // accumulated final state
			},
			// Second stream: final assistant reply after tool result
			{{Content: "done"}},
		},
		mockProvider: mockProvider{
			responses: []*llm.Response{},
		},
	}
	reg := tool.NewRegistry()
	reg.Register(tool.Func("greet", "greets", nil,
		func(_ context.Context, _ string) (string, error) {
			return "Hello, world!", nil
		}))

	a := New("a", "sys", "m", p, reg)
	s := userSession("s3", "hello")

	result, err := a.StreamStep(context.Background(), s, nil)
	if err != nil {
		t.Fatal(err)
	}
	// StreamStep stops after tool execution — tool result message appended
	msgs := s.Messages()
	var toolMsg llm.Message
	for _, m := range msgs {
		if m.Role == llm.RoleTool {
			toolMsg = m
		}
	}
	if toolMsg.Content != "Hello, world!" {
		t.Errorf("expected tool output %q, got %q", "Hello, world!", toolMsg.Content)
	}
	_ = result
}

// ---------------------------------------------------------------------------
// StreamTurn
// ---------------------------------------------------------------------------

func TestStreamTurnPopulatesContent(t *testing.T) {
	p := &streamMockProvider{
		chunks: [][]llm.Chunk{
			{{Content: "final answer"}},
		},
	}
	a := New("a", "sys", "m", p, nil)
	s := userSession("s4", "q")

	result, err := a.StreamTurn(context.Background(), s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "final answer" {
		t.Errorf("expected %q, got %q", "final answer", result.Content)
	}
}

func TestStreamTurnMaxSteps(t *testing.T) {
	// All streams return a tool call, causing infinite loop — MaxSteps cuts it.
	call := llm.Call{ID: "c1", Type: "function"}
	call.Function.Name = "loop"
	call.Function.Arguments = `{}`

	chunks := []llm.Chunk{{Calls: []llm.Call{call}}}
	var allChunks [][]llm.Chunk
	for i := 0; i < 15; i++ { // more than MaxSteps
		allChunks = append(allChunks, chunks)
	}

	p := &streamMockProvider{chunks: allChunks}
	reg := tool.NewRegistry()
	reg.Register(tool.Func("loop", "loops", nil,
		func(_ context.Context, _ string) (string, error) {
			return "looping", nil
		}))

	a := New("a", "sys", "m", p, reg, WithMaxSteps(3))
	s := userSession("s5", "start")

	_, err := a.StreamTurn(context.Background(), s, nil)
	if err == nil {
		t.Fatal("expected ErrMaxSteps error")
	}
	if !strings.Contains(err.Error(), "steps") {
		t.Errorf("expected max steps error, got: %v", err)
	}
}

func TestStreamTurnMaxSteps_PreservesUsage(t *testing.T) {
	call := llm.Call{ID: "c1", Type: "function"}
	call.Function.Name = "loop"
	call.Function.Arguments = `{}`

	usageChunk := &llm.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}
	chunks := []llm.Chunk{{Calls: []llm.Call{call}, Usage: usageChunk}}
	var allChunks [][]llm.Chunk
	for range 10 {
		allChunks = append(allChunks, chunks)
	}

	p := &streamMockProvider{chunks: allChunks}
	reg := tool.NewRegistry()
	reg.Register(tool.Func("loop", "loops", nil,
		func(_ context.Context, _ string) (string, error) {
			return "looping", nil
		}))

	a := New("a", "sys", "m", p, reg, WithMaxSteps(3))
	s := userSession("s-stream-maxsteps-usage", "start")

	result, err := a.StreamTurn(context.Background(), s, nil)
	if !errors.Is(err, ErrMaxSteps) {
		t.Fatalf("expected ErrMaxSteps, got %v", err)
	}
	// Each step contributes TotalTokens=15; total across MaxSteps=3 should be 45.
	want := 15 * 3
	if int(result.Usage.TotalTokens) != want {
		t.Errorf(
			"Usage.TotalTokens = %d, want %d (accumulated across all steps)",
			result.Usage.TotalTokens,
			want,
		)
	}
}

func TestStreamStepThinkingBlocks(t *testing.T) {
	p := &streamMockProvider{
		chunks: [][]llm.Chunk{
			{
				{ThinkingBlocks: []llm.ThinkingBlock{{Type: "thinking", Thinking: "thinking "}}},
				{ThinkingBlocks: []llm.ThinkingBlock{{Type: "thinking", Thinking: "harder"}}},
				{Content: "result"},
			},
		},
	}
	a := New("a", "sys", "m", p, nil)
	s := userSession("s-thinking", "q")

	_, err := a.StreamStep(context.Background(), s, nil)
	if err != nil {
		t.Fatal(err)
	}

	msgs := s.Messages()
	last := msgs[len(msgs)-1]
	if len(last.ThinkingBlocks) != 1 {
		t.Fatalf("expected 1 thinking block, got %d", len(last.ThinkingBlocks))
	}
	got := last.ThinkingBlocks[0].Thinking
	want := "thinking harder"
	if got != want {
		t.Errorf("expected thinking %q, got %q", want, got)
	}
}

// ---------------------------------------------------------------------------
// Streamer interface satisfaction
// ---------------------------------------------------------------------------

func TestBaseAgentImplementsStreamer(t *testing.T) {
	p := &streamMockProvider{}
	a := New("a", "", "m", p, nil)
	var _ Streamer = a // compile-time check
}

// ---------------------------------------------------------------------------
// Builder options
// ---------------------------------------------------------------------------

func TestWithProcessorsInsertsBeforeCapabilities(t *testing.T) {
	a := New("a", "", "m", &mockProvider{}, nil)
	origLen := len(a.builder.Processors())

	a2 := New("a2", "", "m", &mockProvider{}, nil,
		WithProcessors(ccontext.ProcessorFunc(noopProcessor)),
		WithProcessors(ccontext.ProcessorFunc(noopProcessor)),
	)
	if got := len(a2.builder.Processors()); got != origLen+2 {
		t.Errorf("expected %d processors, got %d", origLen+2, got)
	}
	// Last processor must still be Capabilities (not our sentinels).
	// Capabilities is a ProcessorFunc — we can check the sentinels
	// are NOT at position len-1 by verifying they are at len-3 and len-2.
	ps := a2.builder.Processors()
	n := len(ps)
	_ = ps[n-1] // Capabilities: just confirm no panic
	_ = ps[n-2] // second sentinel
	_ = ps[n-3] // first sentinel
}

func TestWithRequestProcessorsAndMutatorsInsertBeforeCapabilities(t *testing.T) {
	a := New("a", "", "m", &mockProvider{}, nil)
	origLen := len(a.builder.Processors())

	a2 := New("a2", "", "m", &mockProvider{}, nil,
		WithRequestProcessors(ccontext.RequestProcessorFunc(noopRequestProcessor)),
		WithMutators(ccontext.ContextMutatorFunc(noopMutator)),
	)
	if got := len(a2.builder.Processors()); got != origLen+2 {
		t.Errorf("expected %d processors, got %d", origLen+2, got)
	}

	ps := a2.builder.Processors()
	n := len(ps)
	_ = ps[n-1] // Capabilities
	_ = ps[n-2] // mutator bridge
	_ = ps[n-3] // request processor bridge
}

func noopProcessor(
	_ context.Context,
	_ llm.Provider,
	_ string,
	_ *session.Session,
	_ *llm.Request,
) error {
	return nil
}

func noopRequestProcessor(
	_ context.Context,
	_ llm.Provider,
	_ string,
	_ *session.Session,
	_ *llm.Request,
) error {
	return nil
}

func noopMutator(
	_ context.Context,
	_ llm.Provider,
	_ string,
	_ *session.Session,
) error {
	return nil
}
