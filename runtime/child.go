package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/hook"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
	"github.com/oklog/ulid/v2"
)

// ChildSpec defines a single child run request. Agent selection, initial
// messages, and decomposition strategy are supplied by the caller.
type ChildSpec struct {
	ID              string
	SessionID       string
	Agent           agent.Agent
	Mode            session.ChildMode
	Task            string
	Context         string
	ParentEventID   string
	SharedPrefixKey string
	InitialMessages []llm.Message
	Metadata        map[string]any
	// Detached keeps child execution running even if the Spawn context is
	// canceled. The default is attached execution, which inherits cancellation.
	Detached bool
}

// ChildRef identifies a spawned child run.
type ChildRef struct {
	ID              string
	SessionID       string
	ParentSessionID string
	AgentID         string
	Mode            session.ChildMode
}

// ChildResult is the durable execution outcome of a child run.
type ChildResult struct {
	Ref    ChildRef
	Result agent.StepResult
	Err    error
}

type childHandle struct {
	done   chan struct{}
	result ChildResult
}

// ChildRunner materializes child sessions, records lifecycle facts in the
// parent session, and executes child agents with bounded concurrency.
type ChildRunner struct {
	Store            session.Store
	WaitTimeout      time.Duration
	ExecutionTimeout time.Duration
	Coordinator      Coordinator
	Hooks            *hook.Runner
	MaxConcurrent    int

	queue   *serialQueue
	mu      sync.Mutex
	handles map[string]*childHandle
	sem     chan struct{}
}

// NewChildRunner creates a child-runner using the same timeout defaults as Runner.
func NewChildRunner(store session.Store) *ChildRunner {
	return &ChildRunner{
		Store:            store,
		WaitTimeout:      defaultWaitTimeout,
		ExecutionTimeout: defaultExecutionTimeout,
		queue:            newSerialQueue(),
		Hooks:            hook.NewRunner(),
		handles:          make(map[string]*childHandle),
	}
}

// Close stops the internal local coordinator.
func (r *ChildRunner) Close() {
	if r.queue != nil {
		r.queue.stop()
	}
}

// Spawn creates a child session, records a durable request event in the parent,
// and starts child execution asynchronously.
func (r *ChildRunner) Spawn(
	ctx context.Context,
	parent *session.Session,
	spec ChildSpec,
) (ChildRef, error) {
	if parent == nil {
		return ChildRef{}, errors.New("spawn child: nil parent session")
	}
	if spec.Agent == nil {
		return ChildRef{}, errors.New("spawn child: nil agent")
	}

	r.ensureSemaphore()

	childID := spec.ID
	if childID == "" {
		childID = ulid.Make().String()
	}
	childSessionID := spec.SessionID
	if childSessionID == "" {
		childSessionID = childID
	}
	if spec.Mode == "" {
		spec.Mode = session.ChildModeHandoff
	}

	childSess, err := r.materializeChildSession(ctx, parent, childSessionID, spec)
	if err != nil {
		return ChildRef{}, err
	}

	ref := ChildRef{
		ID:              childID,
		SessionID:       childSessionID,
		ParentSessionID: parent.ID(),
		AgentID:         spec.Agent.ID(),
		Mode:            spec.Mode,
	}

	if err := parent.Append(ctx, session.NewChildRequestedEvent(parent.ID(), session.ChildRequestedData{
		ChildID:         ref.ID,
		ChildSessionID:  ref.SessionID,
		ParentEventID:   spec.ParentEventID,
		AgentID:         ref.AgentID,
		Mode:            ref.Mode,
		Task:            spec.Task,
		Context:         spec.Context,
		SharedPrefixKey: spec.SharedPrefixKey,
		Metadata:        spec.Metadata,
	})); err != nil {
		return ChildRef{}, err
	}

	handle := &childHandle{done: make(chan struct{})}
	r.mu.Lock()
	r.handles[ref.ID] = handle
	r.mu.Unlock()

	runCtx := ctx
	if spec.Detached {
		runCtx = context.WithoutCancel(ctx)
	}

	go r.runChild(runCtx, parent, childSess, spec.Agent, ref, spec.Metadata, handle)

	return ref, nil
}

