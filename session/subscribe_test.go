package session

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestSubscribe_ReceivesEvents(t *testing.T) {
	s := New("sess-1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := s.Subscribe(ctx)

	e := NewEvent("sess-1", EventTypeMessageAdded, map[string]string{"role": "user"})
	s.Append(e)

	select {
	case got := <-ch:
		if got.ID != e.ID {
			t.Fatalf("got event ID %v, want %v", got.ID, e.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestSubscribe_MultipleSubscribers(t *testing.T) {
	s := New("sess-2")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch1 := s.Subscribe(ctx)
	ch2 := s.Subscribe(ctx)

	e := NewEvent("sess-2", EventTypeToolCalled, nil)
	s.Append(e)

	for _, ch := range []<-chan Event{ch1, ch2} {
		select {
		case got := <-ch:
			if got.ID != e.ID {
				t.Fatalf("got event ID %v, want %v", got.ID, e.ID)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for event on subscriber")
		}
	}
}

func TestSubscribe_CancelClosesChannel(t *testing.T) {
	s := New("sess-3")
	ctx, cancel := context.WithCancel(context.Background())

	ch := s.Subscribe(ctx)
	cancel()

	// Channel should close shortly after cancel.
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected closed channel, got value")
		}
	case <-time.After(time.Second):
		t.Fatal("channel not closed after context cancel")
	}

	// Subscriber removed from list.
	s.mu.RLock()
	n := len(s.subscribers)
	s.mu.RUnlock()
	if n != 0 {
		t.Fatalf("subscriber list len = %d, want 0", n)
	}
}

func TestSubscribe_SlowSubscriberDoesNotBlock(t *testing.T) {
	s := New("sess-4")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Subscribe but never read.
	_ = s.Subscribe(ctx)

	done := make(chan struct{})
	go func() {
		// Fill beyond buffer — Append must not block.
		for i := range subscriberBufSize + 10 {
			_ = i
			s.Append(NewEvent("sess-4", EventTypeMessageAdded, nil))
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Append blocked on slow subscriber")
	}
}

func TestSubscribe_NoSubscribers(t *testing.T) {
	s := New("sess-5")
	// Append with no subscribers must not panic.
	s.Append(NewEvent("sess-5", EventTypeSessionCreated, nil))
}

// TestSubscribe_ConcurrentAppendCancel exercises the race between Append and
// context cancellation. Run with -race to verify no data race or panic.
func TestSubscribe_ConcurrentAppendCancel(t *testing.T) {
	const goroutines = 8
	const eventsPerWriter = 200

	s := New("sess-race")
	ctx, cancel := context.WithCancel(context.Background())

	_ = s.Subscribe(ctx)

	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range eventsPerWriter {
				s.Append(NewEvent("sess-race", EventTypeMessageAdded, nil))
			}
		}()
	}

	// Cancel mid-flight — should not panic.
	go func() {
		time.Sleep(2 * time.Millisecond)
		cancel()
	}()

	wg.Wait()
}

func TestSubscribe_EventsBeforeSubscribeNotReceived(t *testing.T) {
	s := New("sess-6")

	// Append before subscribe.
	s.Append(NewEvent("sess-6", EventTypeMessageAdded, nil))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := s.Subscribe(ctx)

	// Append after subscribe.
	e := NewEvent("sess-6", EventTypeToolCalled, nil)
	s.Append(e)

	select {
	case got := <-ch:
		if got.ID != e.ID {
			t.Fatalf("got wrong event; want post-subscribe event")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for post-subscribe event")
	}
}
