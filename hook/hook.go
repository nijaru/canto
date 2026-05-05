package hook

import "context"

// Event defines the lifecycle events that triggers a hook.
type Event string

const (
	EventSessionStart       Event = "SessionStart"
	EventUserPromptSubmit   Event = "UserPromptSubmit"
	EventPreToolUse         Event = "PreToolUse"
	EventPostToolUse        Event = "PostToolUse"
	EventPostToolUseFailure Event = "PostToolUseFailure"
	EventSessionEnd         Event = "SessionEnd"
	EventStop               Event = "Stop"
)

// Action determines how the hook runner should proceed.
type Action int

const (
	ActionProceed Action = 0 // Exit 0: Proceed normally
	ActionLog     Action = 1 // Exit 1: Log output but proceed
	ActionBlock   Action = 2 // Exit 2: Block execution
)

// SessionMeta carries the session identifiers sent to hooks.
// It replaces *session.Session to avoid shipping the full event log
// to every hook subprocess.
type SessionMeta struct {
	ID      string `json:"id"`
	AgentID string `json:"agent_id,omitzero"`
}

// Payload is the JSON payload sent to the hook via stdin.
type Payload struct {
	Event   Event          `json:"event"`
	Session SessionMeta    `json:"session"`
	Data    map[string]any `json:"data,omitzero"`
}

// Result is the result of executing a hook.
type Result struct {
	Action Action
	Output string
	Data   map[string]any
	Error  error
}

// Handler interface defines a lifecycle hook.
type Handler interface {
	Name() string
	Events() []Event
	Execute(ctx context.Context, payload *Payload) *Result
}
