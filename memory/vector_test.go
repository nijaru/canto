package memory

import (
	"context"
	"path/filepath"
	"testing"
)

func TestNewVectorStore_DefaultsToHNSW(t *testing.T) {
	store, err := NewVectorStore(t.Context(), t.TempDir()+"/default.sqlite")
	if err != nil {
		t.Fatalf("NewVectorStore: %v", err)
	}
	defer store.Close()

	if _, ok := store.(*HNSWStore); !ok {
		t.Fatalf("default vector store = %T, want *HNSWStore", store)
	}
}

func TestSQLiteVectorStore(t *testing.T) {
	dbFile := filepath.Join(t.TempDir(), "test_vector.db")

	store, err := NewSQLiteVectorStore(dbFile)
	if err != nil {
		t.Fatalf("failed to create vector store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// 1. Upsert vectors
	v1 := []float32{1.0, 0.0}
	v2 := []float32{0.0, 1.0}
	v3 := []float32{0.707, 0.707} // 45 degrees

	store.Upsert(ctx, "v1", v1, map[string]any{"name": "v1"})
	store.Upsert(ctx, "v2", v2, map[string]any{"name": "v2"})
	store.Upsert(ctx, "v3", v3, map[string]any{"name": "v3"})

	// 2. Search
	query := []float32{1.0, 0.1}
	results, err := store.Search(ctx, query, 2, nil)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}

	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}

	// v1 should be the closest
	if results[0].ID != "v1" {
		t.Errorf("expected first result to be v1, got %s", results[0].ID)
	}
}

func TestSQLiteVectorStoreSearchLimitAndNumericFilter(t *testing.T) {
	store, err := NewSQLiteVectorStore(filepath.Join(t.TempDir(), "test_vector_filter.db"))
	if err != nil {
		t.Fatalf("failed to create vector store: %v", err)
	}
	defer store.Close()

	ctx := t.Context()
	if err := store.Upsert(ctx, "v1", []float32{1, 0}, map[string]any{"worker": 3}); err != nil {
		t.Fatalf("upsert v1: %v", err)
	}
	if err := store.Upsert(ctx, "v2", []float32{0, 1}, map[string]any{"worker": 4}); err != nil {
		t.Fatalf("upsert v2: %v", err)
	}

	results, err := store.Search(ctx, []float32{1, 0}, -1, nil)
	if err != nil {
		t.Fatalf("negative-limit search: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("negative-limit search returned %#v, want none", results)
	}

	results, err = store.Search(ctx, []float32{1, 0}, 2, map[string]any{"worker": 3})
	if err != nil {
		t.Fatalf("numeric filtered search: %v", err)
	}
	if len(results) != 1 || results[0].ID != "v1" {
		t.Fatalf("numeric filtered results = %#v, want v1 only", results)
	}
}
