package governor_test

import (
	"context"
	"errors"
	"testing"
	"time"

	ccontext "github.com/nijaru/canto/context"
	"github.com/nijaru/canto/governor"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

type mockMutator struct {
	delay time.Duration
	err   error
	calls int
}

func (m *mockMutator) Mutate(
	ctx context.Context,
	p llm.Provider,
	model string,
	sess *session.Session,
) error {
	m.calls++
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	return m.err
}

func (m *mockMutator) Effects() ccontext.SideEffects {
	return ccontext.SideEffects{Session: true}
}

func (m *mockMutator) CompactionStrategy() string {
	return "mock"
}

func TestCompactionQueue_AsyncExecution(t *testing.T) {
	mutator := &mockMutator{delay: 50 * time.Millisecond}
	queue := governor.NewCompactionQueue(mutator)

	if queue.IsCompacting() {
		t.Fatal("should not be compacting initially")
	}

	err := queue.Mutate(context.Background(), nil, "model", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !queue.IsCompacting() {
		t.Fatal("should be compacting after Mutate call")
	}

	// Wait for completion
	err = queue.Wait(context.Background())
	if err != nil {
		t.Fatalf("unexpected error from Wait: %v", err)
	}

	if queue.IsCompacting() {
		t.Fatal("should not be compacting after Wait completes")
	}
	if mutator.calls != 1 {
		t.Fatalf("expected 1 call, got %d", mutator.calls)
	}
}

func TestCompactionQueue_SkipsConcurrentCalls(t *testing.T) {
	mutator := &mockMutator{delay: 50 * time.Millisecond}
	queue := governor.NewCompactionQueue(mutator)

	_ = queue.Mutate(context.Background(), nil, "model", nil)
	_ = queue.Mutate(context.Background(), nil, "model", nil) // Should be skipped

	_ = queue.Wait(context.Background())

	if mutator.calls != 1 {
		t.Fatalf("expected 1 call due to concurrency skip, got %d", mutator.calls)
	}
}

func TestCompactionQueue_PropagatesError(t *testing.T) {
	expectedErr := errors.New("compaction failed")
	mutator := &mockMutator{err: expectedErr}
	queue := governor.NewCompactionQueue(mutator)

	_ = queue.Mutate(context.Background(), nil, "model", nil)
	err := queue.Wait(context.Background())

	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected %v, got %v", expectedErr, err)
	}
}

func TestCompactionQueue_SequentialCycles(t *testing.T) {
	mutator := &mockMutator{delay: 10 * time.Millisecond}
	queue := governor.NewCompactionQueue(mutator)

	for i := 0; i < 3; i++ {
		err := queue.Mutate(context.Background(), nil, "model", nil)
		if err != nil {
			t.Fatalf("cycle %d: Mutate error: %v", i, err)
		}
		if err := queue.Wait(context.Background()); err != nil {
			t.Fatalf("cycle %d: Wait error: %v", i, err)
		}
	}

	if mutator.calls != 3 {
		t.Fatalf("expected 3 calls across sequential cycles, got %d", mutator.calls)
	}
}
