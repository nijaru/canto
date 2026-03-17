package memory

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func TestHNSWStore_Concurrency(t *testing.T) {
	tmpDir := t.TempDir()
	dsn := filepath.Join(tmpDir, "concurrency.sqlite")
	ctx := t.Context()

	store, err := NewHNSWStore(ctx, dsn)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	const numWorkers = 10
	const itemsPerWorker = 50
	total := numWorkers * itemsPerWorker
	var wg sync.WaitGroup

	// Concurrent upserts
	for i := 0; i < numWorkers; i++ {
		wg.Go(func() {
			for j := 0; j < itemsPerWorker; j++ {
				docID := fmt.Sprintf("worker%d_item%d", i, j)
				vec := []float32{float32(i + 1), float32(j + 1), 0.0}
				if err := store.Upsert(ctx, docID, vec, map[string]any{"worker": i, "idx": j}); err != nil {
					t.Errorf("Upsert failed: %v", err)
					return
				}
			}
		})
	}

	// Concurrent searches
	for range numWorkers {
		wg.Go(func() {
			for range itemsPerWorker {
				if _, err := store.Search(ctx, []float32{0.5, 0.5, 0.0}, 5, nil); err != nil {
					t.Errorf("Search failed: %v", err)
					return
				}
			}
		})
	}

	wg.Wait()

	// Verify DB has all items
	var count int
	if err := store.DB.QueryRow("SELECT COUNT(*) FROM vectors").Scan(&count); err != nil {
		t.Fatalf("failed to count rows: %v", err)
	}
	if count != total {
		t.Errorf("expected %d rows in DB, got %d", total, count)
	}

	// Verify all writes landed
	results, err := store.Search(ctx, []float32{0.5, 0.5, 0.0}, total, nil)
	if err != nil {
		t.Fatalf("final search failed: %v", err)
	}
	if len(results) != total {
		t.Errorf("expected %d results after concurrent writes, got %d", total, len(results))
	}
}
