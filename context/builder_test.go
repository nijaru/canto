package context

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
)

func TestBuilder_Build(t *testing.T) {
	sess := session.New("test-session")
	sess.Append(session.NewEvent(sess.ID(), session.EventTypeMessageAdded, llm.Message{
		Role:    llm.RoleUser,
		Content: "Hello world",
	}))

	reg := tool.NewRegistry()
	// Add a mock tool
	// ... (assuming registry works)

	builder := NewBuilder(
		InstructionProcessor("You are a helpful assistant."),
		HistoryProcessor(),
		ToolProcessor(reg),
	)

	req := &llm.LLMRequest{
		Model: "gpt-4o",
	}

	err := builder.Build(context.Background(), sess, req)
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}

	// Verify messages
	if len(req.Messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(req.Messages))
	}
	if req.Messages[0].Role != llm.RoleSystem {
		t.Errorf("expected first message to be system, got %s", req.Messages[0].Role)
	}
	if req.Messages[1].Content != "Hello world" {
		t.Errorf("expected second message to be 'Hello world', got %s", req.Messages[1].Content)
	}
}

func TestOffloadProcessor(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "canto-offload-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	sess := session.New("test-session")

	// Large tool result
	largeContent := ""
	for i := 0; i < 2000; i++ {
		largeContent += "large content "
	}

	req := &llm.LLMRequest{
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "request"},
			{Role: llm.RoleAssistant, Content: "calling tool..."},
			{Role: llm.RoleTool, Content: largeContent, ToolID: "t1"},
			{Role: llm.RoleAssistant, Content: "done"},
			{Role: llm.RoleUser, Content: "next"},
		},
	}

	// Threshold is 60%, MaxTokens = 1000.
	// largeContent is ~3000 tokens (chars/4 heuristic).
	offloader := NewOffloadProcessor(1000, tempDir)
	offloader.MinKeepTurns = 2 // Keep last 2 messages

	err = offloader.Process(context.Background(), sess, req)
	if err != nil {
		t.Fatalf("offload failed: %v", err)
	}

	// Message 2 (RoleTool) should be offloaded because it's older than last 2
	if len(req.Messages[2].Content) > 1000 {
		t.Errorf(
			"expected message to be offloaded, but still have %d chars",
			len(req.Messages[2].Content),
		)
	}

	// Verify file exists
	files, err := filepath.Glob(filepath.Join(tempDir, "*.json"))
	if err != nil || len(files) == 0 {
		t.Errorf("expected offload file to be created")
	}
}
