package session

import (
	"encoding/json"
	"time"

	"github.com/oklog/ulid/v2"
)

// EventType identifies the type of an event.
type EventType string

const (
	EventTypeMessageAdded   EventType = "message_added"
	EventTypeToolCalled     EventType = "tool_called"
	EventTypeToolOutput     EventType = "tool_output"
	EventTypeSessionCreated EventType = "session_created"
	EventTypeMetaUpdated    EventType = "meta_updated"
	EventTypeCompaction     EventType = "compaction"
	EventTypeContextOffload EventType = "context_offload"
	EventTypeHandoff        EventType = "handoff"
)

// Event is a single append-only fact in a session.
type Event struct {
	ID        ulid.ULID       `json:"id"`
	SessionID string          `json:"session_id"`
	Type      EventType       `json:"type"`
	Timestamp time.Time       `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
	Cost      float64         `json:"cost,omitempty"`
}

// NewEvent creates a new event with a unique ID and current timestamp.
func NewEvent(sessionID string, eventType EventType, data any) Event {
	raw, err := json.Marshal(data)
	if err != nil {
		// This should only happen if data contains something that cannot be marshaled
		// like a channel or a cyclic reference. We use a fallback error message.
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
