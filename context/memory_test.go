package context

import (
	"strings"
	"testing"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/memory"
	"github.com/nijaru/canto/session"
)

func TestMemoryPrompt_InjectsRetrievedMemory(t *testing.T) {
	store, err := memory.NewCoreStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewCoreStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	manager := memory.NewManager(store, nil, nil, memory.WritePolicy{})
	namespace := memory.Namespace{Scope: memory.ScopeThread, ID: "thread-1"}
	if err := manager.UpsertBlock(t.Context(), namespace, "persona", "Agent Name: Archivist", nil); err != nil {
		t.Fatalf("UpsertBlock: %v", err)
	}
	if _, err := manager.Write(t.Context(), memory.WriteInput{
		Namespace: namespace,
		Role:      memory.RoleSemantic,
		Content:   "User likes tea",
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	sess := session.New("thread-1")
	if err := sess.Append(t.Context(), session.NewMessage(sess.ID(), llm.Message{
		Role:    llm.RoleUser,
		Content: "tea",
	})); err != nil {
		t.Fatalf("Append: %v", err)
	}
	req := &llm.Request{}
	proc := MemoryPrompt(manager, MemoryPromptOptions{
		Namespaces: []memory.Namespace{namespace},
		Roles:      []memory.Role{memory.RoleCore, memory.RoleSemantic},
		Limit:      5,
	})
	if err := proc.ApplyRequest(t.Context(), nil, "", sess, req); err != nil {
		t.Fatalf("ApplyRequest: %v", err)
	}
	if len(req.Messages) != 1 {
		t.Fatalf("expected 1 injected system message, got %d", len(req.Messages))
	}
	if req.Messages[0].Role != llm.RoleSystem {
		t.Fatalf("expected system role, got %s", req.Messages[0].Role)
	}
	if req.Messages[0].Content == "" || !containsAll(req.Messages[0].Content, "Archivist", "User likes tea") {
		t.Fatalf("unexpected memory prompt: %q", req.Messages[0].Content)
	}
}

func containsAll(s string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(s, part) {
			return false
		}
	}
	return true
}
