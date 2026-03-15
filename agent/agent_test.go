package agent

import (
	"context"
	"testing"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

type mockProvider struct {
	llm.Provider
	responses []*llm.LLMResponse
}

func (m *mockProvider) ID() string { return "mock" }
func (m *mockProvider) Generate(ctx context.Context, req *llm.LLMRequest) (*llm.LLMResponse, error) {
	if len(m.responses) == 0 {
		return &llm.LLMResponse{Content: "no more responses"}, nil
	}
	resp := m.responses[0]
	m.responses = m.responses[1:]
	return resp, nil
}

func TestAgentTurn(t *testing.T) {
	p := &mockProvider{
		responses: []*llm.LLMResponse{
			{Content: "Hello! How can I help?"},
		},
	}

	a := New("test-agent", "You are a helpful assistant.", "gpt-4", p, nil)
	s := session.New("test-session")
	s.Append(session.NewEvent("test-session", session.EventTypeMessageAdded, llm.Message{
		Role:    llm.RoleUser,
		Content: "Hi!",
	}))

	_, err := a.Turn(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}

	messages := s.Messages()
	if len(messages) != 2 {
		t.Errorf("expected 2 messages (user + assistant), got %d", len(messages))
	}

	if messages[1].Content != "Hello! How can I help?" {
		t.Errorf("expected response 'Hello! How can I help?', got '%s'", messages[1].Content)
	}
}

func TestAgentToolTurn(t *testing.T) {
	// Not implemented yet because we need a real tool registry for a full test,
	// or another mock.
}
