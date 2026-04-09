package memory

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCoreStore_ListMemories(t *testing.T) {
	store, err := NewCoreStore(filepath.Join(t.TempDir(), "index.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := t.Context()
	ns := Namespace{Scope: ScopeUser, ID: "user-1"}
	now := time.Now().UTC()
	forgottenAt := now.Add(10 * time.Minute)

	for _, memory := range []Memory{
		{
			ID:         "mem-1",
			Namespace:  ns,
			Role:       RoleSemantic,
			Key:        "active-pref",
			Content:    "Prefers concise answers.",
			UpdatedAt:  now,
			ObservedAt: &now,
		},
		{
			ID:           "mem-2",
			Namespace:    ns,
			Role:         RoleSemantic,
			Key:          "retired-pref",
			Content:      "Old preference that should be hidden.",
			UpdatedAt:    now.Add(time.Minute),
			ForgottenAt:  &forgottenAt,
			SupersededBy: "mem-1",
		},
	} {
		if err := store.UpsertMemory(ctx, memory); err != nil {
			t.Fatalf("UpsertMemory(%s): %v", memory.ID, err)
		}
	}

	memories, err := store.ListMemories(ctx, MemoryListInput{
		Namespaces: []Namespace{ns},
		Roles:      []Role{RoleSemantic},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(memories) != 1 {
		t.Fatalf("ListMemories returned %d memories, want 1", len(memories))
	}
	if memories[0].ID != "mem-1" {
		t.Fatalf("ListMemories returned %q, want mem-1", memories[0].ID)
	}

	memories, err = store.ListMemories(ctx, MemoryListInput{
		Namespaces:        []Namespace{ns},
		Roles:             []Role{RoleSemantic},
		IncludeForgotten:  true,
		IncludeSuperseded: true,
		Limit:             1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(memories) != 1 {
		t.Fatalf("ListMemories with limit returned %d memories, want 1", len(memories))
	}
	if memories[0].ID != "mem-2" {
		t.Fatalf("ListMemories returned newest memory %q, want mem-2", memories[0].ID)
	}
}

func TestIndexSnapshot_RendersPointerTree(t *testing.T) {
	store, err := NewCoreStore(filepath.Join(t.TempDir(), "snapshot.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := t.Context()
	ns := Namespace{Scope: ScopeThread, ID: "thread-1"}
	if err := store.UpsertBlock(ctx, Block{
		Namespace: ns,
		Name:      "persona",
		Content:   "Answer directly and keep responses concise unless detail is requested.",
	}); err != nil {
		t.Fatalf("UpsertBlock: %v", err)
	}
	if err := store.UpsertMemory(ctx, Memory{
		ID:        "mem-abcdef12",
		Namespace: ns,
		Role:      RoleSemantic,
		Key:       "favorite theme",
		Content:   "This body should not appear in full because the summary metadata should win.",
		Metadata: map[string]any{
			"summary": "User prefers warm terminal themes and compact layouts for long sessions.",
		},
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertMemory: %v", err)
	}

	index := NewIndex(
		store,
		WithIndexMaxEntries(8),
		WithIndexMaxBlockEntries(4),
		WithIndexMaxSummaryRunes(48),
	)
	snapshot, err := index.Snapshot(ctx, IndexQuery{
		Namespaces: []Namespace{ns},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Entries) != 2 {
		t.Fatalf("Snapshot returned %d entries, want 2", len(snapshot.Entries))
	}
	if snapshot.Entries[0].Path != "thread/thread-1/core/persona" {
		t.Fatalf("first path = %q, want thread/thread-1/core/persona", snapshot.Entries[0].Path)
	}
	if snapshot.Entries[1].Path != "thread/thread-1/semantic/favorite-theme--mem-abcd" {
		t.Fatalf("second path = %q", snapshot.Entries[1].Path)
	}
	rendered := snapshot.String()
	if !strings.Contains(rendered, "thread/") {
		t.Fatalf("rendered snapshot missing scope tree:\n%s", rendered)
	}
	if !strings.Contains(rendered, "persona -- Answer directly and keep responses concise") {
		t.Fatalf("rendered snapshot missing clipped block summary:\n%s", rendered)
	}
	if !strings.Contains(
		rendered,
		"favorite-theme--mem-abcd -- User prefers warm terminal themes and compact",
	) {
		t.Fatalf("rendered snapshot missing memory summary:\n%s", rendered)
	}
	if strings.Contains(rendered, "This body should not appear in full") {
		t.Fatalf("rendered snapshot used raw body instead of summary metadata:\n%s", rendered)
	}
}
