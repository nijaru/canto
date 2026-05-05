package session

import (
	"context"
	"errors"
	"iter"
	"log/slog"
	"sync"

	"github.com/nijaru/canto/llm"
	"github.com/oklog/ulid/v2"
)

type contextKey int

const (
	metadataKey contextKey = iota
)

// WithMetadata attaches metadata to the context. This metadata will be
// automatically added to all events appended to a session using this context.
func WithMetadata(ctx context.Context, md map[string]any) context.Context {
	if len(md) == 0 {
		return ctx
	}
	existing, _ := ctx.Value(metadataKey).(map[string]any)
	if len(existing) == 0 {
		return context.WithValue(ctx, metadataKey, md)
	}

	merged := make(map[string]any, len(existing)+len(md))
	for k, v := range existing {
		merged[k] = v
	}
	for k, v := range md {
		merged[k] = v
	}
	return context.WithValue(ctx, metadataKey, merged)
}

// MetadataFromContext retrieves metadata from the context.
func MetadataFromContext(ctx context.Context) map[string]any {
	md, _ := ctx.Value(metadataKey).(map[string]any)
	return md
}

const subscriberBufSize = 64

var errEmptyAssistantMessage = errors.New(
	"session append: assistant message has no content, reasoning, thinking blocks, or tool calls",
)

var errInvalidMessageRole = errors.New("session append: message has invalid role")

var errUnmatchedToolMessage = errors.New(
	"session append: tool message has no matching pending assistant tool call",
)

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
	writerCh    *writerChannel
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

// ID returns the session identifier.
func (s *Session) ID() string {
	return s.id
}

func (s *Session) setWriterChannel(ch chan<- Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writerCh = &writerChannel{ch: ch}
}

func (s *Session) unsetWriterChannel() {
	s.mu.Lock()
	writerCh := s.writerCh
	s.writerCh = nil
	s.mu.Unlock()

	if writerCh != nil {
		writerCh.close()
	}
}

// Append adds a new event to the session and notifies all subscribers.
// If a writer is attached, the event is persisted to the store immediately.
// If the context contains metadata (via WithMetadata), it is merged into the event's metadata.
func (s *Session) Append(ctx context.Context, e Event) error {
	if err := validateWritableEvent(&e); err != nil {
		return err
	}

	if md := MetadataFromContext(ctx); len(md) > 0 {
		newMd := make(map[string]any, len(e.Metadata)+len(md))
		if e.Metadata != nil {
			for k, v := range e.Metadata {
				newMd[k] = v
			}
		}
		for k, v := range md {
			if _, exists := newMd[k]; !exists {
				newMd[k] = v
			}
		}
		e.Metadata = newMd
	}

	s.mu.Lock()
	if err := s.validateWritableSequenceLocked(&e); err != nil {
		s.mu.Unlock()
		return err
	}
	writer := s.writer
	writerCh := s.writerCh

	if writer != nil {
		if err := writer.Save(ctx, e); err != nil {
			s.mu.Unlock()
			return err
		}
	}

	if writerCh != nil {
		if err := writerCh.send(ctx, e); err != nil {
			s.mu.Unlock()
			return err
		}
	}

	s.events = append(s.events, e)
	if s.reducer != nil {
		s.state = s.reducer(s.state, e)
	}
	subs := append([]*subscriber(nil), s.subscribers...)
	s.mu.Unlock()

	for _, sub := range subs {
		sub.trySend(e)
	}
	return nil
}

func (s *Session) validateWritableSequenceLocked(e *Event) error {
	if e.Type != MessageAdded {
		return nil
	}
	msg, err := e.ensureMessage()
	if err != nil {
		return err
	}
	if msg.Role != llm.RoleTool {
		return nil
	}
	if msg.ToolID == "" {
		return errUnmatchedToolMessage
	}
	pending, err := pendingToolCalls(s.events)
	if err != nil {
		return err
	}
	if pending[msg.ToolID] == 0 {
		return errUnmatchedToolMessage
	}
	return nil
}

func validateWritableEvent(e *Event) error {
	if e.Type != MessageAdded {
		return nil
	}
	msg, err := e.ensureMessage()
	if err != nil {
		return err
	}
	return validateModelMessage(*msg)
}

func validateModelMessage(msg llm.Message) error {
	switch msg.Role {
	case llm.RoleSystem, llm.RoleDeveloper, llm.RoleUser, llm.RoleAssistant, llm.RoleTool:
	default:
		return errInvalidMessageRole
	}
	if msg.Role == llm.RoleAssistant && !assistantMessageHasPayload(msg) {
		return errEmptyAssistantMessage
	}
	return nil
}

