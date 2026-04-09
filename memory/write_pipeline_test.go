package memory

import (
	"context"
	"testing"
)

func TestManager_WriteBatchDedupesExtractedCandidates(t *testing.T) {
	store, err := NewCoreStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewCoreStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	manager := NewManager(store, WithWritePolicy(WritePolicy{
		ConflictMode: ConflictMerge,
		Extractor: func(_ context.Context, candidate Candidate) ([]Candidate, error) {
			return []Candidate{
				candidate,
				{
					Namespace: candidate.Namespace,
					Role:      candidate.Role,
					Key:       candidate.Key,
					Content:   "extracted duplicate",
					Metadata:  map[string]any{"source": "extractor"},
				},
			}, nil
		},
	}))
	ns := Namespace{Scope: ScopeUser, ID: "user-batch"}
	result, err := manager.WriteBatch(t.Context(), []WriteInput{
		{
			Namespace: ns,
			Role:      RoleSemantic,
			Key:       "pref",
			Content:   "prefers concise output",
		},
	})
	if err != nil {
		t.Fatalf("WriteBatch: %v", err)
	}
	if result.Stored != 1 {
		t.Fatalf("WriteBatch stored %d records, want 1", result.Stored)
	}
	hits, err := manager.Retrieve(t.Context(), Query{
		Namespaces: []Namespace{ns},
		Roles:      []Role{RoleSemantic},
		Text:       "prefers concise",
		Limit:      5,
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("Retrieve returned %d hits, want 1", len(hits))
	}
	if hits[0].Content != "prefers concise output\nextracted duplicate" {
		t.Fatalf("deduped content = %q", hits[0].Content)
	}
}

func TestManager_RememberBatchAlias(t *testing.T) {
	store, err := NewCoreStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewCoreStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	manager := NewManager(store)
	ns := Namespace{Scope: ScopeAgent, ID: "agent-batch"}
	result, err := manager.RememberBatch(t.Context(), []WriteInput{
		{
			Namespace: ns,
			Role:      RoleEpisodic,
			Content:   "completed deployment dry run",
		},
		{
			Namespace: ns,
			Role:      RoleEpisodic,
			Content:   "opened rollback checklist",
		},
	})
	if err != nil {
		t.Fatalf("RememberBatch: %v", err)
	}
	if result.Stored != 2 {
		t.Fatalf("RememberBatch stored %d records, want 2", result.Stored)
	}
}

func TestManager_ConsolidateAppliesPlan(t *testing.T) {
	store, err := NewCoreStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewCoreStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	manager := NewManager(store)
	ns := Namespace{Scope: ScopeWorkspace, ID: "repo-consolidate"}
	first, err := manager.Write(t.Context(), WriteInput{
		Namespace: ns,
		Role:      RoleSemantic,
		Key:       "pref-v1",
		Content:   "Uses Bun for frontend tasks",
	})
	if err != nil {
		t.Fatalf("Write first: %v", err)
	}
	second, err := manager.Write(t.Context(), WriteInput{
		Namespace: ns,
		Role:      RoleSemantic,
		Key:       "pref-v2",
		Content:   "Uses Bun for frontend tasks and tooling automation",
	})
	if err != nil {
		t.Fatalf("Write second: %v", err)
	}

	result, err := manager.Consolidate(t.Context(), ConsolidationInput{
		Namespaces: []Namespace{ns},
		Roles:      []Role{RoleSemantic},
		Limit:      10,
	}, ConsolidatorFunc(func(
		_ context.Context,
		memories []Memory,
	) (ConsolidationPlan, error) {
		if len(memories) != 2 {
			t.Fatalf("Consolidator saw %d memories, want 2", len(memories))
		}
		return ConsolidationPlan{
			Upserts: []WriteInput{
				{
					Namespace:  ns,
					Role:       RoleSemantic,
					Key:        "pref-stable",
					Content:    "Uses Bun for frontend tasks and automation",
					Supersedes: second.IDs[0],
				},
			},
			Forgets: []ForgetInput{
				{ID: first.IDs[0], Reason: "merged"},
			},
		}, nil
	}))
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	if result.Examined != 2 || result.Written.Stored != 1 || result.Forgotten != 1 {
		t.Fatalf("unexpected consolidation result: %#v", result)
	}
	hits, err := manager.Retrieve(t.Context(), Query{
		Namespaces:        []Namespace{ns},
		Roles:             []Role{RoleSemantic},
		Text:              "frontend tasks",
		IncludeForgotten:  true,
		IncludeSuperseded: true,
		Limit:             10,
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(hits) != 3 {
		t.Fatalf("expected 3 records after consolidation, got %#v", hits)
	}
}
