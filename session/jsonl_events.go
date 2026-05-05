package session

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"

	"github.com/oklog/ulid/v2"
)

// Save appends an event to the session file.
func (s *JSONLStore) Save(ctx context.Context, e Event) error {
	if err := validateWritableEvent(&e); err != nil {
		return err
	}

	mu := s.getSessionMu(e.SessionID)
	mu.Lock()
	defer mu.Unlock()

	if err := s.saveLocked(e); err != nil {
		return err
	}

	s.ancestryMu.Lock()
	defer s.ancestryMu.Unlock()
	_, err := s.ensureRootAncestryLocked(e.SessionID, e.Timestamp)
	return err
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
	_, err = f.Write([]byte("\n"))
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
	if err := replayJSONLEvents(f, replayer, sess, ulid.ULID{}); err != nil {
		return nil, err
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
	if err := replayJSONLEvents(f, replayer, sess, eventID); err != nil {
		return nil, err
	}
	return sess, nil
}

func replayJSONLEvents(
	r io.Reader,
	replayer *Replayer,
	sess *Session,
	until ulid.ULID,
) error {
	reader := bufio.NewReader(r)
	for {
		line, readErr := reader.ReadBytes('\n')
		if readErr != nil && readErr != io.EOF {
			return readErr
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
			return err
		}
		if !until.IsZero() && e.ID.Compare(until) > 0 {
			break
		}
		if err := replayer.Apply(sess, e); err != nil {
			return err
		}
		if !until.IsZero() && e.ID.Compare(until) == 0 {
			break
		}
		if readErr == io.EOF {
			break
		}
	}
	return nil
}
