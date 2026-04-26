package governor

import (
	"context"
	"sync"
	"time"

	"github.com/nijaru/canto/llm"
	prompt "github.com/nijaru/canto/prompt"
	"github.com/nijaru/canto/session"
)

const defaultCompactTimeout = 5 * time.Minute

// CompactQueue manages background compaction tasks.
// It wraps an underlying ContextMutator (like Summarizer) and runs it asynchronously.
// This allows the main CLI/TUI thread to queue incoming user messages without freezing
// while the agent's durable state is rebuilt.
type CompactQueue struct {
	mutator prompt.ContextMutator

	mu      sync.Mutex
	running bool
	done    chan struct{}
	err     error
}

// NewCompactQueue creates a non-blocking wrapper for a compaction mutator.
func NewCompactQueue(m prompt.ContextMutator) *CompactQueue {
	return &CompactQueue{
		mutator: m,
		done:    make(chan struct{}),
	}
}

// Effects delegates to the underlying mutator if it implements prompt.SideEffects.
func (q *CompactQueue) Effects() prompt.SideEffects {
	if eff, ok := q.mutator.(interface{ Effects() prompt.SideEffects }); ok {
		return eff.Effects()
	}
	return prompt.SideEffects{}
}

// CompactionStrategy delegates to the underlying mutator if it implements prompt.Compactor.
func (q *CompactQueue) CompactionStrategy() string {
	if cmp, ok := q.mutator.(prompt.Compactor); ok {
		return cmp.CompactionStrategy()
	}
	return "async"
}

// Mutate triggers the underlying mutator asynchronously if it is not already running.
// It returns immediately without error. Call Wait() to block until completion if needed.
func (q *CompactQueue) Mutate(
	ctx context.Context,
	p llm.Provider,
	model string,
	sess *session.Session,
) error {
	q.mu.Lock()
	if q.running {
		q.mu.Unlock()
		return nil
	}
	q.running = true
	// Replace done channel so subsequent Mutate calls can be awaited sequentially
	q.done = make(chan struct{})
	q.err = nil
	q.mu.Unlock()

	// Detach context so background compaction isn't killed if the immediate request finishes
	bgCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultCompactTimeout)

	go func() {
		defer func() {
			q.mu.Lock()
			q.running = false
			close(q.done)
			q.mu.Unlock()
			cancel()
		}()

		err := q.mutator.Mutate(bgCtx, p, model, sess)

		q.mu.Lock()
		q.err = err
		q.mu.Unlock()
	}()

	return nil
}

// IsCompacting returns true if a compaction is currently running.
func (q *CompactQueue) IsCompacting() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.running
}

// Wait blocks until the current compaction finishes, returning its error.
// If no compaction is running, it returns nil immediately.
func (q *CompactQueue) Wait(ctx context.Context) error {
	q.mu.Lock()
	running := q.running
	done := q.done
	q.mu.Unlock()

	if !running {
		q.mu.Lock()
		err := q.err
		q.mu.Unlock()
		return err
	}

	select {
	case <-done:
		q.mu.Lock()
		defer q.mu.Unlock()
		return q.err
	case <-ctx.Done():
		return ctx.Err()
	}
}
