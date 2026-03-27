package runtime

import (
	"time"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/hook"
	"github.com/nijaru/canto/session"
)

// ExecutionConfig captures the coordination and timeout settings commonly
// shared across Runner and ChildRunner instances in one host process.
type ExecutionConfig struct {
	WaitTimeout      time.Duration
	ExecutionTimeout time.Duration
	Coordinator      Coordinator
	Hooks            *hook.Runner
}

// NewRunnerWithConfig creates a Runner and applies cfg.
func NewRunnerWithConfig(
	store session.Store,
	a agent.Agent,
	cfg ExecutionConfig,
) *Runner {
	r := NewRunner(store, a)
	cfg.ApplyRunner(r)
	return r
}

// NewChildRunnerWithConfig creates a ChildRunner and applies cfg.
func NewChildRunnerWithConfig(
	store session.Store,
	cfg ExecutionConfig,
) *ChildRunner {
	r := NewChildRunner(store)
	cfg.ApplyChildRunner(r)
	return r
}

// Config returns the runner's current coordination and timeout settings.
func (r *Runner) Config() ExecutionConfig {
	if r == nil {
		return ExecutionConfig{}
	}
	return ExecutionConfig{
		WaitTimeout:      r.WaitTimeout,
		ExecutionTimeout: r.ExecutionTimeout,
		Coordinator:      r.Coordinator,
		Hooks:            r.Hooks,
	}
}

// ChildRunner creates a child-run helper that inherits this runner's store,
// timeout, coordinator, and hook settings.
func (r *Runner) ChildRunner() *ChildRunner {
	if r == nil {
		return nil
	}
	return NewChildRunnerWithConfig(r.Store, r.Config())
}

// ApplyRunner copies the configured execution settings onto r.
func (cfg ExecutionConfig) ApplyRunner(r *Runner) {
	if r == nil {
		return
	}
	if cfg.WaitTimeout != 0 {
		r.WaitTimeout = cfg.WaitTimeout
	}
	if cfg.ExecutionTimeout != 0 {
		r.ExecutionTimeout = cfg.ExecutionTimeout
	}
	if cfg.Coordinator != nil {
		r.Coordinator = cfg.Coordinator
	}
	if cfg.Hooks != nil {
		r.Hooks = cfg.Hooks
	}
}

// ApplyChildRunner copies the configured execution settings onto r.
func (cfg ExecutionConfig) ApplyChildRunner(r *ChildRunner) {
	if r == nil {
		return
	}
	if cfg.WaitTimeout != 0 {
		r.WaitTimeout = cfg.WaitTimeout
	}
	if cfg.ExecutionTimeout != 0 {
		r.ExecutionTimeout = cfg.ExecutionTimeout
	}
	if cfg.Coordinator != nil {
		r.Coordinator = cfg.Coordinator
	}
	if cfg.Hooks != nil {
		r.Hooks = cfg.Hooks
	}
}
