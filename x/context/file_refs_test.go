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
	if req.Messages[0].Role != llm.RoleUser {
		t.Fatalf("expected user context role, got %s", req.Messages[0].Role)
	}
	if req.Messages[0].Content == "" ||
		!contains(req.Messages[0].Content, "notes.txt", "hello from file", "sha256:") {
		t.Fatalf("unexpected file reference content: %q", req.Messages[0].Content)
	}
}

func TestFileReferencePromptDeduplicatesRepeatedRefs(t *testing.T) {
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
		Content: "please inspect @notes.txt and @notes.txt again",
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
	if count := strings.Count(req.Messages[0].Content, "<file path="); count != 1 {
		t.Fatalf("expected one injected file block, got %d in %q", count, req.Messages[0].Content)
	}
}

func TestFileReferencePromptDeduplicatesAcrossSession(t *testing.T) {
	root, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatalf("workspace.Open: %v", err)
	}
	t.Cleanup(func() { _ = root.Close() })
	if err := root.WriteFile("notes.txt", []byte("hello from file"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	sess := session.New("refs")
	mutator := RecordFileReferences(root, FileReferenceOptions{})
	proc := FileReferencePrompt(root, FileReferenceOptions{})

	appendUser := func(content string) {
		if err := sess.Append(t.Context(), session.NewMessage(sess.ID(), llm.Message{
			Role:    llm.RoleUser,
			Content: content,
		})); err != nil {
			t.Fatalf("Append: %v", err)
		}
		if err := mutator.Mutate(t.Context(), nil, "", sess); err != nil {
			t.Fatalf("Mutate: %v", err)
		}
	}

	appendUser("please inspect @notes.txt")
	req := &llm.Request{}
	if err := proc.ApplyRequest(t.Context(), nil, "", sess, req); err != nil {
		t.Fatalf("ApplyRequest: %v", err)
	}
	if !contains(req.Messages[0].Content, "hello from file") {
		t.Fatalf("expected initial full file content, got %q", req.Messages[0].Content)
	}

	appendUser("please inspect @notes.txt again")
	req = &llm.Request{}
	if err := proc.ApplyRequest(t.Context(), nil, "", sess, req); err != nil {
		t.Fatalf("ApplyRequest: %v", err)
	}
	if contains(req.Messages[0].Content, "hello from file") {
		t.Fatalf("expected cached reference without full content, got %q", req.Messages[0].Content)
	}
	if !contains(req.Messages[0].Content, `cached="true"`) {
		t.Fatalf("expected cached file reference, got %q", req.Messages[0].Content)
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
