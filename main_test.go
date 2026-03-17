package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/runtime"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
	tools "github.com/nijaru/canto/x/tools"
	"github.com/oklog/ulid/v2"
)

// MockProvider is a fake LLM provider for testing.
type MockProvider struct {
	Response  *llm.Response
	StepCount int
}

func (m *MockProvider) ID() string { return "mock" }

func (m *MockProvider) Generate(
	ctx context.Context,
	req *llm.Request,
) (*llm.Response, error) {
	m.StepCount++
	// On second call, return text only
	if m.StepCount > 1 {
		return &llm.Response{
			Content: "I see some files.",
		}, nil
	}
	return m.Response, nil
}

func (m *MockProvider) Stream(ctx context.Context, req *llm.Request) (llm.Stream, error) {
	return nil, nil
}

func (m *MockProvider) Models(ctx context.Context) ([]catwalk.Model, error) {
	return nil, nil
}

func (m *MockProvider) CountTokens(
	ctx context.Context,
	model string,
	messages []llm.Message,
) (int, error) {
	return 0, nil
}

func (m *MockProvider) Cost(ctx context.Context, model string, usage llm.Usage) float64 {
	return 0
}
func (m *MockProvider) Capabilities(_ string) llm.Capabilities { return llm.DefaultCapabilities() }
func (m *MockProvider) IsTransient(err error) bool             { return false }

func TestMain(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewJSONLStore(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	registry := tool.NewRegistry()
	registry.Register(&tools.BashTool{})

	// 1. Initial agent response with a tool call
	mock := &MockProvider{
		Response: &llm.Response{
			Content: "I will check the current directory.",
			Calls: []llm.Call{
				{
					ID:   "call_123",
					Type: "function",
					Function: struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					}{
						Name:      "bash",
						Arguments: `{"command": "ls"}`,
					},
				},
			},
		},
	}

	a := agent.New("test-agent", "You are a helpful assistant.", "mock-model", mock, registry)
	sessionID := "sess_" + ulid.Make().String()

	// 2. Add initial user message to store manually for now
	userMsg := llm.Message{Role: llm.RoleUser, Content: "List files"}
	store.Save(
		context.Background(),
		session.NewEvent(sessionID, session.MessageAdded, userMsg),
	)

	// 3. Run agent via Runner
	r := runtime.NewRunner(store, a)
	_, err = r.Run(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}

	// 4. Verify session state from reloaded session
	sess, err := store.Load(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("failed to load session: %v", err)
	}
	messages := sess.Messages()

	// Expected:
	// 1. User: "List files"
	// 2. Assistant: "I will check..." + Call
	// 3. Tool: (output of ls)
	// 4. Assistant: "I see some files."

	if len(messages) != 4 {
		t.Errorf("expected 4 messages, got %d", len(messages))
		for i, m := range messages {
			t.Logf("msg %d: %s: %s", i, m.Role, m.Content)
		}
	}

	// 5. Verify persistence (all 3 new events should be in the file)
	path := filepath.Join(tmpDir, sessionID+".jsonl")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("session file was not created")
	}
}

type RSSTool struct{}

func (t *RSSTool) Spec() llm.Spec {
	return llm.Spec{
		Name:        "rss",
		Description: "Fetches an RSS feed",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{"type": "string"},
			},
		},
	}
}

func (t *RSSTool) Execute(ctx context.Context, args string) (string, error) {
	return "Article 1: AI reaches AGI.\nArticle 2: Go 1.25 Released.", nil
}
