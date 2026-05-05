package memory

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/coder/hnsw"
	"github.com/go-json-experiment/json"
	_ "modernc.org/sqlite"
)

// HNSWStore is a pure Go implementation of VectorStore that pairs a high-performance
// in-memory HNSW index (github.com/coder/hnsw) with an embedded SQLite database
// for durable metadata storage and cross-session persistence.
type HNSWStore struct {
	db    *sql.DB
	graph *hnsw.Graph[string]
	mu    sync.RWMutex

	// OverfetchFactor controls the number of extra nodes fetched during an
	// approximate search to satisfy metadata filters. Default is 3.0.
	OverfetchFactor float64
}

// NewHNSWStore creates a new HNSW-backed vector store.
// It initializes the SQLite db at dsn, and rebuilds the HNSW graph from the durable DB
// on startup to restore its in-memory state.
func NewHNSWStore(ctx context.Context, dsn string) (*HNSWStore, error) {
	if !strings.Contains(dsn, "?") {
		dsn += "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	} else if !strings.Contains(dsn, "journal_mode") {
		dsn += "&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("hnsw: open sqlite: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("hnsw: ping sqlite: %w", err)
	}

	s := &HNSWStore{
		db: db,
		// Initialize the Graph with default performant parameters using Cosine Distance
		graph:           hnsw.NewGraph[string](),
		OverfetchFactor: 3.0,
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

	// The github.com/coder/hnsw library has a bug in Delete() that corrupts the graph
	// and causes panics during Search. As a workaround, we rebuild the graph from SQLite.
	s.graph = hnsw.NewGraph[string]()
	if err := s.load(ctx); err != nil {
		return fmt.Errorf("reload graph after upsert: %w", err)
	}

	return nil
}

// Search performs an efficient approximate k-NN search using the in-memory HNSW graph
// with optional metadata filtering from the durable SQLite store.
func (s *HNSWStore) Search(
	ctx context.Context,
	queryVector []float32,
	k int,
	filter map[string]any,
) ([]SearchResult, error) {
	if k <= 0 {
		return nil, nil
	}

	// Request more nodes than k to allow for filtering downstream and to improve
	// approximate search quality for large k.
	overfetchFactor := s.OverfetchFactor
	if overfetchFactor <= 0 {
		overfetchFactor = 3.0
	}
	searchK := int(float64(k) * overfetchFactor)
	if searchK < k {
		searchK = k
	}

	var nodes []hnsw.Node[string]
	func() {
		s.mu.RLock()
		defer s.mu.RUnlock()
		nodes = s.graph.Search(queryVector, searchK)
	}()

	if len(nodes) == 0 {
		return nil, nil
	}

	// Batch retrieve metadata for the matched subset
	ids := make([]string, len(nodes))
	nodeMap := make(map[string]hnsw.Node[string])
	for i, node := range nodes {
		ids[i] = node.Key
		nodeMap[node.Key] = node
	}

	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	q := fmt.Sprintf(
		"SELECT id, metadata FROM vectors WHERE id IN (%s)",
		strings.Join(placeholders, ","),
	)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("hnsw: metadata query: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var id, mData string
		if err := rows.Scan(&id, &mData); err != nil {
			return nil, err
		}

		var metadata map[string]any
		if err := json.Unmarshal([]byte(mData), &metadata); err != nil {
			continue
		}

		if len(filter) > 0 && !metadataMatchesFilter(metadata, filter) {
			continue
		}

		node := nodeMap[id]
		results = append(results, SearchResult{
			ID:       id,
			Score:    1.0 - hnsw.CosineDistance(queryVector, node.Value),
			Metadata: metadata,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, err
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

	// Truncate to requested k if overfetched
	if len(results) > k {
		results = results[:k]
	}

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

	// The github.com/coder/hnsw library has a bug in Delete() that corrupts the graph
	// and causes panics during Search. As a workaround, we rebuild the graph from SQLite.
	s.graph = hnsw.NewGraph[string]()
	if err := s.load(ctx); err != nil {
		return fmt.Errorf("reload graph after delete: %w", err)
	}

	return nil
}

// DB returns the underlying SQLite database handle.
// Use this only for advanced introspection or testing; prefer the store methods.
func (s *HNSWStore) DB() *sql.DB {
	return s.db
}

// Close finalizes connection pooling for the underlying SQLite proxy db.
func (s *HNSWStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Close()
}
