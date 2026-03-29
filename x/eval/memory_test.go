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

func TestEvaluateMemoryCases_UsesAssertions(t *testing.T) {
	store, err := memory.NewCoreStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewCoreStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	manager := memory.NewManager(store, nil, nil, memory.WritePolicy{})
	userNS := memory.Namespace{Scope: memory.ScopeUser, ID: "u-assert"}
	otherNS := memory.Namespace{Scope: memory.ScopeUser, ID: "u-other"}

	if err := manager.UpsertBlock(t.Context(), userNS, "persona", "Helpful assistant", nil); err != nil {
		t.Fatalf("UpsertBlock: %v", err)
	}
	if _, err := manager.Write(t.Context(), memory.WriteInput{
		Namespace: userNS,
		Role:      memory.RoleSemantic,
		Content:   "User likes sencha",
	}); err != nil {
		t.Fatalf("Write userNS: %v", err)
	}
	if _, err := manager.Write(t.Context(), memory.WriteInput{
		Namespace: otherNS,
		Role:      memory.RoleSemantic,
		Content:   "Other user likes coffee",
	}); err != nil {
		t.Fatalf("Write otherNS: %v", err)
	}

	results, err := EvaluateMemoryCases(t.Context(), manager, []MemoryCase{{
		Name: "keeps scope and role boundaries",
		Query: memory.Query{
			Namespaces: []memory.Namespace{userNS},
			Roles:      []memory.Role{memory.RoleCore, memory.RoleSemantic},
			Text:       "sencha",
			Limit:      5,
		},
		Expect: MemoryExpectation{
			Contains: []string{"Helpful assistant", "User likes sencha"},
			Excludes: []string{"Other user likes coffee"},
		},
		Assert: func(hits []memory.Memory) error {
			for _, assert := range []MemoryAssertion{
				RequireRoles(memory.RoleCore, memory.RoleSemantic),
				ExcludeNamespaces(otherNS),
			} {
				if err := assert(hits); err != nil {
					return err
				}
			}
			return nil
		},
	}})
	if err != nil {
		t.Fatalf("EvaluateMemoryCases: %v", err)
	}
	if len(results) != 1 || !results[0].Passed {
		t.Fatalf("unexpected eval results: %#v", results)
	}
}

func TestMemoryAssertions(t *testing.T) {
	hits := []memory.Memory{{
		ID:        "m1",
		Namespace: memory.Namespace{Scope: memory.ScopeUser, ID: "u1"},
		Role:      memory.RoleSemantic,
		Content:   "semantic hit",
	}}

	if err := RequireRoles(memory.RoleSemantic)(hits); err != nil {
		t.Fatalf("RequireRoles semantic: %v", err)
	}
	if err := ExcludeNamespaces(memory.Namespace{Scope: memory.ScopeUser, ID: "u2"})(hits); err != nil {
		t.Fatalf("ExcludeNamespaces u2: %v", err)
	}
	if err := RequireRoles(memory.RoleCore)(hits); err == nil {
		t.Fatalf("expected missing role error, got %v", err)
	}
	if err := ExcludeNamespaces(memory.Namespace{Scope: memory.ScopeUser, ID: "u1"})(hits); err == nil {
		t.Fatalf("expected namespace exclusion error, got %v", err)
	}
}
