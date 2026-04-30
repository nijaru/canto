package runtime

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

func (r *Runner) executeUnderLease(
	ctx context.Context,
	sess *session.Session,
	chunkFn func(*llm.Chunk),
	lease Lease,
	mutate sessionMutation,
) (agent.StepResult, error) {
	if r.coordinator == nil {
		return r.execute(ctx, sess, chunkFn, mutate)
	}

	execCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var leaseMu sync.Mutex
	currentLease := lease
	renewErrCh := make(chan error, 1)
	stopRenew := make(chan struct{})

	// Ensure lease is released/acknowledged even on panic.
	defer func() {
		if rec := recover(); rec != nil {
			close(stopRenew)
			leaseMu.Lock()
			finalLease := currentLease
			leaseMu.Unlock()

			resultMeta := Result{
				CompletedAt: time.Now().UTC(),
				Status:      ResultStatusFailed,
				Error:       "panic in agent execution",
				Metadata:    map[string]any{"session_id": sess.ID()},
			}
			// Best effort release on panic.
			_ = r.coordinator.Nack(context.Background(), finalLease, resultMeta)
			panic(rec) // re-throw after best-effort cleanup
		}
	}()

	go func() {
		for {
			leaseMu.Lock()
			wait := time.Until(currentLease.ExpiresAt) / 2
			leaseMu.Unlock()
			if wait <= 0 {
				wait = time.Millisecond
			}

			timer := time.NewTimer(wait)
			select {
			case <-stopRenew:
				timer.Stop()
				return
			case <-execCtx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}

			leaseMu.Lock()
			renewed, err := r.coordinator.Renew(context.WithoutCancel(execCtx), currentLease)
			if err == nil {
				currentLease = renewed
			}
			leaseMu.Unlock()
			if err != nil {
				select {
				case renewErrCh <- err:
				default:
				}
				cancel()
				return
			}
		}
	}()

	result, execErr := r.execute(execCtx, sess, chunkFn, mutate)
	close(stopRenew)

	leaseMu.Lock()
	finalLease := currentLease
	leaseMu.Unlock()

	var renewErr error
	select {
	case renewErr = <-renewErrCh:
	default:
	}

	resultMeta := Result{
		CompletedAt: time.Now().UTC(),
		Metadata:    map[string]any{"session_id": sess.ID()},
	}
	switch {
	case execErr == nil:
		resultMeta.Status = ResultStatusCompleted
	case errors.Is(execErr, context.Canceled), errors.Is(execErr, context.DeadlineExceeded):
		resultMeta.Status = ResultStatusCanceled
		resultMeta.Error = execErr.Error()
	default:
		resultMeta.Status = ResultStatusFailed
		resultMeta.Error = execErr.Error()
	}

	ackErr := r.coordinator.Ack(context.WithoutCancel(ctx), finalLease, resultMeta)
	if renewErr != nil && execErr == nil {
		return result, errors.Join(renewErr, ackErr)
	}
	if execErr != nil {
		return result, errors.Join(execErr, renewErr, ackErr)
	}
	if ackErr != nil {
		return result, ackErr
	}
	return result, renewErr
}
