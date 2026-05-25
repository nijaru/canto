package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// TimeoutError identifies which runtime operation hit a deadline while still
// unwrapping to context.DeadlineExceeded for callers that classify by cause.
type TimeoutError struct {
	Operation string
	Timeout   time.Duration
	Err       error
}

func (e *TimeoutError) Error() string {
	operation := strings.TrimSpace(e.Operation)
	if operation == "" {
		operation = "runtime operation"
	}
	if e.Timeout > 0 {
		return fmt.Sprintf("%s timed out after %s", operation, e.Timeout)
	}
	return operation + " timed out"
}

func (e *TimeoutError) Unwrap() error {
	if e == nil || e.Err == nil {
		return context.DeadlineExceeded
	}
	return e.Err
}

func wrapTimeoutError(err error, operation string, timeout time.Duration) error {
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var timeoutErr *TimeoutError
	if errors.As(err, &timeoutErr) {
		return err
	}
	return &TimeoutError{
		Operation: operation,
		Timeout:   timeout,
		Err:       err,
	}
}

func wrapWaitTimeoutError(err error, sessionID string, timeout time.Duration) error {
	operation := "wait for session execution slot"
	if strings.TrimSpace(sessionID) != "" {
		operation = fmt.Sprintf("wait for session %q execution slot", sessionID)
	}
	return wrapTimeoutError(err, operation, timeout)
}

func wrapExecutionTimeoutError(
	err error,
	execCtx context.Context,
	timeout time.Duration,
) error {
	if err == nil || !errors.Is(execCtx.Err(), context.DeadlineExceeded) {
		return err
	}
	return wrapTimeoutError(err, "agent execution", timeout)
}
