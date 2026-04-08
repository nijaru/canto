package graph_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/x/graph"
	ctesting "github.com/nijaru/canto/x/testing"
)

type mockProvider struct {
	llm.Provider
	msg string
}

func (m *mockProvider) ID() string                             { return "mock" }
func (m *mockProvider) Capabilities(_ string) llm.Capabilities { return llm.DefaultCapabilities() }
func (m *mockProvider) IsTransient(_ error) bool               { return false }
func (m *mockProvider) Generate(_ context.Context, _ *llm.Request) (*llm.Response, error) {
	return &llm.Response{Content: m.msg}, nil
}

func (m *mockProvider) Stream(_ context.Context, _ *llm.Request) (llm.Stream, error) {
	return ctesting.NewMockStream(llm.Chunk{Content: m.msg}), nil
}

func (m *mockProvider) CountTokens(_ context.Context, _ string, _ []llm.Message) (int, error) {
	return 0, nil
}
func (m *mockProvider) Cost(_ context.Context, _ string, _ llm.Usage) float64 { return 0 }
func (m *mockProvider) Models(_ context.Context) ([]llm.Model, error)         { return nil, nil }

type scriptedAgent struct {
	id     string
	msg    string
	usage  llm.Usage
	err    error
	calls  int
	onTurn func(*scriptedAgent) error
	result *agent.StepResult
}

func (a *scriptedAgent) ID() string { return a.id }

func (a *scriptedAgent) Step(ctx context.Context, sess *session.Session) (agent.StepResult, error) {
	return a.Turn(ctx, sess)
}

func (a *scriptedAgent) Turn(ctx context.Context, sess *session.Session) (agent.StepResult, error) {
	a.calls++
	if a.onTurn != nil {
		if err := a.onTurn(a); err != nil {
			return agent.StepResult{}, err
		}
	}
	if a.err != nil {
		return agent.StepResult{}, a.err
	}
	if a.result != nil {
		res := *a.result
		res.Usage = a.usage
		return res, nil
	}
	msg := llm.Message{Role: llm.RoleAssistant, Content: a.msg}
	if err := sess.Append(ctx, session.NewEvent(sess.ID(), session.MessageAdded, msg)); err != nil {
		return agent.StepResult{}, err
	}
	return agent.StepResult{Content: a.msg, Usage: a.usage}, nil
}

type memoryCheckpointStore struct {
	byKey map[string]graph.Checkpoint
}

func newMemoryCheckpointStore() *memoryCheckpointStore {
	return &memoryCheckpointStore{byKey: make(map[string]graph.Checkpoint)}
}

func checkpointKey(graphID, sessionID string) string {
	return graphID + "::" + sessionID
}

func (s *memoryCheckpointStore) Load(
	_ context.Context,
	graphID, sessionID string,
) (*graph.Checkpoint, error) {
	cp, ok := s.byKey[checkpointKey(graphID, sessionID)]
	if !ok {
		return nil, nil
	}
	copy := cp
	return &copy, nil
}

func (s *memoryCheckpointStore) Save(_ context.Context, checkpoint graph.Checkpoint) error {
	s.byKey[checkpointKey(checkpoint.GraphID, checkpoint.SessionID)] = checkpoint
	return nil
}

func (s *memoryCheckpointStore) Clear(_ context.Context, graphID, sessionID string) error {
	delete(s.byKey, checkpointKey(graphID, sessionID))
	return nil
}

func TestGraphConditionalRouting(t *testing.T) {
	ctx := context.Background()

	researcher := agent.New("researcher", "Research the topic.", "gpt-4",
		&mockProvider{msg: "research done"}, nil)
	writer := agent.New("writer", "Write the report.", "gpt-4",
		&mockProvider{msg: "report written"}, nil)

	g := graph.New("main", "researcher")
	g.AddNode(researcher)
	g.AddNode(writer)

	// Edge: always route from researcher → writer.
	g.AddEdge("researcher", "writer", func(r agent.StepResult) bool {
		return r.Handoff == nil // no handoff required — unconditional
	})

	sess := session.New("graph-test")
	_ = sess.Append(
		context.Background(),
		session.NewEvent("graph-test", session.MessageAdded, llm.Message{
			Role:    llm.RoleUser,
			Content: "Write a report on Go.",
		}),
	)

	result, err := g.Run(ctx, sess)
	if err != nil {
		t.Fatalf("graph.Run: %v", err)
	}
	_ = result

	// Both agents should have appended messages to the session.
	messages := sess.Messages()
	// user + researcher assistant + writer assistant = 3
	if len(messages) < 3 {
		t.Errorf("expected at least 3 messages, got %d", len(messages))
	}

	// Last message should be from the writer.
	last := messages[len(messages)-1]
	if last.Content != "report written" {
		t.Errorf("expected last message from writer, got %q", last.Content)
	}
}

