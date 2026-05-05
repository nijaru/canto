package memory

import (
	"context"
	"errors"
	"testing"
	"time"
)

type testEmbedder struct{}

func (e testEmbedder) EmbedContent(_ context.Context, text string) ([]float32, error) {
	v := make([]float32, 4)
	for i, r := range text {
		v[i%4] += float32(r)
	}
	return v, nil
}

type vectorOnlyPlanner struct{}

func (p vectorOnlyPlanner) Plan(query Query, _ RetrievalCapabilities) []RetrievalRequest {
	limit := query.Limit
	if limit <= 0 {
		limit = 5
	}
	return []RetrievalRequest{{
		Source: RetrievalVector,
		Limit:  limit,
	}}
}

type failingMemoryStore struct {
	err error
}

func (s failingMemoryStore) GetBlock(context.Context, Namespace, string) (*Block, error) {
	return nil, s.err
}

func (s failingMemoryStore) ListBlocks(context.Context, BlockListInput) ([]Block, error) {
	return nil, s.err
}

func (s failingMemoryStore) UpsertBlock(context.Context, Block) error {
	return s.err
}

func (s failingMemoryStore) GetMemory(context.Context, string) (*Memory, error) {
	return nil, s.err
}

func (s failingMemoryStore) ListMemories(context.Context, MemoryListInput) ([]Memory, error) {
	return nil, s.err
}

func (s failingMemoryStore) UpsertMemory(context.Context, Memory) error {
	return s.err
}

