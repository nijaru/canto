package runtime

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/hook"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

// SendText appends a plain user message to the session and runs the agent.
func (r *Runner) SendText(
	ctx context.Context,
	sessionID, message string,
) (agent.StepResult, error) {
	return r.Send(ctx, sessionID, llm.TextPrompt(message))
}

// Send appends typed prompt input to the session and runs the agent.
// It returns the final StepResult so callers can read the assistant reply
// without a separate store load.
func (r *Runner) Send(
	ctx context.Context,
	sessionID string,
	prompt llm.Prompt,
) (agent.StepResult, error) {
	sess, err := r.getOrLoad(ctx, sessionID)
	if err != nil {
		return agent.StepResult{}, err
	}

	return r.run(ctx, sess, nil, appendPrompt(prompt), nil)
}

// SendTextStream appends a plain user message and runs the agent with streaming.
func (r *Runner) SendTextStream(
	ctx context.Context,
	sessionID, message string,
	chunkFn func(*llm.Chunk),
) (agent.StepResult, error) {
	return r.SendStream(ctx, sessionID, llm.TextPrompt(message), chunkFn)
}

// SendStream appends typed prompt input and runs the agent with streaming.
// If the agent implements agent.Streamer, chunkFn receives tokens as they
// arrive; otherwise the call falls back to non-streaming Turn.
func (r *Runner) SendStream(
	ctx context.Context,
	sessionID string,
	prompt llm.Prompt,
	chunkFn func(*llm.Chunk),
) (agent.StepResult, error) {
	return r.SendStreamWithRuntime(ctx, sessionID, prompt, chunkFn, agent.RuntimeConfig{})
}

// SendStreamWithRuntime appends typed prompt input and runs the agent with a
// per-turn runtime configuration. The base Runner agent is not mutated.
func (r *Runner) SendStreamWithRuntime(
	ctx context.Context,
	sessionID string,
	prompt llm.Prompt,
	chunkFn func(*llm.Chunk),
	cfg agent.RuntimeConfig,
) (agent.StepResult, error) {
	sess, err := r.getOrLoad(ctx, sessionID)
	if err != nil {
		return agent.StepResult{}, err
	}

	return r.run(ctx, sess, chunkFn, appendPrompt(prompt), &cfg)
}

// Run executes the agent on an existing session without appending a new user
// message first.
//
// This is an advanced/manual entry point. Host applications should usually
// prefer Send or SendStream so session mutation and execution go through one
// canonical path.
func (r *Runner) Run(ctx context.Context, sessionID string) (agent.StepResult, error) {
	sess, err := r.getOrLoad(ctx, sessionID)
	if err != nil {
		return agent.StepResult{}, err
	}
	return r.run(ctx, sess, nil, nil, nil)
}

// RunStream executes the agent with streaming on an existing session without
// appending a new user message first.
//
// This is an advanced/manual entry point. Host applications should usually
// prefer SendStream.
func (r *Runner) RunStream(
	ctx context.Context,
	sessionID string,
	chunkFn func(*llm.Chunk),
) (agent.StepResult, error) {
	sess, err := r.getOrLoad(ctx, sessionID)
	if err != nil {
		return agent.StepResult{}, err
	}
	return r.run(ctx, sess, chunkFn, nil, nil)
}

// run is the shared entry point for Run/RunStream/Send/SendStream.
// It applies per-session coordination and delegates to execute.
type sessionMutation func(context.Context, *session.Session) error

func appendPrompt(prompt llm.Prompt) sessionMutation {
	return func(ctx context.Context, sess *session.Session) error {
		if len(prompt.Messages) == 0 {
			return fmt.Errorf("runtime send: prompt must contain at least one message")
		}
		return sess.AppendPrompt(ctx, prompt)
	}
}

func (r *Runner) run(
	ctx context.Context,
	sess *session.Session,
	chunkFn func(*llm.Chunk),
	mutate sessionMutation,
	cfg *agent.RuntimeConfig,
) (agent.StepResult, error) {
	ctx, _ = session.EnsureTurnID(ctx)
	if r.coordinator != nil {
		return r.executeWithCoordinator(ctx, sess, chunkFn, mutate, cfg)
	}
	if r.queue == nil {
		return r.execute(ctx, sess, chunkFn, mutate, cfg)
	}

	waitCtx := ctx
	if r.waitTimeout > 0 {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, r.waitTimeout)
		defer cancel()
	}

	var result agent.StepResult
	var started atomic.Bool
	errCh := r.queue.executeWithWait(waitCtx, ctx, sess.ID(), func(laneCtx context.Context) error {
		started.Store(true)
		execCtx := laneCtx
		if r.executionTimeout > 0 {
			var cancel context.CancelFunc
			execCtx, cancel = context.WithTimeout(laneCtx, r.executionTimeout)
			defer cancel()
		}

		var err error
		result, err = r.execute(execCtx, sess, chunkFn, mutate, cfg)
		return wrapExecutionTimeoutError(err, execCtx, r.executionTimeout)
	})
	err := <-errCh
	if err != nil && !started.Load() {
		err = wrapWaitTimeoutError(err, sess.ID(), r.waitTimeout)
		r.appendTurnCompletedError(ctx, sess, err)
	}
	return result, err
}

