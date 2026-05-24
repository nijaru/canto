package canto

import (
	"context"
	"slices"
	"strings"

	"github.com/nijaru/canto/agent"
	"github.com/nijaru/canto/approval"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

// RunLifecycleType identifies the framework lifecycle surface represented by
// a streamed run event.
type RunLifecycleType string

const (
	RunLifecycleUsage      RunLifecycleType = "usage"
	RunLifecycleRun        RunLifecycleType = "run"
	RunLifecycleTurn       RunLifecycleType = "turn"
	RunLifecycleStep       RunLifecycleType = "step"
	RunLifecycleTool       RunLifecycleType = "tool"
	RunLifecycleChild      RunLifecycleType = "child"
	RunLifecycleWait       RunLifecycleType = "wait"
	RunLifecycleApproval   RunLifecycleType = "approval"
	RunLifecycleCompaction RunLifecycleType = "compaction"
	RunLifecycleRetry      RunLifecycleType = "retry"
)

// RunLifecycleStatus is the normalized state transition for a lifecycle event.
type RunLifecycleStatus string

const (
	RunLifecycleStarted   RunLifecycleStatus = "started"
	RunLifecycleRequested RunLifecycleStatus = "requested"
	RunLifecycleUpdated   RunLifecycleStatus = "updated"
	RunLifecycleBlocked   RunLifecycleStatus = "blocked"
	RunLifecycleCompleted RunLifecycleStatus = "completed"
	RunLifecycleFailed    RunLifecycleStatus = "failed"
	RunLifecycleCanceled  RunLifecycleStatus = "canceled"
	RunLifecycleMerged    RunLifecycleStatus = "merged"
	RunLifecycleRetrying  RunLifecycleStatus = "retrying"
)

// RunUsageKind describes where a usage observation came from.
type RunUsageKind string

const (
	RunUsageProviderDelta RunUsageKind = "provider_delta"
	RunUsageStep          RunUsageKind = "step"
	RunUsageTurn          RunUsageKind = "turn"
	RunUsageChild         RunUsageKind = "child"
)

// RunUsage carries both a cumulative usage observation and, when meaningful,
// the delta from the previous observation in the same provider request.
type RunUsage struct {
	Kind       RunUsageKind `json:"kind"`
	Delta      llm.Usage    `json:"delta,omitzero"`
	Cumulative llm.Usage    `json:"cumulative,omitzero"`
}

// RunToolLifecycle is a normalized view of tool lifecycle events.
type RunToolLifecycle struct {
	ID             string `json:"id,omitzero"`
	Name           string `json:"name,omitzero"`
	Arguments      string `json:"args,omitzero"`
	IdempotencyKey string `json:"idempotency_key,omitzero"`
	Output         string `json:"output,omitzero"`
	Delta          string `json:"delta,omitzero"`
	Snapshot       bool   `json:"snapshot,omitzero"`
	Error          string `json:"error,omitzero"`
}

// RunChildLifecycle is a normalized view of child-agent lifecycle events.
type RunChildLifecycle struct {
	ID        string            `json:"id,omitzero"`
	SessionID string            `json:"session_id,omitzero"`
	AgentID   string            `json:"agent_id,omitzero"`
	Mode      session.ChildMode `json:"mode,omitzero"`
	Task      string            `json:"task,omitzero"`
	Context   string            `json:"context,omitzero"`
	Status    string            `json:"status,omitzero"`
	Message   string            `json:"message,omitzero"`
	Summary   string            `json:"summary,omitzero"`
	Reason    string            `json:"reason,omitzero"`
	Error     string            `json:"error,omitzero"`
}

// RunWaitLifecycle is a normalized view of external wait lifecycle events.
type RunWaitLifecycle struct {
	Reason     string `json:"reason,omitzero"`
	ExternalID string `json:"external_id,omitzero"`
}

// RunApprovalLifecycle is a normalized view of tool approval lifecycle events.
type RunApprovalLifecycle struct {
	ID        string            `json:"id,omitzero"`
	Tool      string            `json:"tool,omitzero"`
	Category  string            `json:"category,omitzero"`
	Operation string            `json:"operation,omitzero"`
	Resource  string            `json:"resource,omitzero"`
	Decision  approval.Decision `json:"decision,omitzero"`
	Reason    string            `json:"reason,omitzero"`
}

// RunCompactionLifecycle summarizes a durable compaction snapshot event.
type RunCompactionLifecycle struct {
	Strategy      string  `json:"strategy,omitzero"`
	MaxTokens     int     `json:"max_tokens,omitzero"`
	ThresholdPct  float64 `json:"threshold_pct,omitzero"`
	CurrentTokens int     `json:"current_tokens,omitzero"`
	CutoffEventID string  `json:"cutoff_event_id,omitzero"`
}

// RunRetryLifecycle summarizes a framework-owned retry that is hidden from the
// outer host result.
type RunRetryLifecycle struct {
	Scope       string `json:"scope,omitzero"`
	Target      string `json:"target,omitzero"`
	Attempt     int    `json:"attempt,omitzero"`
	DelayMillis int64  `json:"delay_ms,omitzero"`
	Error       string `json:"error,omitzero"`
}

// RunLifecycle is normalized framework lifecycle metadata for a RunEvent.
// Hosts can project this field directly instead of decoding Canto event
// payloads and reconstructing generic runtime state.
type RunLifecycle struct {
	Type        RunLifecycleType        `json:"type"`
	Status      RunLifecycleStatus      `json:"status"`
	AgentID     string                  `json:"agent_id,omitzero"`
	Error       string                  `json:"error,omitzero"`
	StopReason  string                  `json:"stop_reason,omitzero"`
	Terminal    bool                    `json:"terminal,omitzero"`
	Canceled    bool                    `json:"canceled,omitzero"`
	Usage       *RunUsage               `json:"usage,omitempty"`
	Tool        *RunToolLifecycle       `json:"tool,omitempty"`
	ActiveTools []RunToolLifecycle      `json:"active_tools,omitzero"`
	Child       *RunChildLifecycle      `json:"child,omitempty"`
	Wait        *RunWaitLifecycle       `json:"wait,omitempty"`
	Approval    *RunApprovalLifecycle   `json:"approval,omitempty"`
	Compaction  *RunCompactionLifecycle `json:"compaction,omitempty"`
	Retry       *RunRetryLifecycle      `json:"retry,omitempty"`
}

type runLifecycleState struct {
	providerUsage runUsageAccumulator
	emittedUsage  llm.Usage
	activeTools   map[string]RunToolLifecycle
}

func (s *runLifecycleState) annotate(event *RunEvent) {
	switch event.Kind() {
	case RunEventChunk:
		chunk, ok := event.Chunk()
		if ok {
			s.annotateChunk(event, chunk)
		}
	case RunEventRetry:
		retry, ok := event.Retry()
		if ok {
			s.annotateRetry(event, retry)
		}
	case RunEventSession:
		sessionEvent, ok := event.SessionEvent()
		if ok {
			s.annotateSession(event, sessionEvent)
		}
	case RunEventResult:
		result, ok := event.Result()
		if ok {
			s.annotateResult(event, result)
		}
	case RunEventError:
		err, ok := event.Err()
		if ok {
			s.annotateError(event, err)
		}
	}
}

func (s *runLifecycleState) annotateChunk(event *RunEvent, chunk llm.Chunk) {
	usage, ok := s.providerUsage.delta(chunk.Usage)
	if !ok {
		return
	}
	s.recordEmittedUsage(usage.Delta)
	event.Usage = &usage
	event.Lifecycle = &RunLifecycle{
		Type:   RunLifecycleUsage,
		Status: RunLifecycleUpdated,
		Usage:  &usage,
	}
}

func (s *runLifecycleState) annotateRetry(event *RunEvent, retry llm.RetryEvent) {
	errText := ""
	if retry.Err != nil {
		errText = retry.Err.Error()
	}
	event.Lifecycle = &RunLifecycle{
		Type:   RunLifecycleRetry,
		Status: RunLifecycleRetrying,
		Retry: &RunRetryLifecycle{
			Scope:       "provider",
			Target:      "provider",
			Attempt:     retry.Attempt,
			DelayMillis: retry.Delay.Milliseconds(),
			Error:       errText,
		},
	}
}

func (s *runLifecycleState) annotateSession(event *RunEvent, sessionEvent session.Event) {
	switch sessionEvent.Type {
	case session.TurnStarted:
		data, ok, err := sessionEvent.TurnStartedData()
		if err != nil || !ok {
			return
		}
		event.Lifecycle = &RunLifecycle{
			Type:    RunLifecycleTurn,
			Status:  RunLifecycleStarted,
			AgentID: data.AgentID,
		}
	case session.TurnCompleted:
		data, ok, err := sessionEvent.TurnCompletedData()
		if err != nil || !ok {
			return
		}
		usage := s.terminalUsage(RunUsageTurn, data.Usage)
		status, canceled := statusFromError(data.Error)
		event.Usage = usage
		event.Lifecycle = &RunLifecycle{
			Type:       RunLifecycleTurn,
			Status:     status,
			AgentID:    data.AgentID,
			Error:      data.Error,
			StopReason: data.TurnStopReason,
			Terminal:   true,
			Canceled:   canceled,
			Usage:      usage,
		}
	case session.StepStarted:
		data, ok, err := sessionEvent.StepStartedData()
		if err != nil || !ok {
			return
		}
		event.Lifecycle = &RunLifecycle{
			Type:    RunLifecycleStep,
			Status:  RunLifecycleStarted,
			AgentID: data.AgentID,
		}
	case session.StepCompleted:
		data, ok, err := sessionEvent.StepCompletedData()
		if err != nil || !ok {
			return
		}
		usage := usageFromCumulative(RunUsageStep, data.Usage)
		status, canceled := statusFromError(data.Error)
		event.Usage = usage
		event.Lifecycle = &RunLifecycle{
			Type:     RunLifecycleStep,
			Status:   status,
			AgentID:  data.AgentID,
			Error:    data.Error,
			Canceled: canceled,
			Usage:    usage,
		}
	case session.ToolStarted:
		data, ok, err := sessionEvent.ToolStartedData()
		if err != nil || !ok {
			return
		}
		tool := RunToolLifecycle{
			ID:             data.ID,
			Name:           data.Tool,
			Arguments:      data.Arguments,
			IdempotencyKey: data.IdempotencyKey,
		}
		s.rememberTool(tool)
		event.Lifecycle = &RunLifecycle{
			Type:        RunLifecycleTool,
			Status:      RunLifecycleStarted,
			Tool:        &tool,
			ActiveTools: s.activeToolSnapshot(),
		}
	case session.ToolOutputDelta:
		tool, ok := toolDeltaLifecycle(sessionEvent)
		if !ok {
			return
		}
		event.Lifecycle = &RunLifecycle{
			Type:        RunLifecycleTool,
			Status:      RunLifecycleUpdated,
			Tool:        &tool,
			ActiveTools: s.activeToolSnapshot(),
		}
	case session.ToolCompleted:
		data, ok, err := sessionEvent.ToolCompletedData()
		if err != nil || !ok {
			return
		}
		tool := s.forgetTool(data.ID)
		tool.ID = firstNonEmpty(tool.ID, data.ID)
		tool.Name = firstNonEmpty(tool.Name, data.Tool)
		tool.IdempotencyKey = firstNonEmpty(tool.IdempotencyKey, data.IdempotencyKey)
		tool.Output = data.Output
		tool.Error = data.Error
		status, canceled := statusFromError(data.Error)
		s.providerUsage.reset()
		event.Lifecycle = &RunLifecycle{
			Type:        RunLifecycleTool,
			Status:      status,
			Tool:        &tool,
			ActiveTools: s.activeToolSnapshot(),
			Canceled:    canceled,
			Error:       data.Error,
		}
	case session.ChildRequested:
		data, ok, err := sessionEvent.ChildRequestedData()
		if err != nil || !ok {
			return
		}
		event.Lifecycle = &RunLifecycle{
			Type:    RunLifecycleChild,
			Status:  RunLifecycleRequested,
			AgentID: data.AgentID,
			Child: &RunChildLifecycle{
				ID:        data.ChildID,
				SessionID: data.ChildSessionID,
				AgentID:   data.AgentID,
				Mode:      data.Mode,
				Task:      data.Task,
				Context:   data.Context,
			},
		}
	case session.ChildStarted:
		data, ok, err := sessionEvent.ChildStartedData()
		if err != nil || !ok {
			return
		}
		event.Lifecycle = &RunLifecycle{
			Type:    RunLifecycleChild,
			Status:  RunLifecycleStarted,
			AgentID: data.AgentID,
			Child: &RunChildLifecycle{
				ID:        data.ChildID,
				SessionID: data.ChildSessionID,
				AgentID:   data.AgentID,
			},
		}
	case session.ChildProgressed:
		data, ok, err := sessionEvent.ChildProgressedData()
		if err != nil || !ok {
			return
		}
		event.Lifecycle = &RunLifecycle{
			Type:   RunLifecycleChild,
			Status: RunLifecycleUpdated,
			Child: &RunChildLifecycle{
				ID:        data.ChildID,
				SessionID: data.ChildSessionID,
				Status:    data.Status,
				Message:   data.Message,
			},
		}
	case session.ChildBlocked:
		data, ok, err := sessionEvent.ChildBlockedData()
		if err != nil || !ok {
			return
		}
		event.Lifecycle = &RunLifecycle{
			Type:   RunLifecycleChild,
			Status: RunLifecycleBlocked,
			Child: &RunChildLifecycle{
				ID:        data.ChildID,
				SessionID: data.ChildSessionID,
				Reason:    data.Reason,
			},
		}
	case session.ChildCompleted:
		data, ok, err := sessionEvent.ChildCompletedData()
		if err != nil || !ok {
			return
		}
		usage := childUsage(data.Usage)
		event.Usage = usage
		event.Lifecycle = &RunLifecycle{
			Type:     RunLifecycleChild,
			Status:   RunLifecycleCompleted,
			Terminal: true,
			Usage:    usage,
			Child: &RunChildLifecycle{
				ID:        data.ChildID,
				SessionID: data.ChildSessionID,
				Summary:   data.Summary,
			},
		}
	case session.ChildFailed:
		data, ok, err := sessionEvent.ChildFailedData()
		if err != nil || !ok {
			return
		}
		event.Lifecycle = &RunLifecycle{
			Type:     RunLifecycleChild,
			Status:   RunLifecycleFailed,
			Error:    data.Error,
			Terminal: true,
			Child: &RunChildLifecycle{
				ID:        data.ChildID,
				SessionID: data.ChildSessionID,
				Error:     data.Error,
			},
		}
	case session.ChildCanceled:
		data, ok, err := sessionEvent.ChildCanceledData()
		if err != nil || !ok {
			return
		}
		event.Lifecycle = &RunLifecycle{
			Type:     RunLifecycleChild,
			Status:   RunLifecycleCanceled,
			Terminal: true,
			Canceled: true,
			Child: &RunChildLifecycle{
				ID:        data.ChildID,
				SessionID: data.ChildSessionID,
				Reason:    data.Reason,
			},
		}
	case session.ChildMerged:
		data, ok, err := sessionEvent.ChildMergedData()
		if err != nil || !ok {
			return
		}
		event.Lifecycle = &RunLifecycle{
			Type:   RunLifecycleChild,
			Status: RunLifecycleMerged,
			Child: &RunChildLifecycle{
				ID:        data.ChildID,
				SessionID: data.ChildSessionID,
				Message:   data.Note,
			},
		}
	case session.WaitStarted:
		data, ok, err := sessionEvent.WaitData()
		if err != nil || !ok {
			return
		}
		event.Lifecycle = &RunLifecycle{
			Type:   RunLifecycleWait,
			Status: RunLifecycleStarted,
			Wait: &RunWaitLifecycle{
				Reason:     data.Reason,
				ExternalID: data.ExternalID,
			},
		}
	case session.WaitResolved:
		data, ok, err := sessionEvent.WaitData()
		if err != nil || !ok {
			return
		}
		event.Lifecycle = &RunLifecycle{
			Type:   RunLifecycleWait,
			Status: RunLifecycleCompleted,
			Wait: &RunWaitLifecycle{
				Reason:     data.Reason,
				ExternalID: data.ExternalID,
			},
		}
	case session.ApprovalRequested:
		var data approval.Request
		if err := sessionEvent.UnmarshalData(&data); err != nil {
			return
		}
		event.Lifecycle = &RunLifecycle{
			Type:   RunLifecycleApproval,
			Status: RunLifecycleRequested,
			Approval: &RunApprovalLifecycle{
				ID:        data.ID,
				Tool:      data.Tool,
				Category:  data.Category,
				Operation: data.Operation,
				Resource:  data.Resource,
			},
		}
	case session.ApprovalResolved:
		var data struct {
			ID       string            `json:"id"`
			Decision approval.Decision `json:"decision"`
			Reason   string            `json:"reason"`
		}
		if err := sessionEvent.UnmarshalData(&data); err != nil {
			return
		}
		event.Lifecycle = &RunLifecycle{
			Type:   RunLifecycleApproval,
			Status: RunLifecycleCompleted,
			Approval: &RunApprovalLifecycle{
				ID:       data.ID,
				Decision: data.Decision,
				Reason:   data.Reason,
			},
		}
	case session.ApprovalCanceled:
		var data struct {
			ID     string `json:"id"`
			Tool   string `json:"tool"`
			Reason string `json:"reason"`
		}
		if err := sessionEvent.UnmarshalData(&data); err != nil {
			return
		}
		event.Lifecycle = &RunLifecycle{
			Type:     RunLifecycleApproval,
			Status:   RunLifecycleCanceled,
			Canceled: true,
			Approval: &RunApprovalLifecycle{
				ID:     data.ID,
				Tool:   data.Tool,
				Reason: data.Reason,
			},
		}
	case session.CompactionStarted:
		data, ok, err := sessionEvent.CompactionStartedData()
		if err != nil || !ok {
			return
		}
		event.Lifecycle = &RunLifecycle{
			Type:   RunLifecycleCompaction,
			Status: RunLifecycleStarted,
			Compaction: &RunCompactionLifecycle{
				Strategy:      data.Strategy,
				MaxTokens:     data.MaxTokens,
				ThresholdPct:  data.ThresholdPct,
				CurrentTokens: data.CurrentTokens,
			},
		}
	case session.CompactionTriggered:
		snapshot, ok, err := sessionEvent.CompactionSnapshot()
		if err != nil || !ok {
			return
		}
		event.Lifecycle = &RunLifecycle{
			Type:   RunLifecycleCompaction,
			Status: RunLifecycleCompleted,
			Compaction: &RunCompactionLifecycle{
				Strategy:      snapshot.Strategy,
				MaxTokens:     snapshot.MaxTokens,
				ThresholdPct:  snapshot.ThresholdPct,
				CurrentTokens: snapshot.CurrentTokens,
				CutoffEventID: snapshot.CutoffEventID,
			},
		}
	case session.EscalationRetried:
		data, ok, err := sessionEvent.EscalationRetriedData()
		if err != nil || !ok {
			return
		}
		event.Lifecycle = &RunLifecycle{
			Type:    RunLifecycleRetry,
			Status:  RunLifecycleRetrying,
			AgentID: data.AgentID,
			Error:   data.Error,
			Retry: &RunRetryLifecycle{
				Scope:   data.Scope,
				Target:  data.Target,
				Attempt: data.Attempt,
				Error:   data.Error,
			},
		}
	}
}

func (s *runLifecycleState) annotateResult(event *RunEvent, result agent.StepResult) {
	usage := s.terminalUsage(RunUsageTurn, result.Usage)
	event.Usage = usage
	event.Lifecycle = &RunLifecycle{
		Type:       RunLifecycleRun,
		Status:     RunLifecycleCompleted,
		StopReason: string(result.TurnStopReason),
		Terminal:   true,
		Usage:      usage,
	}
}

func (s *runLifecycleState) recordEmittedUsage(delta llm.Usage) {
	s.emittedUsage.InputTokens += delta.InputTokens
	s.emittedUsage.OutputTokens += delta.OutputTokens
	s.emittedUsage.CacheReadTokens += delta.CacheReadTokens
	s.emittedUsage.CacheCreationTokens += delta.CacheCreationTokens
	s.emittedUsage.TotalTokens += delta.TotalTokens
	s.emittedUsage.Cost += delta.Cost
}

func (s *runLifecycleState) terminalUsage(kind RunUsageKind, cumulative llm.Usage) *RunUsage {
	cumulative = normalizeUsage(cumulative)
	if !usageHasValue(cumulative) {
		return nil
	}
	usage := &RunUsage{Kind: kind, Cumulative: cumulative}
	if !usageRegressed(cumulative, s.emittedUsage) {
		delta := subtractUsage(cumulative, s.emittedUsage)
		if usageHasValue(delta) {
			usage.Delta = delta
			s.recordEmittedUsage(delta)
		}
	}
	return usage
}

func (s *runLifecycleState) annotateError(event *RunEvent, err error) {
	errText := ""
	if err != nil {
		errText = err.Error()
	}
	status, canceled := statusFromError(errText)
	event.Lifecycle = &RunLifecycle{
		Type:     RunLifecycleRun,
		Status:   status,
		Error:    errText,
		Terminal: true,
		Canceled: canceled,
	}
}

func (s *runLifecycleState) rememberTool(tool RunToolLifecycle) {
	if tool.ID == "" {
		return
	}
	if s.activeTools == nil {
		s.activeTools = make(map[string]RunToolLifecycle)
	}
	s.activeTools[tool.ID] = tool
}

func (s *runLifecycleState) forgetTool(id string) RunToolLifecycle {
	if id == "" || s.activeTools == nil {
		return RunToolLifecycle{}
	}
	tool := s.activeTools[id]
	delete(s.activeTools, id)
	return tool
}

func (s *runLifecycleState) activeToolSnapshot() []RunToolLifecycle {
	if len(s.activeTools) == 0 {
		return nil
	}
	tools := make([]RunToolLifecycle, 0, len(s.activeTools))
	for _, tool := range s.activeTools {
		tools = append(tools, tool)
	}
	slices.SortFunc(tools, func(a, b RunToolLifecycle) int {
		return strings.Compare(a.ID, b.ID)
	})
	return tools
}

type runUsageAccumulator struct {
	seen    bool
	current llm.Usage
}

func (a *runUsageAccumulator) reset() {
	*a = runUsageAccumulator{}
}

func (a *runUsageAccumulator) delta(next *llm.Usage) (RunUsage, bool) {
	if next == nil {
		return RunUsage{}, false
	}
	current := normalizeUsage(*next)
	if !usageHasValue(current) {
		return RunUsage{}, false
	}
	if a.seen && usageRegressed(current, a.current) {
		a.reset()
	}
	delta := current
	if a.seen {
		delta = subtractUsage(current, a.current)
	}
	a.seen = true
	a.current = current
	if !usageHasValue(delta) {
		return RunUsage{}, false
	}
	return RunUsage{
		Kind:       RunUsageProviderDelta,
		Delta:      delta,
		Cumulative: current,
	}, true
}

func usageFromCumulative(kind RunUsageKind, cumulative llm.Usage) *RunUsage {
	cumulative = normalizeUsage(cumulative)
	if !usageHasValue(cumulative) {
		return nil
	}
	return &RunUsage{Kind: kind, Cumulative: cumulative}
}

func childUsage(usage llm.Usage) *RunUsage {
	usage = normalizeUsage(usage)
	if !usageHasValue(usage) {
		return nil
	}
	return &RunUsage{Kind: RunUsageChild, Delta: usage, Cumulative: usage}
}

func normalizeUsage(usage llm.Usage) llm.Usage {
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
	return usage
}

func subtractUsage(current, previous llm.Usage) llm.Usage {
	return llm.Usage{
		InputTokens:         current.InputTokens - previous.InputTokens,
		OutputTokens:        current.OutputTokens - previous.OutputTokens,
		CacheReadTokens:     current.CacheReadTokens - previous.CacheReadTokens,
		CacheCreationTokens: current.CacheCreationTokens - previous.CacheCreationTokens,
		TotalTokens:         current.TotalTokens - previous.TotalTokens,
		Cost:                current.Cost - previous.Cost,
	}
}

func usageRegressed(current, previous llm.Usage) bool {
	return current.InputTokens < previous.InputTokens ||
		current.OutputTokens < previous.OutputTokens ||
		current.CacheReadTokens < previous.CacheReadTokens ||
		current.CacheCreationTokens < previous.CacheCreationTokens ||
		current.TotalTokens < previous.TotalTokens ||
		current.Cost < previous.Cost
}

func usageHasValue(usage llm.Usage) bool {
	return usage.InputTokens != 0 ||
		usage.OutputTokens != 0 ||
		usage.CacheReadTokens != 0 ||
		usage.CacheCreationTokens != 0 ||
		usage.TotalTokens != 0 ||
		usage.Cost != 0
}

func statusFromError(errText string) (RunLifecycleStatus, bool) {
	if errText == "" {
		return RunLifecycleCompleted, false
	}
	if isCancellationErrorText(errText) {
		return RunLifecycleCanceled, true
	}
	return RunLifecycleFailed, false
}

func isCancellationErrorText(errText string) bool {
	return strings.Contains(errText, context.Canceled.Error()) ||
		strings.Contains(errText, context.DeadlineExceeded.Error())
}

func toolDeltaLifecycle(event session.Event) (RunToolLifecycle, bool) {
	var data struct {
		Tool     string `json:"tool"`
		ID       string `json:"id"`
		Delta    string `json:"delta"`
		Snapshot bool   `json:"snapshot"`
	}
	if err := event.UnmarshalData(&data); err != nil {
		return RunToolLifecycle{}, false
	}
	return RunToolLifecycle{
		ID:       data.ID,
		Name:     data.Tool,
		Delta:    data.Delta,
		Snapshot: data.Snapshot,
	}, true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
