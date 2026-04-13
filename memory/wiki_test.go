package memory

import (
	"context"
	"testing"

	"github.com/nijaru/canto/workspace"
)

func TestIngestWiki(t *testing.T) {
	root := t.TempDir()
	fs, err := workspace.Open(root)
	if err != nil {
		t.Fatalf("workspace.Open: %v", err)
	}
	defer fs.Close()

	if err := fs.MkdirAll("ai/research", 0o755); err != nil {
		t.Fatalf("MkdirAll ai/research: %v", err)
	}
	if err := fs.MkdirAll("ai/research/sub", 0o755); err != nil {
		t.Fatalf("MkdirAll ai/research/sub: %v", err)
	}

	fs.WriteFile("ai/research/one.md", []byte("---\nsummary: first file\n---\nbody one"), 0o644)
	fs.WriteFile("ai/research/two.md", []byte("no frontmatter"), 0o644)
	fs.WriteFile("ai/research/skip.txt", []byte("skip me"), 0o644)
	fs.WriteFile("ai/research/sub/three.md", []byte("---\nkey: val\n---\nbody three"), 0o644)

	store, err := NewCoreStore("file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("NewCoreStore: %v", err)
	}
	defer store.Close()

	manager := NewManager(store)
	ns := Namespace{Scope: ScopeWorkspace, ID: "test"}
	role := RoleSemantic

	count, err := IngestWiki(context.Background(), fs, "ai/research", manager, ns, role)
	if err != nil {
		t.Fatalf("IngestWiki: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected 3 files ingested, got %d", count)
	}

	memories, err := manager.Retrieve(context.Background(), Query{
		Namespaces: []Namespace{ns},
		Roles:      []Role{role},
		Text:       "body",
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(memories) != 2 {
		t.Fatalf("expected 2 memories with 'body', got %d", len(memories))
	}

	var one Memory
	for _, m := range memories {
		if m.Key == "one" {
			one = m
		}
	}
	if one.Content != "body one" {
		t.Errorf("unexpected content: %q", one.Content)
	}
	if one.Metadata["summary"] != "first file" {
		t.Errorf("expected summary 'first file', got %v", one.Metadata["summary"])
	}
}
