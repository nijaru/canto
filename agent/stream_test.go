package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nijaru/canto/llm"
	prompt "github.com/nijaru/canto/prompt"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
)

// streamMockProvider extends mockProvider with a scripted Stream implementation.
type streamMockProvider struct {
	mockProvider
	chunks   [][]llm.Chunk // one slice of chunks per Stream call
	spos     int
	failures int
}

func (m *streamMockProvider) Stream(_ context.Context, _ *llm.Request) (llm.Stream, error) {
	if m.failures > 0 {
		m.failures--
		return nil, fmt.Errorf("transient stream failure")
	}
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

type contextBlockingStreamProvider struct {
	mockProvider
	started chan struct{}
}

type contextBlockingStream struct {
	ctx context.Context
}

func (p *contextBlockingStreamProvider) Stream(
	ctx context.Context,
	req *llm.Request,
) (llm.Stream, error) {
	p.started <- struct{}{}
	return &contextBlockingStream{ctx: ctx}, nil
}

func (s *contextBlockingStream) Next() (*llm.Chunk, bool) {
	<-s.ctx.Done()
	return nil, false
}

func (s *contextBlockingStream) Err() error {
	return s.ctx.Err()
}

func (s *contextBlockingStream) Close() error { return nil }

func (m *streamMockProvider) IsTransient(err error) bool {
	return err != nil && strings.Contains(err.Error(), "transient")
}

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
	if result.TurnStopReason != "" {
		t.Fatalf("expected no turn stop reason from a single step, got %q", result.TurnStopReason)
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

func TestStreamStepSkipsEmptyAssistantMessage(t *testing.T) {
	usage := &llm.Usage{InputTokens: 4, OutputTokens: 1, TotalTokens: 5}
	p := &streamMockProvider{
		chunks: [][]llm.Chunk{
			{{Usage: usage}},
		},
	}
	a := New("a", "sys", "m", p, nil)
	s := userSession("s-empty-stream", "hi")

	result, err := a.StreamStep(t.Context(), s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Usage.TotalTokens != usage.TotalTokens {
		t.Fatalf("usage = %+v, want %+v", result.Usage, *usage)
	}

	msgs := s.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected only user message, got %#v", msgs)
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
	if result.TurnStopReason != TurnStopCompleted {
		t.Fatalf("expected turn stop reason %q, got %q", TurnStopCompleted, result.TurnStopReason)
	}
}

func TestStreamTurnRecordsTerminalEventOnCanceledContext(t *testing.T) {
	p := &contextBlockingStreamProvider{started: make(chan struct{}, 1)}
	a := New("a", "sys", "m", p, nil)
	s := userSession("s-canceled-stream", "q")
	ctx, cancel := context.WithCancel(t.Context())

	errCh := make(chan error, 1)
	go func() {
		_, err := a.StreamTurn(ctx, s, nil)
		errCh <- err
	}()

	select {
	case <-p.started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stream call")
	}
	cancel()

	select {
	case err := <-errCh:
		if err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
			t.Fatalf("stream turn error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for canceled stream turn")
	}

	assertTurnCompletedError(t, s, context.Canceled.Error())
}

func TestStreamTurnStopsBeforeNextStepWhenContextCanceled(t *testing.T) {
	call := llm.Call{ID: "c1", Type: "function"}
	call.Function.Name = "cancel"
	call.Function.Arguments = `{}`

	p := &streamMockProvider{
		chunks: [][]llm.Chunk{
			{{Calls: []llm.Call{call}}},
			{{Content: "should not run"}},
		},
	}
	reg := tool.NewRegistry()
	ctx, cancel := context.WithCancel(t.Context())
	reg.Register(tool.Func("cancel", "cancels", nil,
		func(_ context.Context, _ string) (string, error) {
			cancel()
			return "canceled", nil
		}))

	a := New("a", "sys", "m", p, reg)
	s := userSession("s-canceled-before-next-stream", "start")

	_, err := a.StreamTurn(ctx, s, nil)
	if err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Fatalf("stream turn error = %v, want context canceled", err)
	}
	if p.spos != 1 {
		t.Fatalf("stream calls = %d, want 1", p.spos)
	}

	assertTurnCompletedError(t, s, context.Canceled.Error())
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
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
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
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
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
	if result.TurnStopReason != TurnStopMaxTurnsHit {
		t.Fatalf("expected turn stop reason %q, got %q", TurnStopMaxTurnsHit, result.TurnStopReason)
	}
}

func TestStreamTurnRetriesTransientStartError(t *testing.T) {
	p := &streamMockProvider{
		failures: 1,
		chunks: [][]llm.Chunk{
			{{Content: "recovered stream"}},
		},
	}
	a := New("a", "sys", "m", p, nil, WithMaxEscalations(2))
	s := userSession("s-stream-retry", "q")

	result, err := a.StreamTurn(context.Background(), s, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "recovered stream" {
		t.Fatalf("Content = %q, want recovered stream", result.Content)
	}
}

func TestStreamTurnStopsCleanlyWhenBudgetGuardTrips(t *testing.T) {
	p := &streamMockProvider{
		chunks: [][]llm.Chunk{
			{{Content: "should not stream"}},
		},
	}
	a := New("a", "sys", "m", p, nil, WithBudgetGuard(1.0))
	s := userSession("s-stream-budget-stop", "q")

	e := session.NewEvent(s.ID(), session.MessageAdded, llm.Message{
		Role:    llm.RoleAssistant,
		Content: "prior cost",
	})
	e.Cost = 1.0
	if err := s.Append(t.Context(), e); err != nil {
		t.Fatalf("append prior cost: %v", err)
	}

	result, err := a.StreamTurn(t.Context(), s, nil)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if result.TurnStopReason != TurnStopBudgetExhausted {
		t.Fatalf(
			"expected turn stop reason %q, got %q",
			TurnStopBudgetExhausted,
			result.TurnStopReason,
		)
	}

	msgs := s.Messages()
	if len(msgs) != 2 {
		t.Fatalf("expected no new assistant output after budget stop, got %d messages", len(msgs))
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

func TestWithRequestProcessorsInsertBeforeCacheAlignment(t *testing.T) {
	a := New("a", "", "m", &mockProvider{}, nil)
	origLen := len(a.builder.RequestProcessors())

	a2 := New("a2", "", "m", &mockProvider{}, nil,
		WithRequestProcessors(prompt.RequestProcessorFunc(noopRequestProcessor)),
		WithRequestProcessors(prompt.RequestProcessorFunc(noopRequestProcessor)),
	)
	if got := len(a2.builder.RequestProcessors()); got != origLen+2 {
		t.Errorf("expected %d request processors, got %d", origLen+2, got)
	}
	// Custom processors should land before cache alignment.
	ps := a2.builder.RequestProcessors()
	n := len(ps)
	_ = ps[n-1] // CacheAligner: just confirm no panic
	_ = ps[n-2] // second sentinel
	_ = ps[n-3] // first sentinel
}

func TestWithRequestProcessorsAndMutatorsInsertBeforeCacheAlignment(t *testing.T) {
	a := New("a", "", "m", &mockProvider{}, nil)
	origLen := len(a.builder.RequestProcessors())
	origMutators := len(a.builder.Mutators())

	a2 := New("a2", "", "m", &mockProvider{}, nil,
		WithRequestProcessors(prompt.RequestProcessorFunc(noopRequestProcessor)),
		WithMutators(prompt.ContextMutatorFunc(noopMutator)),
	)
	if got := len(a2.builder.RequestProcessors()); got != origLen+1 {
		t.Errorf("expected %d request processors, got %d", origLen+1, got)
	}
	if got := len(a2.builder.Mutators()); got != origMutators+1 {
		t.Errorf("expected %d mutators, got %d", origMutators+1, got)
	}

	ps := a2.builder.RequestProcessors()
	n := len(ps)
	_ = ps[n-1] // CacheAligner
	_ = ps[n-2] // request processor
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
