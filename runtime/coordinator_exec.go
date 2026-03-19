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
	lease LaneLease,
) (agent.StepResult, error) {
	if r.Coordinator == nil {
		return r.execute(ctx, sess, chunkFn)
	}

	execCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var leaseMu sync.Mutex
	currentLease := lease
	renewErrCh := make(chan error, 1)
	stopRenew := make(chan struct{})

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
			renewed, err := r.Coordinator.Renew(context.WithoutCancel(execCtx), currentLease)
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

	result, execErr := r.execute(execCtx, sess, chunkFn)
	close(stopRenew)

	leaseMu.Lock()
	finalLease := currentLease
	leaseMu.Unlock()

	var renewErr error
	select {
	case renewErr = <-renewErrCh:
	default:
	}

	laneResult := LaneResult{
		CompletedAt: time.Now().UTC(),
		Metadata:    map[string]any{"session_id": sess.ID()},
	}
	switch {
	case execErr == nil:
		laneResult.Status = "completed"
	case errors.Is(execErr, context.Canceled), errors.Is(execErr, context.DeadlineExceeded):
		laneResult.Status = "canceled"
		laneResult.Error = execErr.Error()
	default:
		laneResult.Status = "failed"
		laneResult.Error = execErr.Error()
	}

	ackErr := r.Coordinator.Ack(context.WithoutCancel(ctx), finalLease, laneResult)
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
