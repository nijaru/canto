package context

import (
	"strings"
	"testing"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/workspace"
)

func TestFileReferencePrompt(t *testing.T) {
	root, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatalf("workspace.Open: %v", err)
	}
	t.Cleanup(func() { _ = root.Close() })
	if err := root.WriteFile("notes.txt", []byte("hello from file"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	sess := session.New("refs")
	if err := sess.Append(t.Context(), session.NewMessage(sess.ID(), llm.Message{
		Role:    llm.RoleUser,
		Content: "please inspect @notes.txt",
	})); err != nil {
		t.Fatalf("Append: %v", err)
	}

	req := &llm.Request{}
	proc := FileReferencePrompt(root, FileReferenceOptions{})
	if err := proc.ApplyRequest(t.Context(), nil, "", sess, req); err != nil {
		t.Fatalf("ApplyRequest: %v", err)
	}
	if len(req.Messages) != 1 {
		t.Fatalf("expected 1 injected message, got %d", len(req.Messages))
	}
	if req.Messages[0].Role != llm.RoleSystem {
		t.Fatalf("expected system role, got %s", req.Messages[0].Role)
	}
	if req.Messages[0].Content == "" ||
		!contains(req.Messages[0].Content, "notes.txt", "hello from file") {
		t.Fatalf("unexpected file reference content: %q", req.Messages[0].Content)
	}
}

func contains(s string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(s, part) {
			return false
		}
	}
	return true
}
