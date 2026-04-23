package runtime

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
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
		child, err = parent.Branch(ctx, childSessionID, session.ForkOptions{})
		if err != nil {
			return nil, fmt.Errorf("materialize forked child session: %w", err)
		}
	default:
		child = session.New(childSessionID).WithWriter(r.store)
	}

	for _, msg := range childSeedMessages(spec) {
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
	defer close(handle.done)
	defer func() {
		if handle.cleanup != nil {
			handle.cleanup()
		}
	}()
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
		Metadata:       metadata,
	}))

	childRuntime := NewRunner(r.store, childAgent)
	childRuntime.queue = r.queue
	childRuntime.coordinator = r.coordinator
	childRuntime.hooks = r.hooks
	childRuntime.waitTimeout = r.waitTimeout
	childRuntime.executionTimeout = r.executionTimeout
	childRuntime.sessions[childSess.ID()] = childSess

	// Propagate metadata to the child execution context
	ctx = session.WithMetadata(ctx, map[string]any{
		"agent_id": ref.AgentID,
	})
	if len(metadata) > 0 {
		ctx = session.WithMetadata(ctx, metadata)
	}

	stepResult, runErr := childRuntime.Run(ctx, childSess.ID())
	result.TurnStopReason = stepResult.TurnStopReason
	result.Summary = stepResult.Content
	result.Usage = stepResult.Usage
	result.Artifacts = collectArtifacts(childSess)
	result.Err = runErr
	recordChildArtifacts(eventCtx, parent, ref, result.Artifacts)
	if result.Err != nil {
		result.Status = session.ChildStatusFailed
		if errors.Is(result.Err, context.Canceled) ||
			errors.Is(result.Err, context.DeadlineExceeded) {
			result.Status = session.ChildStatusCanceled
			_ = parent.Append(
				eventCtx,
				session.NewChildCanceledEvent(parent.ID(), session.ChildCanceledData{
					ChildID:        ref.ID,
					ChildSessionID: ref.SessionID,
					Reason:         result.Err.Error(),
					Metadata:       metadata,
				}),
			)
		} else {
			_ = parent.Append(eventCtx, session.NewChildFailedEvent(parent.ID(), session.ChildFailedData{
				ChildID:        ref.ID,
				ChildSessionID: ref.SessionID,
				Error:          result.Err.Error(),
				Metadata:       metadata,
			}))
		}
	} else if stepResult.TurnStopReason == agent.TurnStopWaiting {
		result.Status = session.ChildStatusBlocked
		waitReason, externalID := childWaitReason(childSess)
		_ = parent.Append(eventCtx, session.NewChildBlockedEvent(parent.ID(), session.ChildBlockedData{
			ChildID:        ref.ID,
			ChildSessionID: ref.SessionID,
			Reason:         waitReason,
			Metadata: mergeMetadata(metadata, map[string]any{
				"external_id": externalID,
			}),
		}))
	} else {
		result.Status = session.ChildStatusCompleted
		artifactIDs := make([]string, 0, len(result.Artifacts))
		for _, artifact := range result.Artifacts {
			if artifact.ID != "" {
				artifactIDs = append(artifactIDs, artifact.ID)
			}
		}
		_ = parent.Append(eventCtx, session.NewChildCompletedEvent(parent.ID(), session.ChildCompletedData{
			ChildID:        ref.ID,
			ChildSessionID: ref.SessionID,
			Summary:        result.Summary,
			ArtifactIDs:    artifactIDs,
			Usage:          result.Usage,
			Metadata:       metadata,
		}))
	}

	handle.result = result
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

func recordChildArtifacts(
	ctx context.Context,
	parent *session.Session,
	ref ChildRef,
	artifacts []session.ArtifactRef,
) {
	for _, artifact := range artifacts {
		_ = session.RecordArtifact(ctx, parent, session.ArtifactRecordedData{
			ChildID:   ref.ID,
			Artifact:  artifact,
			SessionID: ref.SessionID,
		})
	}
}

func validateChildSpec(spec ChildSpec) error {
	switch spec.Mode {
	case session.ChildModeFork, session.ChildModeHandoff, session.ChildModeFresh:
		return nil
	case "":
		return nil
	default:
		return fmt.Errorf("spawn child: unsupported mode %q", spec.Mode)
	}
}

func configureChildAgent(a agent.Agent, reg *tool.Registry) (agent.Agent, error) {
	return configureChildAgentWithRuntime(a, agent.RuntimeConfig{Tools: reg})
}

func configureChildAgentWithRuntime(
	a agent.Agent,
	cfg agent.RuntimeConfig,
) (agent.Agent, error) {
	if cfg.Tools == nil && len(cfg.RequestProcessors) == 0 {
		return a, nil
	}
	configurable, ok := a.(agent.RuntimeConfigurable)
	if !ok {
		return nil, fmt.Errorf("spawn child: agent %q does not support runtime overrides", a.ID())
	}
	return configurable.ConfigureRuntime(cfg), nil
}

func normalizeChildSpec(spec ChildSpec) (ChildSpec, error) {
	if spec.Agent == nil {
		return ChildSpec{}, errors.New("spawn child: nil agent")
	}
	if spec.Mode == "" {
		spec.Mode = session.ChildModeHandoff
	}
	if err := validateChildSpec(spec); err != nil {
		return ChildSpec{}, err
	}
	if spec.ID == "" {
		spec.ID = ulid.Make().String()
	}
	if spec.SessionID == "" {
		spec.SessionID = spec.ID
	}
	return spec, nil
}

func childWaitReason(sess *session.Session) (reason string, externalID string) {
	for e := range sess.Backward() {
		data, ok, err := e.WaitData()
		if err != nil || !ok || e.Type != session.WaitStarted {
			continue
		}
		return data.Reason, data.ExternalID
	}
	return "waiting", ""
}

func collectArtifacts(sess *session.Session) []session.ArtifactRef {
	artifacts := make([]session.ArtifactRef, 0)
	for e := range sess.All() {
		data, ok, err := e.ArtifactRecordedData()
		if err != nil || !ok {
			continue
		}
		if session.IsWorkspaceFileReferenceArtifact(data.Artifact) {
			continue
		}
		artifacts = append(artifacts, data.Artifact)
	}
	return artifacts
}

func mergeMetadata(base map[string]any, extra map[string]any) map[string]any {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	out := make(map[string]any, len(base)+len(extra))
	for key, value := range base {
		out[key] = value
	}
	for key, value := range extra {
		if value == nil {
			continue
		}
		if text, ok := value.(string); ok && text == "" {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	keys := make([]string, 0, len(out))
	for key := range out {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	ordered := make(map[string]any, len(out))
	for _, key := range keys {
		ordered[key] = out[key]
	}
	return ordered
}

func childSeedMessages(spec ChildSpec) []llm.Message {
	if len(spec.InitialMessages) > 0 {
		return append([]llm.Message(nil), spec.InitialMessages...)
	}
	if spec.Mode != session.ChildModeHandoff {
		return nil
	}

	var parts []string
	if spec.Task != "" {
		parts = append(parts, "Task: "+spec.Task)
	}
	if spec.Context != "" {
		parts = append(parts, "Context: "+spec.Context)
	}
	if len(parts) == 0 {
		return nil
	}

	return []llm.Message{{
		Role:    llm.RoleUser,
		Content: strings.Join(parts, "\n"),
	}}
}
