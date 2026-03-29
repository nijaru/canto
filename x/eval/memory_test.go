package eval

import (
	"testing"

	"github.com/nijaru/canto/memory"
)

func TestEvaluateMemoryCases(t *testing.T) {
	store, err := memory.NewCoreStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewCoreStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	manager := memory.NewManager(store, nil, nil, memory.WritePolicy{})
	ns := memory.Namespace{Scope: memory.ScopeUser, ID: "u1"}
	if _, err := manager.Write(t.Context(), memory.WriteInput{
		Namespace: ns,
		Role:      memory.RoleSemantic,
		Content:   "User likes tea",
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	results, err := EvaluateMemoryCases(t.Context(), manager, []MemoryCase{{
		Name: "retains semantic memory",
		Query: memory.Query{
			Namespaces: []memory.Namespace{ns},
			Roles:      []memory.Role{memory.RoleSemantic},
			Text:       "tea",
			Limit:      5,
		},
		Expect: MemoryExpectation{
			Contains: []string{"User likes tea"},
			Excludes: []string{"coffee"},
		},
	}})
	if err != nil {
		t.Fatalf("EvaluateMemoryCases: %v", err)
	}
	if len(results) != 1 || !results[0].Passed {
		t.Fatalf("unexpected eval results: %#v", results)
	}
}
