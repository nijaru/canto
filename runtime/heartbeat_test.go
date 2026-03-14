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
	h.Start()

	// Use @every 1s because cron.WithSeconds() gives 1s resolution
	sessionID := "test-heartbeat"
	if _, err = h.Schedule("@every 1s", sessionID); err != nil {
		t.Fatal(err)
	}

	// Wait for at least 1 execution or timeout
	select {
	case <-p.done:
		// Success
	case <-time.After(3 * time.Second):
		t.Errorf("timed out waiting for heartbeat execution")
	}

	if p.count.Load() < 1 {
		t.Errorf("expected at least 1 execution, got %d", p.count.Load())
	}

	h.Stop()
	// Allow background goroutines to cleanly close file handles before returning (so TempDir cleans up).
	time.Sleep(100 * time.Millisecond)
}
