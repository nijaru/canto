package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	agentskills "github.com/nijaru/agentskills"
	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/hook"
	"github.com/nijaru/canto/llm"
	prompt "github.com/nijaru/canto/prompt"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/skill"
	"github.com/nijaru/canto/tool"
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
	Tools           *tool.Registry
	Worktree        *WorktreeSpec
	Skills          []*agentskills.Skill
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
	WorkspacePath   string
}

// ChildResult is the durable execution outcome of a child run.
type ChildResult struct {
	Ref            ChildRef
	Status         session.ChildStatus
	Summary        string
	TurnStopReason agent.TurnStopReason
	Usage          llm.Usage
	Artifacts      []session.ArtifactRef
	Err            error
}

type childHandle struct {
	done    chan struct{}
	result  ChildResult
	cleanup func()
}

// ChildRunner materializes child sessions, records lifecycle facts in the
// parent session, and executes child agents with bounded concurrency.
type ChildRunner struct {
	store            session.Store
	waitTimeout      time.Duration
	executionTimeout time.Duration
	coordinator      Coordinator
	hooks            *hook.Runner
	maxConcurrent    int

	queue   *serialQueue
	mu      sync.Mutex
	handles map[string]*childHandle
	sem     chan struct{}
}

// NewChildRunner creates a child-runner using the same timeout defaults as Runner.
func NewChildRunner(store session.Store, opts ...Option) *ChildRunner {
	cfg := applyOptions(opts)
	return &ChildRunner{
		store:            store,
		waitTimeout:      cfg.waitTimeout,
		executionTimeout: cfg.executionTimeout,
		coordinator:      cfg.coordinator,
		queue:            newSerialQueue(),
		hooks:            cfg.hooks,
		handles:          make(map[string]*childHandle),
		maxConcurrent:    cfg.maxConcurrent,
	}
}

// Close stops the internal local coordinator.
func (r *ChildRunner) Close() {
	if r.queue != nil {
		r.queue.stop()
	}
}

// Run executes a child request synchronously through the same durable spawn
// lifecycle used by Spawn/Wait.
func (r *ChildRunner) Run(
	ctx context.Context,
	parent *session.Session,
	spec ChildSpec,
) (ChildResult, error) {
	ref, err := r.Spawn(ctx, parent, spec)
	if err != nil {
		return ChildResult{}, err
	}
	return r.Wait(ctx, ref.ID)
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

	r.ensureSemaphore()

	spec, err := normalizeChildSpec(spec)
	if err != nil {
		return ChildRef{}, err
	}

	runtimeCfg := agent.RuntimeConfig{Tools: spec.Tools}
	if len(spec.Skills) > 0 {
		securityHooks := skill.DefaultSecurityHooks()
		if err := securityHooks.Validate(ctx, spec.Skills...); err != nil {
			return ChildRef{}, err
		}
		scopedTools, err := securityHooks.ScopeRegistry(spec.Tools, spec.Skills...)
		if err != nil {
			return ChildRef{}, err
		}
		runtimeCfg.Tools = scopedTools
	}
	if len(spec.Skills) > 0 {
		runtimeCfg.RequestProcessors = []prompt.RequestProcessor{
			skill.PreloadPrompt(spec.Skills...),
		}
	}
	childAgent, err := configureChildAgentWithRuntime(spec.Agent, runtimeCfg)
	if err != nil {
		return ChildRef{}, err
	}
	metadata := mergeMetadata(spec.Metadata, nil)
	var worktree *Worktree
	if spec.Worktree != nil {
		worktree, err = PrepareWorktree(ctx, *spec.Worktree)
		if err != nil {
			return ChildRef{}, err
		}
		metadata = mergeMetadata(metadata, map[string]any{
			"workspace_path": worktree.Path(),
			"workspace_repo": worktree.RepositoryPath(),
		})
	}

	childSess, err := r.materializeChildSession(ctx, parent, spec.SessionID, spec)
	if err != nil {
		if worktree != nil {
			worktree.Close()
		}
		return ChildRef{}, err
	}

	ref := ChildRef{
		ID:              spec.ID,
		SessionID:       spec.SessionID,
		ParentSessionID: parent.ID(),
		AgentID:         childAgent.ID(),
		Mode:            spec.Mode,
		WorkspacePath:   workspacePath(worktree),
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
		Metadata:        metadata,
	})); err != nil {
		if worktree != nil {
			worktree.Close()
		}
		return ChildRef{}, err
	}

	handle := &childHandle{done: make(chan struct{})}
	if worktree != nil {
		handle.cleanup = worktree.Close
	}
	r.mu.Lock()
	r.handles[ref.ID] = handle
	r.mu.Unlock()

	runCtx := ctx
	if spec.Detached {
		runCtx = context.WithoutCancel(ctx)
	}

	go r.runChild(runCtx, parent, childSess, childAgent, ref, metadata, handle)

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
		r.mu.Lock()
		delete(r.handles, childID)
		r.mu.Unlock()
		return handle.result, nil
	case <-ctx.Done():
		return ChildResult{}, ctx.Err()
	}
}

func (r *ChildRunner) ensureSemaphore() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.maxConcurrent <= 0 {
		r.sem = nil
		return
	}
	if r.sem == nil || cap(r.sem) != r.maxConcurrent {
		r.sem = make(chan struct{}, r.maxConcurrent)
	}
}
