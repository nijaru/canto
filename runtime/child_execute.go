package runtime

import (
	"context"
	"errors"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/session"
)

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
		select {
		case r.sem <- struct{}{}:
		case <-ctx.Done():
			eventCtx := context.WithoutCancel(ctx)
			result := ChildResult{Ref: ref, Err: ctx.Err()}
			result.Status = r.recordChildError(eventCtx, parent, ref, metadata, result.Err)
			handle.result = result
			return
		}
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

	switch {
	case result.Err != nil:
		result.Status = r.recordChildError(eventCtx, parent, ref, metadata, result.Err)
	case stepResult.TurnStopReason == agent.TurnStopWaiting:
		result.Status = r.recordBlockedChild(eventCtx, parent, childSess, ref, metadata)
	default:
		result.Status = r.recordCompletedChild(eventCtx, parent, ref, metadata, result)
	}

	handle.result = result
}

func (r *ChildRunner) recordChildError(
	ctx context.Context,
	parent *session.Session,
	ref ChildRef,
	metadata map[string]any,
	err error,
) session.ChildStatus {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		_ = parent.Append(ctx, session.NewChildCanceledEvent(parent.ID(), session.ChildCanceledData{
			ChildID:        ref.ID,
			ChildSessionID: ref.SessionID,
			Reason:         err.Error(),
			Metadata:       metadata,
		}))
		return session.ChildStatusCanceled
	}
	_ = parent.Append(ctx, session.NewChildFailedEvent(parent.ID(), session.ChildFailedData{
		ChildID:        ref.ID,
		ChildSessionID: ref.SessionID,
		Error:          err.Error(),
		Metadata:       metadata,
	}))
	return session.ChildStatusFailed
}

func (r *ChildRunner) recordBlockedChild(
	ctx context.Context,
	parent *session.Session,
	childSess *session.Session,
	ref ChildRef,
	metadata map[string]any,
) session.ChildStatus {
	waitReason, externalID := childWaitReason(childSess)
	_ = parent.Append(ctx, session.NewChildBlockedEvent(parent.ID(), session.ChildBlockedData{
		ChildID:        ref.ID,
		ChildSessionID: ref.SessionID,
		Reason:         waitReason,
		Metadata: mergeMetadata(metadata, map[string]any{
			"external_id": externalID,
		}),
	}))
	return session.ChildStatusBlocked
}

func (r *ChildRunner) recordCompletedChild(
	ctx context.Context,
	parent *session.Session,
	ref ChildRef,
	metadata map[string]any,
	result ChildResult,
) session.ChildStatus {
	artifactIDs := make([]string, 0, len(result.Artifacts))
	for _, artifact := range result.Artifacts {
		if artifact.ID != "" {
			artifactIDs = append(artifactIDs, artifact.ID)
		}
	}
	_ = parent.Append(ctx, session.NewChildCompletedEvent(parent.ID(), session.ChildCompletedData{
		ChildID:        ref.ID,
		ChildSessionID: ref.SessionID,
		Summary:        result.Summary,
		ArtifactIDs:    artifactIDs,
		Usage:          result.Usage,
		Metadata:       metadata,
	}))
	return session.ChildStatusCompleted
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
