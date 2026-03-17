package session

import (
	"log/slog"
	"time"

	"github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"
	"github.com/oklog/ulid/v2"
)

// EventType identifies the type of an event.
type EventType string

const (
	EventTypeMessageAdded  EventType = "message_added"
	EventTypeHandoff       EventType = "handoff"
	EventTypeExternalInput EventType = "external_input"

	// Observability / Lifecycle
	EventTypeTurnStarted           EventType = "turn_started"
	EventTypeTurnCompleted         EventType = "turn_completed"
	EventTypeStepStarted           EventType = "step_started"
	EventTypeStepCompleted         EventType = "step_completed"
	EventTypeToolExecutionStarted   EventType = "tool_execution_started"
	EventTypeToolExecutionCompleted EventType = "tool_execution_completed"
	EventTypeCompactionTriggered    EventType = "compaction_triggered"
)

// Event is a single append-only fact in a session.
type Event struct {
	ID        ulid.ULID      `json:"id"`
	SessionID string         `json:"session_id"`
	Type      EventType      `json:"type"`
	Timestamp time.Time      `json:"timestamp"`
	Data      jsontext.Value `json:"data"`
	Cost      float64        `json:"cost,omitzero"`
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
