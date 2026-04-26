package session

import (
	"strings"
	"testing"

	"github.com/nijaru/canto/llm"
)

func TestContextAddedIsModelVisibleButNotTranscript(t *testing.T) {
	sess := New("context-session")
	if err := sess.AppendContext(t.Context(), ContextEntry{
		Kind:    ContextKindBootstrap,
		Content: "workspace context",
	}); err != nil {
		t.Fatalf("AppendContext: %v", err)
	}
	if err := sess.AppendUser(t.Context(), "hello"); err != nil {
		t.Fatalf("AppendUser: %v", err)
	}

	if got := sess.Messages(); len(got) != 1 || got[0].Content != "hello" {
		t.Fatalf("expected raw transcript only, got %#v", got)
	}

	effective, err := sess.EffectiveMessages()
	if err != nil {
		t.Fatalf("EffectiveMessages: %v", err)
	}
	if len(effective) != 2 {
		t.Fatalf("expected context plus transcript, got %#v", effective)
	}
	if effective[0].Role != llm.RoleUser ||
		!strings.Contains(effective[0].Content, "workspace context") {
		t.Fatalf("expected context as user-role history, got %#v", effective[0])
	}
	if effective[1].Content != "hello" {
		t.Fatalf("expected transcript after context, got %#v", effective)
	}
}
