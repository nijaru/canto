package session

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// JSONLStore is a file-backed store that saves events as JSON lines.
type JSONLStore struct {
	mu  sync.RWMutex
	dir string
}

// NewJSONLStore creates a new JSONL store.
func NewJSONLStore(dir string) (*JSONLStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &JSONLStore{dir: dir}, nil
}

// Save appends an event to the session file.
func (s *JSONLStore) Save(ctx context.Context, e Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := filepath.Join(s.dir, fmt.Sprintf("%s.jsonl", e.SessionID))
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	return json.NewEncoder(f).Encode(e)
}

// Load reads all events for a session and reconstructs it.
func (s *JSONLStore) Load(ctx context.Context, sessionID string) (*Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	path := filepath.Join(s.dir, fmt.Sprintf("%s.jsonl", sessionID))
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return New(sessionID), nil
		}
		return nil, err
	}
	defer f.Close()

	sess := New(sessionID)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var e Event
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			return nil, err
		}
		sess.Append(e)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return sess, nil
}

// Search implements the Store interface for JSONLStore.
// For now, it returns an error as Search is optimized for SQLite.
func (s *JSONLStore) Search(ctx context.Context, sessionID string, query string) ([]Event, error) {
	return nil, fmt.Errorf("search not implemented for JSONLStore; use SQLiteStore")
}