func TestGraphTerminatesAtTerminalNode(t *testing.T) {
	ctx := context.Background()

	solo := agent.New("solo", "Do everything.", "gpt-4",
		&mockProvider{msg: "done"}, nil)

	g := graph.New("main", "solo")
	g.AddNode(solo)
	// No edges — solo is a terminal node.

	sess := session.New("terminal-test")
	_ = sess.Append(
		context.Background(),
		session.NewEvent("terminal-test", session.MessageAdded, llm.Message{
			Role:    llm.RoleUser,
			Content: "Do it.",
		}),
	)

	_, err := g.Run(ctx, sess)
	if err != nil {
		t.Fatalf("graph.Run with terminal node: %v", err)
	}

	msgs := sess.Messages()
	if len(msgs) < 2 {
		t.Errorf("expected at least 2 messages, got %d", len(msgs))
	}
}

// --- Validate tests ---

func TestValidate_ValidGraph(t *testing.T) {
	a := agent.New("a", "Do A.", "gpt-4", &mockProvider{msg: "a"}, nil)
	b := agent.New("b", "Do B.", "gpt-4", &mockProvider{msg: "b"}, nil)

	g := graph.New("main", "a")
	g.AddNode(a)
	g.AddNode(b)
	g.AddEdge("a", "b", nil)

	if err := g.Validate(); err != nil {
		t.Fatalf("expected valid graph, got error: %v", err)
	}
}

func TestValidate_EntryNodeMissing(t *testing.T) {
	g := graph.New("main", "missing")
	// No nodes registered — entry node not present.
	err := g.Validate()
	if err == nil {
		t.Fatal("expected error for missing entry node, got nil")
	}
}

func TestValidate_EdgeMissingSourceNode(t *testing.T) {
	a := agent.New("a", "Do A.", "gpt-4", &mockProvider{msg: "a"}, nil)
	b := agent.New("b", "Do B.", "gpt-4", &mockProvider{msg: "b"}, nil)

	g := graph.New("main", "a")
	g.AddNode(a)
	g.AddNode(b)
	// Add an edge from a node that is not registered.
	g.AddEdge("ghost", "b", nil)

	err := g.Validate()
	if err == nil {
		t.Fatal("expected error for edge referencing unregistered source node, got nil")
	}
}

func TestValidate_EdgeMissingTargetNode(t *testing.T) {
	a := agent.New("a", "Do A.", "gpt-4", &mockProvider{msg: "a"}, nil)

	g := graph.New("main", "a")
	g.AddNode(a)
	g.AddEdge("a", "nowhere", nil)

	err := g.Validate()
	if err == nil {
		t.Fatal("expected error for edge referencing unregistered target node, got nil")
	}
}

func TestValidate_CycleDetected(t *testing.T) {
	a := agent.New("a", "Do A.", "gpt-4", &mockProvider{msg: "a"}, nil)
	b := agent.New("b", "Do B.", "gpt-4", &mockProvider{msg: "b"}, nil)
	c := agent.New("c", "Do C.", "gpt-4", &mockProvider{msg: "c"}, nil)

	g := graph.New("main", "a")
	g.AddNode(a)
	g.AddNode(b)
	g.AddNode(c)
	// a → b → c → b forms a cycle.
	g.AddEdge("a", "b", nil)
	g.AddEdge("b", "c", nil)
	g.AddEdge("c", "b", nil)

	err := g.Validate()
	if err == nil {
		t.Fatal("expected error for cycle, got nil")
	}
}

// --- AddEdge: nil condition (unconditional) ---

