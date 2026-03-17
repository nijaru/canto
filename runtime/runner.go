package runtime

import (
	"context"
	"time"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/hook"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

const (
	defaultWaitTimeout      = 30 * time.Second
	defaultExecutionTimeout = 2 * time.Minute
)

// Runner orchestrates the execution of an agent within a session.
// It always uses a LaneManager to serialize execution within a session
// while allowing concurrent execution across different sessions.
type Runner struct {
	Store session.Store
	Agent agent.Agent

	// WaitTimeout is the maximum time to wait in the lane queue for a session.
	WaitTimeout time.Duration
	// ExecutionTimeout is the maximum time to spend running the agent turn.
	ExecutionTimeout time.Duration

	Lanes *LaneManager
	Hooks *hook.Runner
}

// NewRunner creates a Runner with per-session lane serialization enabled.
func NewRunner(s session.Store, a agent.Agent) *Runner {
	return &Runner{
		Store:            s,
		Agent:            a,
		WaitTimeout:      defaultWaitTimeout,
		ExecutionTimeout: defaultExecutionTimeout,
		Lanes:            NewLaneManager(),
		Hooks:            hook.NewRunner(),
	}
}

// Close gracefully stops the internal lane manager and any active goroutines.
func (r *Runner) Close() {
	if r.Lanes != nil {
		r.Lanes.Stop()
	}
}

// Subscribe returns a channel that receives all events for the given session.
// It loads the current session to attach the subscriber.
func (r *Runner) Subscribe(ctx context.Context, sessionID string) (<-chan session.Event, error) {
	sess, err := r.Store.Load(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return sess.Subscribe(ctx), nil
}

// Search searches the session history for the given query.
func (r *Runner) Search(ctx context.Context, sessionID, query string) ([]session.Event, error) {
	return r.Store.Search(ctx, sessionID, query)
}

// Send appends a user message to the session and runs the agent.
// It returns the final StepResult so callers can read the assistant reply
// without a separate store load.
func (r *Runner) Send(ctx context.Context, sessionID, message string) (agent.StepResult, error) {
	sess, err := r.Store.Load(ctx, sessionID)
	if err != nil {
		return agent.StepResult{}, err
	}

	e := session.NewEvent(sessionID, session.EventTypeMessageAdded, llm.Message{
		Role:    llm.RoleUser,
		Content: message,
	})
	if err := sess.Append(ctx, e); err != nil {
		return agent.StepResult{}, err
	}
	return r.Run(ctx, sessionID)
}

// SendStream appends a user message and runs the agent with streaming.
// If the agent implements agent.Streamer, chunkFn receives tokens as they
// arrive; otherwise the call falls back to non-streaming Turn.
func (r *Runner) SendStream(
	ctx context.Context,
	sessionID, message string,
	chunkFn func(*llm.Chunk),
) (agent.StepResult, error) {
	sess, err := r.Store.Load(ctx, sessionID)
	if err != nil {
		return agent.StepResult{}, err
	}

	e := session.NewEvent(sessionID, session.EventTypeMessageAdded, llm.Message{
		Role:    llm.RoleUser,
		Content: message,
	})
	if err := sess.Append(ctx, e); err != nil {
		return agent.StepResult{}, err
	}
	return r.RunStream(ctx, sessionID, chunkFn)
}

// Run executes the agent on the given session. If a LaneManager is configured,
// execution is serialized within the session lane.
func (r *Runner) Run(ctx context.Context, sessionID string) (agent.StepResult, error) {
	if r.Lanes == nil {
		return r.execute(ctx, sessionID, nil)
	}

	waitCtx := ctx
	if r.WaitTimeout > 0 {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, r.WaitTimeout)
		defer cancel()
	}

	var result agent.StepResult
	errCh := r.Lanes.Execute(waitCtx, sessionID, func(laneCtx context.Context) error {
		execCtx := laneCtx
		if r.ExecutionTimeout > 0 {
			var cancel context.CancelFunc
			execCtx, cancel = context.WithTimeout(laneCtx, r.ExecutionTimeout)
			defer cancel()
		}

		var err error
		result, err = r.execute(execCtx, sessionID, nil)
		return err
	})
	return result, <-errCh
}

// RunStream executes the agent with streaming. If a LaneManager is configured,
// execution is serialized within the session lane.
func (r *Runner) RunStream(
	ctx context.Context,
	sessionID string,
	chunkFn func(*llm.Chunk),
) (agent.StepResult, error) {
	if r.Lanes == nil {
		return r.execute(ctx, sessionID, chunkFn)
	}

	waitCtx := ctx
	if r.WaitTimeout > 0 {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, r.WaitTimeout)
		defer cancel()
	}

	var result agent.StepResult
	errCh := r.Lanes.Execute(waitCtx, sessionID, func(laneCtx context.Context) error {
		execCtx := laneCtx
		if r.ExecutionTimeout > 0 {
			var cancel context.CancelFunc
			execCtx, cancel = context.WithTimeout(laneCtx, r.ExecutionTimeout)
			defer cancel()
		}

		var err error
		result, err = r.execute(execCtx, sessionID, chunkFn)
		return err
	})
	return result, <-errCh
}

func (r *Runner) execute(
	ctx context.Context,
	sessionID string,
	chunkFn func(*llm.Chunk),
) (agent.StepResult, error) {
	// 1. Load session
	sess, err := r.Store.Load(ctx, sessionID)
	if err != nil {
		return agent.StepResult{}, err
	}

	meta := hook.SessionMeta{ID: sess.ID()}
	if _, err := r.Hooks.Run(ctx, hook.EventSessionStart, meta, nil); err != nil {
		return agent.StepResult{}, err
	}
	defer func() {
		r.Hooks.Run(context.Background(), hook.EventSessionEnd, meta, nil)
	}()

	// 2. Execute agent turn.
	// Use streaming if chunkFn is set and the agent supports it.
	var result agent.StepResult
	if chunkFn != nil {
		if s, ok := r.Agent.(agent.Streamer); ok {
			result, err = s.StreamTurn(ctx, sess, chunkFn)
		} else {
			result, err = r.Agent.Turn(ctx, sess)
		}
	} else {
		result, err = r.Agent.Turn(ctx, sess)
	}
	if err != nil {
		return agent.StepResult{}, err
	}

	return result, nil
}
