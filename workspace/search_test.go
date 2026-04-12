package workspace

import (
	"slices"
	"testing"
)

func TestTrigramIndexUpsertSearchAndUpdate(t *testing.T) {
	index := NewSearchIndex()

	first := ContentRef{
		Path:   "notes/first.txt",
		Digest: "sha256:first",
		Size:   int64(len("alpha beta")),
	}
	if err := index.Upsert(t.Context(), first, []byte("alpha beta")); err != nil {
		t.Fatalf("Upsert(first): %v", err)
	}

	hits, err := index.Search(t.Context(), "alpha", 10)
	if err != nil {
		t.Fatalf("Search(alpha): %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("Search(alpha) hits = %d, want 1", len(hits))
	}
	if hits[0].Ref.Path != first.Path {
		t.Fatalf("Search(alpha)[0].Path = %q, want %q", hits[0].Ref.Path, first.Path)
	}
	if hits[0].Ref.Digest != first.Digest {
		t.Fatalf("Search(alpha)[0].Digest = %q, want %q", hits[0].Ref.Digest, first.Digest)
	}

	updated := ContentRef{
		Path:   "notes/first.txt",
		Digest: "sha256:second",
		Size:   int64(len("gamma delta")),
	}
	if err := index.Upsert(t.Context(), updated, []byte("gamma delta")); err != nil {
		t.Fatalf("Upsert(updated): %v", err)
	}

	hits, err = index.Search(t.Context(), "alpha", 10)
	if err != nil {
		t.Fatalf("Search(alpha after update): %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("Search(alpha after update) hits = %d, want 0", len(hits))
	}

	hits, err = index.Search(t.Context(), "gamma", 10)
	if err != nil {
		t.Fatalf("Search(gamma): %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("Search(gamma) hits = %d, want 1", len(hits))
	}
	if hits[0].Ref.Digest != updated.Digest {
		t.Fatalf("Search(gamma)[0].Digest = %q, want %q", hits[0].Ref.Digest, updated.Digest)
	}

	if err := index.Delete(t.Context(), updated.Path); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	hits, err = index.Search(t.Context(), "gamma", 10)
	if err != nil {
		t.Fatalf("Search(gamma after delete): %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("Search(gamma after delete) hits = %d, want 0", len(hits))
	}
}

func TestIndexWorkspaceIndexesWorkspaceFS(t *testing.T) {
	root, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = root.Close() })

	if err := root.WriteFile("docs/readme.md", []byte("workspace search substrate"), 0o644); err != nil {
		t.Fatalf("WriteFile(readme): %v", err)
	}
	if err := root.WriteFile("src/main.go", []byte("package main\nfunc main() { println(\"index me\") }"), 0o644); err != nil {
		t.Fatalf("WriteFile(main): %v", err)
	}

	index := NewSearchIndex()
	count, err := IndexWorkspace(t.Context(), root, index)
	if err != nil {
		t.Fatalf("IndexWorkspace: %v", err)
	}
	if count != 2 {
		t.Fatalf("IndexWorkspace count = %d, want 2", count)
	}

	hits, err := index.Search(t.Context(), "substrate", 10)
	if err != nil {
		t.Fatalf("Search(substrate): %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("Search(substrate) hits = %d, want 1", len(hits))
	}
	if hits[0].Ref.Path != "docs/readme.md" {
		t.Fatalf("Search(substrate)[0].Path = %q, want docs/readme.md", hits[0].Ref.Path)
	}

	hits, err = index.Search(t.Context(), "main.go", 10)
	if err != nil {
		t.Fatalf("Search(main.go): %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("Search(main.go) hits = %d, want 1", len(hits))
	}
	if hits[0].Ref.Path != "src/main.go" {
		t.Fatalf("Search(main.go)[0].Path = %q, want src/main.go", hits[0].Ref.Path)
	}

	hits, err = index.Search(t.Context(), "go", 10)
	if err != nil {
		t.Fatalf("Search(go): %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("Search(go) hits = %d, want 1", len(hits))
	}
	if hits[0].Ref.Path != "src/main.go" {
		t.Fatalf("Search(go)[0].Path = %q, want src/main.go", hits[0].Ref.Path)
	}
}

func TestTrigramIndexReturnsStablePathOrdering(t *testing.T) {
	index := NewSearchIndex()
	for _, file := range []ContentRef{
		{Path: "b.txt", Digest: "sha256:b", Size: 3},
		{Path: "a.txt", Digest: "sha256:a", Size: 3},
	} {
		if err := index.Upsert(t.Context(), file, []byte("shared term")); err != nil {
			t.Fatalf("Upsert(%s): %v", file.Path, err)
		}
	}

	hits, err := index.Search(t.Context(), "shared", 10)
	if err != nil {
		t.Fatalf("Search(shared): %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("Search(shared) hits = %d, want 2", len(hits))
	}
	got := []string{hits[0].Ref.Path, hits[1].Ref.Path}
	want := []string{"a.txt", "b.txt"}
	if !slices.Equal(got, want) {
		t.Fatalf("Search(shared) paths = %#v, want %#v", got, want)
	}
}
