package session

import "fmt"

// ToolCompletedData captures the durable outcome of a completed tool call.
type ToolCompletedData struct {
	Tool           string `json:"tool"`
	ID             string `json:"id"`
	IdempotencyKey string `json:"idempotency_key,omitzero"`
	Output         string `json:"output,omitzero"`
}

// NewToolCompletedEvent records the durable result of a tool execution.
func NewToolCompletedEvent(sessionID string, result ToolCompletedData) Event {
	return NewEvent(sessionID, ToolCompleted, result)
}

// ToolCompletedData decodes the payload of a tool-completed event.
func (e Event) ToolCompletedData() (ToolCompletedData, bool, error) {
	if e.Type != ToolCompleted {
		return ToolCompletedData{}, false, nil
	}

	var result ToolCompletedData
	if err := e.UnmarshalData(&result); err != nil {
		return ToolCompletedData{}, true, fmt.Errorf(
			"decode tool completed event %s: %w",
			e.ID,
			err,
		)
	}
	return result, true, nil
}
