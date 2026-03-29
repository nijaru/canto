package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/nijaru/canto/memory"
)

// mockEmbedder implements llm.Embedder exclusively for tests without making real API calls.
type mockEmbedder struct{}

func (m *mockEmbedder) EmbedContent(ctx context.Context, text string) ([]float32, error) {
	// Simple deterministic mock embedding based on character content for unit testing
	vec := make([]float32, 3)
	if strings.Contains(text, "query") {
		vec[0] = 0.9
		vec[1] = 0.1
	} else if strings.Contains(text, "red") {
		vec[0] = 1.0
	} else {
		vec[1] = 1.0
	}
	return vec, nil
}

func TestArchivalMemoryInsertTool_Spec(t *testing.T) {
	tool := &ArchivalMemoryInsertTool{}
	spec := tool.Spec()
	if spec.Name != "archival_memory_insert" {
		t.Errorf("expected name 'archival_memory_insert', got %q", spec.Name)
	}
	if spec.Description == "" {
		t.Error("expected non-empty description")
	}
}

func TestArchivalMemorySearchTool_Spec(t *testing.T) {
	tool := &ArchivalMemorySearchTool{}
	spec := tool.Spec()
	if spec.Name != "archival_memory_search" {
		t.Errorf("expected name 'archival_memory_search', got %q", spec.Name)
	}
	if spec.Description == "" {
		t.Error("expected non-empty description")
	}
}

func TestArchivalMemoryInsertTool(t *testing.T) {
	ctx := context.Background()

	// Use pure Go SQLite-less brute force or a temp HNSW.
	// We'll use SQLiteVectorStore with a temporary in-memory db for testing just the Tool interface cleanly.
	store, err := memory.NewSQLiteVectorStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("failed to init store: %v", err)
	}

	insertTool := ArchivalMemoryInsertTool{
		Store:    store,
		Embedder: &mockEmbedder{},
	}

	res, err := insertTool.Execute(ctx, `{"content": "the sky is red", "source": "user"}`)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	if !strings.Contains(res, "Successfully memorized") {
		t.Errorf("expected success message, got: %s", res)
	}
}

func TestArchivalMemorySearchTool(t *testing.T) {
	ctx := context.Background()
	store, _ := memory.NewSQLiteVectorStore("file::memory:?cache=shared")

	// Pre-seed some mock data directly
	_ = store.Upsert(
		ctx,
		"id1",
		[]float32{0.9, 0.1, 0.0},
		map[string]any{"content": "the sky query", "source": "log"},
	)

	searchTool := ArchivalMemorySearchTool{
		Store:    store,
		Embedder: &mockEmbedder{},
		TopK:     2,
	}

	res, err := searchTool.Execute(ctx, `{"query": "my visual query"}`)
	if err != nil {
		t.Fatalf("execute search failed: %v", err)
	}

	if !strings.Contains(res, "the sky query") {
		t.Errorf("expected memory retrieval content, got: %s", res)
	}

	if !strings.Contains(res, "log") {
		t.Errorf("expected source in result, got: %s", res)
	}
}

func TestRememberAndRecallTool(t *testing.T) {
	ctx := context.Background()
	store, err := memory.NewCoreStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewCoreStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	manager := memory.NewManager(store, nil, nil, memory.WritePolicy{})
	namespace := memory.Namespace{Scope: memory.ScopeUser, ID: "u1"}

	remember := &RememberTool{
		Manager:   manager,
		Namespace: namespace,
		Role:      memory.RoleSemantic,
	}
	if _, err := remember.Execute(ctx, `{"content":"user likes tea","key":"pref"}`); err != nil {
		t.Fatalf("remember: %v", err)
	}

	if err := manager.UpsertBlock(ctx, namespace, "persona", "Be concise", nil); err != nil {
		t.Fatalf("UpsertBlock: %v", err)
	}

	recall := &RecallTool{
		Manager:    manager,
		Namespaces: []memory.Namespace{namespace},
		Roles:      []memory.Role{memory.RoleCore, memory.RoleSemantic},
		Limit:      5,
	}
	out, err := recall.Execute(ctx, `{"query":"tea"}`)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if !strings.Contains(out, "user likes tea") {
		t.Fatalf("expected semantic memory in output: %s", out)
	}
}