func (s *Session) removeSubscriber(target *subscriber) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, sub := range s.subscribers {
		if sub == target {
			s.subscribers = append(s.subscribers[:i], s.subscribers[i+1:]...)
			return
		}
	}
}

// Events returns the full event log.
func (s *Session) Events() []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make([]Event, len(s.events))
	copy(res, s.events)
	for i := range res {
		if err := res[i].ensureMetadata(); err != nil {
			slog.Warn("event metadata decode failed", "event_id", res[i].ID, "error", err)
		}
	}
	return res
}

// HasWatchers returns true if the session has any active Watch subscriptions.
func (s *Session) HasWatchers() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.subscribers) > 0
}

func (s *Session) snapshotEvents() []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make([]Event, len(s.events))
	copy(res, s.events)
	return res
}

// All returns an iterator over the full event log from oldest to newest.
func (s *Session) All() iter.Seq[Event] {
	return func(yield func(Event) bool) {
		for _, e := range s.snapshotEvents() {
			if !yield(e) {
				return
			}
		}
	}
}

// Backward returns an iterator over the full event log from newest to oldest.
func (s *Session) Backward() iter.Seq[Event] {
	return func(yield func(Event) bool) {
		events := s.snapshotEvents()
		for i := len(events) - 1; i >= 0; i-- {
			if !yield(events[i]) {
				return
			}
		}
	}
}

// Messages extracts all messages from the event log.
func (s *Session) Messages() []llm.Message {
	s.mu.Lock() // Use Lock instead of RLock to allow ensureMessage to mutate the internal slice
	defer s.mu.Unlock()

	var res []llm.Message
	for i := range s.events {
		if s.events[i].Type == MessageAdded {
			m, err := s.events[i].ensureMessage()
			if err == nil {
				res = append(res, *m)
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

// IsWaiting returns true if the session is currently waiting for external input
// or approval (HITL).
func (s *Session) IsWaiting() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Scan backwards for the latest lifecycle state.
	for i := len(s.events) - 1; i >= 0; i-- {
		e := s.events[i]
		switch e.Type {
		case WaitStarted, ApprovalRequested:
			return true
		case WaitResolved, ApprovalResolved, ApprovalCanceled:
			return false
		}
	}
	return false
}

// LastEvent returns the most recent event in the session, if any.
func (s *Session) LastEvent() (Event, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.events) == 0 {
		return Event{}, false
	}
	return s.events[len(s.events)-1], true
}

// LastMessage returns the most recent message in the session, if any.
func (s *Session) LastMessage() (llm.Message, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := len(s.events) - 1; i >= 0; i-- {
		e := &s.events[i]
		if e.Type == MessageAdded {
			m, err := e.ensureMessage()
			if err == nil {
				return *m, true
			}
		}
	}
	return llm.Message{}, false
}

// LastAssistantMessage returns the most recent assistant message without tool
// calls in the session, if any.
func (s *Session) LastAssistantMessage() (llm.Message, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := len(s.events) - 1; i >= 0; i-- {
		e := &s.events[i]
		if e.Type == MessageAdded {
			m, err := e.ensureMessage()
			if err == nil && m.Role == llm.RoleAssistant && len(m.Calls) == 0 &&
				validModelMessage(*m) {
				return *m, true
			}
		}
	}
	return llm.Message{}, false
}

// Store is an interface for persisting session state.
type Store interface {
	Save(ctx context.Context, e Event) error
	Load(ctx context.Context, sessionID string) (*Session, error)
	// LoadUntil loads a session up to (and including) the given event ID.
	LoadUntil(ctx context.Context, sessionID string, eventID ulid.ULID) (*Session, error)
	// Fork creates a new session by copying all events from an existing session.
	Fork(ctx context.Context, originalSessionID, newSessionID string) (*Session, error)
}

// SearchStore exposes full-text search over persisted session events.
// Not every store implements this capability.
type SearchStore interface {
	Search(ctx context.Context, sessionID string, query string) ([]Event, error)
}

var (
	_ SessionTreeStore   = (*SQLiteStore)(nil)
	_ ForkStore          = (*SQLiteStore)(nil)
	_ SessionBranchStore = (*SQLiteStore)(nil)
	_ SessionTreeStore   = (*JSONLStore)(nil)
	_ ForkStore          = (*JSONLStore)(nil)
	_ SessionBranchStore = (*JSONLStore)(nil)
)
