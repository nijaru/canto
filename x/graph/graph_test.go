package graph_test

import (
	"context"
	"errors"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
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
func (m *mockProvider) Generate(_ context.Context, _ *llm.LLMRequest) (*llm.LLMResponse, error) {
	return &llm.LLMResponse{Content: m.msg}, nil
}
func (m *mockProvider) Stream(_ context.Context, _ *llm.LLMRequest) (llm.Stream, error) {
	return ctesting.NewMockStream(llm.Chunk{Content: m.msg}), nil
}
func (m *mockProvider) CountTokens(_ context.Context, _ string, _ []llm.Message) (int, error) {
	return 0, nil
}
func (m *mockProvider) Cost(_ context.Context, _ string, _ llm.Usage) float64 { return 0 }
func (m *mockProvider) Models(_ context.Context) ([]catwalk.Model, error) { return nil, nil }

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
		session.NewEvent("graph-test", session.EventTypeMessageAdded, llm.Message{
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
		session.NewEvent("terminal-test", session.EventTypeMessageAdded, llm.Message{
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
		session.NewEvent("nil-cond-test", session.EventTypeMessageAdded, llm.Message{
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
		session.NewEvent("cancel-test", session.EventTypeMessageAdded, llm.Message{
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
		session.NewEvent("no-entry-test", session.EventTypeMessageAdded, llm.Message{
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
	_ = sess.Append(ctx, session.NewEvent("nest-test", session.EventTypeMessageAdded, llm.Message{
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

func (m *streamingMockProvider) Stream(_ context.Context, _ *llm.LLMRequest) (llm.Stream, error) {
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
	_ = sess.Append(ctx, session.NewEvent("stream-test", session.EventTypeMessageAdded, llm.Message{
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
