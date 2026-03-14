package runtime

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

type mockProvider struct {
	llm.Provider
	count atomic.Int32
	done  chan struct{}
}

func (m *mockProvider) ID() string { return "mock" }
func (m *mockProvider) Generate(ctx context.Context, req *llm.LLMRequest) (*llm.LLMResponse, error) {
	if m.count.Add(1) == 1 {
		close(m.done)
	}
	return &llm.LLMResponse{Content: "heartbeat received"}, nil
}

func TestHeartbeat(t *testing.T) {
	p := &mockProvider{done: make(chan struct{})}
	a := agent.New("scheduler", "test", "gpt-4", p, nil)
	s, err := session.NewJSONLStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := NewRunner(s, a)

	h := NewHeartbeat(r)

	sessionID := "test-heartbeat"
	if err = h.Schedule("@every 1s", sessionID); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Run Start in background; it blocks until ctx is cancelled.
	errCh := make(chan error, 1)
	go func() { errCh <- h.Start(ctx) }()

	// Wait for at least 1 execution or timeout.
	select {
	case <-p.done:
		// Success — cancel the context to let Start return.
		cancel()
	case <-ctx.Done():
		t.Error("timed out waiting for heartbeat execution")
	}

	// Start must return after ctx is cancelled (and in-flights drain).
	if err := <-errCh; err != nil {
		t.Errorf("Start returned error: %v", err)
	}

	if p.count.Load() < 1 {
		t.Errorf("expected at least 1 execution, got %d", p.count.Load())
	}
}
