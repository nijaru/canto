package memory

import (
	"context"
	"path/filepath"
	"testing"
)

func TestHNSWStore(t *testing.T) {
	tmpDir := t.TempDir()
	dsn := filepath.Join(tmpDir, "test_hnsw.sqlite")
	ctx := context.Background()

	store, err := NewHNSWStore(ctx, dsn)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	if err := store.Upsert(ctx, "doc1", []float32{1.0, 0.0, 0.0}, map[string]any{"color": "red"}); err != nil {
		t.Fatalf("upsert failed: %v", err)
	}
	if err := store.Upsert(ctx, "doc2", []float32{0.0, 1.0, 0.0}, map[string]any{"color": "green"}); err != nil {
		t.Fatalf("upsert failed: %v", err)
	}
	if err := store.Upsert(ctx, "doc3", []float32{0.0, 0.0, 1.0}, map[string]any{"color": "blue"}); err != nil {
		t.Fatalf("upsert failed: %v", err)
	}

	// Search for something close to doc1
	results, err := store.Search(ctx, []float32{0.9, 0.1, 0.0}, 2, nil)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// First result should clearly be doc1 due to proximity
	if results[0].ID != "doc1" {
		t.Errorf("expected top result to be doc1, got %q", results[0].ID)
	}
	if results[0].Metadata["color"] != "red" {
		t.Errorf("expected metadata color red, got %v", results[0].Metadata["color"])
	}

	// Test persistence (reloading the DB)
	if err := store.Close(); err != nil {
		t.Fatalf("failed to close: %v", err)
	}

	store2, err := NewHNSWStore(ctx, dsn)
	if err != nil {
		t.Fatalf("failed to reload store: %v", err)
	}
	defer store2.Close()

	// Re-search against the reloaded graph
	results2, err := store2.Search(ctx, []float32{0.9, 0.1, 0.0}, 1, nil)
	if err != nil {
		t.Fatalf("search failed on reloaded store: %v", err)
	}

	if len(results2) != 1 || results2[0].ID != "doc1" {
		t.Errorf("reloaded store returned unexpected subset: %v", results2)
	}

	// Test delete
	if err := store2.Delete(ctx, "doc1"); err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	results3, err := store2.Search(ctx, []float32{0.9, 0.1, 0.0}, 1, nil)
	if err != nil {
		t.Fatalf("search three failed: %v", err)
	}

	for _, r := range results3 {
		if r.ID == "doc1" {
			t.Errorf("expected doc1 to be deleted, returned: %v", results3)
		}
	}
}

func TestHNSWStoreNumericMetadataFilter(t *testing.T) {
	tmpDir := t.TempDir()
	dsn := filepath.Join(tmpDir, "test_hnsw_filter.sqlite")
	ctx := t.Context()

	store, err := NewHNSWStore(ctx, dsn)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	if err := store.Upsert(ctx, "doc1", []float32{1, 0, 0}, map[string]any{"worker": 3}); err != nil {
		t.Fatalf("upsert doc1: %v", err)
	}
	if err := store.Upsert(ctx, "doc2", []float32{0, 1, 0}, map[string]any{"worker": 4}); err != nil {
		t.Fatalf("upsert doc2: %v", err)
	}

	results, err := store.Search(ctx, []float32{1, 0, 0}, 2, map[string]any{"worker": 3})
	if err != nil {
		t.Fatalf("filtered search: %v", err)
	}
	if len(results) != 1 || results[0].ID != "doc1" {
		t.Fatalf("filtered results = %#v, want doc1 only", results)
	}
}

func TestHNSWStoreSearchNormalizesLimits(t *testing.T) {
	tmpDir := t.TempDir()
	dsn := filepath.Join(tmpDir, "test_hnsw_limits.sqlite")
	ctx := t.Context()

	store, err := NewHNSWStore(ctx, dsn)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	if err := store.Upsert(ctx, "doc1", []float32{1, 0, 0}, map[string]any{"color": "red"}); err != nil {
		t.Fatalf("upsert doc1: %v", err)
	}

	results, err := store.Search(ctx, []float32{1, 0, 0}, 0, nil)
	if err != nil {
		t.Fatalf("zero-limit search: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("zero-limit search returned %#v, want none", results)
	}

	store.OverfetchFactor = 0
	results, err = store.Search(ctx, []float32{1, 0, 0}, 1, nil)
	if err != nil {
		t.Fatalf("default-overfetch search: %v", err)
	}
	if len(results) != 1 || results[0].ID != "doc1" {
		t.Fatalf("default-overfetch search returned %#v, want doc1", results)
	}
}
