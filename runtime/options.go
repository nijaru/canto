package runtime

import (
	"time"

	"github.com/nijaru/canto/hook"
)

type options struct {
	waitTimeout      time.Duration
	executionTimeout time.Duration
	coordinator      Coordinator
	hooks            *hook.Runner
	maxConcurrent    int
}

func defaultOptions() options {
	return options{
		waitTimeout:      defaultWaitTimeout,
		executionTimeout: defaultExecutionTimeout,
		hooks:            hook.NewRunner(),
	}
}

type Option func(*options)

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

func WithMaxConcurrent(n int) Option {
	return func(opts *options) {
		opts.maxConcurrent = n
	}
}

func applyOptions(opts []Option) options {
	cfg := defaultOptions()
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return cfg
}
