// Package swarm provides decentralized multi-agent coordination via a shared blackboard.
//
// No central orchestrator — agents post observations and claim tasks from a
// shared blackboard. Coordination emerges from shared state, not explicit routing.
// All task claiming is atomic (first-write-wins); no agent can claim a task
// already held by another.
package swarm

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Task is a unit of work posted to the blackboard.
type Task struct {
	ID          string
	Description string
	ClaimedBy   string // empty if unclaimed
	ClaimedAt   time.Time
}

// Blackboard is the shared coordination surface for a swarm.
// All methods must be safe for concurrent access.
type Blackboard interface {
	// Post writes an arbitrary key-value pair under the given agent's namespace.
	Post(ctx context.Context, agentID, key string, value any) error

	// Read retrieves a value by key. Returns (nil, nil) if not found.
	Read(ctx context.Context, key string) (any, error)

	// ReadAgent retrieves a value by agent and key (matches Post namespace).
	ReadAgent(ctx context.Context, agentID, key string) (any, error)

	// ClaimTask atomically claims an unclaimed task for agentID.
	// Returns true if the claim succeeded; false if already claimed.
	ClaimTask(ctx context.Context, agentID, taskID string) (bool, error)

	// ListUnclaimed returns all tasks that have not yet been claimed.
	ListUnclaimed(ctx context.Context) ([]Task, error)

	// AddTask adds a new unclaimed task to the board (used by swarm setup).
	AddTask(ctx context.Context, task Task) error
}

// MemoryBlackboard is an in-memory, thread-safe Blackboard implementation.
// Suitable for single-process swarms. For distributed use, replace with a
// Redis or SQLite-backed implementation that satisfies the same interface.
type MemoryBlackboard struct {
	mu    sync.RWMutex
	kv    map[string]any
	tasks map[string]*Task
}

// NewMemoryBlackboard creates an empty in-memory blackboard.
func NewMemoryBlackboard() *MemoryBlackboard {
	return &MemoryBlackboard{
		kv:    make(map[string]any),
		tasks: make(map[string]*Task),
	}
}

func (b *MemoryBlackboard) Post(_ context.Context, agentID, key string, value any) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.kv[agentID+"/"+key] = value
	return nil
}

func (b *MemoryBlackboard) Read(_ context.Context, key string) (any, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.kv[key], nil
}

func (b *MemoryBlackboard) ReadAgent(_ context.Context, agentID, key string) (any, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.kv[agentID+"/"+key], nil
}

func (b *MemoryBlackboard) ClaimTask(_ context.Context, agentID, taskID string) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	t, ok := b.tasks[taskID]
	if !ok {
		return false, fmt.Errorf("blackboard: task %q not found", taskID)
	}
	if t.ClaimedBy != "" {
		return false, nil // already claimed
	}
	t.ClaimedBy = agentID
	t.ClaimedAt = time.Now().UTC()
	return true, nil
}

func (b *MemoryBlackboard) ListUnclaimed(_ context.Context) ([]Task, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var out []Task
	for _, t := range b.tasks {
		if t.ClaimedBy == "" {
			out = append(out, *t)
		}
	}
	return out, nil
}

func (b *MemoryBlackboard) AddTask(_ context.Context, task Task) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.tasks[task.ID]; exists {
		return fmt.Errorf("blackboard: task %q already exists", task.ID)
	}
	cp := task // copy
	b.tasks[task.ID] = &cp
	return nil
}
