package runtime

import (
	"context"
	"sync"
	"time"
)

type scheduledTask struct {
	mu        sync.Mutex
	ref       ScheduleRef
	fn        func(context.Context) error
	runCtx    context.Context
	runCancel context.CancelFunc
	owner     *LocalScheduler
	timer     *time.Timer
	done      chan struct{}
	err       error
	started   bool
	finished  bool
}

func (t *scheduledTask) Ref() ScheduleRef {
	return t.ref
}

func (t *scheduledTask) Wait(ctx context.Context) error {
	select {
	case <-t.done:
		t.mu.Lock()
		defer t.mu.Unlock()
		return t.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *scheduledTask) Cancel(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return t.cancelTask(context.Canceled)
}

func (t *scheduledTask) start() {
	t.mu.Lock()
	if t.finished {
		t.mu.Unlock()
		return
	}
	t.started = true
	runCtx := t.runCtx
	t.mu.Unlock()

	if err := runCtx.Err(); err != nil {
		_ = t.finish(err)
		return
	}

	_ = t.finish(t.fn(runCtx))
}

func (t *scheduledTask) cancelTask(err error) error {
	t.mu.Lock()
	if t.finished {
		doneErr := t.err
		t.mu.Unlock()
		if doneErr != nil {
			return doneErr
		}
		return ErrScheduledTaskDone
	}
	if t.started {
		cancel := t.runCancel
		t.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		return ErrScheduledTaskStarted
	}
	timer := t.timer
	t.finished = true
	t.err = err
	close(t.done)
	cancel := t.runCancel
	t.mu.Unlock()

	if timer != nil {
		timer.Stop()
	}
	if cancel != nil {
		cancel()
	}
	if t.owner != nil {
		t.owner.removeTask(t.ref.ID)
	}
	return nil
}

func (t *scheduledTask) finish(err error) error {
	t.mu.Lock()
	if t.finished {
		t.mu.Unlock()
		return t.err
	}
	t.finished = true
	t.err = err
	close(t.done)
	cancel := t.runCancel
	timer := t.timer
	t.mu.Unlock()

	if timer != nil {
		timer.Stop()
	}
	if cancel != nil {
		cancel()
	}
	if t.owner != nil {
		t.owner.removeTask(t.ref.ID)
	}
	return err
}
