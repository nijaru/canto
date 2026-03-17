package session

import (
	"context"
	"github.com/go-json-experiment/json"
	"sync"

	"github.com/nijaru/canto/llm"
	"github.com/oklog/ulid/v2"
)

const subscriberBufSize = 64

// subscriber is a single fan-out recipient.
// The mu guards ch against concurrent trySend and close calls.
type subscriber struct {
	mu     sync.Mutex
	ch     chan Event
	closed bool
}

// trySend delivers e to the subscriber without blocking.
// Safe to call concurrently with close.
func (sub *subscriber) trySend(e Event) {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	if sub.closed {
		return
	}
	select {
	case sub.ch <- e:
	default: // slow subscriber; drop
	}
}

// close marks the subscriber done and closes the channel.
// Idempotent; safe to call concurrently with trySend.
func (sub *subscriber) close() {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	if !sub.closed {
		sub.closed = true
		close(sub.ch)
	}
}

// Writer persists events to a durable store.
type Writer interface {
	Save(ctx context.Context, e Event) error
}

// Reducer computes a state snapshot from a sequence of events.
type Reducer func(state map[string]any, e Event) map[string]any

// Session is a durable container for a conversation.
// All state is derived from an append-only event log.
type Session struct {
	mu          sync.RWMutex
	id          string
	events      []Event
	state       map[string]any
	subscribers []*subscriber
	writer      Writer
	reducer     Reducer
}

// New creates a new session.
func New(id string) *Session {
	return &Session{
		id:    id,
		state: make(map[string]any),
	}
}

// WithReducer attaches a reducer to the session for state management.
func (s *Session) WithReducer(r Reducer) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reducer = r
	// Recompute state from existing events
	s.state = make(map[string]any)
	for _, e := range s.events {
		s.state = r(s.state, e)
	}
	return s
}

// State returns a snapshot of the current session state.
func (s *Session) State() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make(map[string]any, len(s.state))
	for k, v := range s.state {
		res[k] = v
	}
	return res
}

// WithWriter attaches a writer to the session for write-through persistence.
func (s *Session) WithWriter(w Writer) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writer = w
	return s
}

// Fork creates a new session with a new ID, copying all existing events from
// this session. The subscribers are not copied.
func (s *Session) Fork(newID string) *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()

	events := make([]Event, len(s.events))
	for i, e := range s.events {
		e.SessionID = newID
		events[i] = e
	}

	res := &Session{
		id:      newID,
		events:  events,
		state:   make(map[string]any, len(s.state)),
		writer:  s.writer,
		reducer: s.reducer,
	}
	for k, v := range s.state {
		res.state[k] = v
	}
	return res
}

// ID returns the session identifier.
func (s *Session) ID() string {
	return s.id
}

// Append adds a new event to the session and notifies all subscribers.
// If a writer is attached, the event is persisted to the store immediately.
func (s *Session) Append(ctx context.Context, e Event) error {
	s.mu.Lock()
	s.events = append(s.events, e)
	if s.reducer != nil {
		s.state = s.reducer(s.state, e)
	}
	subs := s.subscribers // snapshot pointer slice under lock
	writer := s.writer
	s.mu.Unlock()

	if writer != nil {
		if err := writer.Save(ctx, e); err != nil {
			return err
		}
	}

	for _, sub := range subs {
		sub.trySend(e)
	}
	return nil
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
		sub.close() // safe: mu prevents race with concurrent trySend
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
	// LoadUntil loads a session up to (and including) the given event ID.
	LoadUntil(ctx context.Context, sessionID string, eventID ulid.ULID) (*Session, error)
	// Fork creates a new session by copying all events from an existing session.
	Fork(ctx context.Context, originalSessionID, newSessionID string) (*Session, error)
	Search(ctx context.Context, sessionID string, query string) ([]Event, error)
}
