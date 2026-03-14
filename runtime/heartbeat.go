package runtime

import (
	"context"
	"fmt"
	"sync"

	"github.com/robfig/cron/v3"
)

// Heartbeat manages scheduled execution of agent tasks.
type Heartbeat struct {
	mu     sync.RWMutex
	cron   *cron.Cron
	runner *Runner
	tasks  map[cron.EntryID]string // ID -> SessionID
	ctx    context.Context
	cancel context.CancelFunc
}

// NewHeartbeat creates a new heartbeat manager.
func NewHeartbeat(r *Runner) *Heartbeat {
	ctx, cancel := context.WithCancel(context.Background())
	return &Heartbeat{
		cron:   cron.New(cron.WithSeconds()), // Support 6-field cron expressions
		runner: r,
		tasks:  make(map[cron.EntryID]string),
		ctx:    ctx,
		cancel: cancel,
	}
}

// Schedule adds a task to be executed according to the cron expression.
// Supports standard cron "0 30 * * * *" and "@every 5s" / "@daily" etc.
func (h *Heartbeat) Schedule(spec string, sessionID string) (cron.EntryID, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	id, err := h.cron.AddFunc(spec, func() {
		fmt.Printf("heartbeat: triggering task for session %s\n", sessionID)
		// Run task in its own goroutine to avoid blocking the scheduler
		go func() {
			if err := h.runner.Run(h.ctx, sessionID); err != nil {
				// TODO: Structured logging via x/obs
				fmt.Printf("heartbeat: task failed for session %s: %v\n", sessionID, err)
			}
		}()
	})
	if err != nil {
		return 0, err
	}

	h.tasks[id] = sessionID
	return id, nil
}

// Start starts the heartbeat scheduler.
func (h *Heartbeat) Start() {
	h.cron.Start()
}

// Stop stops the heartbeat scheduler and cancels all active tasks.
func (h *Heartbeat) Stop() {
	h.cron.Stop()
	h.cancel()
}

// Remove removes a task by its entry ID.
func (h *Heartbeat) Remove(id cron.EntryID) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cron.Remove(id)
	delete(h.tasks, id)
}
