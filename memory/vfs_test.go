package memory

import (
	"context"
	"io/fs"
	"os"
	"strings"
	"testing"
)

func TestVFS(t *testing.T) {
	ctx := context.Background()
	store, err := NewCoreStore(":memory:")
	if err != nil {
		t.Fatalf("NewCoreStore: %v", err)
	}
	defer store.Close()

	mgr := NewManager(store)

	_, err = mgr.Write(ctx, WriteInput{
		Namespace: Namespace{Scope: "test", ID: "1"},
		Role:      RoleSemantic,
		Key:       "doc1",
		Content:   "This is chunk 1 of the document. ",
		Metadata:  map[string]any{"path": "my_document.txt", "chunk": 1},
		Mode:      WriteSync,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = mgr.Write(ctx, WriteInput{
		Namespace: Namespace{Scope: "test", ID: "1"},
		Role:      RoleSemantic,
		Key:       "doc1_2",
		Content:   "This is chunk 2 of the document.",
		Metadata:  map[string]any{"path": "my_document.txt", "chunk": 2},
		Mode:      WriteSync,
	})
	if err != nil {
		t.Fatal(err)
	}

	fsys := NewFS(mgr)

	// test read document by path (lazy reassembly)
	data, err := fsys.ReadFile("docs/my_document.txt")
	if err != nil {
		t.Errorf("ReadFile docs: %v", err)
	}
	expected := "This is chunk 1 of the document. This is chunk 2 of the document."
	if string(data) != expected {
		t.Errorf("Docs output mismatch.\nGot: %q\nWant: %q", string(data), expected)
	}

	// test search
	data, err = fsys.ReadFile("search/chunk.md")
	if err != nil {
		if os.IsNotExist(err) && (mgr.vector == nil || mgr.embedder == nil) {
			t.Log("Search skipped (no vector/embedder)")
		} else {
			t.Errorf("ReadFile search: %v", err)
		}
	} else {
		t.Logf("Search output:\n%s", data)
		// With SQLite FTS5 (trigram), this should work even without vector search.
		if !strings.Contains(string(data), "Match 1") {
			t.Errorf("Search output missing Match 1")
		}
	}
}

func TestVFSReadDirUnknownPathNotExist(t *testing.T) {
	store, err := NewCoreStore(":memory:")
	if err != nil {
		t.Fatalf("NewCoreStore: %v", err)
	}
	defer store.Close()

	fsys := NewFS(NewManager(store))
	if _, err := fsys.ReadDir("missing"); !os.IsNotExist(err) {
		t.Fatalf("ReadDir missing error = %v, want not exist", err)
	}
}

func TestVFSStandardFSReadDir(t *testing.T) {
	store, err := NewCoreStore(":memory:")
	if err != nil {
		t.Fatalf("NewCoreStore: %v", err)
	}
	defer store.Close()

	fsys := NewFS(NewManager(store))
	entries, err := fs.ReadDir(fsys.FS(), ".")
	if err != nil {
		t.Fatalf("fs.ReadDir root: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	got := strings.Join(names, ",")
	want := "blocks,docs,memories,search"
	if got != want {
		t.Fatalf("root entries = %q, want %q", got, want)
	}
}

func TestVFSStatUsesStableVirtualModTime(t *testing.T) {
	store, err := NewCoreStore(":memory:")
	if err != nil {
		t.Fatalf("NewCoreStore: %v", err)
	}
	defer store.Close()

	fsys := NewFS(NewManager(store))
	first, err := fsys.Stat("docs")
	if err != nil {
		t.Fatalf("Stat docs first: %v", err)
	}
	second, err := fsys.Stat("docs")
	if err != nil {
		t.Fatalf("Stat docs second: %v", err)
	}
	if !first.ModTime().Equal(second.ModTime()) {
		t.Fatalf("virtual ModTime changed: %v -> %v", first.ModTime(), second.ModTime())
	}
}
