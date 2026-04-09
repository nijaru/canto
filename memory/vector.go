package memory

import (
	"context"
	"database/sql"
	"encoding/binary"
	"math"
	"slices"
	"strings"

	"github.com/go-json-experiment/json"
	_ "modernc.org/sqlite"
)

// VectorStore is an interface for storing and searching high-dimensional vectors.
type VectorStore interface {
	Upsert(ctx context.Context, id string, vector []float32, metadata map[string]any) error
	// Search finds the k nearest vectors to the query.
	// filter is an optional set of metadata key-value constraints; implementations
	// may ignore filters they do not support (e.g. SQLite brute-force).
	Search(
		ctx context.Context,
		vector []float32,
		k int,
		filter map[string]any,
	) ([]SearchResult, error)
	Delete(ctx context.Context, id string) error
	Close() error
}

// SearchResult represents a match in the vector store.
type SearchResult struct {
	ID       string
	Score    float32
	Metadata map[string]any
}

// NewVectorStore creates the default vector backend for memory/.
//
// The default is HNSW-backed approximate search with durable SQLite metadata
// storage. Callers that need an exact brute-force implementation can opt into
// NewSQLiteVectorStore explicitly.
func NewVectorStore(ctx context.Context, dsn string) (VectorStore, error) {
	return NewHNSWStore(ctx, dsn)
}

// SQLiteVectorStore is a brute-force vector store backed by SQLite.
// It is efficient for small collections (< 10k vectors).
type SQLiteVectorStore struct {
	db *sql.DB
}

// NewSQLiteVectorStore creates a new SQLite-backed vector store.
func NewSQLiteVectorStore(dsn string) (*SQLiteVectorStore, error) {
	if !strings.Contains(dsn, "?") {
		dsn += "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	} else if !strings.Contains(dsn, "journal_mode") {
		dsn += "&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	if err := db.Ping(); err != nil {
		db.Close()
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
func (s *SQLiteVectorStore) Upsert(
	ctx context.Context,
	id string,
	vector []float32,
	metadata map[string]any,
) error {
	vData := make([]byte, len(vector)*4)
	for i, f := range vector {
		binary.LittleEndian.PutUint32(vData[i*4:], math.Float32bits(f))
	}

	mData, err := json.Marshal(metadata)
	if err != nil {
		return err
	}

	_, err = s.db.ExecContext(
		ctx,
		"INSERT INTO vectors (id, vector, metadata) VALUES (?, ?, ?) ON CONFLICT(id) DO UPDATE SET vector=excluded.vector, metadata=excluded.metadata",
		id,
		vData,
		string(mData),
	)
	return err
}

// Search performs a brute-force cosine similarity search with optional metadata filtering.
// NOTE: This implementation performs an O(n) scan of the entire collection in memory
// and is intended for small collections (< 10k vectors). For larger datasets,
// consider using HNSWStore which provides sub-linear approximate search.
func (s *SQLiteVectorStore) Search(
	ctx context.Context,
	queryVector []float32,
	k int,
	filter map[string]any,
) ([]SearchResult, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, vector, metadata FROM vectors")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type rawResult struct {
		id       string
		score    float32
		metadata map[string]any
	}
	var rawResults []rawResult
	for rows.Next() {
		var id string
		var vData []byte
		var mData string
		if err := rows.Scan(&id, &vData, &mData); err != nil {
			return nil, err
		}

		var metadata map[string]any
		if err := json.Unmarshal([]byte(mData), &metadata); err != nil {
			continue // Skip corrupt entries
		}

		// Apply filter if provided
		if len(filter) > 0 {
			match := true
			for k, v := range filter {
				if mv, ok := metadata[k]; !ok || mv != v {
					match = false
					break
				}
			}
			if !match {
				continue
			}
		}

		if len(vData)%4 != 0 {
			continue
		}

		vector := make([]float32, len(vData)/4)
		for i := 0; i < len(vector); i++ {
			vector[i] = math.Float32frombits(binary.LittleEndian.Uint32(vData[i*4:]))
		}

		score := cosineSimilarity(queryVector, vector)
		rawResults = append(rawResults, rawResult{
			id:       id,
			score:    score,
			metadata: metadata,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Sort results descending by score
	slices.SortFunc(rawResults, func(a, b rawResult) int {
		if a.score > b.score {
			return -1
		}
		if a.score < b.score {
			return 1
		}
		return 0
	})

	if len(rawResults) > k {
		rawResults = rawResults[:k]
	}

	var results []SearchResult
	for _, r := range rawResults {
		results = append(results, SearchResult{
			ID:       r.id,
			Score:    r.score,
			Metadata: r.metadata,
		})
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

// Delete removes a vector from the store by ID.
func (s *SQLiteVectorStore) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM vectors WHERE id = ?", id)
	return err
}
