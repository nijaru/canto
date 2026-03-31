package governor_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ccontext "github.com/nijaru/canto/context"
	"github.com/nijaru/canto/governor"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

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
	history := []llm.Message{
		{Role: llm.RoleUser, Content: "request"},
		{Role: llm.RoleAssistant, Content: "calling tool..."},
		{Role: llm.RoleTool, Content: largeContent, ToolID: "t1"},
		{Role: llm.RoleAssistant, Content: "done"},
		{Role: llm.RoleUser, Content: "next"},
	}
	for _, msg := range history {
		if err := sess.Append(context.Background(), session.NewMessage(sess.ID(), msg)); err != nil {
			t.Fatalf("append history: %v", err)
		}
	}

	// Threshold is 60%, MaxTokens = 1000.
	// largeContent is ~3000 tokens (chars/4 heuristic).
	offloader := governor.NewOffloader(1000, tempDir)
	offloader.MinKeepTurns = 2 // Keep last 2 messages

	if err := offloader.Mutate(context.Background(), nil, "", sess); err != nil {
		t.Fatalf("offload failed: %v", err)
	}

	req := &llm.Request{
		Messages: []llm.Message{{Role: llm.RoleSystem, Content: "instructions"}},
	}
	if err := ccontext.History().ApplyRequest(context.Background(), nil, "", sess, req); err != nil {
		t.Fatalf("history rebuild failed: %v", err)
	}

	// Message 3 (RoleTool) should be offloaded because it's older than last 2.
	if len(req.Messages[3].Content) > 1000 {
		t.Errorf(
			"expected message to be offloaded, but still have %d chars",
			len(req.Messages[3].Content),
		)
	}

	// Verify file exists
	files, err := filepath.Glob(filepath.Join(tempDir, "objects", "*", "body"))
	if err != nil || len(files) == 0 {
		t.Errorf("expected offload file to be created")
	}

	var artifactEvents int
	for _, event := range sess.Events() {
		if event.Type == session.ArtifactRecorded {
			artifactEvents++
		}
	}
	if artifactEvents == 0 {
		t.Fatalf("expected offload to record artifact descriptors")
	}

	historyReq := &llm.Request{}
	if err := ccontext.History().ApplyRequest(context.Background(), nil, "", sess, historyReq); err != nil {
		t.Fatalf("history rebuild failed: %v", err)
	}
	if len(historyReq.Messages) != len(history) {
		t.Fatalf(
			"expected %d rebuilt history messages, got %d",
			len(history),
			len(historyReq.Messages),
		)
	}
	if len(historyReq.Messages[2].Content) > 1000 {
		t.Fatalf(
			"expected rebuilt tool result to stay offloaded, got %d chars",
			len(historyReq.Messages[2].Content),
		)
	}
}

func TestOffloadProcessor_DuplicateToolOutputsGetDistinctArtifacts(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "canto-offload-dupe-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	sess := session.New("dupe-session")
	largeContent := strings.Repeat("same large content ", 200)
	history := []llm.Message{
		{Role: llm.RoleUser, Content: "request"},
		{Role: llm.RoleTool, Content: largeContent, ToolID: "t1"},
		{Role: llm.RoleTool, Content: largeContent, ToolID: "t2"},
		{Role: llm.RoleAssistant, Content: "done"},
		{Role: llm.RoleUser, Content: "next"},
	}
	for _, msg := range history {
		if err := sess.Append(context.Background(), session.NewMessage(sess.ID(), msg)); err != nil {
			t.Fatalf("append history: %v", err)
		}
	}

	offloader := governor.NewOffloader(1000, tempDir)
	offloader.MinKeepTurns = 2
	if err := offloader.Mutate(context.Background(), nil, "", sess); err != nil {
		t.Fatalf("offload failed: %v", err)
	}

	req := &llm.Request{
		Messages: []llm.Message{{Role: llm.RoleSystem, Content: "instructions"}},
	}
	if err := ccontext.History().ApplyRequest(context.Background(), nil, "", sess, req); err != nil {
		t.Fatalf("history rebuild failed: %v", err)
	}

	files, err := filepath.Glob(filepath.Join(tempDir, "objects", "*", "body"))
	if err != nil {
		t.Fatalf("glob offload files: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 distinct offload artifacts, got %d", len(files))
	}
	if req.Messages[2].Content == req.Messages[3].Content {
		t.Fatalf(
			"expected distinct placeholders for duplicate outputs, got %q",
			req.Messages[2].Content,
		)
	}
}
