package session

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
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

	if err := writeEventJSON(f, e); err != nil {
		return err
	}
	_, err = f.Write([]byte("\n"))
	return err
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

	sess := New(sessionID).WithWriter(s)
	reader := bufio.NewReader(f)
	for {
		line, readErr := reader.ReadBytes('\n')
		if readErr != nil && readErr != io.EOF {
			return nil, readErr
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			if readErr == io.EOF {
				break
			}
			continue
		}

		e, err := decodeEventJSON(line)
		if err != nil {
			return nil, err
		}
		// Internal load doesn't need write-through back to itself.
		sess.mu.Lock()
		sess.events = append(sess.events, e)
		sess.mu.Unlock()
		if readErr == io.EOF {
			break
		}
	}

	return sess, nil
}

// LoadUntil loads a session up to (and including) the given event ID.
func (s *JSONLStore) LoadUntil(
	ctx context.Context,
	sessionID string,
	eventID ulid.ULID,
) (*Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	path := filepath.Join(s.dir, fmt.Sprintf("%s.jsonl", sessionID))
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sess := New(sessionID).WithWriter(s)
	reader := bufio.NewReader(f)
	for {
		line, readErr := reader.ReadBytes('\n')
		if readErr != nil && readErr != io.EOF {
			return nil, readErr
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			if readErr == io.EOF {
				break
			}
			continue
		}

		e, err := decodeEventJSON(line)
		if err != nil {
			return nil, err
		}
		// Internal load doesn't need write-through back to itself.
		sess.mu.Lock()
		sess.events = append(sess.events, e)
		sess.mu.Unlock()
		if e.ID == eventID {
			break
		}
		if readErr == io.EOF {
			break
		}
	}
	return sess, nil
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
