package graph_test

import (
	"context"
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

func (m *mockProvider) ID() string { return "mock" }
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
