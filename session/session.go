package session

import (
	"context"
	"crypto/rand"
	"log/slog"
	"sync"

	"github.com/go-json-experiment/json"

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

// subscriber is a single fan-out recipient.
// The mu guards ch against concurrent trySend and close calls.
type subscriber struct {
	mu     sync.Mutex
	ch     chan Event
	closed bool
}

type writerChannel struct {
	mu     sync.RWMutex
	ch     chan<- Event
	closed bool
	wg     sync.WaitGroup
}

func (w *writerChannel) send(ctx context.Context, e Event) error {
	w.mu.RLock()
	if w.closed {
		w.mu.RUnlock()
		return nil
	}
	w.wg.Add(1)
	ch := w.ch
	w.mu.RUnlock()

	defer w.wg.Done()
	select {
	case ch <- e:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *writerChannel) close() {
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
	w.wg.Wait()
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

// Fork creates a new session with a new ID, copying all existing events from
// this session. The subscribers are not copied.
func (s *Session) Fork(newID string) *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()

	events := make([]Event, len(s.events))
	entropy := ulid.Monotonic(rand.Reader, 0)
	idMap := make(map[string]string, len(s.events))
	for i, e := range s.events {
		if err := e.ensureMetadata(); err != nil {
			slog.Warn("fork metadata decode failed", "event_id", e.ID, "error", err)
		}
		originSessionID := e.SessionID
		originEventID := e.ID
		e.ID = ulid.MustNew(ulid.Timestamp(e.Timestamp), entropy)
		idMap[originEventID.String()] = e.ID.String()
		e.SessionID = newID
		e.Metadata = cloneMetadata(e.Metadata)
		e.Metadata["fork_origin"] = ForkOrigin{
			SessionID: originSessionID,
			EventID:   originEventID.String(),
		}.metadataValue()
		events[i] = e
	}
	for i, e := range events {
		events[i] = remapForkedEventData(e, idMap)
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

func cloneMetadata(src map[string]any) map[string]any {
	if len(src) == 0 {
		return make(map[string]any)
	}

	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func remapForkedEventData(e Event, idMap map[string]string) Event {
	snapshot, ok, err := e.CompactionSnapshot()
	if err != nil || !ok {
		return e
	}

	rewritten, marshalErr := json.Marshal(remapCompactionSnapshot(snapshot, idMap))
	if marshalErr != nil {
		return e
	}
	e.Data = rewritten
	return e
}

// ID returns the session identifier.
func (s *Session) ID() string {
	return s.id
}

// SetWriterChannel registers a channel that receives every event appended to the session.
// Unlike Subscribe, this channel is non-lossy: Append will block until the event
// is accepted by the channel.
func (s *Session) SetWriterChannel(ch chan<- Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writerCh = &writerChannel{ch: ch}
}

// UnsetWriterChannel removes the writer channel.
func (s *Session) UnsetWriterChannel() {
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

	s.mu.RLock()
	writer := s.writer
	writerCh := s.writerCh
	s.mu.RUnlock()

	if writer != nil {
		if err := writer.Save(ctx, e); err != nil {
			return err
		}
	}

	if writerCh != nil {
		if err := writerCh.send(ctx, e); err != nil {
			return err
		}
	}

	s.mu.Lock()
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

// HasSubscribers returns true if the session has any active subscribers.
func (s *Session) HasSubscribers() bool {
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

// ForEachEvent executes fn for each event in the session, from oldest to newest.
// The iteration stops if fn returns false.
func (s *Session) ForEachEvent(fn func(Event) bool) {
	s.mu.RLock()
	events := s.events
	s.mu.RUnlock()

	for _, e := range events {
		if !fn(e) {
			break
		}
	}
}

// ForEachEventReverse executes fn for each event in the session, from newest to oldest.
// The iteration stops if fn returns false.
func (s *Session) ForEachEventReverse(fn func(Event) bool) {
	s.mu.RLock()
	events := s.events
	s.mu.RUnlock()

	for i := len(events) - 1; i >= 0; i-- {
		if !fn(events[i]) {
			break
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
			if err == nil && m.Role == llm.RoleAssistant && len(m.Calls) == 0 {
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
	Search(ctx context.Context, sessionID string, query string) ([]Event, error)
}

var (
	_ SessionTreeStore = (*SQLiteStore)(nil)
	_ ForkStore        = (*SQLiteStore)(nil)
	_ SessionTreeStore = (*JSONLStore)(nil)
	_ ForkStore        = (*JSONLStore)(nil)
)