func TestAddEdge_NilConditionIsUnconditional(t *testing.T) {
	ctx := context.Background()

	a := agent.New("a", "Do A.", "gpt-4", &mockProvider{msg: "a"}, nil)
	b := agent.New("b", "Do B.", "gpt-4", &mockProvider{msg: "b"}, nil)

	g := graph.New("main", "a")
	g.AddNode(a)
	g.AddNode(b)
	// nil condition — must be treated as unconditional.
	g.AddEdge("a", "b", nil)

	sess := session.New("nil-cond-test")
	_ = sess.Append(
		context.Background(),
		session.NewEvent("nil-cond-test", session.MessageAdded, llm.Message{
			Role:    llm.RoleUser,
			Content: "Go.",
		}),
	)

	_, err := g.Run(ctx, sess)
	if err != nil {
		t.Fatalf("graph.Run with nil condition edge: %v", err)
	}

	msgs := sess.Messages()
	last := msgs[len(msgs)-1]
	if last.Content != "b" {
		t.Errorf("expected last message from node b, got %q", last.Content)
	}
}

// --- Run: context cancellation ---

func TestRun_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	a := agent.New("a", "Do A.", "gpt-4", &mockProvider{msg: "a"}, nil)

	g := graph.New("main", "a")
	g.AddNode(a)

	sess := session.New("cancel-test")
	_ = sess.Append(
		context.Background(),
		session.NewEvent("cancel-test", session.MessageAdded, llm.Message{
			Role:    llm.RoleUser,
			Content: "Go.",
		}),
	)

	_, err := g.Run(ctx, sess)
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

// --- Run: entry node not registered ---

func TestRun_EntryNodeNotRegistered(t *testing.T) {
	ctx := context.Background()

	g := graph.New("main", "missing")
	// No nodes added.

	sess := session.New("no-entry-test")
	_ = sess.Append(
		context.Background(),
		session.NewEvent("no-entry-test", session.MessageAdded, llm.Message{
			Role:    llm.RoleUser,
			Content: "Go.",
		}),
	)

	_, err := g.Run(ctx, sess)
	if err == nil {
		t.Fatal("expected error for unregistered entry node, got nil")
	}
}

func TestNestedGraphs(t *testing.T) {
	ctx := context.Background()

	// Child graph: a -> b
	a := agent.New("a", "Do A.", "gpt-4", &mockProvider{msg: "a"}, nil)
	b := agent.New("b", "Do B.", "gpt-4", &mockProvider{msg: "b"}, nil)
	child := graph.New("child", "a")
	child.AddNode(a)
	child.AddNode(b)
	child.AddEdge("a", "b", nil)

	// Parent graph: child -> c
	c := agent.New("c", "Do C.", "gpt-4", &mockProvider{msg: "c"}, nil)
	parent := graph.New("parent", "child")
	parent.AddNode(child)
	parent.AddNode(c)
	parent.AddEdge("child", "c", nil)

	sess := session.New("nest-test")
	_ = sess.Append(ctx, session.NewEvent("nest-test", session.MessageAdded, llm.Message{
		Role:    llm.RoleUser,
		Content: "Go.",
	}))

	result, err := parent.Run(ctx, sess)
	if err != nil {
		t.Fatalf("parent.Run: %v", err)
	}

	if result.Content != "c" {
		t.Errorf("expected final result 'c', got %q", result.Content)
	}

	msgs := sess.Messages()
	// user + child(a+b) + c = 4
	if len(msgs) != 4 {
		t.Errorf("expected 4 messages, got %d", len(msgs))
	}
}

type streamingMockProvider struct {
	mockProvider
	chunks []llm.Chunk
}

func (m *streamingMockProvider) Stream(_ context.Context, _ *llm.Request) (llm.Stream, error) {
	return ctesting.NewMockStream(m.chunks...), nil
}

