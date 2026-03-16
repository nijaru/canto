package session

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/nijaru/canto/llm"
)

const subscriberBufSize = 64

// subscriber is a single fan-out recipient.
type subscriber struct {
	ch chan Event
}

// Session is a durable container for a conversation.
// All state is derived from an append-only event log.
type Session struct {
	mu          sync.RWMutex
	id          string
	events      []Event
	meta        map[string]any
	subscribers []*subscriber
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

// Append adds a new event to the session and notifies all subscribers.
func (s *Session) Append(e Event) {
	s.mu.Lock()
	s.events = append(s.events, e)
	subs := s.subscribers // snapshot pointer slice under lock
	s.mu.Unlock()

	for _, sub := range subs {
		select {
		case sub.ch <- e:
		default:
			// subscriber too slow; drop event rather than block
		}
	}
}

// Subscribe returns a channel that receives every event appended after this call.
// The channel is buffered and closed when ctx is done.
// Slow subscribers drop events rather than blocking Append.
func (s *Session) Subscribe(ctx context.Context) <-chan Event {
	ch := make(chan Event, subscriberBufSize)
	sub := &subscriber{ch: ch}

	s.mu.Lock()
	s.subscribers = append(s.subscribers, sub)
	s.mu.Unlock()

	go func() {
		<-ctx.Done()
		s.mu.Lock()
		for i, ss := range s.subscribers {
			if ss == sub {
				s.subscribers = append(s.subscribers[:i], s.subscribers[i+1:]...)
				break
			}
		}
		s.mu.Unlock()
		close(ch)
	}()

	return ch
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

// TotalCost returns the sum of costs across all events in the session.
func (s *Session) TotalCost() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var total float64
	for _, e := range s.events {
		total += e.Cost
	}
	return total
}

// Store is an interface for persisting session state.
type Store interface {
	Save(ctx context.Context, e Event) error
	Load(ctx context.Context, sessionID string) (*Session, error)
	Search(ctx context.Context, sessionID string, query string) ([]Event, error)
}
