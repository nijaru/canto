package canto

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/approval"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

func TestRunLifecycleAnnotatesCompactionRetryAndCancellation(t *testing.T) {
	var state runLifecycleState

	started := RunEvent{
		Type: RunEventSession,
		Event: session.NewCompactionStartedEvent("sess", session.CompactionStartedData{
			Strategy:      "summarize",
			MaxTokens:     1000,
			CurrentTokens: 1500,
		}),
	}
	state.annotate(&started)
	if started.Lifecycle == nil ||
		started.Lifecycle.Type != RunLifecycleCompaction ||
		started.Lifecycle.Status != RunLifecycleStarted ||
		started.Lifecycle.Compaction == nil ||
		started.Lifecycle.Compaction.CurrentTokens != 1500 {
		t.Fatalf("compaction started lifecycle = %#v", started.Lifecycle)
	}

	compaction := RunEvent{
		Type: RunEventSession,
		Event: session.NewCompactionEvent("sess", session.CompactionSnapshot{
			Strategy:      "summarize",
			MaxTokens:     1000,
			CurrentTokens: 1500,
			CutoffEventID: "event-1",
		}),
	}
	state.annotate(&compaction)
	if compaction.Lifecycle == nil ||
		compaction.Lifecycle.Type != RunLifecycleCompaction ||
		compaction.Lifecycle.Status != RunLifecycleCompleted ||
		compaction.Lifecycle.Compaction == nil ||
		compaction.Lifecycle.Compaction.Strategy != "summarize" {
		t.Fatalf("compaction lifecycle = %#v", compaction.Lifecycle)
	}

	retry := RunEvent{
		Type: RunEventSession,
		Event: session.NewEscalationRetriedEvent("sess", session.EscalationRetriedData{
			AgentID: "agent",
			Scope:   "tool",
			Target:  "call-1",
			Attempt: 2,
			Error:   "transient",
		}),
	}
	state.annotate(&retry)
	if retry.Lifecycle == nil ||
		retry.Lifecycle.Type != RunLifecycleRetry ||
		retry.Lifecycle.Status != RunLifecycleRetrying ||
		retry.Lifecycle.Retry == nil ||
		retry.Lifecycle.Retry.Attempt != 2 {
		t.Fatalf("retry lifecycle = %#v", retry.Lifecycle)
	}

	canceledTurn := RunEvent{
		Type: RunEventSession,
		Event: session.NewTurnCompletedEvent("sess", session.TurnCompletedData{
			AgentID: "agent",
			Error:   context.Canceled.Error(),
		}),
	}
	state.annotate(&canceledTurn)
	if canceledTurn.Lifecycle == nil ||
		canceledTurn.Lifecycle.Type != RunLifecycleTurn ||
		canceledTurn.Lifecycle.Status != RunLifecycleCanceled ||
		!canceledTurn.Lifecycle.Canceled ||
		!canceledTurn.Lifecycle.Terminal {
		t.Fatalf("canceled turn lifecycle = %#v", canceledTurn.Lifecycle)
	}

	runErr := RunEvent{Type: RunEventError, Err: context.Canceled}
	state.annotate(&runErr)
	if runErr.Lifecycle == nil ||
		runErr.Lifecycle.Type != RunLifecycleRun ||
		runErr.Lifecycle.Status != RunLifecycleCanceled ||
		!runErr.Lifecycle.Canceled ||
		!runErr.Lifecycle.Terminal {
		t.Fatalf("canceled run lifecycle = %#v", runErr.Lifecycle)
	}
}

func TestRunLifecycleAnnotatesFailedRunError(t *testing.T) {
	var state runLifecycleState
	event := RunEvent{Type: RunEventError, Err: errors.New("provider failed")}
	state.annotate(&event)
	if event.Lifecycle == nil ||
		event.Lifecycle.Type != RunLifecycleRun ||
		event.Lifecycle.Status != RunLifecycleFailed ||
		event.Lifecycle.Error != "provider failed" ||
		!event.Lifecycle.Terminal {
		t.Fatalf("failed run lifecycle = %#v", event.Lifecycle)
	}
}

