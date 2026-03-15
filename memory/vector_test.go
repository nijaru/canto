package memory

import (
	"context"
	"os"
	"testing"
)

func TestSQLiteVectorStore(t *testing.T) {
	dbFile := "test_vector.db"
	defer os.Remove(dbFile)

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
