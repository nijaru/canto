package canto_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/runtime"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
	cantotest "github.com/nijaru/canto/x/testing"
	tools "github.com/nijaru/canto/x/tools"
	"github.com/oklog/ulid/v2"
)

func TestMain(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewJSONLStore(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	registry := tool.NewRegistry()
	registry.Register(&tools.BashTool{})

	provider := cantotest.NewMockProvider("main",
		cantotest.Step{
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
		cantotest.Step{Content: "I see some files."},
	)

	a := agent.New(
		"test-agent",
		"You are a helpful assistant.",
		"mock-model",
		provider,
		registry,
	)
	sessionID := "sess_" + ulid.Make().String()
	r := runtime.NewRunner(store, a)
	result, err := r.Send(context.Background(), sessionID, "List files")
	if err != nil {
		t.Fatal(err)
	}
	if result.Content == "" {
		t.Fatal("expected assistant text in step result")
	}

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

	path := filepath.Join(tmpDir, sessionID+".jsonl")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("session file was not created")
	}

	provider.AssertExhausted(t)
}
