package session

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/oklog/ulid/v2"
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

	return s.saveLocked(e)
}

func (s *JSONLStore) saveLocked(e Event) error {
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

// LoadUntil loads a session up to (and including) the given event ID.
func (s *JSONLStore) LoadUntil(ctx context.Context, sessionID string, eventID ulid.ULID) (*Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	path := filepath.Join(s.dir, fmt.Sprintf("%s.jsonl", sessionID))
	f, err := os.Open(path)
	if err != nil {
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
		if e.ID == eventID {
			break
		}
	}
	return sess, scanner.Err()
}

// Fork creates a new session by copying all events from an existing session.
func (s *JSONLStore) Fork(ctx context.Context, originalID, newID string) (*Session, error) {
	sess, err := s.Load(ctx, originalID)
	if err != nil {
		return nil, err
	}

	forked := sess.Fork(newID)

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range forked.Events() {
		if err := s.saveLocked(e); err != nil {
			return nil, err
		}
	}
	return forked, nil
}

// Search implements the Store interface for JSONLStore.
// For now, it returns an error as Search is optimized for SQLite.
func (s *JSONLStore) Search(ctx context.Context, sessionID string, query string) ([]Event, error) {
	return nil, fmt.Errorf("search not implemented for JSONLStore; use SQLiteStore")
}
