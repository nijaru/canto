package session

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-json-experiment/json"
	"github.com/oklog/ulid/v2"
)

// JSONLStore is a file-backed store that saves events as JSON lines.
type JSONLStore struct {
	root       *os.Root
	ancestryMu sync.RWMutex
	sessionMus sync.Map // map[string]*sync.RWMutex
}

// NewJSONLStore creates a new JSONL store rooted at dir.
func NewJSONLStore(dir string) (*JSONLStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	return &JSONLStore{root: root}, nil
}

// Close releases the underlying filesystem root.
func (s *JSONLStore) Close() error {
	if s == nil || s.root == nil {
		return nil
	}
	return s.root.Close()
}

func (s *JSONLStore) getSessionMu(sessionID string) *sync.RWMutex {
	mu, _ := s.sessionMus.LoadOrStore(sessionID, &sync.RWMutex{})
	return mu.(*sync.RWMutex)
}

// Save appends an event to the session file.
func (s *JSONLStore) Save(ctx context.Context, e Event) error {
	if err := validateWritableEvent(&e); err != nil {
		return err
	}

	mu := s.getSessionMu(e.SessionID)
	mu.Lock()
	defer mu.Unlock()

	return s.saveLocked(e)
}

func (s *JSONLStore) saveLocked(e Event) error {
	path := fmt.Sprintf("%s.jsonl", e.SessionID)
	f, err := s.root.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := writeEventJSON(f, e); err != nil {
		return err
	}
	if _, err := f.Write([]byte("\n")); err != nil {
		return err
	}
	_, err = s.ensureRootAncestryLocked(e.SessionID, e.Timestamp)
	return err
}

// Load reads all events for a session and reconstructs it.
func (s *JSONLStore) Load(ctx context.Context, sessionID string) (*Session, error) {
	mu := s.getSessionMu(sessionID)
	mu.RLock()
	defer mu.RUnlock()

	path := fmt.Sprintf("%s.jsonl", sessionID)
	f, err := s.root.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return New(sessionID).WithWriter(s), nil
		}
		return nil, err
	}
	defer f.Close()

	replayer := NewReplayer()
	sess := replayer.NewSession(sessionID).WithWriter(s)
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
		if err := replayer.Apply(sess, e); err != nil {
			return nil, err
		}
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
	mu := s.getSessionMu(sessionID)
	mu.RLock()
	defer mu.RUnlock()

	path := fmt.Sprintf("%s.jsonl", sessionID)
	f, err := s.root.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	replayer := NewReplayer()
	sess := replayer.NewSession(sessionID).WithWriter(s)
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

		if e.ID.Compare(eventID) > 0 {
			break
		}

		if err := replayer.Apply(sess, e); err != nil {
			return nil, err
		}

		if e.ID.Compare(eventID) == 0 {
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
	return s.ForkWithOptions(ctx, originalID, newID, ForkOptions{})
}

// ForkWithOptions creates a new session by copying all events from an existing
// session and records session-level ancestry metadata in the JSONL index.
func (s *JSONLStore) ForkWithOptions(
	ctx context.Context,
	originalID, newID string,
	opts ForkOptions,
) (*Session, error) {
	sess, err := s.Load(ctx, originalID)
	if err != nil {
		return nil, err
	}

	return s.BranchSession(ctx, sess, newID, opts)
}

// BranchSession creates a persisted child branch from the current in-memory
// parent session, preserving copied history and ancestry metadata on disk.
func (s *JSONLStore) BranchSession(
	_ context.Context,
	parent *Session,
	newID string,
	opts ForkOptions,
) (*Session, error) {
	if parent == nil {
		return nil, fmt.Errorf("fork live session %q: nil parent", newID)
	}
	parentID := parent.ID()
	forked := parent.Fork(newID)
	parentEvents := parent.Events()
	forkPointEventID := ""
	if n := len(parentEvents); n > 0 {
		forkPointEventID = parentEvents[n-1].ID.String()
	}
	parentCreatedAt := sessionCreatedAt(parent)
	childCreatedAt := time.Now().UTC()

	childMu := s.getSessionMu(newID)
	childMu.Lock()
	defer childMu.Unlock()

	s.ancestryMu.Lock()
	defer s.ancestryMu.Unlock()
	parentDepth, err := s.ensureRootAncestryLocked(parentID, parentCreatedAt)
	if err != nil {
		return nil, err
	}
	if err := s.appendAncestryLocked(SessionAncestry{
		SessionID:        newID,
		ParentSessionID:  parentID,
		ForkPointEventID: forkPointEventID,
		BranchLabel:      opts.BranchLabel,
		ForkReason:       opts.ForkReason,
		Depth:            parentDepth + 1,
		CreatedAt:        childCreatedAt,
	}); err != nil {
		return nil, err
	}
	for _, e := range forked.Events() {
		if err := s.saveLocked(e); err != nil {
			return nil, err
		}
	}
	return forked, nil
}

