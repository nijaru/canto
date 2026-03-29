package memory

import (
	"context"
	"errors"
	"testing"
)

type testEmbedder struct{}

func (e testEmbedder) EmbedContent(_ context.Context, text string) ([]float32, error) {
	v := make([]float32, 4)
	for i, r := range text {
		v[i%4] += float32(r)
	}
	return v, nil
}

func TestManager_ScopeIsolationAndBlocks(t *testing.T) {
	store, err := NewCoreStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewCoreStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	manager := NewManager(store)
	threadA := Namespace{Scope: ScopeThread, ID: "thread-a"}
	threadB := Namespace{Scope: ScopeThread, ID: "thread-b"}

	if err := manager.UpsertBlock(t.Context(), threadA, "persona", "A content", nil); err != nil {
		t.Fatalf("UpsertBlock A: %v", err)
	}
	if err := manager.UpsertBlock(t.Context(), threadB, "persona", "B content", nil); err != nil {
		t.Fatalf("UpsertBlock B: %v", err)
	}

	results, err := manager.Retrieve(t.Context(), Query{
		Namespaces:  []Namespace{threadA},
		Roles:       []Role{RoleCore},
		IncludeCore: true,
		Limit:       5,
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(results) != 1 || results[0].Content != "A content" {
		t.Fatalf("unexpected results: %#v", results)
	}
}

func TestManager_WriteConflictModesAndRetrieve(t *testing.T) {
	store, err := NewCoreStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewCoreStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ns := Namespace{Scope: ScopeUser, ID: "user-1"}
	manager := NewManager(store, WithWritePolicy(WritePolicy{ConflictMode: ConflictMerge}))
	if _, err := manager.Write(t.Context(), WriteInput{
		Namespace: ns,
		Role:      RoleSemantic,
		Key:       "favorite",
		Content:   "User likes tea",
	}); err != nil {
		t.Fatalf("Write first: %v", err)
	}
	if _, err := manager.Write(t.Context(), WriteInput{
		Namespace: ns,
		Role:      RoleSemantic,
		Key:       "favorite",
		Content:   "User likes coffee",
	}); err != nil {
		t.Fatalf("Write second: %v", err)
	}

	results, err := manager.Retrieve(t.Context(), Query{
		Namespaces: []Namespace{ns},
		Roles:      []Role{RoleSemantic},
		Text:       "likes",
		Limit:      5,
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 merged memory, got %#v", results)
	}
	if results[0].Content != "User likes tea\nUser likes coffee" {
		t.Fatalf("unexpected merged content: %q", results[0].Content)
	}
}

func TestManager_SemanticRetrieval(t *testing.T) {
	store, err := NewCoreStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewCoreStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	vector, err := NewSQLiteVectorStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewSQLiteVectorStore: %v", err)
	}
	t.Cleanup(func() { _ = vector.Close() })

	manager := NewManager(store, WithVectorStore(vector), WithEmbedder(testEmbedder{}))
	ns := Namespace{Scope: ScopeWorkspace, ID: "repo"}
	if _, err := manager.Write(t.Context(), WriteInput{
		Namespace: ns,
		Role:      RoleSemantic,
		Key:       "tooling",
		Content:   "The repo uses Bun for TypeScript tasks",
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	results, err := manager.Retrieve(t.Context(), Query{
		Namespaces:  []Namespace{ns},
		Roles:       []Role{RoleSemantic},
		Text:        "TypeScript tasks use Bun",
		UseSemantic: true,
		Limit:       5,
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected semantic retrieval results")
	}
}

func TestManager_AsyncWrite(t *testing.T) {
	store, err := NewCoreStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewCoreStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	manager := NewManager(store, WithWritePolicy(WritePolicy{DefaultMode: WriteAsync}))
	ns := Namespace{Scope: ScopeAgent, ID: "agent-1"}
	result, err := manager.Write(t.Context(), WriteInput{
		Namespace: ns,
		Role:      RoleEpisodic,
		Content:   "Agent completed the task.",
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if result.Pending != 1 {
		t.Fatalf("expected async pending write, got %#v", result)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	results, err := manager.Retrieve(t.Context(), Query{
		Namespaces: []Namespace{ns},
		Roles:      []Role{RoleEpisodic},
		Text:       "completed",
		Limit:      5,
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected async memory to persist, got %#v", results)
	}
}

func TestManager_IncludeRecentControlsQuerylessRecall(t *testing.T) {
	store, err := NewCoreStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewCoreStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	manager := NewManager(store)
	ns := Namespace{Scope: ScopeUser, ID: "user-recent"}
	if _, err := manager.Write(t.Context(), WriteInput{
		Namespace: ns,
		Role:      RoleEpisodic,
		Content:   "User finished onboarding yesterday.",
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	results, err := manager.Retrieve(t.Context(), Query{
		Namespaces: []Namespace{ns},
		Roles:      []Role{RoleEpisodic},
		Limit:      5,
	})
	if err != nil {
		t.Fatalf("Retrieve without recent: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected no queryless recall without IncludeRecent, got %#v", results)
	}

	results, err = manager.Retrieve(t.Context(), Query{
		Namespaces:    []Namespace{ns},
		Roles:         []Role{RoleEpisodic},
		IncludeRecent: true,
		Limit:         5,
	})
	if err != nil {
		t.Fatalf("Retrieve with recent: %v", err)
	}
	if len(results) != 1 || results[0].Content != "User finished onboarding yesterday." {
		t.Fatalf("expected recent recall when IncludeRecent is true, got %#v", results)
	}
}

func TestManager_RetrievePolicyPostprocess(t *testing.T) {
	store, err := NewCoreStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewCoreStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ns := Namespace{Scope: ScopeUser, ID: "user-postprocess"}
	manager := NewManager(store, WithRetrievePolicy(RetrievePolicy{
		Postprocess: func(query Query, results []Memory) ([]Memory, error) {
			if query.Text != "tea" {
				t.Fatalf("unexpected query: %#v", query)
			}
			filtered := results[:0]
			for _, result := range results {
				if result.Role == RoleSemantic {
					filtered = append(filtered, result)
				}
			}
			return filtered, nil
		},
	}))
	if err := manager.UpsertBlock(t.Context(), ns, "persona", "Should be filtered", nil); err != nil {
		t.Fatalf("UpsertBlock: %v", err)
	}
	if _, err := manager.Write(t.Context(), WriteInput{
		Namespace: ns,
		Role:      RoleSemantic,
		Content:   "User likes tea",
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	results, err := manager.Retrieve(t.Context(), Query{
		Namespaces:  []Namespace{ns},
		Roles:       []Role{RoleCore, RoleSemantic},
		IncludeCore: true,
		Text:        "tea",
		Limit:       5,
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(results) != 1 || results[0].Role != RoleSemantic {
		t.Fatalf("expected semantic-only results after postprocess, got %#v", results)
	}
}

func TestManager_RetrievePolicyError(t *testing.T) {
	store, err := NewCoreStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewCoreStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	want := errors.New("boom")
	manager := NewManager(store, WithRetrievePolicy(RetrievePolicy{
		Postprocess: func(Query, []Memory) ([]Memory, error) {
			return nil, want
		},
	}))
	if _, err := manager.Write(t.Context(), WriteInput{
		Namespace: Namespace{Scope: ScopeUser, ID: "u-err"},
		Role:      RoleSemantic,
		Content:   "User likes tea",
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	_, err = manager.Retrieve(t.Context(), Query{
		Namespaces: []Namespace{{Scope: ScopeUser, ID: "u-err"}},
		Roles:      []Role{RoleSemantic},
		Text:       "tea",
		Limit:      5,
	})
	if !errors.Is(err, want) {
		t.Fatalf("expected %v, got %v", want, err)
	}
}
