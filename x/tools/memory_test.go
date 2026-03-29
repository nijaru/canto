package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/nijaru/canto/memory"
)

type stubWriter struct {
	last memory.WriteInput
}

func (s *stubWriter) Write(_ context.Context, input memory.WriteInput) (memory.WriteResult, error) {
	s.last = input
	return memory.WriteResult{Stored: 1, IDs: []string{"stub"}}, nil
}

type stubRetriever struct {
	results []memory.Memory
}

func (s stubRetriever) Retrieve(context.Context, memory.Query) ([]memory.Memory, error) {
	return s.results, nil
}

func TestRememberTool_Spec(t *testing.T) {
	tool := &RememberTool{}
	spec := tool.Spec()
	if spec.Name != "remember_memory" {
		t.Errorf("expected name 'remember_memory', got %q", spec.Name)
	}
	if spec.Description == "" {
		t.Error("expected non-empty description")
	}
}

func TestRecallTool_Spec(t *testing.T) {
	tool := &RecallTool{}
	spec := tool.Spec()
	if spec.Name != "recall_memory" {
		t.Errorf("expected name 'recall_memory', got %q", spec.Name)
	}
	if spec.Description == "" {
		t.Error("expected non-empty description")
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
		Writer:    manager,
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
		Retriever:  manager,
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

func TestRememberTool_UsesWriterInterface(t *testing.T) {
	writer := &stubWriter{}
	tool := &RememberTool{
		Writer:    writer,
		Namespace: memory.Namespace{Scope: memory.ScopeUser, ID: "u1"},
		Role:      memory.RoleSemantic,
	}
	out, err := tool.Execute(t.Context(), `{"content":"stubbed","key":"pref"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if writer.last.Content != "stubbed" || writer.last.Key != "pref" {
		t.Fatalf("unexpected write input: %#v", writer.last)
	}
	if !strings.Contains(out, "\"Stored\": 1") {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestRecallTool_UsesRetrieverInterface(t *testing.T) {
	tool := &RecallTool{
		Retriever: stubRetriever{
			results: []memory.Memory{
				{
					ID:        "m1",
					Namespace: memory.Namespace{Scope: memory.ScopeUser, ID: "u1"},
					Role:      memory.RoleSemantic,
					Content:   "stubbed memory",
				},
			},
		},
		Limit: 5,
	}
	out, err := tool.Execute(t.Context(), `{"query":"stubbed"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "stubbed memory") {
		t.Fatalf("unexpected output: %s", out)
	}
}
