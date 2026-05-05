package hook

import (
	"context"
	"fmt"
	"sync"
)

// Runner manages and executes hooks.
type Runner struct {
	mu    sync.RWMutex
	hooks []Handler
}

// NewRunner creates a new hook runner.
func NewRunner() *Runner {
	return &Runner{}
}

// Register adds a hook to the runner.
func (r *Runner) Register(h Handler) {
	if h == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hooks = append(r.hooks, h)
}

// Run executes all hooks registered for the given event.
// It returns an error if any hook blocks execution (Exit Code 2).
func (r *Runner) Run(
	ctx context.Context,
	event Event,
	meta SessionMeta,
	data map[string]any,
) ([]*Result, error) {
	payload := &Payload{
		Event:   event,
		Session: meta,
		Data:    data,
	}

	var results []*Result

	r.mu.RLock()
	hooks := make([]Handler, len(r.hooks))
	copy(hooks, r.hooks)
	r.mu.RUnlock()

	for _, h := range hooks {
		if !handlerMatchesEvent(h, event) {
			continue
		}

		res := executeHook(ctx, h, payload)
		results = append(results, res)

		if res.Action == ActionBlock {
			if res.Error != nil {
				return results, fmt.Errorf(
					"hook %s blocked execution for event %s: %w",
					h.Name(), event, res.Error,
				)
			}
			return results, fmt.Errorf(
				"hook %s blocked execution for event %s",
				h.Name(), event,
			)
		}
	}

	return results, nil
}

func handlerMatchesEvent(h Handler, event Event) bool {
	for _, e := range h.Events() {
		if e == event {
			return true
		}
	}
	return false
}

func executeHook(ctx context.Context, h Handler, payload *Payload) (res *Result) {
	defer func() {
		if value := recover(); value != nil {
			res = &Result{
				Action: ActionBlock,
				Error:  fmt.Errorf("hook panicked: %v", value),
			}
		}
		if res == nil {
			res = &Result{
				Action: ActionBlock,
				Error:  fmt.Errorf("hook returned nil result"),
			}
		}
	}()
	return h.Execute(ctx, payload)
}
