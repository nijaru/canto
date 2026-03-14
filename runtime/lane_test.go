package runtime

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestLaneManager_Serialization(t *testing.T) {
	m := NewLaneManager()
	sessionID := "test-session"
	
	var (
		mu      sync.Mutex
		results []int
	)

	count := 5
	channels := make([]<-chan error, count)
	
	for i := 0; i < count; i++ {
		val := i
		channels[i] = m.Execute(context.Background(), sessionID, func(ctx context.Context) error {
			// Simulate work
			time.Sleep(10 * time.Millisecond)
			mu.Lock()
			results = append(results, val)
			mu.Unlock()
			return nil
		})
	}

	// Wait for all to finish
	for _, ch := range channels {
		if err := <-ch; err != nil {
			t.Errorf("request failed: %v", err)
		}
	}

	if len(results) != count {
		t.Errorf("expected %d results, got %d", count, len(results))
	}

	// Check order
	for i, res := range results {
		if res != i {
			t.Errorf("expected result %d to be %d, got %d", i, i, res)
		}
	}
}

func TestLaneManager_Concurrency(t *testing.T) {
	m := NewLaneManager()
	
	// Two different sessions should run concurrently
	session1 := "s1"
	session2 := "s2"
	
	start := time.Now()
	
	ch1 := m.Execute(context.Background(), session1, func(ctx context.Context) error {
		time.Sleep(50 * time.Millisecond)
		return nil
	})
	ch2 := m.Execute(context.Background(), session2, func(ctx context.Context) error {
		time.Sleep(50 * time.Millisecond)
		return nil
	})
	
	// If serial, this would take 100ms. If concurrent, ~50ms.
	<-ch1
	<-ch2
	
	duration := time.Since(start)
	if duration > 90*time.Millisecond {
		t.Errorf("expected sessions to run concurrently, but took %v", duration)
	}
}

func TestLaneManager_IdleTimeout(t *testing.T) {
	m := NewLaneManager()
	m.IdleTimeout = 100 * time.Millisecond
	sessionID := "timeout-session"

	// Create a lane
	ch := m.Execute(context.Background(), sessionID, func(ctx context.Context) error {
		return nil
	})
	<-ch

	m.mu.RLock()
	_, ok := m.lanes[sessionID]
	m.mu.RUnlock()
	if !ok {
		t.Fatal("expected lane to exist after first execution")
	}

	// Wait for idle timeout
	time.Sleep(200 * time.Millisecond)

	m.mu.RLock()
	_, ok = m.lanes[sessionID]
	m.mu.RUnlock()
	if ok {
		t.Fatal("expected lane to be removed after idle timeout")
	}
}
