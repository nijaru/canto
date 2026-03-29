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
// By default it uses built-in local coordination to serialize execution within
// a session while allowing concurrent execution across different sessions.
//
// Runner maintains an in-memory session registry so that Subscribe, Send,
// Run, and execute all share the same *session.Session object for a given
// session ID. This is required for Subscribe to receive events emitted by
// execute — without a shared object the channel is permanently silent.
type Runner struct {
	Store session.Store
	Agent agent.Agent

	// WaitTimeout is the maximum time to wait in the local queue or custom
	// coordinator for a session run to start.
	WaitTimeout time.Duration
	// ExecutionTimeout is the maximum time to spend running the agent turn.
	ExecutionTimeout time.Duration

	Coordinator Coordinator
	Hooks       *hook.Runner

	queue    *serialQueue
	mu       sync.Mutex
	sessions map[string]*session.Session
}

// NewRunner creates a Runner with per-session coordination enabled.
func NewRunner(s session.Store, a agent.Agent) *Runner {
	return &Runner{
		Store:            s,
		Agent:            a,
		WaitTimeout:      defaultWaitTimeout,
		ExecutionTimeout: defaultExecutionTimeout,
		queue:            newSerialQueue(),
		Hooks:            hook.NewRunner(),
		sessions:         make(map[string]*session.Session),
	}
}

// Close gracefully stops the internal local coordinator and any active goroutines.
func (r *Runner) Close() {
	if r.queue != nil {
		r.queue.stop()
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
//
// Eviction is a no-op if the session has an active execution lane or
// live subscribers.
func (r *Runner) Evict(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	sess, ok := r.sessions[sessionID]
	if !ok {
		return
	}

	if sess.HasSubscribers() {
		return
	}

	if r.queue != nil && r.queue.IsActive(sessionID) {
		return
	}

	delete(r.sessions, sessionID)
}

// Watch returns a live, lossy stream of events for the given session.
//
// Events emitted by Run/Send on the same Runner are delivered to this
// subscription because both share the same in-memory session object.
func (r *Runner) Watch(ctx context.Context, sessionID string) (*session.Subscription, error) {
	sess, err := r.getOrLoad(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return sess.Watch(ctx), nil
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

// Run executes the agent on the given session.
func (r *Runner) Run(ctx context.Context, sessionID string) (agent.StepResult, error) {
	sess, err := r.getOrLoad(ctx, sessionID)
	if err != nil {
		return agent.StepResult{}, err
	}
	return r.run(ctx, sess, nil)
}

// RunStream executes the agent with streaming.
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
// It applies per-session coordination and delegates to execute.
func (r *Runner) run(
	ctx context.Context,
	sess *session.Session,
	chunkFn func(*llm.Chunk),
) (agent.StepResult, error) {
	if r.Coordinator != nil {
		return r.executeWithCoordinator(ctx, sess, chunkFn)
	}
	if r.queue == nil {
		return r.execute(ctx, sess, chunkFn)
	}

	waitCtx := ctx
	if r.WaitTimeout > 0 {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, r.WaitTimeout)
		defer cancel()
	}

	var result agent.StepResult
	errCh := r.queue.execute(waitCtx, sess.ID(), func(laneCtx context.Context) error {
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

func (r *Runner) executeWithCoordinator(
	ctx context.Context,
	sess *session.Session,
	chunkFn func(*llm.Chunk),
) (agent.StepResult, error) {
	if r.Coordinator == nil {
		return r.execute(ctx, sess, chunkFn)
	}

	waitCtx := ctx
	if r.WaitTimeout > 0 {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, r.WaitTimeout)
		defer cancel()
	}

	ticket, err := r.Coordinator.Enqueue(waitCtx, sess.ID())
	if err != nil {
		return agent.StepResult{}, err
	}
	lease, err := r.Coordinator.Await(waitCtx, ticket)
	if err != nil {
		return agent.StepResult{}, err
	}

	execCtx := ctx
	if r.ExecutionTimeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, r.ExecutionTimeout)
		defer cancel()
	}

	result, execErr := r.executeUnderLease(execCtx, sess, chunkFn, lease)
	return result, execErr
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
