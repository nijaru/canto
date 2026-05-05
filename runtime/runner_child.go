package runtime

import (
	"context"
	"fmt"
	"time"
)

// ChildRunner creates a child-run helper that inherits this runner's store,
// timeout, coordinator, and hook settings. Additional opts override the
// inherited defaults for the child runner only.
func (r *Runner) ChildRunner(opts ...Option) *ChildRunner {
	if r == nil {
		return nil
	}
	inherited := runnerChildOptions(options{
		waitTimeout:      r.waitTimeout,
		executionTimeout: r.executionTimeout,
		coordinator:      r.coordinator,
		hooks:            r.hooks,
	})
	inherited = append(inherited, opts...)
	return NewChildRunner(r.store, inherited...)
}

// Delegate executes a child request synchronously against a parent session.
func (r *Runner) Delegate(
	ctx context.Context,
	parentSessionID string,
	spec ChildSpec,
) (ChildResult, error) {
	parent, err := r.getOrLoad(ctx, parentSessionID)
	if err != nil {
		return ChildResult{}, err
	}
	return r.sharedChildRunner().Run(ctx, parent, spec)
}

// SpawnChild starts child execution asynchronously against a parent session.
func (r *Runner) SpawnChild(
	ctx context.Context,
	parentSessionID string,
	spec ChildSpec,
) (ChildRef, error) {
	parent, err := r.getOrLoad(ctx, parentSessionID)
	if err != nil {
		return ChildRef{}, err
	}
	return r.sharedChildRunner().Spawn(ctx, parent, spec)
}

// WaitChild waits for a previously spawned child on the runner's shared
// delegation API.
func (r *Runner) WaitChild(ctx context.Context, childID string) (ChildResult, error) {
	return r.sharedChildRunner().Wait(ctx, childID)
}

// ScheduleChild queues a child run for future execution.
func (r *Runner) ScheduleChild(
	ctx context.Context,
	parentSessionID string,
	dueAt time.Time,
	spec ChildSpec,
) (ScheduledChild, error) {
	if r.scheduler == nil {
		return nil, fmt.Errorf("runner scheduler unavailable")
	}

	parent, err := r.getOrLoad(ctx, parentSessionID)
	if err != nil {
		return nil, err
	}
	spec, err = normalizeChildSpec(spec)
	if err != nil {
		return nil, err
	}

	childRunner := r.sharedChildRunner()
	if childRunner == nil {
		return nil, fmt.Errorf("runner child runner unavailable")
	}

	handle := newScheduledChildHandle(ChildRef{
		ID:              spec.ID,
		SessionID:       spec.SessionID,
		ParentSessionID: parent.ID(),
		AgentID:         spec.Agent.ID(),
		Mode:            spec.Mode,
	})

	task, err := r.scheduler.Schedule(ctx, dueAt, func(runCtx context.Context) error {
		parent, loadErr := r.getOrLoad(runCtx, parentSessionID)
		if loadErr != nil {
			handle.finish(ChildResult{
				Ref: handle.ChildRef(),
				Err: loadErr,
			}, loadErr)
			return loadErr
		}

		result, runErr := childRunner.Run(runCtx, parent, spec)
		handle.finish(result, runErr)
		return runErr
	})
	if err != nil {
		return nil, err
	}
	handle.attach(task)
	return handle, nil
}

func (r *Runner) sharedChildRunner() *ChildRunner {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.childRunner
}

func runnerChildOptions(cfg options) []Option {
	inherited := []Option{
		WithWaitTimeout(cfg.waitTimeout),
		WithExecutionTimeout(cfg.executionTimeout),
	}
	if cfg.coordinator != nil {
		inherited = append(inherited, WithCoordinator(cfg.coordinator))
	}
	if cfg.hooks != nil {
		inherited = append(inherited, WithHooks(cfg.hooks))
	}
	if cfg.maxConcurrent > 0 {
		inherited = append(inherited, WithMaxConcurrent(cfg.maxConcurrent))
	}
	return inherited
}
