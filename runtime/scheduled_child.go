package runtime

import (
	"context"
	"errors"
	"sync"

	"github.com/nijaru/canto/session"
)

// ScheduledChild is the handle returned by scheduled child delegation.
type ScheduledChild interface {
	ScheduleRef() ScheduleRef
	ChildRef() ChildRef
	Wait(ctx context.Context) (ChildResult, error)
	Cancel(ctx context.Context) error
}

type scheduledChildHandle struct {
	mu       sync.Mutex
	task     ScheduledTask
	ref      ChildRef
	result   ChildResult
	done     chan struct{}
	finished bool
}

func newScheduledChildHandle(ref ChildRef) *scheduledChildHandle {
	return &scheduledChildHandle{
		ref:  ref,
		done: make(chan struct{}),
	}
}

func (h *scheduledChildHandle) attach(task ScheduledTask) {
	h.mu.Lock()
	h.task = task
	h.mu.Unlock()
}

func (h *scheduledChildHandle) ScheduleRef() ScheduleRef {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.task == nil {
		return ScheduleRef{}
	}
	return h.task.Ref()
}

func (h *scheduledChildHandle) ChildRef() ChildRef {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ref
}

func (h *scheduledChildHandle) Wait(ctx context.Context) (ChildResult, error) {
	select {
	case <-h.done:
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.result, nil
	case <-ctx.Done():
		return ChildResult{}, ctx.Err()
	}
}

func (h *scheduledChildHandle) Cancel(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	h.mu.Lock()
	task := h.task
	h.mu.Unlock()
	if task == nil {
		return errors.New("scheduled child handle not attached")
	}

	err := task.Cancel(ctx)
	if err == nil {
		h.finish(ChildResult{
			Ref:    h.ChildRef(),
			Status: session.ChildStatusCanceled,
			Err:    context.Canceled,
		}, context.Canceled)
	}
	return err
}

func (h *scheduledChildHandle) finish(result ChildResult, err error) {
	if err != nil && result.Err == nil {
		result.Err = err
	}
	if result.Err == nil && result.Status == "" {
		result.Status = scheduledChildStatus(nil)
	}
	if result.Err != nil && result.Status == "" {
		result.Status = scheduledChildStatus(result.Err)
	}
	if result.Ref.ID == "" {
		result.Ref = h.ChildRef()
	}

	h.mu.Lock()
	if h.finished {
		h.mu.Unlock()
		return
	}
	if result.Ref.ID != "" {
		h.ref = result.Ref
	}
	h.result = result
	h.finished = true
	close(h.done)
	h.mu.Unlock()
}

func scheduledChildStatus(err error) session.ChildStatus {
	switch {
	case err == nil:
		return session.ChildStatusCompleted
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return session.ChildStatusCanceled
	default:
		return session.ChildStatusFailed
	}
}
