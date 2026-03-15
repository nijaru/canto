package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"sync"

	"github.com/coder/hnsw"
	_ "modernc.org/sqlite"
)

// HNSWStore is a pure Go implementation of VectorStore that pairs a high-performance
// in-memory HNSW index (github.com/coder/hnsw) with an embedded SQLite database
// for durable metadata storage and cross-session persistence.
type HNSWStore struct {
	db    *sql.DB
	graph *hnsw.Graph[string]
	mu    sync.RWMutex
}

// NewHNSWStore creates a new HNSW-backed vector store.
// It initializes the SQLite db at dsn, and rebuilds the HNSW graph from the durable DB
// on startup to restore its in-memory state.
func NewHNSWStore(ctx context.Context, dsn string) (*HNSWStore, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("hnsw: open sqlite: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("hnsw: ping sqlite: %w", err)
	}

	s := &HNSWStore{
		db: db,
		// Initialize the Graph with default performant parameters using Cosine Distance
		graph: hnsw.NewGraph[string](),
	}

	if err := s.init(ctx); err != nil {
		db.Close()
		return nil, err
	}

	if err := s.load(ctx); err != nil {
		db.Close()
		return nil, err
	}

	return s, nil
}

func (s *HNSWStore) init(ctx context.Context) error {
	q := `CREATE TABLE IF NOT EXISTS vectors (
		id TEXT PRIMARY KEY,
		vector TEXT,
		metadata TEXT
	)`
	_, err := s.db.ExecContext(ctx, q)
	return err
}

// load reconstructs the in-memory HNSW index from SQLite rows on startup.
func (s *HNSWStore) load(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, "SELECT id, vector FROM vectors")
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		var vData string
		if err := rows.Scan(&id, &vData); err != nil {
			return err
		}

		var vector []float32
		if err := json.Unmarshal([]byte(vData), &vector); err != nil {
			return fmt.Errorf("decode vector %s: %w", id, err)
		}

		node := hnsw.MakeNode(id, vector)
		s.graph.Add(node)
	}

	return rows.Err()
}

// Upsert adds or updates a vector in the store.
// It writes durably to SQLite and updates the in-memory index.
func (s *HNSWStore) Upsert(
	ctx context.Context,
	id string,
	vector []float32,
	metadata map[string]any,
) error {
	vData, err := json.Marshal(vector)
	if err != nil {
		return err
	}

	mData, err := json.Marshal(metadata)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Write durably to disk
	_, err = s.db.ExecContext(
		ctx,
		"INSERT INTO vectors (id, vector, metadata) VALUES (?, ?, ?) ON CONFLICT(id) DO UPDATE SET vector=excluded.vector, metadata=excluded.metadata",
		id,
		string(vData),
		string(mData),
	)
	if err != nil {
		return fmt.Errorf("hnsw: sqlite insert: %w", err)
	}

	// Update in-memory graph (delete old node if present, then add updated one)
	s.graph.Delete(id)
	s.graph.Add(hnsw.MakeNode(id, vector))

	return nil
}

// Search performs a highly efficient approximate k-NN search using the memory-mapped HNSW graph.
// Results are populated with their durable metadata from SQLite.
func (s *HNSWStore) Search(
	ctx context.Context,
	queryVector []float32,
	k int,
	_ map[string]any, // Unfiltered pure Go implementation (drops filter args per Phase 1 spec)
) ([]SearchResult, error) {
	s.mu.RLock()
	nodes := s.graph.Search(queryVector, k)
	s.mu.RUnlock()

	if len(nodes) == 0 {
		return nil, nil
	}

	// Retrieve durable metadata for the matched subset
	results := make([]SearchResult, 0, len(nodes))
	for _, node := range nodes {
		var mData string
		err := s.db.QueryRowContext(ctx, "SELECT metadata FROM vectors WHERE id = ?", node.Key).
			Scan(&mData)
		if err != nil {
			// If node exists in memory index but not disk, ignore cleanly
			if err == sql.ErrNoRows {
				continue
			}
			return nil, err
		}

		var metadata map[string]any
		if err := json.Unmarshal([]byte(mData), &metadata); err != nil {
			return nil, fmt.Errorf("hnsw decode metadata %s: %w", node.Key, err)
		}

		results = append(results, SearchResult{
			ID:       node.Key,
			Score:    1.0 - hnsw.CosineDistance(queryVector, node.Value),
			Metadata: metadata,
		})
	}

	slices.SortFunc(results, func(a, b SearchResult) int {
		if a.Score > b.Score {
			return -1
		}
		if a.Score < b.Score {
			return 1
		}
		return 0
	})

	return results, nil
}

// Delete removes a vector from the store by ID.
func (s *HNSWStore) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.ExecContext(ctx, "DELETE FROM vectors WHERE id = ?", id)
	if err != nil {
		return err
	}

	s.graph.Delete(id)
	return nil
}

// Close finalizes connection pooling for the underlying SQLite proxy db.
func (s *HNSWStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Close()
}