func TestRunLifecycleAnnotatesChildEvents(t *testing.T) {
	tests := []struct {
		name     string
		event    session.Event
		status   RunLifecycleStatus
		terminal bool
		canceled bool
	}{
		{
			name: "requested",
			event: session.NewChildRequestedEvent("sess", session.ChildRequestedData{
				ChildID:        "child-1",
				ChildSessionID: "child-session",
				AgentID:        "reviewer",
				Mode:           session.ChildModeHandoff,
				Task:           "inspect",
				Context:        "repo",
			}),
			status: RunLifecycleRequested,
		},
		{
			name: "started",
			event: session.NewChildStartedEvent("sess", session.ChildStartedData{
				ChildID:        "child-1",
				ChildSessionID: "child-session",
				AgentID:        "reviewer",
			}),
			status: RunLifecycleStarted,
		},
		{
			name: "progressed",
			event: session.NewChildProgressedEvent("sess", session.ChildProgressedData{
				ChildID:        "child-1",
				ChildSessionID: "child-session",
				Status:         "running tests",
				Message:        "partial output",
			}),
			status: RunLifecycleUpdated,
		},
		{
			name: "blocked",
			event: session.NewChildBlockedEvent("sess", session.ChildBlockedData{
				ChildID:        "child-1",
				ChildSessionID: "child-session",
				Reason:         "approval required",
			}),
			status: RunLifecycleBlocked,
		},
		{
			name: "completed",
			event: session.NewChildCompletedEvent("sess", session.ChildCompletedData{
				ChildID:        "child-1",
				ChildSessionID: "child-session",
				Summary:        "done",
				Usage:          llm.Usage{InputTokens: 3, OutputTokens: 4, TotalTokens: 7},
			}),
			status:   RunLifecycleCompleted,
			terminal: true,
		},
		{
			name: "failed",
			event: session.NewChildFailedEvent("sess", session.ChildFailedData{
				ChildID:        "child-1",
				ChildSessionID: "child-session",
				Error:          "boom",
			}),
			status:   RunLifecycleFailed,
			terminal: true,
		},
		{
			name: "canceled",
			event: session.NewChildCanceledEvent("sess", session.ChildCanceledData{
				ChildID:        "child-1",
				ChildSessionID: "child-session",
				Reason:         "user",
			}),
			status:   RunLifecycleCanceled,
			terminal: true,
			canceled: true,
		},
		{
			name: "merged",
			event: session.NewChildMergedEvent("sess", session.ChildMergedData{
				ChildID:        "child-1",
				ChildSessionID: "child-session",
				Note:           "merged artifacts",
			}),
			status: RunLifecycleMerged,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var state runLifecycleState
			event := RunEvent{Type: RunEventSession, Event: tt.event}
			state.annotate(&event)
			if event.Lifecycle == nil ||
				event.Lifecycle.Type != RunLifecycleChild ||
				event.Lifecycle.Status != tt.status ||
				event.Lifecycle.Terminal != tt.terminal ||
				event.Lifecycle.Canceled != tt.canceled ||
				event.Lifecycle.Child == nil ||
				event.Lifecycle.Child.ID != "child-1" ||
				event.Lifecycle.Child.SessionID != "child-session" {
				t.Fatalf("child lifecycle = %#v", event.Lifecycle)
			}
			if tt.status == RunLifecycleCompleted &&
				(event.Usage == nil ||
					event.Usage.Kind != RunUsageChild ||
					event.Usage.Cumulative.TotalTokens != 7 ||
					event.Usage.Delta.TotalTokens != 7) {
				t.Fatalf("child usage = %#v", event.Usage)
			}
		})
	}
}

func TestRunLifecycleAnnotatesWaitEvents(t *testing.T) {
	tests := []struct {
		name   string
		event  session.Event
		status RunLifecycleStatus
	}{
		{
			name: "started",
			event: session.NewWaitStartedEvent("sess", session.WaitData{
				Reason:     "approval required",
				ExternalID: "approver-1",
			}),
			status: RunLifecycleStarted,
		},
		{
			name: "resolved",
			event: session.NewWaitResolvedEvent("sess", session.WaitData{
				Reason:     "approval complete",
				ExternalID: "approver-1",
			}),
			status: RunLifecycleCompleted,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var state runLifecycleState
			event := RunEvent{Type: RunEventSession, Event: tt.event}
			state.annotate(&event)
			if event.Lifecycle == nil ||
				event.Lifecycle.Type != RunLifecycleWait ||
				event.Lifecycle.Status != tt.status ||
				event.Lifecycle.Wait == nil ||
				event.Lifecycle.Wait.ExternalID != "approver-1" ||
				event.Lifecycle.Wait.Reason == "" {
				t.Fatalf("wait lifecycle = %#v", event.Lifecycle)
			}
		})
	}
}

