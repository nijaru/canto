package session

import (
	"log/slog"
	"time"

	"github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"
	"github.com/nijaru/canto/llm"
	"github.com/oklog/ulid/v2"
)

// EventType identifies the type of an event.
type EventType string

const (
	MessageAdded  EventType = "message_added"
	Handoff       EventType = "handoff"
	ExternalInput EventType = "external_input"

	// Observability / Lifecycle
	TurnStarted         EventType = "turn_started"
	TurnCompleted       EventType = "turn_completed"
	StepStarted         EventType = "step_started"
	StepCompleted       EventType = "step_completed"
	ToolStarted         EventType = "tool_started"
	ToolCompleted       EventType = "tool_completed"
	CompactionTriggered EventType = "compaction_triggered"
	ChildRequested      EventType = "child_requested"
	ChildStarted        EventType = "child_started"
	ChildProgressed     EventType = "child_progressed"
	ChildBlocked        EventType = "child_blocked"
	ChildCompleted      EventType = "child_completed"
	ChildFailed         EventType = "child_failed"
	ChildCanceled       EventType = "child_canceled"
	ChildMerged         EventType = "child_merged"
	ArtifactRecorded    EventType = "artifact_recorded"

	// Framework Extensions
	ToolOutputDelta EventType = "tool_output_delta"
)

// Event is a single append-only fact in a session.
type Event struct {
	ID        ulid.ULID      `json:"id"`
	SessionID string         `json:"session_id"`
	Type      EventType      `json:"type"`
	Timestamp time.Time      `json:"timestamp"`
	Data      jsontext.Value `json:"data"`
	Metadata  map[string]any `json:"metadata,omitzero"`
	Cost      float64        `json:"cost,omitzero"`

	metadataRaw jsontext.Value
}

// UnmarshalData unmarshals the event's data into the given value.
func (e Event) UnmarshalData(v any) error {
	return json.Unmarshal(e.Data, v)
}

func (e *Event) ensureMetadata() error {
	if e.Metadata != nil || len(e.metadataRaw) == 0 {
		return nil
	}

	var metadata map[string]any
	if err := json.Unmarshal(e.metadataRaw, &metadata); err != nil {
		return err
	}
	e.Metadata = metadata
	return nil
}

func (e Event) encodedMetadata() (jsontext.Value, error) {
	if e.Metadata != nil {
		raw, err := json.Marshal(e.Metadata)
		if err != nil {
			return nil, err
		}
		return raw, nil
	}
	if len(e.metadataRaw) > 0 {
		return e.metadataRaw, nil
	}
	return nil, nil
}

// NewEvent creates a new event with a unique ID and current timestamp.
func NewEvent(sessionID string, eventType EventType, data any) Event {
	raw, err := json.Marshal(data)
	if err != nil {
		slog.Warn("event marshal failed", "error", err)
		raw, _ = json.Marshal(
			map[string]string{"error": "failed to marshal event data: " + err.Error()},
		)
	}
	return Event{
		ID:        ulid.Make(),
		SessionID: sessionID,
		Type:      eventType,
		Timestamp: time.Now().UTC(),
		Data:      raw,
	}
}

// NewMessage creates a new message event.
func NewMessage(sessionID string, msg llm.Message) Event {
	return NewEvent(sessionID, MessageAdded, msg)
}
