package service

import (
	"context"
	"time"
)

// RetryPolicy controls retry behavior for transient service/API failures.
type RetryPolicy struct {
	MaxAttempts int
	Delay       time.Duration
	Retryable   func(error) bool
}

func (t *Tool[A, R]) executeWithRetry(ctx context.Context, input A) (R, error) {
	var zero R
	attempts := t.retry.MaxAttempts
	if attempts <= 0 {
		attempts = 1
	}
	var lastErr error
	for attempt := range attempts {
		result, err := t.execute(ctx, input)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if attempt == attempts-1 || !t.retry.shouldRetry(err) {
			return zero, err
		}
		if t.retry.Delay <= 0 {
			continue
		}
		timer := time.NewTimer(t.retry.Delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return zero, ctx.Err()
		case <-timer.C:
		}
	}
	return zero, lastErr
}

func (p RetryPolicy) shouldRetry(err error) bool {
	if p.Retryable == nil {
		return false
	}
	return p.Retryable(err)
}