func (s failingMemoryStore) SearchMemories(context.Context, SearchInput) ([]Memory, error) {
	return nil, s.err
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

func TestManager_RetrieveCoreBlocksKeepsNamespacesDistinct(t *testing.T) {
	store, err := NewCoreStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewCoreStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	manager := NewManager(store)
	threadA := Namespace{Scope: ScopeThread, ID: "core-ns-a"}
	threadB := Namespace{Scope: ScopeThread, ID: "core-ns-b"}
	if err := manager.UpsertBlock(t.Context(), threadA, "persona", "A content", nil); err != nil {
		t.Fatalf("UpsertBlock A: %v", err)
	}
	if err := manager.UpsertBlock(t.Context(), threadB, "persona", "B content", nil); err != nil {
		t.Fatalf("UpsertBlock B: %v", err)
	}

	results, err := manager.Retrieve(t.Context(), Query{
		Namespaces:  []Namespace{threadA, threadB},
		Roles:       []Role{RoleCore},
		IncludeCore: true,
		Limit:       5,
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected both core blocks, got %#v", results)
	}
	seen := map[Namespace]string{}
	for _, result := range results {
		if result.ID == "persona" {
			t.Fatalf("core memory ID must include namespace, got %#v", result)
		}
		seen[result.Namespace] = result.Content
	}
	if seen[threadA] != "A content" || seen[threadB] != "B content" {
		t.Fatalf("unexpected namespace contents: %#v", seen)
	}
}

func TestManager_UpsertBlockRequiresStore(t *testing.T) {
	manager := NewManager(nil)
	err := manager.UpsertBlock(
		t.Context(),
		Namespace{Scope: ScopeThread, ID: "thread"},
		"persona",
		"content",
		nil,
	)
	if err == nil {
		t.Fatal("expected missing store error")
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

	vector, err := NewVectorStore(t.Context(), "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewVectorStore: %v", err)
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

func TestManager_SemanticRetrievalRespectsMultipleNamespaces(t *testing.T) {
	store, err := NewCoreStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewCoreStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	vector, err := NewVectorStore(t.Context(), "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewVectorStore: %v", err)
	}
	t.Cleanup(func() { _ = vector.Close() })

	manager := NewManager(
		store,
		WithVectorStore(vector),
		WithEmbedder(testEmbedder{}),
		WithRetrievePolicy(RetrievePolicy{Planner: vectorOnlyPlanner{}}),
	)
	threadA := Namespace{Scope: ScopeThread, ID: "semantic-ns-a"}
	threadB := Namespace{Scope: ScopeThread, ID: "semantic-ns-b"}
	threadC := Namespace{Scope: ScopeThread, ID: "semantic-ns-c"}
	for _, input := range []WriteInput{
		{Namespace: threadA, Role: RoleSemantic, Key: "a", Content: "shared vector namespace token"},
		{Namespace: threadB, Role: RoleSemantic, Key: "b", Content: "shared vector namespace token"},
		{Namespace: threadC, Role: RoleSemantic, Key: "c", Content: "shared vector namespace token"},
	} {
		if _, err := manager.Write(t.Context(), input); err != nil {
			t.Fatalf("Write %s: %v", input.Namespace.ID, err)
		}
	}

	results, err := manager.Retrieve(t.Context(), Query{
		Namespaces:  []Namespace{threadA, threadB},
		Roles:       []Role{RoleSemantic},
		Text:        "shared vector namespace token",
		UseSemantic: true,
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 namespace-filtered semantic hits, got %#v", results)
	}
	for _, result := range results {
		if result.Namespace == threadC {
			t.Fatalf("unexpected semantic hit outside requested namespaces: %#v", results)
		}
	}
}

func TestManager_SemanticRetrievalRespectsMultipleRoles(t *testing.T) {
	store, err := NewCoreStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewCoreStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	vector, err := NewVectorStore(t.Context(), "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewVectorStore: %v", err)
	}
	t.Cleanup(func() { _ = vector.Close() })

	manager := NewManager(
		store,
		WithVectorStore(vector),
		WithEmbedder(testEmbedder{}),
		WithRetrievePolicy(RetrievePolicy{Planner: vectorOnlyPlanner{}}),
	)
	ns := Namespace{Scope: ScopeWorkspace, ID: "semantic-role-filter"}
	for _, input := range []WriteInput{
		{Namespace: ns, Role: RoleSemantic, Key: "semantic", Content: "shared vector role token"},
		{Namespace: ns, Role: RoleProcedural, Key: "procedural", Content: "shared vector role token"},
		{Namespace: ns, Role: RoleEpisodic, Key: "episodic", Content: "shared vector role token"},
	} {
		if _, err := manager.Write(t.Context(), input); err != nil {
			t.Fatalf("Write %s: %v", input.Role, err)
		}
	}

	results, err := manager.Retrieve(t.Context(), Query{
		Namespaces:  []Namespace{ns},
		Roles:       []Role{RoleSemantic, RoleProcedural},
		Text:        "shared vector role token",
		UseSemantic: true,
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 role-filtered semantic hits, got %#v", results)
	}
	for _, result := range results {
		if result.Role == RoleEpisodic {
			t.Fatalf("unexpected semantic hit outside requested roles: %#v", results)
		}
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

func TestManager_CloseReportsAsyncWriteError(t *testing.T) {
	writeErr := errors.New("store unavailable")
	manager := NewManager(failingMemoryStore{err: writeErr}, WithWritePolicy(WritePolicy{
		DefaultMode: WriteAsync,
	}))

	result, err := manager.Write(t.Context(), WriteInput{
		Namespace: Namespace{Scope: ScopeAgent, ID: "agent-async-error"},
		Role:      RoleEpisodic,
		Content:   "will fail asynchronously",
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if result.Pending != 1 {
		t.Fatalf("expected async pending write, got %#v", result)
	}
	if err := manager.Close(); !errors.Is(err, writeErr) {
		t.Fatalf("Close error = %v, want %v", err, writeErr)
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

func TestManagerMemoryContractAliases(t *testing.T) {
	store, err := NewCoreStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewCoreStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	manager := NewManager(store)
	ns := Namespace{Scope: ScopeAgent, ID: "agent-contract"}

	if _, err := manager.Remember(t.Context(), WriteInput{
		Namespace: ns,
		Role:      RoleEpisodic,
		Content:   "agent remembered this",
	}); err != nil {
		t.Fatalf("Remember: %v", err)
	}

	results, err := manager.Search(t.Context(), Query{
		Namespaces: []Namespace{ns},
		Roles:      []Role{RoleEpisodic},
		Text:       "remembered",
		Limit:      5,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].Content != "agent remembered this" {
		t.Fatalf("unexpected search results: %#v", results)
	}
}

func TestManagerCapabilities(t *testing.T) {
	store, err := NewCoreStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewCoreStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	manager := NewManager(store)
	caps := manager.Capabilities()
	if !caps.Namespaced || !caps.Blocks || !caps.Memories || !caps.Search || !caps.Forget ||
		!caps.Temporal ||
		!caps.AsyncWrite {
		t.Fatalf("expected core memory capabilities, got %#v", caps)
	}
	if caps.SemanticSearch {
		t.Fatalf("expected semantic search to be disabled without vector store, got %#v", caps)
	}

	vector, err := NewVectorStore(t.Context(), "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewVectorStore: %v", err)
	}
	t.Cleanup(func() { _ = vector.Close() })

	semanticManager := NewManager(store, WithVectorStore(vector), WithEmbedder(testEmbedder{}))
	semanticCaps := semanticManager.Capabilities()
	if !semanticCaps.SemanticSearch {
		t.Fatalf("expected semantic search capability with vector store, got %#v", semanticCaps)
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

func TestManager_ForgetExcludesByDefault(t *testing.T) {
	store, err := NewCoreStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewCoreStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	manager := NewManager(store)
	ns := Namespace{Scope: ScopeUser, ID: "user-forget"}
	result, err := manager.Write(t.Context(), WriteInput{
		Namespace: ns,
		Role:      RoleSemantic,
		Content:   "User likes genmaicha",
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(result.IDs) != 1 {
		t.Fatalf("expected 1 stored id, got %#v", result)
	}
	if err := manager.Forget(t.Context(), result.IDs[0], "stale"); err != nil {
		t.Fatalf("Forget: %v", err)
	}

	hits, err := manager.Retrieve(t.Context(), Query{
		Namespaces: []Namespace{ns},
		Roles:      []Role{RoleSemantic},
		Text:       "genmaicha",
		Limit:      5,
	})
	if err != nil {
		t.Fatalf("Retrieve default: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("expected forgotten memory to be hidden by default, got %#v", hits)
	}

	hits, err = manager.Retrieve(t.Context(), Query{
		Namespaces:       []Namespace{ns},
		Roles:            []Role{RoleSemantic},
		Text:             "genmaicha",
		IncludeForgotten: true,
		Limit:            5,
	})
	if err != nil {
		t.Fatalf("Retrieve include forgotten: %v", err)
	}
	if len(hits) != 1 || hits[0].ForgottenAt == nil {
		t.Fatalf("expected forgotten memory when explicitly included, got %#v", hits)
	}
}

func TestManager_SupersededMemoriesAreHiddenByDefault(t *testing.T) {
	store, err := NewCoreStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewCoreStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	manager := NewManager(store)
	ns := Namespace{Scope: ScopeUser, ID: "user-supersede"}
	first, err := manager.Write(t.Context(), WriteInput{
		Namespace: ns,
		Role:      RoleSemantic,
		Key:       "tea_pref_v1",
		Content:   "User likes green tea",
	})
	if err != nil {
		t.Fatalf("Write first: %v", err)
	}
	second, err := manager.Write(t.Context(), WriteInput{
		Namespace:  ns,
		Role:       RoleSemantic,
		Key:        "tea_pref_v2",
		Content:    "User prefers oolong tea",
		Supersedes: first.IDs[0],
	})
	if err != nil {
		t.Fatalf("Write second: %v", err)
	}
	if len(second.IDs) != 1 {
		t.Fatalf("expected second write id, got %#v", second)
	}

	hits, err := manager.Retrieve(t.Context(), Query{
		Namespaces: []Namespace{ns},
		Roles:      []Role{RoleSemantic},
		Text:       "tea",
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("Retrieve default: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != second.IDs[0] {
		t.Fatalf("expected only the successor by default, got %#v", hits)
	}

	hits, err = manager.Retrieve(t.Context(), Query{
		Namespaces:        []Namespace{ns},
		Roles:             []Role{RoleSemantic},
		Text:              "tea",
		IncludeSuperseded: true,
		Limit:             10,
	})
	if err != nil {
		t.Fatalf("Retrieve include superseded: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected both memories when including superseded, got %#v", hits)
	}
}

func TestManager_TemporalFiltering(t *testing.T) {
	store, err := NewCoreStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewCoreStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	manager := NewManager(store)
	ns := Namespace{Scope: ScopeWorkspace, ID: "repo-temporal"}
	observed := time.Date(2026, 3, 29, 8, 0, 0, 0, time.UTC)
	validFrom := time.Date(2026, 3, 29, 9, 0, 0, 0, time.UTC)
	validTo := time.Date(2026, 3, 29, 11, 0, 0, 0, time.UTC)
	if _, err := manager.Write(t.Context(), WriteInput{
		Namespace:  ns,
		Role:       RoleSemantic,
		Content:    "Deployment window is 9-11 UTC.",
		ObservedAt: &observed,
		ValidFrom:  &validFrom,
		ValidTo:    &validTo,
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	inside := time.Date(2026, 3, 29, 10, 0, 0, 0, time.UTC)
	hits, err := manager.Retrieve(t.Context(), Query{
		Namespaces: []Namespace{ns},
		Roles:      []Role{RoleSemantic},
		Text:       "deployment window",
		ValidAt:    &inside,
		Limit:      5,
	})
	if err != nil {
		t.Fatalf("Retrieve inside window: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected hit inside validity window, got %#v", hits)
	}

	outside := time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC)
	hits, err = manager.Retrieve(t.Context(), Query{
		Namespaces: []Namespace{ns},
		Roles:      []Role{RoleSemantic},
		Text:       "deployment window",
		ValidAt:    &outside,
		Limit:      5,
	})
	if err != nil {
		t.Fatalf("Retrieve outside window: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("expected no hits outside validity window, got %#v", hits)
	}

	afterObserved := observed.Add(time.Hour)
	hits, err = manager.Retrieve(t.Context(), Query{
		Namespaces:    []Namespace{ns},
		Roles:         []Role{RoleSemantic},
		Text:          "deployment window",
		ObservedAfter: &afterObserved,
		Limit:         5,
	})
	if err != nil {
		t.Fatalf("Retrieve observed-after: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("expected observed-after filter to exclude hit, got %#v", hits)
	}
}

func TestManager_SemanticRetrievalRespectsLifecycle(t *testing.T) {
	store, err := NewCoreStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewCoreStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	vector, err := NewVectorStore(t.Context(), "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewVectorStore: %v", err)
	}
	t.Cleanup(func() { _ = vector.Close() })

	manager := NewManager(store, WithVectorStore(vector), WithEmbedder(testEmbedder{}))
	ns := Namespace{Scope: ScopeWorkspace, ID: "repo-semantic-lifecycle"}
	result, err := manager.Write(t.Context(), WriteInput{
		Namespace: ns,
		Role:      RoleSemantic,
		Key:       "tooling",
		Content:   "The repo uses Bun for TypeScript tasks",
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := manager.Forget(t.Context(), result.IDs[0], "obsolete"); err != nil {
		t.Fatalf("Forget: %v", err)
	}

	hits, err := manager.Retrieve(t.Context(), Query{
		Namespaces:  []Namespace{ns},
		Roles:       []Role{RoleSemantic},
		Text:        "TypeScript tasks use Bun",
		UseSemantic: true,
		Limit:       5,
	})
	if err != nil {
		t.Fatalf("Retrieve default: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("expected forgotten semantic memory to be hidden by default, got %#v", hits)
	}

	hits, err = manager.Retrieve(t.Context(), Query{
		Namespaces:       []Namespace{ns},
		Roles:            []Role{RoleSemantic},
		Text:             "TypeScript tasks use Bun",
		UseSemantic:      true,
		IncludeForgotten: true,
		Limit:            5,
	})
	if err != nil {
		t.Fatalf("Retrieve include forgotten: %v", err)
	}
	if len(hits) != 1 || hits[0].ForgottenAt == nil {
		t.Fatalf("expected forgotten semantic hit when explicitly included, got %#v", hits)
	}
}