func (r *Runner) executeWithCoordinator(
	ctx context.Context,
	sess *session.Session,
	chunkFn func(*llm.Chunk),
	mutate sessionMutation,
	cfg *agent.RuntimeConfig,
) (agent.StepResult, error) {
	if r.coordinator == nil {
		return r.execute(ctx, sess, chunkFn, mutate, cfg)
	}

	waitCtx := ctx
	if r.waitTimeout > 0 {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, r.waitTimeout)
		defer cancel()
	}

	ticket, err := r.coordinator.Enqueue(waitCtx, sess.ID())
	if err != nil {
		err = wrapWaitTimeoutError(err, sess.ID(), r.waitTimeout)
		r.appendTurnCompletedError(ctx, sess, err)
		return agent.StepResult{}, err
	}
	lease, err := r.coordinator.Await(waitCtx, ticket)
	if err != nil {
		err = wrapWaitTimeoutError(err, sess.ID(), r.waitTimeout)
		r.appendTurnCompletedError(ctx, sess, err)
		return agent.StepResult{}, err
	}

	execCtx := ctx
	if r.executionTimeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, r.executionTimeout)
		defer cancel()
	}

	result, execErr := r.executeUnderLease(execCtx, sess, chunkFn, lease, mutate, cfg)
	execErr = wrapExecutionTimeoutError(execErr, execCtx, r.executionTimeout)
	return result, execErr
}

func (r *Runner) appendTurnCompletedError(
	ctx context.Context,
	sess *session.Session,
	err error,
) {
	data := session.TurnCompletedData{
		AgentID: r.agent.ID(),
		Error:   err.Error(),
	}
	_ = sess.Append(context.WithoutCancel(ctx), session.NewTurnCompletedEvent(sess.ID(), data))
}

func (r *Runner) execute(
	ctx context.Context,
	sess *session.Session,
	chunkFn func(*llm.Chunk),
	mutate sessionMutation,
	cfg *agent.RuntimeConfig,
) (agent.StepResult, error) {
	runAgent, cfgErr := configuredAgent(r.agent, cfg)
	if cfgErr != nil {
		return agent.StepResult{}, cfgErr
	}
	if mutate != nil {
		if err := r.runBeforeRun(ctx, sess); err != nil {
			return agent.StepResult{}, err
		}
		if err := mutate(ctx, sess); err != nil {
			return agent.StepResult{}, err
		}
	}

	meta := hook.SessionMeta{ID: sess.ID()}
	if _, err := r.hooks.Run(ctx, hook.EventSessionStart, meta, nil); err != nil {
		return agent.StepResult{}, err
	}
	defer func() {
		r.hooks.Run(context.Background(), hook.EventSessionEnd, meta, nil)
	}()

	// Execute agent turn.
	// Use streaming if chunkFn is set and the agent supports it.
	var (
		result agent.StepResult
		err    error
	)
	for attempt := 0; ; attempt++ {
		if mutate == nil || attempt > 0 {
			if err := r.runBeforeRun(ctx, sess); err != nil {
				return agent.StepResult{}, err
			}
		}

		if chunkFn != nil {
			if s, ok := runAgent.(agent.Streamer); ok {
				result, err = s.StreamTurn(ctx, sess, chunkFn)
			} else {
				result, err = runAgent.Turn(ctx, sess)
			}
		} else {
			result, err = runAgent.Turn(ctx, sess)
		}

		if err == nil {
			return result, nil
		}
		if !r.shouldRecoverOverflow(err, attempt) {
			return agent.StepResult{}, err
		}
		if retryErr := r.appendOverflowRecoveryRetry(ctx, sess, attempt+1, err); retryErr != nil {
			return agent.StepResult{}, retryErr
		}
		if compactErr := r.overflowRecovery.compact(ctx, sess); compactErr != nil {
			return agent.StepResult{}, fmt.Errorf(
				"overflow recovery: compact failed: %w (original: %v)",
				compactErr,
				err,
			)
		}
	}
}

func configuredAgent(base agent.Agent, cfg *agent.RuntimeConfig) (agent.Agent, error) {
	if cfg == nil || (cfg.Tools == nil && len(cfg.RequestProcessors) == 0) {
		return base, nil
	}
	configurable, ok := base.(agent.RuntimeConfigurable)
	if !ok {
		return nil, fmt.Errorf("runtime agent config: agent %q is not configurable", base.ID())
	}
	configured := configurable.ConfigureRuntime(*cfg)
	if configured == nil {
		return nil, fmt.Errorf("runtime agent config: agent %q returned nil", base.ID())
	}
	return configured, nil
}

func (r *Runner) runBeforeRun(ctx context.Context, sess *session.Session) error {
	for _, fn := range r.beforeRun {
		if err := fn(ctx, sess); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) shouldRecoverOverflow(err error, attempt int) bool {
	recovery := r.overflowRecovery
	return recovery.isOverflow != nil &&
		recovery.compact != nil &&
		attempt < recovery.maxRetries &&
		recovery.isOverflow(err)
}

func (r *Runner) appendOverflowRecoveryRetry(
	ctx context.Context,
	sess *session.Session,
	attempt int,
	err error,
) error {
	data := session.EscalationRetriedData{
		AgentID: r.agent.ID(),
		Scope:   "overflow_recovery",
		Target:  "turn",
		Attempt: attempt,
		Error:   err.Error(),
	}
	return sess.Append(ctx, session.NewEscalationRetriedEvent(sess.ID(), data))
}