// Wait blocks until the child finishes or ctx is canceled.
func (r *ChildRunner) Wait(ctx context.Context, childID string) (ChildResult, error) {
	r.mu.Lock()
	handle, ok := r.handles[childID]
	r.mu.Unlock()
	if !ok {
		return ChildResult{}, fmt.Errorf("wait child %q: not found", childID)
	}

	select {
	case <-handle.done:
		return handle.result, nil
	case <-ctx.Done():
		return ChildResult{}, ctx.Err()
	}
}

func (r *ChildRunner) materializeChildSession(
	ctx context.Context,
	parent *session.Session,
	childSessionID string,
	spec ChildSpec,
) (*session.Session, error) {
	var child *session.Session
	var err error

	switch spec.Mode {
	case session.ChildModeFork:
		if r.Store == nil {
			return nil, errors.New("materialize forked child session: nil store")
		}
		child, err = r.Store.Fork(ctx, parent.ID(), childSessionID)
		if err != nil {
			return nil, fmt.Errorf("materialize forked child session: %w", err)
		}
	default:
		child = session.New(childSessionID).WithWriter(r.Store)
	}

	for _, msg := range spec.InitialMessages {
		if err := child.Append(ctx, session.NewMessage(child.ID(), msg)); err != nil {
			return nil, fmt.Errorf("materialize child initial message: %w", err)
		}
	}

	return child, nil
}

func (r *ChildRunner) runChild(
	ctx context.Context,
	parent *session.Session,
	childSess *session.Session,
	childAgent agent.Agent,
	ref ChildRef,
	metadata map[string]any,
	handle *childHandle,
) {
	if r.sem != nil {
		r.sem <- struct{}{}
		defer func() { <-r.sem }()
	}

	eventCtx := context.WithoutCancel(ctx)
	result := ChildResult{Ref: ref}
	_ = parent.Append(eventCtx, session.NewChildStartedEvent(parent.ID(), session.ChildStartedData{
		ChildID:        ref.ID,
		ChildSessionID: ref.SessionID,
		AgentID:        ref.AgentID,
	}))

	childRuntime := NewRunner(r.Store, childAgent)
	childRuntime.queue = r.queue
	childRuntime.Coordinator = r.Coordinator
	childRuntime.Hooks = r.Hooks
	childRuntime.WaitTimeout = r.WaitTimeout
	childRuntime.ExecutionTimeout = r.ExecutionTimeout
	childRuntime.sessions[childSess.ID()] = childSess

	// Propagate metadata to the child execution context
	ctx = session.WithMetadata(ctx, map[string]any{
		"agent_id": ref.AgentID,
	})
	if len(metadata) > 0 {
		ctx = session.WithMetadata(ctx, metadata)
	}

	result.Result, result.Err = childRuntime.Run(ctx, childSess.ID())
	if result.Err != nil {
		if errors.Is(result.Err, context.Canceled) ||
			errors.Is(result.Err, context.DeadlineExceeded) {
			_ = parent.Append(
				eventCtx,
				session.NewChildCanceledEvent(parent.ID(), session.ChildCanceledData{
					ChildID:        ref.ID,
					ChildSessionID: ref.SessionID,
					Reason:         result.Err.Error(),
				}),
			)
		} else {
			_ = parent.Append(eventCtx, session.NewChildFailedEvent(parent.ID(), session.ChildFailedData{
				ChildID:        ref.ID,
				ChildSessionID: ref.SessionID,
				Error:          result.Err.Error(),
			}))
		}
	} else {
		_ = parent.Append(eventCtx, session.NewChildCompletedEvent(parent.ID(), session.ChildCompletedData{
			ChildID:        ref.ID,
			ChildSessionID: ref.SessionID,
			Summary:        result.Result.Content,
			Usage:          result.Result.Usage,
		}))
	}

	handle.result = result
	close(handle.done)
}

func (r *ChildRunner) ensureSemaphore() {
	if r.MaxConcurrent <= 0 {
		r.sem = nil
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sem == nil || cap(r.sem) != r.MaxConcurrent {
		r.sem = make(chan struct{}, r.MaxConcurrent)
	}
}