func TestRunLifecycleAnnotatesApprovalEvents(t *testing.T) {
	tests := []struct {
		name     string
		event    session.Event
		status   RunLifecycleStatus
		canceled bool
	}{
		{
			name: "requested",
			event: session.NewEvent("sess", session.ApprovalRequested, approval.Request{
				ID:        "approval-1",
				Tool:      "bash",
				Category:  "execution",
				Operation: "run",
				Resource:  "make test",
			}),
			status: RunLifecycleRequested,
		},
		{
			name: "resolved",
			event: session.NewEvent("sess", session.ApprovalResolved, map[string]any{
				"id":       "approval-1",
				"decision": approval.DecisionAllow,
				"reason":   "safe",
			}),
			status: RunLifecycleCompleted,
		},
		{
			name: "canceled",
			event: session.NewEvent("sess", session.ApprovalCanceled, map[string]any{
				"id":     "approval-1",
				"tool":   "bash",
				"reason": "context canceled",
			}),
			status:   RunLifecycleCanceled,
			canceled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var state runLifecycleState
			event := RunEvent{Type: RunEventSession, Event: tt.event}
			state.annotate(&event)
			if event.Lifecycle == nil ||
				event.Lifecycle.Type != RunLifecycleApproval ||
				event.Lifecycle.Status != tt.status ||
				event.Lifecycle.Canceled != tt.canceled ||
				event.Lifecycle.Approval == nil ||
				event.Lifecycle.Approval.ID != "approval-1" {
				t.Fatalf("approval lifecycle = %#v", event.Lifecycle)
			}
		})
	}
}

func TestRunLifecycleTerminalUsageEmitsUnreportedDeltaOnce(t *testing.T) {
	var state runLifecycleState

	chunk := RunEvent{
		Type: RunEventChunk,
		Chunk: llm.Chunk{
			Usage: &llm.Usage{InputTokens: 10, TotalTokens: 10, Cost: 0.01},
		},
	}
	state.annotate(&chunk)
	if chunk.Usage == nil ||
		chunk.Usage.Delta.TotalTokens != 10 ||
		chunk.Usage.Cumulative.TotalTokens != 10 {
		t.Fatalf("chunk usage = %#v", chunk.Usage)
	}

	turn := RunEvent{
		Type: RunEventSession,
		Event: session.NewTurnCompletedEvent("sess", session.TurnCompletedData{
			Usage: llm.Usage{
				InputTokens:  12,
				OutputTokens: 5,
				TotalTokens:  17,
				Cost:         0.017,
			},
		}),
	}
	state.annotate(&turn)
	if turn.Usage == nil ||
		turn.Usage.Kind != RunUsageTurn ||
		turn.Usage.Cumulative.TotalTokens != 17 ||
		turn.Usage.Delta.InputTokens != 2 ||
		turn.Usage.Delta.OutputTokens != 5 ||
		turn.Usage.Delta.TotalTokens != 7 ||
		math.Abs(turn.Usage.Delta.Cost-0.007) > 1e-9 {
		t.Fatalf("turn terminal usage = %#v", turn.Usage)
	}

	result := RunEvent{
		Type: RunEventResult,
		Result: agent.StepResult{Usage: llm.Usage{
			InputTokens:  12,
			OutputTokens: 5,
			TotalTokens:  17,
			Cost:         0.017,
		}},
	}
	state.annotate(&result)
	if result.Usage == nil ||
		result.Usage.Cumulative.TotalTokens != 17 ||
		usageHasValue(result.Usage.Delta) {
		t.Fatalf("result terminal usage = %#v", result.Usage)
	}
}