func TestGraph_StreamTurn(t *testing.T) {
	ctx := context.Background()

	chunks := []llm.Chunk{
		{Content: "hello "},
		{Content: "world"},
	}
	a := agent.New("a", "Greeting.", "gpt-4", &streamingMockProvider{chunks: chunks}, nil)
	b := agent.New("b", "Ending.", "gpt-4", &mockProvider{msg: "!"}, nil)

	g := graph.New("main", "a")
	g.AddNode(a)
	g.AddNode(b)
	g.AddEdge("a", "b", nil)

	sess := session.New("stream-test")
	_ = sess.Append(ctx, session.NewEvent("stream-test", session.MessageAdded, llm.Message{
		Role:    llm.RoleUser,
		Content: "Greet me.",
	}))

	var seen string
	_, err := g.StreamTurn(ctx, sess, func(chunk *llm.Chunk) {
		seen += chunk.Content
	})
	if err != nil {
		t.Fatalf("g.StreamTurn: %v", err)
	}

	// We expect chunks from 'a' to be relayed. 'b' also relays its message as a chunk.
	if seen != "hello world!" {
		t.Errorf("expected 'hello world!', got %q", seen)
	}
}

func TestGraphCheckpointingResumesFromNextNode(t *testing.T) {
	ctx := context.Background()
	store := newMemoryCheckpointStore()

	first := &scriptedAgent{id: "a", msg: "a", usage: llm.Usage{TotalTokens: 3}}
	second := &scriptedAgent{
		id:  "b",
		msg: "b",
		onTurn: func(a *scriptedAgent) error {
			if a.calls == 0 {
				return fmt.Errorf("calls not incremented")
			}
			if a.calls == 1 {
				return errors.New("boom")
			}
			return nil
		},
		usage: llm.Usage{TotalTokens: 5},
	}

	g := graph.New("checkpointed", "a")
	g.SetCheckpointStore(store)
	g.AddNode(first)
	g.AddNode(second)
	g.AddEdge("a", "b", nil)

	sess := session.New("graph-checkpoint")
	if err := sess.Append(ctx, session.NewEvent(sess.ID(), session.MessageAdded, llm.Message{
		Role:    llm.RoleUser,
		Content: "run",
	})); err != nil {
		t.Fatalf("append user message: %v", err)
	}

	if _, err := g.Run(ctx, sess); err == nil {
		t.Fatal("expected first run to fail on second node")
	}
	if first.calls != 1 {
		t.Fatalf("expected first node to run once, got %d", first.calls)
	}
	if second.calls != 1 {
		t.Fatalf("expected second node to run once, got %d", second.calls)
	}

	second.onTurn = nil
	result, err := g.Run(ctx, sess)
	if err != nil {
		t.Fatalf("resume run: %v", err)
	}
	if first.calls != 1 {
		t.Fatalf("expected resume to skip first node, got %d calls", first.calls)
	}
	if second.calls != 2 {
		t.Fatalf("expected resume to rerun second node once, got %d calls", second.calls)
	}
	if result.Content != "b" {
		t.Fatalf("expected final content from second node, got %q", result.Content)
	}
	if result.Usage.TotalTokens != 8 {
		t.Fatalf("expected accumulated usage 8, got %d", result.Usage.TotalTokens)
	}
}

func TestSQLiteCheckpointStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "graph-checkpoints.db")
	store, err := graph.NewSQLiteCheckpointStore(path)
	if err != nil {
		t.Fatalf("new sqlite checkpoint store: %v", err)
	}

	want := graph.Checkpoint{
		GraphID:     "g1",
		SessionID:   "s1",
		NextNode:    "writer",
		Steps:       2,
		LastEventID: "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		Usage:       llm.Usage{InputTokens: 10, OutputTokens: 4, TotalTokens: 14, Cost: 0.25},
		Result: agent.StepResult{
			Content: "ok",
			Usage:   llm.Usage{TotalTokens: 14},
			ToolResults: []llm.Message{{
				Role:    llm.RoleTool,
				Content: "tool output",
				ToolID:  "c1",
				Name:    "echo",
			}},
		},
		Completed: false,
	}
	if err := store.Save(ctx, want); err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}

	got, err := store.Load(ctx, "g1", "s1")
	if err != nil {
		t.Fatalf("load checkpoint: %v", err)
	}
	if got == nil {
		t.Fatal("expected checkpoint, got nil")
	}
	if got.NextNode != want.NextNode || got.Steps != want.Steps ||
		got.LastEventID != want.LastEventID {
		t.Fatalf("unexpected checkpoint metadata: %+v", got)
	}
	if got.Usage != want.Usage {
		t.Fatalf("unexpected usage: %+v", got.Usage)
	}
	if got.Result.Content != want.Result.Content || len(got.Result.ToolResults) != 1 {
		t.Fatalf("unexpected result payload: %+v", got.Result)
	}

	if err := store.Clear(ctx, "g1", "s1"); err != nil {
		t.Fatalf("clear checkpoint: %v", err)
	}
	got, err = store.Load(ctx, "g1", "s1")
	if err != nil {
		t.Fatalf("load after clear: %v", err)
	}
	if got != nil {
		t.Fatalf("expected cleared checkpoint, got %+v", got)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected sqlite db file to exist: %v", err)
	}
}

