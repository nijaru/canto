package session

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/nijaru/canto/llm"
)

// Session is a durable container for a conversation.
// All state is derived from an append-only event log.
type Session struct {
	mu     sync.RWMutex
	id     string
	events []Event
	meta   map[string]any
}

// New creates a new session.
func New(id string) *Session {
	return &Session{
		id:   id,
		meta: make(map[string]any),
	}
}

// ID returns the session identifier.
func (s *Session) ID() string {
	return s.id
}

// Append adds a new event to the session.
func (s *Session) Append(e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
}

// Events returns the full event log.
func (s *Session) Events() []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make([]Event, len(s.events))
	copy(res, s.events)
	return res
}

// Messages extracts all messages from the event log.
func (s *Session) Messages() []llm.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var res []llm.Message
	for _, e := range s.events {
		if e.Type == EventTypeMessageAdded {
			var m llm.Message
			if err := json.Unmarshal(e.Data, &m); err == nil {
				res = append(res, m)
			}
		}
	}
	return res
}

// Store is an interface for persisting session state.
type Store interface {
	Save(ctx context.Context, e Event) error
	Load(ctx context.Context, sessionID string) (*Session, error)
	Search(ctx context.Context, sessionID string, query string) ([]Event, error)
}
