package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"

	_ "modernc.org/sqlite"
)

// VectorStore is an interface for storing and searching high-dimensional vectors.
type VectorStore interface {
	Upsert(ctx context.Context, id string, vector []float32, metadata map[string]any) error
	Search(ctx context.Context, vector []float32, k int) ([]SearchResult, error)
}

// SearchResult represents a match in the vector store.
type SearchResult struct {
	ID       string
	Score    float32
	Metadata map[string]any
}

// SQLiteVectorStore is a brute-force vector store backed by SQLite.
// It is efficient for small collections (< 10k vectors).
type SQLiteVectorStore struct {
	db *sql.DB
}

// NewSQLiteVectorStore creates a new SQLite-backed vector store.
func NewSQLiteVectorStore(dsn string) (*SQLiteVectorStore, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	if err := db.Ping(); err != nil {
		return nil, err
	}

	s := &SQLiteVectorStore{db: db}
	if err := s.init(); err != nil {
		return nil, err
	}

	return s, nil
}

func (s *SQLiteVectorStore) init() error {
	q := `CREATE TABLE IF NOT EXISTS vectors (
		id TEXT PRIMARY KEY,
		vector BLOB,
		metadata TEXT
	)`
	_, err := s.db.Exec(q)
	return err
}

// Upsert adds or updates a vector in the store.
func (s *SQLiteVectorStore) Upsert(ctx context.Context, id string, vector []float32, metadata map[string]any) error {
	vData, err := json.Marshal(vector)
	if err != nil {
		return err
	}
	mData, err := json.Marshal(metadata)
	if err != nil {
		return err
	}

	_, err = s.db.ExecContext(ctx,
		"INSERT INTO vectors (id, vector, metadata) VALUES (?, ?, ?) ON CONFLICT(id) DO UPDATE SET vector=excluded.vector, metadata=excluded.metadata",
		id, vData, string(mData),
	)
	return err
}

// Search performs a brute-force cosine similarity search.
func (s *SQLiteVectorStore) Search(ctx context.Context, queryVector []float32, k int) ([]SearchResult, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, vector, metadata FROM vectors")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var id string
		var vData []byte
		var mData string
		if err := rows.Scan(&id, &vData, &mData); err != nil {
			return nil, err
		}

		var vector []float32
		if err := json.Unmarshal(vData, &vector); err != nil {
			return nil, err
		}

		var metadata map[string]any
		if err := json.Unmarshal([]byte(mData), &metadata); err != nil {
			return nil, err
		}

		score := cosineSimilarity(queryVector, vector)
		results = append(results, SearchResult{
			ID:       id,
			Score:    score,
			Metadata: metadata,
		})
	}

	// Sort results and take top k
	// Simple bubble sort for now (since it's a small collection)
	// In a real implementation, use a max-heap.
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[i].Score < results[j].Score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	if len(results) > k {
		results = results[:k]
	}

	return results, nil
}

func cosineSimilarity(v1, v2 []float32) float32 {
	if len(v1) != len(v2) {
		return 0
	}
	var dotProduct, normV1, normV2 float64
	for i := range v1 {
		dotProduct += float64(v1[i] * v2[i])
		normV1 += float64(v1[i] * v1[i])
		normV2 += float64(v2[i] * v2[i])
	}
	if normV1 == 0 || normV2 == 0 {
		return 0
	}
	return float32(dotProduct / (math.Sqrt(normV1) * math.Sqrt(normV2)))
}

func (s *SQLiteVectorStore) Close() error {
	return s.db.Close()
}
