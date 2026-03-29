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
	last    memory.Query
}

func (s *stubRetriever) Retrieve(_ context.Context, query memory.Query) ([]memory.Memory, error) {
	s.last = query
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

	manager := memory.NewManager(store)
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

func TestRememberTool_ParsesLifecycleFields(t *testing.T) {
	writer := &stubWriter{}
	tool := &RememberTool{
		Writer:    writer,
		Namespace: memory.Namespace{Scope: memory.ScopeUser, ID: "u1"},
		Role:      memory.RoleSemantic,
	}
	out, err := tool.Execute(
		t.Context(),
		`{"content":"fresh","observed_at":"2026-03-29T08:00:00Z","valid_from":"2026-03-29T09:00:00Z","valid_to":"2026-03-29T10:00:00Z","supersedes":"old-id"}`,
	)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if writer.last.ObservedAt == nil || writer.last.ValidFrom == nil || writer.last.ValidTo == nil {
		t.Fatalf("expected lifecycle times to be parsed: %#v", writer.last)
	}
	if writer.last.Supersedes != "old-id" {
		t.Fatalf("expected supersedes to be captured: %#v", writer.last)
	}
	if !strings.Contains(out, "\"Stored\": 1") {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestRecallTool_UsesRetrieverInterface(t *testing.T) {
	retriever := &stubRetriever{
		results: []memory.Memory{
			{
				ID:        "m1",
				Namespace: memory.Namespace{Scope: memory.ScopeUser, ID: "u1"},
				Role:      memory.RoleSemantic,
				Content:   "stubbed memory",
			},
		},
	}
	tool := &RecallTool{
		Retriever: retriever,
		Limit:     5,
	}
	out, err := tool.Execute(t.Context(), `{"query":"stubbed"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "stubbed memory") {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestRecallTool_ParsesLifecycleFilters(t *testing.T) {
	retriever := &stubRetriever{
		results: []memory.Memory{
			{
				ID:        "m1",
				Namespace: memory.Namespace{Scope: memory.ScopeUser, ID: "u1"},
				Role:      memory.RoleSemantic,
				Content:   "stubbed memory",
			},
		},
	}
	tool := &RecallTool{
		Retriever: retriever,
		Limit:     5,
	}
	out, err := tool.Execute(
		t.Context(),
		`{"include_recent":true,"include_forgotten":true,"include_superseded":true,"valid_at":"2026-03-29T10:00:00Z","observed_after":"2026-03-29T08:00:00Z","observed_before":"2026-03-29T12:00:00Z"}`,
	)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !retriever.last.IncludeRecent || !retriever.last.IncludeForgotten ||
		!retriever.last.IncludeSuperseded {
		t.Fatalf("expected lifecycle flags in query: %#v", retriever.last)
	}
	if retriever.last.ValidAt == nil || retriever.last.ObservedAfter == nil ||
		retriever.last.ObservedBefore == nil {
		t.Fatalf("expected lifecycle times in query: %#v", retriever.last)
	}
	if !strings.Contains(out, "stubbed memory") {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestRecallTool_RespectsConfiguredRoles(t *testing.T) {
	ctx := context.Background()
	store, err := memory.NewCoreStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewCoreStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	manager := memory.NewManager(store)
	namespace := memory.Namespace{Scope: memory.ScopeUser, ID: "u-role-filter"}
	if err := manager.UpsertBlock(ctx, namespace, "persona", "Should not appear", nil); err != nil {
		t.Fatalf("UpsertBlock: %v", err)
	}
	if _, err := manager.Write(ctx, memory.WriteInput{
		Namespace: namespace,
		Role:      memory.RoleSemantic,
		Content:   "User likes hojicha",
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	recall := &RecallTool{
		Retriever:  manager,
		Namespaces: []memory.Namespace{namespace},
		Roles:      []memory.Role{memory.RoleSemantic},
		Limit:      5,
	}
	out, err := recall.Execute(ctx, `{"query":"hojicha"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.Contains(out, "Should not appear") {
		t.Fatalf("expected recall output to respect configured roles: %s", out)
	}
	if !strings.Contains(out, "User likes hojicha") {
		t.Fatalf("expected semantic memory in output: %s", out)
	}
}
