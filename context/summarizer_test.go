package context

import (
	"context"
	"testing"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

type mockProvider struct {
	id    string
	genFn func(ctx context.Context, req *llm.Request) (*llm.Response, error)
}

func (m *mockProvider) ID() string { return m.id }

func (m *mockProvider) Generate(
	ctx context.Context,
	req *llm.Request,
) (*llm.Response, error) {
	return m.genFn(ctx, req)
}

func (m *mockProvider) Stream(ctx context.Context, req *llm.Request) (llm.Stream, error) {
	return nil, nil
}

func (m *mockProvider) Models(ctx context.Context) ([]llm.Model, error) {
	return nil, nil
}

func (m *mockProvider) CountTokens(
	ctx context.Context,
	model string,
	messages []llm.Message,
) (int, error) {
	return 0, nil
}

func (m *mockProvider) Cost(ctx context.Context, model string, usage llm.Usage) float64 {
	return 0
}
func (m *mockProvider) Capabilities(_ string) llm.Capabilities { return llm.DefaultCapabilities() }
func (m *mockProvider) IsTransient(_ error) bool               { return false }

func TestSummarizer(t *testing.T) {
	sess := session.New("test-session")
	history := []llm.Message{
		{Role: llm.RoleSystem, Content: "System prompt"},
		{Role: llm.RoleUser, Content: "Hello 1"},   // candidate
		{Role: llm.RoleAssistant, Content: "Hi 1"}, // candidate
		{Role: llm.RoleUser, Content: "Hello 2"},   // candidate
		{Role: llm.RoleAssistant, Content: "Hi 2"}, // candidate
		{Role: llm.RoleUser, Content: "Hello 3"},   // recent 1
		{Role: llm.RoleAssistant, Content: "Hi 3"}, // recent 2
		{Role: llm.RoleUser, Content: "Hello 4"},   // recent 3
	}

	// Expand content to trigger threshold
	longStr := ""
	for i := 0; i < 100; i++ {
		longStr += "word "
	}
	for i := range history {
		if i == 0 {
			continue
		}
		history[i].Content += longStr
	}
	for _, msg := range history {
		if err := sess.Append(context.Background(), session.NewMessage(sess.ID(), msg)); err != nil {
			t.Fatalf("append history: %v", err)
		}
	}

	provider := &mockProvider{
		id: "mock",
		genFn: func(ctx context.Context, req *llm.Request) (*llm.Response, error) {
			return &llm.Response{Content: "Summarized conversation"}, nil
		},
	}

	processor := NewSummarizer(100, provider, "mock-model")
	if err := processor.Mutate(context.Background(), nil, "", sess); err != nil {
		t.Fatalf("processor failed: %v", err)
	}

	req := &llm.Request{Messages: []llm.Message{}}
	if err := History().ApplyRequest(context.Background(), nil, "", sess, req); err != nil {
		t.Fatalf("history rebuild failed: %v", err)
	}

	if len(req.Messages) != 5 { // 1 system + 1 summary + 3 recent
		t.Fatalf("expected 5 messages, got %d", len(req.Messages))
	}

	if req.Messages[0].Role != llm.RoleSystem {
		t.Errorf("expected first message to be system, got %s", req.Messages[0].Role)
	}

	if req.Messages[1].Role != llm.RoleSystem {
		t.Errorf(
			"expected second message to be summary (system role), got %s",
			req.Messages[1].Role,
		)
	}

	expectedSummary := "<conversation_summary>\nSummarized conversation\n</conversation_summary>"
	if req.Messages[1].Content != expectedSummary {
		t.Errorf(
			"expected summary content '%s', got '%s'",
			expectedSummary,
			req.Messages[1].Content,
		)
	}

	if req.Messages[4].Content != "Hello 4"+longStr {
		t.Errorf("expected last message to be 'Hello 4...', got '%s'", req.Messages[4].Content)
	}

	historyReq := &llm.Request{}
	if err := History().ApplyRequest(context.Background(), nil, "", sess, historyReq); err != nil {
		t.Fatalf("history rebuild failed: %v", err)
	}
	if len(historyReq.Messages) != 5 {
		t.Fatalf("expected 5 rebuilt history messages, got %d", len(historyReq.Messages))
	}
	if historyReq.Messages[1].Content != expectedSummary {
		t.Fatalf("expected persisted summary, got %q", historyReq.Messages[1].Content)
	}
}
