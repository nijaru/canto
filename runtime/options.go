package runtime

import (
	"context"
	"time"

	"github.com/nijaru/canto/hook"
	"github.com/nijaru/canto/session"
)

type options struct {
	waitTimeout      time.Duration
	executionTimeout time.Duration
	coordinator      Coordinator
	hooks            *hook.Runner
	scheduler        Scheduler
	maxConcurrent    int
	beforeRun        []SessionFunc
	overflowRecovery overflowRecoveryOptions
}

func defaultOptions() options {
	return options{
		waitTimeout:      defaultWaitTimeout,
		executionTimeout: defaultExecutionTimeout,
		hooks:            hook.NewRunner(),
	}
}

type Option func(*options)

// SessionFunc runs against the durable in-memory session immediately before
// execution or recovery retry.
type SessionFunc func(context.Context, *session.Session) error

type overflowRecoveryOptions struct {
	isOverflow func(error) bool
	compact    SessionFunc
	maxRetries int
}

func WithWaitTimeout(d time.Duration) Option {
	return func(opts *options) {
		opts.waitTimeout = d
	}
}

func WithExecutionTimeout(d time.Duration) Option {
	return func(opts *options) {
		opts.executionTimeout = d
	}
}

func WithCoordinator(coord Coordinator) Option {
	return func(opts *options) {
		opts.coordinator = coord
	}
}

func WithHooks(hooks *hook.Runner) Option {
	return func(opts *options) {
		opts.hooks = hooks
	}
}

func WithScheduler(s Scheduler) Option {
	return func(opts *options) {
		opts.scheduler = s
	}
}

func WithMaxConcurrent(n int) Option {
	return func(opts *options) {
		opts.maxConcurrent = n
	}
}

// WithBeforeRun registers a session hook that runs before each agent execution
// attempt. It is intended for orchestration work such as proactive compaction.
func WithBeforeRun(fn SessionFunc) Option {
	return func(opts *options) {
		if fn != nil {
			opts.beforeRun = append(opts.beforeRun, fn)
		}
	}
}

// WithOverflowRecovery retries a turn after compacting the session when err is
// classified as a context overflow. The retry re-enters the agent turn so the
// request is rebuilt from the compacted session.
func WithOverflowRecovery(
	isOverflow func(error) bool,
	compact SessionFunc,
	maxRetries int,
) Option {
	return func(opts *options) {
		if isOverflow == nil || compact == nil {
			return
		}
		if maxRetries <= 0 {
			maxRetries = 1
		}
		opts.overflowRecovery = overflowRecoveryOptions{
			isOverflow: isOverflow,
			compact:    compact,
			maxRetries: maxRetries,
		}
	}
}

func applyOptions(opts []Option) options {
	cfg := defaultOptions()
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.hooks == nil {
		cfg.hooks = hook.NewRunner()
	}
	return cfg
}
