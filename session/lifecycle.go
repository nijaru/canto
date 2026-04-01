package session

import (
	"fmt"

	"github.com/nijaru/canto/llm"
)

// PromptCacheData captures the stable pieces that should influence prefix-cache reuse.
type PromptCacheData struct {
	PrefixHash     string `json:"prefix_hash,omitzero"`
	ToolSchemaHash string `json:"tool_schema_hash,omitzero"`
}

// StepStartedData records the durable start of a step.
type StepStartedData struct {
	AgentID     string          `json:"agent_id"`
	Model       string          `json:"model"`
	PromptCache PromptCacheData `json:"prompt_cache,omitzero"`
}

// StepCompletedData records the durable end of a step.
type StepCompletedData struct {
	AgentID string    `json:"agent_id"`
	Usage   llm.Usage `json:"usage,omitzero"`
	Error   string    `json:"error,omitzero"`
}

// TurnStartedData records the durable start of a turn.
type TurnStartedData struct {
	AgentID string `json:"agent_id"`
}

// TurnCompletedData records the durable end of a turn.
type TurnCompletedData struct {
	AgentID string    `json:"agent_id"`
	Steps   int       `json:"steps"`
	Usage   llm.Usage `json:"usage,omitzero"`
	Error   string    `json:"error,omitzero"`
}

// ToolStartedData records the durable start of a tool call.
type ToolStartedData struct {
	Tool      string `json:"tool"`
	Arguments string `json:"args"`
	ID        string `json:"id"`
}

// NewStepStartedEvent records the durable start of a step.
func NewStepStartedEvent(sessionID string, data StepStartedData) Event {
	return NewEvent(sessionID, StepStarted, data)
}

// NewStepCompletedEvent records the durable end of a step.
func NewStepCompletedEvent(sessionID string, data StepCompletedData) Event {
	return NewEvent(sessionID, StepCompleted, data)
}

// NewTurnStartedEvent records the durable start of a turn.
func NewTurnStartedEvent(sessionID string, data TurnStartedData) Event {
	return NewEvent(sessionID, TurnStarted, data)
}

// NewTurnCompletedEvent records the durable end of a turn.
func NewTurnCompletedEvent(sessionID string, data TurnCompletedData) Event {
	return NewEvent(sessionID, TurnCompleted, data)
}

// NewToolStartedEvent records the durable start of a tool call.
func NewToolStartedEvent(sessionID string, data ToolStartedData) Event {
	return NewEvent(sessionID, ToolStarted, data)
}

// StepStartedData decodes the payload of a step-started event.
func (e Event) StepStartedData() (StepStartedData, bool, error) {
	return decodeEventData[StepStartedData](e, StepStarted, "step started")
}

// StepCompletedData decodes the payload of a step-completed event.
func (e Event) StepCompletedData() (StepCompletedData, bool, error) {
	return decodeEventData[StepCompletedData](e, StepCompleted, "step completed")
}

// TurnStartedData decodes the payload of a turn-started event.
func (e Event) TurnStartedData() (TurnStartedData, bool, error) {
	return decodeEventData[TurnStartedData](e, TurnStarted, "turn started")
}

// TurnCompletedData decodes the payload of a turn-completed event.
func (e Event) TurnCompletedData() (TurnCompletedData, bool, error) {
	return decodeEventData[TurnCompletedData](e, TurnCompleted, "turn completed")
}

// ToolStartedData decodes the payload of a tool-started event.
func (e Event) ToolStartedData() (ToolStartedData, bool, error) {
	return decodeEventData[ToolStartedData](e, ToolStarted, "tool started")
}

// decodeEventData is shared with other typed event helpers.
func decodeEventData[T any](e Event, want EventType, label string) (T, bool, error) {
	if e.Type != want {
		var zero T
		return zero, false, nil
	}

	var data T
	if err := e.UnmarshalData(&data); err != nil {
		return data, true, fmt.Errorf("decode %s event %s: %w", label, e.ID, err)
	}
	return data, true, nil
}