// Parent returns the persisted ancestry record for the parent of sessionID.
func (s *JSONLStore) Parent(ctx context.Context, sessionID string) (*SessionAncestry, error) {
	s.ancestryMu.RLock()
	defer s.ancestryMu.RUnlock()

	index, err := s.loadAncestryIndexLocked()
	if err != nil {
		return nil, err
	}
	record, ok := index[sessionID]
	if !ok {
		return nil, fmt.Errorf("session ancestry %q not found", sessionID)
	}
	if record.ParentSessionID == "" {
		return nil, nil
	}
	parent, ok := index[record.ParentSessionID]
	if !ok {
		return nil, fmt.Errorf("session ancestry %q not found", record.ParentSessionID)
	}
	return &parent, nil
}

// Children lists the persisted ancestry records for direct children of sessionID.
func (s *JSONLStore) Children(ctx context.Context, sessionID string) ([]SessionAncestry, error) {
	s.ancestryMu.RLock()
	defer s.ancestryMu.RUnlock()

	index, err := s.loadAncestryIndexLocked()
	if err != nil {
		return nil, err
	}
	children := make([]SessionAncestry, 0, 8)
	for _, record := range index {
		if record.ParentSessionID == sessionID {
			children = append(children, record)
		}
	}
	slices.SortFunc(children, func(a, b SessionAncestry) int {
		if cmp := a.CreatedAt.Compare(b.CreatedAt); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.SessionID, b.SessionID)
	})
	return children, nil
}

// Lineage returns the root-to-current ancestry chain for sessionID.
func (s *JSONLStore) Lineage(ctx context.Context, sessionID string) ([]SessionAncestry, error) {
	s.ancestryMu.RLock()
	defer s.ancestryMu.RUnlock()

	index, err := s.loadAncestryIndexLocked()
	if err != nil {
		return nil, err
	}
	return lineageFromMap(sessionID, index)
}

// SaveAncestry persists existing ancestry metadata for portable session
// imports.
func (s *JSONLStore) SaveAncestry(_ context.Context, record SessionAncestry) error {
	if err := validateSessionAncestry(record); err != nil {
		return err
	}
	s.ancestryMu.Lock()
	defer s.ancestryMu.Unlock()
	return s.appendAncestryLocked(record)
}

func (s *JSONLStore) ensureRootAncestryLocked(sessionID string, createdAt time.Time) (int, error) {
	index, err := s.loadAncestryIndexLocked()
	if err != nil {
		return 0, err
	}
	if record, ok := index[sessionID]; ok {
		return record.Depth, nil
	}
	record := SessionAncestry{
		SessionID: sessionID,
		Depth:     0,
		CreatedAt: createdAt,
	}
	if err := s.appendAncestryRecordLocked(record); err != nil {
		return 0, err
	}
	return 0, nil
}

func (s *JSONLStore) appendAncestryLocked(record SessionAncestry) error {
	index, err := s.loadAncestryIndexLocked()
	if err != nil {
		return err
	}
	if _, exists := index[record.SessionID]; exists {
		return nil
	}
	return s.appendAncestryRecordLocked(record)
}

func (s *JSONLStore) appendAncestryRecordLocked(record SessionAncestry) error {
	path := "ancestry.jsonl"
	f, err := s.root.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := json.MarshalWrite(f, record); err != nil {
		return err
	}
	_, err = f.Write([]byte("\n"))
	return err
}

func (s *JSONLStore) loadAncestryIndexLocked() (map[string]SessionAncestry, error) {
	path := "ancestry.jsonl"
	f, err := s.root.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]SessionAncestry), nil
		}
		return nil, err
	}
	defer f.Close()

	index := make(map[string]SessionAncestry)
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

		var record SessionAncestry
		if err := json.Unmarshal(line, &record); err != nil {
			return nil, err
		}
		index[record.SessionID] = record
		if readErr == io.EOF {
			break
		}
	}
	return index, nil
}
