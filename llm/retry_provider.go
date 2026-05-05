package llm

import (
	"context"
	"time"
)

// RetryConfig controls the backoff behavior for a RetryProvider.
type RetryConfig struct {
	MaxAttempts               int
	MinInterval               time.Duration
	MaxInterval               time.Duration
	Multiplier                float64
	RetryForever              bool
	RetryForeverTransportOnly bool
	OnRetry                   func(RetryEvent)
}

// DefaultRetryConfig returns a safe default for production LLM usage.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts: 3,
		MinInterval: 1 * time.Second,
		MaxInterval: 10 * time.Second,
		Multiplier:  2.0,
	}
}

// RetryEvent describes a transient provider failure that will be retried.
type RetryEvent struct {
	Attempt int
	Delay   time.Duration
	Err     error
}

// RetryProvider wraps an LLM provider and automatically retries transient
// errors with exponential backoff.
type RetryProvider struct {
	Provider
	Config RetryConfig
}

// NewRetryProvider creates a new provider with the default retry policy.
func NewRetryProvider(p Provider) *RetryProvider {
	return &RetryProvider{
		Provider: p,
		Config:   DefaultRetryConfig(),
	}
}

func normalizedRetryConfig(cfg RetryConfig) RetryConfig {
	defaults := DefaultRetryConfig()
	if cfg.RetryForever && !cfg.RetryForeverTransportOnly {
		cfg.MaxAttempts = 0
	} else if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 1
	}
	if cfg.MinInterval <= 0 {
		cfg.MinInterval = defaults.MinInterval
	}
	if cfg.MaxInterval <= 0 {
		cfg.MaxInterval = defaults.MaxInterval
	}
	if cfg.MaxInterval < cfg.MinInterval {
		cfg.MaxInterval = cfg.MinInterval
	}
	if cfg.Multiplier <= 0 {
		cfg.Multiplier = defaults.Multiplier
	}
	return cfg
}

func retryLimitReached(cfg RetryConfig, attempt int, err error) bool {
	if cfg.RetryForever {
		if !cfg.RetryForeverTransportOnly {
			return false
		}
		if IsTransientTransportError(err) {
			return false
		}
	}
	return attempt >= cfg.MaxAttempts
}

func notifyRetry(cfg RetryConfig, event RetryEvent) {
	if cfg.OnRetry != nil {
		cfg.OnRetry(event)
	}
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}

	timer := time.NewTimer(delay)
	defer func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *RetryProvider) Generate(ctx context.Context, req *Request) (*Response, error) {
	cfg := normalizedRetryConfig(r.Config)
	interval := cfg.MinInterval

	for i := 0; ; i++ {
		resp, err := r.Provider.Generate(ctx, req)
		if err == nil {
			return resp, nil
		}

		if !r.Provider.IsTransient(err) || retryLimitReached(cfg, i+1, err) {
			return nil, err
		}

		notifyRetry(cfg, RetryEvent{Attempt: i + 1, Delay: interval, Err: err})
		if err := waitForRetry(ctx, interval); err != nil {
			return nil, err
		}
		interval = time.Duration(float64(interval) * cfg.Multiplier)
		if interval > cfg.MaxInterval {
			interval = cfg.MaxInterval
		}
	}
}

func (r *RetryProvider) Stream(ctx context.Context, req *Request) (Stream, error) {
	cfg := normalizedRetryConfig(r.Config)
	interval := cfg.MinInterval

	for i := 0; ; i++ {
		s, err := r.Provider.Stream(ctx, req)
		if err == nil {
			return s, nil
		}

		if !r.Provider.IsTransient(err) || retryLimitReached(cfg, i+1, err) {
			return nil, err
		}

		notifyRetry(cfg, RetryEvent{Attempt: i + 1, Delay: interval, Err: err})
		if err := waitForRetry(ctx, interval); err != nil {
			return nil, err
		}
		interval = time.Duration(float64(interval) * cfg.Multiplier)
		if interval > cfg.MaxInterval {
			interval = cfg.MaxInterval
		}
	}
}
