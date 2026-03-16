package session

import (
	"context"
	"sync"
	"testing"
	"time"
)

// memStore is an in-memory Store for testing.
type memStore struct {
	mu     sync.Mutex
	events []Event
}

func (m *memStore) Save(_ context.Context, e Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, e)
	return nil
}

func (m *memStore) Load(_ context.Context, _ string) (*Session, error) { return nil, nil }

func (m *memStore) Search(_ context.Context, _, _ string) ([]Event, error) { return nil, nil }

func (m *memStore) saved() []Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Event, len(m.events))
	copy(out, m.events)
	return out
}

func TestAttachWriteThrough_SavesEvents(t *testing.T) {
	sess := New("wt-1")
	store := &memStore{}

	cancel := AttachWriteThrough(context.Background(), sess, store)
	defer cancel()

	sess.Append(NewEvent("wt-1", EventTypeMessageAdded, nil))
	sess.Append(NewEvent("wt-1", EventTypeMessageAdded, nil))

	// Give the goroutine time to save.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(store.saved()) == 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	saved := store.saved()
	if len(saved) != 2 {
		t.Fatalf("saved %d events, want 2", len(saved))
	}
}

func TestAttachWriteThrough_CancelStops(t *testing.T) {
	sess := New("wt-2")
	store := &memStore{}

	cancel := AttachWriteThrough(context.Background(), sess, store)
	cancel() // detach immediately

	// Append after cancel — should NOT be saved.
	time.Sleep(20 * time.Millisecond)
	sess.Append(NewEvent("wt-2", EventTypeMessageAdded, nil))
	time.Sleep(20 * time.Millisecond)

	if n := len(store.saved()); n != 0 {
		t.Fatalf("saved %d events after cancel, want 0", n)
	}
}

func TestAttachWriteThrough_EventsBeforeAttachNotSaved(t *testing.T) {
	sess := New("wt-3")
	store := &memStore{}

	// Append before attaching.
	sess.Append(NewEvent("wt-3", EventTypeHandoff, nil))

	cancel := AttachWriteThrough(context.Background(), sess, store)
	defer cancel()

	// Append after attaching.
	sess.Append(NewEvent("wt-3", EventTypeMessageAdded, nil))

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(store.saved()) == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	saved := store.saved()
	if len(saved) != 1 {
		t.Fatalf("saved %d events, want 1 (only post-attach)", len(saved))
	}
	if saved[0].Type != EventTypeMessageAdded {
		t.Fatalf("saved event type = %q, want message_added", saved[0].Type)
	}
}
