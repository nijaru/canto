package session

import (
	"strings"
	"time"

	"github.com/go-json-experiment/json"

	"github.com/nijaru/canto/llm"
	"github.com/oklog/ulid/v2"
)

// RunLog represents a structured trace of an agent's execution.
// It is used for evaluation, reinforcement learning (RL) fine-tuning,
// and offline analysis.
type RunLog struct {
	SessionID string         `json:"session_id"`
	AgentID   string         `json:"agent_id"`
	StartTime time.Time      `json:"start_time"`
	EndTime   time.Time      `json:"end_time"`
	Turns     []RunTurn      `json:"turns"`
	TotalCost float64        `json:"total_cost"`
	Metadata  map[string]any `json:"metadata,omitzero"`
}

// RunTurn represents a single perceive-decide-act-observe loop.
type RunTurn struct {
	TurnID      string         `json:"turn_id"`
	Timestamp   time.Time      `json:"timestamp"`
	Input       []llm.Message  `json:"input"`
	Output      llm.Message    `json:"output"`
	ToolCalls   []llm.Call `json:"tool_calls,omitzero"`
	ToolResults []llm.Message  `json:"tool_results,omitzero"`
	Cost        float64        `json:"cost"`
	Metrics     map[string]any `json:"metrics,omitzero"`
}

// Episode is a compressed record of a completed agent run.
// It captures only the signal — successful tool call pairs and the final conclusion —
// discarding the raw conversation transcript. Orchestrators retrieve episodes from
// archival memory rather than full session logs, keeping swarm coordination practical at scale.
type Episode struct {
	ID         string         `json:"id"`
	SessionID  string         `json:"session_id"`
	AgentID    string         `json:"agent_id"`
	StartTime  time.Time      `json:"start_time"`
	EndTime    time.Time      `json:"end_time"`
	Conclusion string         `json:"conclusion"` // last assistant message without tool calls
	Calls      []EpisodeCall  `json:"calls,omitzero"`
	TotalCost  float64        `json:"total_cost"`
	Metadata   map[string]any `json:"metadata,omitzero"`
}

// EpisodeCall is a single successful tool invocation captured in an Episode.
type EpisodeCall struct {
	Tool   string `json:"tool"`
	Args   string `json:"args"`
	Result string `json:"result"`
}

// Text returns the searchable text for this Episode: conclusion followed by tool names.
// Used as FTS5 content when storing in memory.
func (ep *Episode) Text() string {
	var sb strings.Builder
	sb.WriteString(ep.Conclusion)
	for _, c := range ep.Calls {
		sb.WriteByte(' ')
		sb.WriteString(c.Tool)
	}
	return sb.String()
}

// Distill compresses a RunLog into an Episode by extracting only the signal:
// successful tool call pairs (call + result) and the final textual conclusion.
// The raw conversation transcript is discarded. The returned Episode is ready for
// storage in an archival memory store so orchestrators can retrieve completed work
// without loading full session logs.
func Distill(traj *RunLog) *Episode {
	ep := &Episode{
		ID:        ulid.Make().String(),
		SessionID: traj.SessionID,
		AgentID:   traj.AgentID,
		StartTime: traj.StartTime,
		EndTime:   traj.EndTime,
		TotalCost: traj.TotalCost,
	}

	for _, turn := range traj.Turns {
		// Map tool results by call ID for O(1) pairing.
		resultsByID := make(map[string]string, len(turn.ToolResults))
		for _, r := range turn.ToolResults {
			resultsByID[r.ToolID] = r.Content
		}

		for _, call := range turn.ToolCalls {
			result, ok := resultsByID[call.ID]
			if !ok {
				continue // skip calls with no matching result
			}
			ep.Calls = append(ep.Calls, EpisodeCall{
				Tool:   call.Function.Name,
				Args:   call.Function.Arguments,
				Result: result,
			})
		}

		// Track the final conclusion: last assistant message with no tool calls.
		if len(turn.ToolCalls) == 0 && turn.Output.Content != "" {
			ep.Conclusion = turn.Output.Content
		}
	}

	return ep
}

// ExportRun converts a session's event log into a structured RunLog.
func ExportRun(sess *Session) (*RunLog, error) {
	events := sess.Events()
	if len(events) == 0 {
		return &RunLog{
			SessionID: sess.ID(),
			Turns:     []RunTurn{},
		}, nil
	}

	traj := &RunLog{
		SessionID: sess.ID(),
		StartTime: events[0].Timestamp,
		EndTime:   events[len(events)-1].Timestamp,
		Metadata:  make(map[string]any),
	}

	var currentTurn *RunTurn
	var inputBuffer []llm.Message

	for _, e := range events {
		traj.TotalCost += e.Cost

		switch e.Type {
		case MessageAdded:
			var msg llm.Message
			if err := json.Unmarshal(e.Data, &msg); err != nil {
				continue
			}

			if msg.Role == llm.RoleUser || msg.Role == llm.RoleSystem {
				inputBuffer = append(inputBuffer, msg)
			} else if msg.Role == llm.RoleAssistant {
				if currentTurn != nil {
					traj.Turns = append(traj.Turns, *currentTurn)
				}
				currentTurn = &RunTurn{
					TurnID:    e.ID.String(),
					Timestamp: e.Timestamp,
					Input:     make([]llm.Message, len(inputBuffer)),
					Output:    msg,
					ToolCalls: msg.Calls,
					Cost:      e.Cost,
				}
				copy(currentTurn.Input, inputBuffer)
				inputBuffer = nil // Reset input for next turn
			} else if msg.Role == llm.RoleTool && currentTurn != nil {
				currentTurn.ToolResults = append(currentTurn.ToolResults, msg)
			}
		}
	}

	if currentTurn != nil {
		traj.Turns = append(traj.Turns, *currentTurn)
	}

	return traj, nil
}