func TestGraphPauseResumeUsesCheckpointedCurrentNode(t *testing.T) {
	ctx := context.Background()
	store := newMemoryCheckpointStore()

	first := &scriptedAgent{id: "a", msg: "a", usage: llm.Usage{TotalTokens: 2}}
	waiter := &scriptedAgent{
		id:    "b",
		usage: llm.Usage{TotalTokens: 3},
		onTurn: func(a *scriptedAgent) error {
			if a.calls == 1 {
				return nil
			}
			return nil
		},
		result: &agent.StepResult{TurnStopReason: agent.TurnStopWaiting},
	}
	final := &scriptedAgent{id: "c", msg: "c", usage: llm.Usage{TotalTokens: 5}}

	g := graph.New("pause-resume", "a")
	g.SetCheckpointStore(store)
	g.AddNode(first)
	g.AddNode(waiter)
	g.AddNode(final)
	g.AddEdge("a", "b", nil)
	g.AddEdge("b", "c", func(res agent.StepResult) bool { return res.TurnStopReason == "" })

	sess := session.New("graph-pause")
	if err := sess.Append(ctx, session.NewEvent(sess.ID(), session.MessageAdded, llm.Message{
		Role:    llm.RoleUser,
		Content: "run",
	})); err != nil {
		t.Fatalf("append user message: %v", err)
	}

	if err := sess.Append(ctx, session.NewWaitStartedEvent(sess.ID(), session.WaitData{
		Reason: "approval",
	})); err != nil {
		t.Fatalf("append wait started: %v", err)
	}

	result, err := g.Run(ctx, sess)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if result.TurnStopReason != agent.TurnStopWaiting {
		t.Fatalf("expected waiting stop reason, got %q", result.TurnStopReason)
	}
	if first.calls != 1 || waiter.calls != 1 || final.calls != 0 {
		t.Fatalf(
			"unexpected call counts after pause: a=%d b=%d c=%d",
			first.calls,
			waiter.calls,
			final.calls,
		)
	}

	result, err = g.Run(ctx, sess)
	if err != nil {
		t.Fatalf("second run while waiting: %v", err)
	}
	if result.TurnStopReason != agent.TurnStopWaiting {
		t.Fatalf("expected waiting stop reason on parked rerun, got %q", result.TurnStopReason)
	}
	if first.calls != 1 || waiter.calls != 1 || final.calls != 0 {
		t.Fatalf(
			"expected parked rerun to avoid duplicate work: a=%d b=%d c=%d",
			first.calls,
			waiter.calls,
			final.calls,
		)
	}

	if err := sess.Append(ctx, session.NewWaitResolvedEvent(sess.ID(), session.WaitData{
		Reason: "approval",
	})); err != nil {
		t.Fatalf("append wait resolved: %v", err)
	}
	waiter.result = &agent.StepResult{Content: "b"}

	result, err = g.Run(ctx, sess)
	if err != nil {
		t.Fatalf("resume after wait resolved: %v", err)
	}
	if first.calls != 1 || waiter.calls != 2 || final.calls != 1 {
		t.Fatalf(
			"unexpected call counts after resume: a=%d b=%d c=%d",
			first.calls,
			waiter.calls,
			final.calls,
		)
	}
	if result.Content != "c" {
		t.Fatalf("expected final content from c, got %q", result.Content)
	}
	if result.Usage.TotalTokens != 13 {
		t.Fatalf("expected accumulated usage 13, got %d", result.Usage.TotalTokens)
	}
}
