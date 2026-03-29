package context

import (
	"context"
	"strings"
	"testing"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/memory"
	"github.com/nijaru/canto/session"
)

type stubRetriever struct {
	results []memory.Memory
}

func (s stubRetriever) Retrieve(context.Context, memory.Query) ([]memory.Memory, error) {
	return s.results, nil
}

func newTestCoreStore(t *testing.T) *memory.CoreStore {
	t.Helper()
	dsn := "file::memory:?cache=shared&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	store, err := memory.NewCoreStore(dsn)
	if err != nil {
		t.Fatalf("NewCoreStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestMemoryPrompt_InjectsRetrievedMemory(t *testing.T) {
	store := newTestCoreStore(t)
	manager := memory.NewManager(store)
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
	if req.Messages[0].Content == "" ||
		!containsAll(req.Messages[0].Content, "Archivist", "User likes tea") {
		t.Fatalf("unexpected memory prompt: %q", req.Messages[0].Content)
	}
}

func TestMemoryPrompt_ReplacesExistingBlock(t *testing.T) {
	store := newTestCoreStore(t)
	manager := memory.NewManager(store)
	namespace := memory.Namespace{Scope: memory.ScopeThread, ID: "thread-2"}
	if err := manager.UpsertBlock(t.Context(), namespace, "persona", "Agent Name: Updated", nil); err != nil {
		t.Fatalf("UpsertBlock: %v", err)
	}

	sess := session.New("thread-2")
	req := &llm.Request{
		Messages: []llm.Message{
			{
				Role: llm.RoleSystem,
				Content: "<memory_context>\nold memory\n</memory_context>\n\n" +
					"Original system text.",
			},
		},
	}
	proc := MemoryPrompt(manager, MemoryPromptOptions{
		Namespaces: []memory.Namespace{namespace},
		Roles:      []memory.Role{memory.RoleCore},
		Limit:      5,
		Query:      "persona",
	})
	if err := proc.ApplyRequest(t.Context(), nil, "", sess, req); err != nil {
		t.Fatalf("ApplyRequest: %v", err)
	}

	content := req.Messages[0].Content
	if strings.Contains(content, "old memory") {
		t.Fatalf("expected old memory block to be replaced: %q", content)
	}
	if !containsAll(content, "Updated", "Original system text.") {
		t.Fatalf("expected updated memory block plus original instructions: %q", content)
	}
}

func TestMemoryPrompt_UsesEffectiveMessagesAfterCompaction(t *testing.T) {
	store := newTestCoreStore(t)
	manager := memory.NewManager(store)
	namespace := memory.Namespace{Scope: memory.ScopeThread, ID: "km-compacted"}
	if _, err := manager.Write(t.Context(), memory.WriteInput{
		Namespace: namespace,
		Role:      memory.RoleSemantic,
		Content:   "uniqueeffectivetoken",
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	ctx := context.Background()
	sess := session.New("km-compacted")
	if err := sess.Append(ctx, session.NewMessage(sess.ID(), llm.Message{
		Role:    llm.RoleUser,
		Content: "uniquerawtoken",
	})); err != nil {
		t.Fatalf("append raw: %v", err)
	}

	events := sess.Events()
	snapshot := session.CompactionSnapshot{
		Strategy:      "summarize",
		CutoffEventID: events[len(events)-1].ID.String(),
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "uniqueeffectivetoken"},
		},
	}
	if err := sess.Append(ctx, session.NewCompactionEvent(sess.ID(), snapshot)); err != nil {
		t.Fatalf("append compaction: %v", err)
	}

	req := &llm.Request{}
	proc := MemoryPrompt(manager, MemoryPromptOptions{
		Namespaces: []memory.Namespace{namespace},
		Roles:      []memory.Role{memory.RoleSemantic},
		Limit:      5,
	})
	if err := proc.ApplyRequest(ctx, nil, "", sess, req); err != nil {
		t.Fatalf("ApplyRequest: %v", err)
	}

	if len(req.Messages) != 1 ||
		!strings.Contains(req.Messages[0].Content, "uniqueeffectivetoken") {
		t.Fatalf("expected memory prompt to use effective history query: %#v", req.Messages)
	}
}

func TestMemoryPrompt_NilManager(t *testing.T) {
	sess := session.New("thread-nil")
	req := &llm.Request{}
	proc := MemoryPrompt(nil, MemoryPromptOptions{})
	if err := proc.ApplyRequest(t.Context(), nil, "", sess, req); err != nil {
		t.Fatalf("ApplyRequest: %v", err)
	}
	if len(req.Messages) != 0 {
		t.Fatalf("expected no injected messages, got %#v", req.Messages)
	}
}

func TestMemoryPrompt_UsesRetrieverInterface(t *testing.T) {
	sess := session.New("thread-stub")
	req := &llm.Request{}
	proc := MemoryPrompt(stubRetriever{
		results: []memory.Memory{
			{
				ID:        "m1",
				Namespace: memory.Namespace{Scope: memory.ScopeUser, ID: "u1"},
				Role:      memory.RoleSemantic,
				Content:   "Stubbed memory",
			},
		},
	}, MemoryPromptOptions{Limit: 5})
	if err := proc.ApplyRequest(t.Context(), nil, "", sess, req); err != nil {
		t.Fatalf("ApplyRequest: %v", err)
	}
	if len(req.Messages) != 1 || !strings.Contains(req.Messages[0].Content, "Stubbed memory") {
		t.Fatalf("expected injected stub memory, got %#v", req.Messages)
	}
}

func TestMemoryPrompt_RespectsRoleSelection(t *testing.T) {
	store := newTestCoreStore(t)
	manager := memory.NewManager(store)
	namespace := memory.Namespace{Scope: memory.ScopeThread, ID: "thread-role-filter"}
	if err := manager.UpsertBlock(t.Context(), namespace, "persona", "Do not leak me", nil); err != nil {
		t.Fatalf("UpsertBlock: %v", err)
	}
	if _, err := manager.Write(t.Context(), memory.WriteInput{
		Namespace: namespace,
		Role:      memory.RoleSemantic,
		Content:   "User prefers black tea",
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	sess := session.New("thread-role-filter")
	if err := sess.Append(t.Context(), session.NewMessage(sess.ID(), llm.Message{
		Role:    llm.RoleUser,
		Content: "tea",
	})); err != nil {
		t.Fatalf("Append: %v", err)
	}

	req := &llm.Request{}
	proc := MemoryPrompt(manager, MemoryPromptOptions{
		Namespaces: []memory.Namespace{namespace},
		Roles:      []memory.Role{memory.RoleSemantic},
		Limit:      5,
	})
	if err := proc.ApplyRequest(t.Context(), nil, "", sess, req); err != nil {
		t.Fatalf("ApplyRequest: %v", err)
	}
	if len(req.Messages) != 1 {
		t.Fatalf("expected 1 injected system message, got %d", len(req.Messages))
	}
	if strings.Contains(req.Messages[0].Content, "Do not leak me") {
		t.Fatalf(
			"expected core block to stay out of semantic-only recall: %q",
			req.Messages[0].Content,
		)
	}
	if !strings.Contains(req.Messages[0].Content, "User prefers black tea") {
		t.Fatalf("expected semantic memory to be present: %q", req.Messages[0].Content)
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
