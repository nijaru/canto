package runtime

import (
	"context"
	"sync"
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
//
// Runner maintains an in-memory session registry so that Subscribe, Send,
// Run, and execute all share the same *session.Session object for a given
// session ID. This is required for Subscribe to receive events emitted by
// execute — without a shared object the channel is permanently silent.
type Runner struct {
	Store session.Store
	Agent agent.Agent

	// WaitTimeout is the maximum time to wait in the lane queue for a session.
	WaitTimeout time.Duration
	// ExecutionTimeout is the maximum time to spend running the agent turn.
	ExecutionTimeout time.Duration

	Lanes *LaneManager
	Hooks *hook.Runner

	mu       sync.Mutex
	sessions map[string]*session.Session
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
		sessions:         make(map[string]*session.Session),
	}
}

// Close gracefully stops the internal lane manager and any active goroutines.
func (r *Runner) Close() {
	if r.Lanes != nil {
		r.Lanes.Stop()
	}
}

// getOrLoad returns the cached session for sessionID, loading it from the
// store on first access. All Runner methods use this so that Subscribe and
// execute always operate on the same in-memory object.
func (r *Runner) getOrLoad(ctx context.Context, sessionID string) (*session.Session, error) {
	r.mu.Lock()
	if sess, ok := r.sessions[sessionID]; ok {
		r.mu.Unlock()
		return sess, nil
	}
	r.mu.Unlock()

	// Load outside the lock; Store.Load may involve I/O.
	sess, err := r.Store.Load(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	// Double-check: another goroutine may have loaded the same session.
	if existing, ok := r.sessions[sessionID]; ok {
		return existing, nil
	}
	r.sessions[sessionID] = sess
	return sess, nil
}

// Evict removes sessionID from the in-memory registry. The session remains
// in the persistent store; the next access reloads it from there. Use this
// to release memory for idle sessions.
func (r *Runner) Evict(sessionID string) {
	r.mu.Lock()
	delete(r.sessions, sessionID)
	r.mu.Unlock()
}

// Subscribe returns a channel that receives all events for the given session.
// Events emitted by Run/Send on the same Runner are delivered to this channel
// because both share the same in-memory session object.
func (r *Runner) Subscribe(ctx context.Context, sessionID string) (<-chan session.Event, error) {
	sess, err := r.getOrLoad(ctx, sessionID)
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
	sess, err := r.getOrLoad(ctx, sessionID)
	if err != nil {
		return agent.StepResult{}, err
	}

	e := session.NewEvent(sessionID, session.MessageAdded, llm.Message{
		Role:    llm.RoleUser,
		Content: message,
	})
	if err := sess.Append(ctx, e); err != nil {
		return agent.StepResult{}, err
	}
	return r.run(ctx, sess, nil)
}

// SendStream appends a user message and runs the agent with streaming.
// If the agent implements agent.Streamer, chunkFn receives tokens as they
// arrive; otherwise the call falls back to non-streaming Turn.
func (r *Runner) SendStream(
	ctx context.Context,
	sessionID, message string,
	chunkFn func(*llm.Chunk),
) (agent.StepResult, error) {
	sess, err := r.getOrLoad(ctx, sessionID)
	if err != nil {
		return agent.StepResult{}, err
	}

	e := session.NewEvent(sessionID, session.MessageAdded, llm.Message{
		Role:    llm.RoleUser,
		Content: message,
	})
	if err := sess.Append(ctx, e); err != nil {
		return agent.StepResult{}, err
	}
	return r.run(ctx, sess, chunkFn)
}

// Run executes the agent on the given session. If a LaneManager is configured,
// execution is serialized within the session lane.
func (r *Runner) Run(ctx context.Context, sessionID string) (agent.StepResult, error) {
	sess, err := r.getOrLoad(ctx, sessionID)
	if err != nil {
		return agent.StepResult{}, err
	}
	return r.run(ctx, sess, nil)
}

// RunStream executes the agent with streaming. If a LaneManager is configured,
// execution is serialized within the session lane.
func (r *Runner) RunStream(
	ctx context.Context,
	sessionID string,
	chunkFn func(*llm.Chunk),
) (agent.StepResult, error) {
	sess, err := r.getOrLoad(ctx, sessionID)
	if err != nil {
		return agent.StepResult{}, err
	}
	return r.run(ctx, sess, chunkFn)
}

// run is the shared entry point for Run/RunStream/Send/SendStream.
// It applies lane serialization and delegates to execute.
func (r *Runner) run(
	ctx context.Context,
	sess *session.Session,
	chunkFn func(*llm.Chunk),
) (agent.StepResult, error) {
	if r.Lanes == nil {
		return r.execute(ctx, sess, chunkFn)
	}

	waitCtx := ctx
	if r.WaitTimeout > 0 {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, r.WaitTimeout)
		defer cancel()
	}

	var result agent.StepResult
	errCh := r.Lanes.Execute(waitCtx, sess.ID(), func(laneCtx context.Context) error {
		execCtx := laneCtx
		if r.ExecutionTimeout > 0 {
			var cancel context.CancelFunc
			execCtx, cancel = context.WithTimeout(laneCtx, r.ExecutionTimeout)
			defer cancel()
		}

		var err error
		result, err = r.execute(execCtx, sess, chunkFn)
		return err
	})
	return result, <-errCh
}

func (r *Runner) execute(
	ctx context.Context,
	sess *session.Session,
	chunkFn func(*llm.Chunk),
) (agent.StepResult, error) {
	meta := hook.SessionMeta{ID: sess.ID()}
	if _, err := r.Hooks.Run(ctx, hook.EventSessionStart, meta, nil); err != nil {
		return agent.StepResult{}, err
	}
	defer func() {
		r.Hooks.Run(context.Background(), hook.EventSessionEnd, meta, nil)
	}()

	// Execute agent turn.
	// Use streaming if chunkFn is set and the agent supports it.
	var (
		result agent.StepResult
		err    error
	)
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
