package graph_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/x/graph"
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

func TestGraphConditionalRouting(t *testing.T) {
	ctx := context.Background()

	researcher := agent.New("researcher", "Research the topic.", "gpt-4",
		&mockProvider{msg: "research done"}, nil)
	writer := agent.New("writer", "Write the report.", "gpt-4",
		&mockProvider{msg: "report written"}, nil)

	g := graph.New("researcher")
	g.AddNode(researcher)
	g.AddNode(writer)

	// Edge: always route from researcher → writer.
	g.AddEdge("researcher", "writer", func(r agent.StepResult) bool {
		return r.Handoff == nil // no handoff required — unconditional
	})

	sess := session.New("graph-test")
	sess.Append(session.NewEvent("graph-test", session.EventTypeMessageAdded, llm.Message{
		Role:    llm.RoleUser,
		Content: "Write a report on Go.",
	}))

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

	g := graph.New("solo")
	g.AddNode(solo)
	// No edges — solo is a terminal node.

	sess := session.New("terminal-test")
	sess.Append(session.NewEvent("terminal-test", session.EventTypeMessageAdded, llm.Message{
		Role:    llm.RoleUser,
		Content: "Do it.",
	}))

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

	g := graph.New("a")
	g.AddNode(a)
	g.AddNode(b)
	g.AddEdge("a", "b", nil)

	if err := g.Validate(); err != nil {
		t.Fatalf("expected valid graph, got error: %v", err)
	}
}

func TestValidate_EntryNodeMissing(t *testing.T) {
	g := graph.New("missing")
	// No nodes registered — entry node not present.
	err := g.Validate()
	if err == nil {
		t.Fatal("expected error for missing entry node, got nil")
	}
}

func TestValidate_EdgeMissingSourceNode(t *testing.T) {
	a := agent.New("a", "Do A.", "gpt-4", &mockProvider{msg: "a"}, nil)
	b := agent.New("b", "Do B.", "gpt-4", &mockProvider{msg: "b"}, nil)

	g := graph.New("a")
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

	g := graph.New("a")
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

	g := graph.New("a")
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

	g := graph.New("a")
	g.AddNode(a)
	g.AddNode(b)
	// nil condition — must be treated as unconditional.
	g.AddEdge("a", "b", nil)

	sess := session.New("nil-cond-test")
	sess.Append(session.NewEvent("nil-cond-test", session.EventTypeMessageAdded, llm.Message{
		Role:    llm.RoleUser,
		Content: "Go.",
	}))

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

	g := graph.New("a")
	g.AddNode(a)

	sess := session.New("cancel-test")
	sess.Append(session.NewEvent("cancel-test", session.EventTypeMessageAdded, llm.Message{
		Role:    llm.RoleUser,
		Content: "Go.",
	}))

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

	g := graph.New("missing")
	// No nodes added.

	sess := session.New("no-entry-test")
	sess.Append(session.NewEvent("no-entry-test", session.EventTypeMessageAdded, llm.Message{
		Role:    llm.RoleUser,
		Content: "Go.",
	}))

	_, err := g.Run(ctx, sess)
	if err == nil {
		t.Fatal("expected error for unregistered entry node, got nil")
	}
}
