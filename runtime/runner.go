package runtime

import (
	"context"
	"fmt"
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
// Runner maintains an in-memory session registry so that Watch, Send,
// Run, and execute all share the same *session.Session object for a given
// session ID. This is required for Watch to receive events emitted by
// execute — without a shared object the channel is permanently silent.
type Runner struct {
	store            session.Store
	agent            agent.Agent
	waitTimeout      time.Duration
	executionTimeout time.Duration
	coordinator      Coordinator
	hooks            *hook.Runner
	scheduler        Scheduler
	beforeRun        []SessionFunc
	overflowRecovery overflowRecoveryOptions

	queue       *serialQueue
	childRunner *ChildRunner
	mu          sync.Mutex
	sessions    map[string]*session.Session
}

// NewRunner creates a Runner with per-session coordination enabled.
func NewRunner(s session.Store, a agent.Agent, opts ...Option) *Runner {
	cfg := applyOptions(opts)
	scheduler := cfg.scheduler
	if scheduler == nil {
		scheduler = NewLocalScheduler()
	}
	return &Runner{
		store:            s,
		agent:            a,
		waitTimeout:      cfg.waitTimeout,
		executionTimeout: cfg.executionTimeout,
		coordinator:      cfg.coordinator,
		queue:            newSerialQueue(),
		childRunner:      NewChildRunner(s, runnerChildOptions(cfg)...),
		hooks:            cfg.hooks,
		scheduler:        scheduler,
		beforeRun:        append([]SessionFunc(nil), cfg.beforeRun...),
		overflowRecovery: cfg.overflowRecovery,
		sessions:         make(map[string]*session.Session),
	}
}

// Close gracefully stops the internal local coordinator and any active goroutines.
func (r *Runner) Close() {
	if r.scheduler != nil {
		r.scheduler.Close()
	}
	if r.queue != nil {
		r.queue.stop()
	}
	if r.childRunner != nil {
		r.childRunner.Close()
	}
}

// getOrLoad returns the cached session for sessionID, loading it from the
// store on first access. All Runner methods use this so that Watch and
// execute always operate on the same in-memory object.
func (r *Runner) getOrLoad(ctx context.Context, sessionID string) (*session.Session, error) {
	r.mu.Lock()
	if sess, ok := r.sessions[sessionID]; ok {
		r.mu.Unlock()
		return sess, nil
	}
	r.mu.Unlock()

	// Load outside the lock; Store.Load may involve I/O.
	sess, err := r.store.Load(ctx, sessionID)
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

	if sess.HasWatchers() {
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
	searchStore, ok := r.store.(session.SearchStore)
	if !ok {
		return nil, fmt.Errorf("session store does not support search")
	}
	return searchStore.Search(ctx, sessionID, query)
}

// ChildRunner creates a child-run helper that inherits this runner's store,
// timeout, coordinator, and hook settings. Additional opts override the
// inherited defaults for the child runner only.
func (r *Runner) ChildRunner(opts ...Option) *ChildRunner {
	if r == nil {
		return nil
	}
	inherited := runnerChildOptions(options{
		waitTimeout:      r.waitTimeout,
		executionTimeout: r.executionTimeout,
		coordinator:      r.coordinator,
		hooks:            r.hooks,
	})
	inherited = append(inherited, opts...)
	return NewChildRunner(r.store, inherited...)
}

// Delegate executes a child request synchronously against a parent session.
func (r *Runner) Delegate(
	ctx context.Context,
	parentSessionID string,
	spec ChildSpec,
) (ChildResult, error) {
	parent, err := r.getOrLoad(ctx, parentSessionID)
	if err != nil {
		return ChildResult{}, err
	}
	return r.sharedChildRunner().Run(ctx, parent, spec)
}

// SpawnChild starts child execution asynchronously against a parent session.
func (r *Runner) SpawnChild(
	ctx context.Context,
	parentSessionID string,
	spec ChildSpec,
) (ChildRef, error) {
	parent, err := r.getOrLoad(ctx, parentSessionID)
	if err != nil {
		return ChildRef{}, err
	}
	return r.sharedChildRunner().Spawn(ctx, parent, spec)
}

// WaitChild waits for a previously spawned child on the runner's shared
// delegation API.
func (r *Runner) WaitChild(ctx context.Context, childID string) (ChildResult, error) {
	return r.sharedChildRunner().Wait(ctx, childID)
}

// Bootstrap records an environment snapshot as model-visible context so the
// first turn has workspace and tool context up front.
func (r *Runner) Bootstrap(ctx context.Context, sessionID string, snap Bootstrap) error {
	sess, err := r.getOrLoad(ctx, sessionID)
	if err != nil {
		return err
	}
	return snap.Append(ctx, sess)
}

// ScheduleChild queues a child run for future execution.
func (r *Runner) ScheduleChild(
	ctx context.Context,
	parentSessionID string,
	dueAt time.Time,
	spec ChildSpec,
) (ScheduledChild, error) {
	if r.scheduler == nil {
		return nil, fmt.Errorf("runner scheduler unavailable")
	}

	parent, err := r.getOrLoad(ctx, parentSessionID)
	if err != nil {
		return nil, err
	}
	spec, err = normalizeChildSpec(spec)
	if err != nil {
		return nil, err
	}

	childRunner := r.sharedChildRunner()
	if childRunner == nil {
		return nil, fmt.Errorf("runner child runner unavailable")
	}

	handle := newScheduledChildHandle(ChildRef{
		ID:              spec.ID,
		SessionID:       spec.SessionID,
		ParentSessionID: parent.ID(),
		AgentID:         spec.Agent.ID(),
		Mode:            spec.Mode,
	})

	task, err := r.scheduler.Schedule(ctx, dueAt, func(runCtx context.Context) error {
		parent, loadErr := r.getOrLoad(runCtx, parentSessionID)
		if loadErr != nil {
			handle.finish(ChildResult{
				Ref: handle.ChildRef(),
				Err: loadErr,
			}, loadErr)
			return loadErr
		}

		result, runErr := childRunner.Run(runCtx, parent, spec)
		handle.finish(result, runErr)
		return runErr
	})
	if err != nil {
		return nil, err
	}
	handle.attach(task)
	return handle, nil
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
	return r.run(ctx, sess, nil)
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
	return r.run(ctx, sess, chunkFn)
}

// run is the shared entry point for Run/RunStream/Send/SendStream.
// It applies per-session coordination and delegates to execute.
func (r *Runner) run(
	ctx context.Context,
	sess *session.Session,
	chunkFn func(*llm.Chunk),
) (agent.StepResult, error) {
	if r.coordinator != nil {
		return r.executeWithCoordinator(ctx, sess, chunkFn)
	}
	if r.queue == nil {
		return r.execute(ctx, sess, chunkFn)
	}

	waitCtx := ctx
	if r.waitTimeout > 0 {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, r.waitTimeout)
		defer cancel()
	}

	var result agent.StepResult
	errCh := r.queue.execute(waitCtx, sess.ID(), func(laneCtx context.Context) error {
		execCtx := laneCtx
		if r.executionTimeout > 0 {
			var cancel context.CancelFunc
			execCtx, cancel = context.WithTimeout(laneCtx, r.executionTimeout)
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
	if r.coordinator == nil {
		return r.execute(ctx, sess, chunkFn)
	}

	waitCtx := ctx
	if r.waitTimeout > 0 {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, r.waitTimeout)
		defer cancel()
	}

	ticket, err := r.coordinator.Enqueue(waitCtx, sess.ID())
	if err != nil {
		return agent.StepResult{}, err
	}
	lease, err := r.coordinator.Await(waitCtx, ticket)
	if err != nil {
		return agent.StepResult{}, err
	}

	execCtx := ctx
	if r.executionTimeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, r.executionTimeout)
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
		for _, fn := range r.beforeRun {
			if err := fn(ctx, sess); err != nil {
				return agent.StepResult{}, err
			}
		}

		if chunkFn != nil {
			if s, ok := r.agent.(agent.Streamer); ok {
				result, err = s.StreamTurn(ctx, sess, chunkFn)
			} else {
				result, err = r.agent.Turn(ctx, sess)
			}
		} else {
			result, err = r.agent.Turn(ctx, sess)
		}

		if err == nil {
			return result, nil
		}
		if !r.shouldRecoverOverflow(err, attempt) {
			return agent.StepResult{}, err
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

func (r *Runner) shouldRecoverOverflow(err error, attempt int) bool {
	recovery := r.overflowRecovery
	return recovery.isOverflow != nil &&
		recovery.compact != nil &&
		attempt < recovery.maxRetries &&
		recovery.isOverflow(err)
}

func (r *Runner) sharedChildRunner() *ChildRunner {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.childRunner
}

func runnerChildOptions(cfg options) []Option {
	inherited := []Option{
		WithWaitTimeout(cfg.waitTimeout),
		WithExecutionTimeout(cfg.executionTimeout),
	}
	if cfg.coordinator != nil {
		inherited = append(inherited, WithCoordinator(cfg.coordinator))
	}
	if cfg.hooks != nil {
		inherited = append(inherited, WithHooks(cfg.hooks))
	}
	if cfg.maxConcurrent > 0 {
		inherited = append(inherited, WithMaxConcurrent(cfg.maxConcurrent))
	}
	return inherited
}
